package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This state contains only install-level milestones, never product content.
type telemetryProductState struct {
	FirstBriefSeen bool   `json:"first_brief_seen"`
	LastActiveDate string `json:"last_active_date,omitempty"`
}

func loadTelemetryInstallID(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("missing data folder")
	}
	path := filepath.Join(dir, "telemetry_install_id")
	read := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		id, err := uuid.Parse(strings.TrimSpace(string(data)))
		if err != nil || id.Version() != 4 {
			return "", errors.New("invalid install identity")
		}
		return id.String(), nil
	}
	if id, err := read(); !os.IsNotExist(err) {
		return id, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".telemetry-id-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	id := uuid.NewString()
	if _, err := file.WriteString(id); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// Atomically publish without replacing another process's identity.
	if err := os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	return read()
}

func (t *PostHogTelemetry) loadProductStateLocked() {
	if t.stateLoaded {
		return
	}
	t.stateLoaded = true
	data, err := os.ReadFile(filepath.Join(t.dataDir, "telemetry_product_state.json"))
	if err == nil {
		_ = json.Unmarshal(data, &t.state)
	}
}

func (t *PostHogTelemetry) saveProductStateLocked() error {
	if t.dataDir == "" {
		return errors.New("missing data folder")
	}
	if err := os.MkdirAll(t.dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(t.state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(t.dataDir, ".telemetry-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(t.dataDir, "telemetry_product_state.json"))
}

// RecordFirstBriefSeen receives only a view milestone from the UI. The brief's
// ID and contents are deliberately absent from this binding. The local marker
// records the install's actual first view even when no sender is enabled, so
// enabling sharing later cannot misreport an older view as a new milestone.
func (a *App) RecordFirstBriefSeen() {
	t := a.telemetry
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dataDir == "" {
		t.dataDir = configDirOrFallback()
	}
	t.loadProductStateLocked()
	if t.state.FirstBriefSeen {
		return
	}
	t.state.FirstBriefSeen = true
	if err := t.saveProductStateLocked(); err != nil {
		t.state.FirstBriefSeen = false
		return
	}
	// The usual tier/client gate still prevents all enqueue and network work
	// while sharing is off. Keep the marker even when this event is dropped.
	t.captureLocked("brief.first_seen", nil)
}

// RecordProductActivity accepts only a bounded duration and a random session
// UUID. The UI reports foreground activity every 30s, with a five-minute idle
// cutoff. Zero seconds establishes a daily active ping without adding duration.
func (a *App) RecordProductActivity(activeSeconds float64, sessionID string) {
	a.telemetry.recordProductActivity(activeSeconds, sessionID, time.Now())
}

func (t *PostHogTelemetry) recordProductActivity(seconds float64, session string, now time.Time) {
	if t == nil || !t.enabled.Load() {
		return
	}
	// Validate even zero-duration heartbeats; no arbitrary IDs enter the queue.
	if _, ok := productTelemetryProperties("app.active_time", map[string]any{"active_seconds": max(seconds, 0.001), "session_id": session}); !ok || seconds < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !telemetrySharingOn() || t.client == nil {
		return
	}
	t.loadProductStateLocked()
	day := now.UTC().Format(time.DateOnly)
	if t.state.LastActiveDate != day {
		previous := t.state.LastActiveDate
		t.state.LastActiveDate = day
		if err := t.saveProductStateLocked(); err == nil {
			if !t.captureLocked("app.daily_active", map[string]any{"date": day}) {
				t.state.LastActiveDate = previous
				_ = t.saveProductStateLocked()
			}
		} else {
			t.state.LastActiveDate = previous
		}
	}
	if seconds > 0 {
		t.captureLocked("app.active_time", map[string]any{"active_seconds": seconds, "session_id": session})
	}
}

func telemetryOS() string { return runtime.GOOS }

// telemetryBuildChannel separates the shipped release from development builds,
// which share the Isoform flavor and may carry a key from wails-gui/.env.local.
func telemetryBuildChannel() string {
	if isReleaseChannel() {
		return buildChannelRelease
	}
	return "development"
}

// Read the OS release only, never hostname, environment dumps or user agents.
func telemetryOSVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion")
	case "linux":
		cmd = exec.CommandContext(ctx, "uname", "-r")
	case "windows":
		return telemetryWindowsVersion()
	default:
		return "unknown"
	}
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	version := regexp.MustCompile(`[0-9]+(?:\.[0-9]+)+`).FindString(string(out))
	if version == "" {
		return "unknown"
	}
	return version
}
