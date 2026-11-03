package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	posthogIntegrationEnv = "ISOFORM_POSTHOG_INTEGRATION"
	// releasePostHogEnvOverride matches scripts/release/posthog-env.sh.
	releasePostHogEnvOverride = "ISOFORM_RELEASE_POSTHOG_ENV"
	// telemetryTestInstallID marks events this test sends; the dashboards in
	// docs/product-telemetry-dashboards.md exclude it.
	telemetryTestInstallID = "7e570000-0000-4000-8000-000000000001"
)

// TestPostHogIngestIntegration sends one app.open to the release PostHog
// project through the desktop sender (same client options and consent
// transport) and prints the event UUID for the release sign-off record. It
// does not launch Isoform and never prints the key. Opt in with
// ISOFORM_POSTHOG_INTEGRATION=1; settings come from the release key file.
func TestPostHogIngestIntegration(t *testing.T) {
	if os.Getenv(posthogIntegrationEnv) != "1" {
		t.Skipf("set %s=1 to send one test event to the release PostHog project", posthogIntegrationEnv)
	}
	path := os.Getenv(releasePostHogEnvOverride)
	if path == "" {
		// TestMain moved HOME aside; the key file lives under the real one.
		path = filepath.Join(testRealHomeDir, ".config", "isoform-release", "posthog.env")
	}
	key, host, err := readReleasePostHogEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	previousKey, previousHost := DefaultDesktopPostHogKey, DefaultDesktopPostHogHost
	DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = key, host
	t.Cleanup(func() { DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = previousKey, previousHost })
	telemetryTierMu.Lock()
	previousTier := telemetryTier
	telemetryTier = TelemetryTierDetailed
	telemetryTierMu.Unlock()
	t.Cleanup(func() {
		telemetryTierMu.Lock()
		telemetryTier = previousTier
		telemetryTierMu.Unlock()
	})

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "telemetry_install_id"), []byte(telemetryTestInstallID), 0o600); err != nil {
		t.Fatal(err)
	}
	// As NewPostHogTelemetry, with a temporary data folder and a recorder
	// under the consent transport in place of http.DefaultTransport.
	recorder := &posthogBatchRecorder{base: http.DefaultTransport}
	telemetry := &PostHogTelemetry{dataDir: dataDir}
	telemetry.key, telemetry.config.Endpoint = envPostHogConfig()
	telemetry.config.Transport = recorder
	telemetry.SetSharingEnabled(true)
	if !telemetry.enabled.Load() || telemetry.anonymousID != telemetryTestInstallID {
		t.Fatal("the sender did not start with the test install ID; check the host in the key file")
	}
	sentAt := time.Now().UTC()
	telemetry.Capture("app.open", nil)
	telemetry.Close()

	uuids, statuses, err := recorder.result()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0] != http.StatusOK {
		t.Fatalf("PostHog batch responses = %v, want one 200", statuses)
	}
	if len(uuids) != 1 {
		t.Fatalf("sent %d events, want one app.open", len(uuids))
	}
	fmt.Printf("PostHog test event sent\n  event: app.open\n  uuid: %s\n  install id: %s\n  host: %s\n  sent at (UTC): %s\n",
		uuids[0], telemetryTestInstallID, strings.TrimRight(host, "/"), sentAt.Format(time.RFC3339))
}

// readReleasePostHogEnv parses the release key file the way
// scripts/release/posthog-env.sh does, including the deprecated
// YANSU_DESKTOP_POSTHOG_* fallback. Errors never include a value.
func readReleasePostHogEnv(path string) (key, host string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("PostHog release settings not found: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", "", fmt.Errorf("%s must not be readable by others: chmod 600", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	assignment := regexp.MustCompile(`^\s*(?:export\s+)?((?:ISOFORM|YANSU)_DESKTOP_POSTHOG_(?:KEY|HOST))=(.*)$`)
	var legacyKey, legacyHost string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		match := assignment.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		value := strings.TrimSuffix(match[2], "\r")
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		switch match[1] {
		case "ISOFORM_DESKTOP_POSTHOG_KEY":
			key = value
		case "ISOFORM_DESKTOP_POSTHOG_HOST":
			host = value
		case "YANSU_DESKTOP_POSTHOG_KEY":
			legacyKey = value
		case "YANSU_DESKTOP_POSTHOG_HOST":
			legacyHost = value
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	if key == "" {
		key = legacyKey
	}
	if host == "" {
		host = legacyHost
	}
	if key == "" {
		return "", "", fmt.Errorf("%s does not set ISOFORM_DESKTOP_POSTHOG_KEY", path)
	}
	if !strings.HasPrefix(host, "https://") {
		return "", "", fmt.Errorf("%s must set ISOFORM_DESKTOP_POSTHOG_HOST to an https:// origin", path)
	}
	return key, host, nil
}

// posthogBatchRecorder notes each batch's event UUIDs and response status.
// The request body carries the key, so it is decoded but never logged.
type posthogBatchRecorder struct {
	base     http.RoundTripper
	mu       sync.Mutex
	uuids    []string
	statuses []int
	err      error
}

func (r *posthogBatchRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	var batch struct {
		Batch []struct {
			UUID string `json:"uuid"`
		} `json:"batch"`
	}
	decodeErr := json.Unmarshal(body, &batch)
	response, err := r.base.RoundTrip(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if decodeErr != nil {
		r.err = errors.New("could not decode the PostHog batch")
	}
	for _, event := range batch.Batch {
		r.uuids = append(r.uuids, event.UUID)
	}
	if err != nil {
		r.err = fmt.Errorf("PostHog request failed: %v", err)
		return nil, err
	}
	r.statuses = append(r.statuses, response.StatusCode)
	return response, nil
}

func (r *posthogBatchRecorder) result() ([]string, []int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.uuids...), append([]int(nil), r.statuses...), r.err
}
