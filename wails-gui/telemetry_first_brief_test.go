package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryFirstBriefViewedOffIsPersistedWithoutLaterCapture(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierOff)
	app.telemetry.dataDir = configDirOrFallback()
	app.RecordFirstBriefSeen()
	path := filepath.Join(app.telemetry.dataDir, "telemetry_product_state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("first view while off did not persist its local milestone: %v", err)
	}
	var state telemetryProductState
	if err := json.Unmarshal(data, &state); err != nil || !state.FirstBriefSeen {
		t.Fatalf("first view while off was not recorded: %+v (%v)", state, err)
	}
	if app.telemetry.client != nil || len(received()) != 0 {
		t.Fatal("first view while off initialized a sender or made an HTTP request")
	}
	if _, err := os.Stat(filepath.Join(app.telemetry.dataDir, "telemetry_install_id")); !os.IsNotExist(err) {
		t.Fatalf("local first-view milestone created a telemetry identity while off: %v", err)
	}

	// Reload the durable milestone before enabling sharing, as on next launch.
	app.telemetry = NewPostHogTelemetry()
	if err := app.SetTelemetryTier(TelemetryTierDetailed); err != nil {
		t.Fatal(err)
	}
	app.RecordFirstBriefSeen()
	app.telemetry.Close()
	if events := received(); len(events) != 0 {
		t.Fatalf("turning sharing on replayed a first view that happened while off: %+v", events)
	}
}
