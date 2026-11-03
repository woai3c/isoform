package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateBetaChannelAndVersions(t *testing.T) {
	t.Setenv("ISOFORM_UPDATE_CHANNEL", "beta")
	oldFeed := R2ManifestURL
	R2ManifestURL = ""
	t.Cleanup(func() { R2ManifestURL = oldFeed })
	if !strings.HasSuffix(updateFeedURL(), "/releases/download/beta-feed/version.json") {
		t.Fatal(updateFeedURL())
	}
	for _, tc := range []struct {
		remote, local string
		want          bool
	}{
		{"1.0.0-beta.2", "1.0.0-beta.1", true},
		{"1.0.1", "1.0.0", true}, {"1.1.0", "1.0.9", true},
		{"1.0.0-beta.1", "1.0.0-beta.2", false}, {"1.0.0-beta.2", "1.0.0", false},
	} {
		if got := isNewer(tc.remote, tc.local); got != tc.want {
			t.Errorf("%s > %s = %v", tc.remote, tc.local, got)
		}
	}
	t.Setenv("ISOFORM_UPDATE_CHANNEL", "stable")
	if isNewer("1.0.1-beta.1", "1.0.0") {
		t.Fatal("stable offered a prerelease")
	}
}

func TestUpdateScheduleChecksOnWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	checked := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runUpdateScheduleWithWake(ctx, time.Hour, time.Hour, wake, func() { checked <- struct{}{} })
	}()
	wake <- struct{}{}
	select {
	case <-checked:
	case <-time.After(3 * time.Second):
		t.Fatal("wake did not trigger check")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("schedule did not stop")
	}
}

func TestFailedUpdateDownloadLeavesCurrentBundle(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "Isoform.app", "Contents", "version")
	if err := os.MkdirAll(filepath.Dir(current), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("1.0.0-beta.1"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "failed", http.StatusServiceUnavailable) }))
	defer server.Close()
	app := &App{updater: &updater{expectedChecksum: strings.Repeat("a", 64)}}
	if err := app.downloadAttempt(server.Client(), server.URL, filepath.Join(root, "update.tar.gz"), 1); err == nil {
		t.Fatal("failed download succeeded")
	}
	data, err := os.ReadFile(current)
	if err != nil || string(data) != "1.0.0-beta.1" {
		t.Fatalf("current bundle changed: %q %v", data, err)
	}
}

func TestUpdateBetaBundleRequiresSignedFullVersion(t *testing.T) {
	for _, test := range []struct {
		full      string
		wantError bool
	}{
		{"1.0.0-beta.2", false}, {"1.0.0-beta.1", true}, {"", true},
	} {
		run := func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "-r-"):
				return []byte("designated => identifier com.wails.Isoform"), nil
			case name == "/usr/sbin/spctl":
				return []byte("accepted\nsource=Notarized Developer ID"), nil
			case strings.Contains(joined, "CFBundleShortVersionString"):
				return []byte("1.0.0"), nil
			case strings.Contains(joined, "IsoformReleaseVersion"):
				return []byte(test.full), nil
			}
			return nil, nil
		}
		err := verifyUpdateBundleWithTools("old", "new", "1.0.0-beta.2", run, func(string) (int, error) { return 0, nil })
		if (err != nil) != test.wantError {
			t.Errorf("signed version %q: %v", test.full, err)
		}
	}
}

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestBetaFeedFailureDoesNotFallBackToStable(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv("ISOFORM_UPDATE_CHANNEL", "beta")
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	var paths []string
	http.DefaultTransport = updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable")), Request: req}, nil
	})
	app := &App{updater: &updater{}}
	if result := app.fetchLatestRelease(); result.Error == "" {
		t.Fatal("missing beta feed error")
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "/releases/download/beta-feed/") {
		t.Fatalf("unexpected fallback requests: %v", paths)
	}
}
