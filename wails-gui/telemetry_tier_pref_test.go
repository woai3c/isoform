package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryTierFailedSaveKeepsRuntimeAndDoesNotBroadcast(t *testing.T) {
	for _, initial := range []string{TelemetryTierOff, TelemetryTierDetailed} {
		t.Run(initial, func(t *testing.T) {
			telemetry, recorder := newRecordingTelemetry(t)
			telemetryTierMu.Lock()
			telemetryTier = initial
			telemetryTierMu.Unlock()
			app := &App{telemetry: telemetry, eventBroadcaster: NewEventBroadcaster()}
			events := app.eventBroadcaster.AddClient("telemetry-test")
			defer app.eventBroadcaster.RemoveClient("telemetry-test")

			// A directory at the destination deterministically makes rename fail,
			// including when tests run with permission to write read-only files.
			if err := os.MkdirAll(telemetryTierPath(), 0o755); err != nil {
				t.Fatal(err)
			}
			next := TelemetryTierOff
			if initial == TelemetryTierOff {
				next = TelemetryTierDetailed
			}
			if err := app.SetTelemetryTier(next); err == nil {
				t.Fatal("expected persistence failure")
			}
			if got := app.GetTelemetryTier(); got != initial {
				t.Fatalf("runtime tier = %q, want unchanged %q", got, initial)
			}
			select {
			case event := <-events:
				t.Fatalf("failed save broadcast %s", event)
			default:
			}
			telemetry.Capture("chat.turn_sent", nil)
			want := 0
			if initial == TelemetryTierDetailed {
				want = 1
			}
			if got := recorder.count(); got != want {
				t.Fatalf("capture count = %d, want %d for unchanged tier", got, want)
			}
			files, err := filepath.Glob(filepath.Join(filepath.Dir(telemetryTierPath()), ".telemetry-tier-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary preference files remain: %v (%v)", files, err)
			}
		})
	}
}

func TestTelemetryTierSuccessfulSavePublishesPersistedChoice(t *testing.T) {
	newRecordingTelemetry(t)
	app := &App{eventBroadcaster: NewEventBroadcaster()}
	events := app.eventBroadcaster.AddClient("telemetry-test")
	defer app.eventBroadcaster.RemoveClient("telemetry-test")
	for _, tier := range []string{TelemetryTierOff, TelemetryTierDetailed} {
		if err := app.SetTelemetryTier(tier); err != nil {
			t.Fatal(err)
		}
		select {
		case payload := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != "telemetry:tier-changed" || len(event.Args) != 1 || event.Args[0] != tier {
				t.Fatalf("unexpected event: %+v", event)
			}
		default:
			t.Fatal("missing tier-change event")
		}
		persisted, err := os.ReadFile(telemetryTierPath())
		if err != nil || string(persisted) != tier || app.GetTelemetryTier() != tier {
			t.Fatalf("saved=%q runtime=%q want=%q error=%v", persisted, app.GetTelemetryTier(), tier, err)
		}
	}
}
