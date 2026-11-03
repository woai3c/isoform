package main

import (
	"reflect"
	"testing"

	"github.com/posthog/posthog-go"
)

func TestTasksTelemetryProductContract(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		input   map[string]any
		want    map[string]any
		allowed bool
	}{
		{"tasks row", "tasks.action", map[string]any{"action": "T16", "kind": "do", "outcome": "stopped", "ms_to_result": 40, "title": "private title"}, map[string]any{"action": "T16", "kind": "do", "outcome": "stopped", "ms_to_result": 40}, true},
		{"schedule row", "tasks.action", map[string]any{"action": "C03", "kind": "do", "outcome": "failed", "ms_to_result": 3}, map[string]any{"action": "C03", "kind": "do", "outcome": "failed", "ms_to_result": 3}, true},
		{"all done row", "tasks.action", map[string]any{"action": "D03", "kind": "do", "outcome": "done", "ms_to_result": 0}, map[string]any{"action": "D03", "kind": "do", "outcome": "done", "ms_to_result": 0}, true},
		{"entry row", "tasks.action", map[string]any{"action": "E03", "kind": "open", "outcome": "opened", "ms_to_result": 0}, map[string]any{"action": "E03", "kind": "open", "outcome": "opened", "ms_to_result": 0}, true},
		{"section", "tasks.section_seen", map[string]any{"section": "scheduled", "rows": 4, "name": "Weekly update"}, map[string]any{"section": "scheduled", "rows": 4}, true},
		{"beyond the map", "tasks.action", map[string]any{"action": "T32", "kind": "do", "outcome": "done", "ms_to_result": 1}, nil, false},
		{"zero row", "tasks.action", map[string]any{"action": "C00", "kind": "do", "outcome": "done", "ms_to_result": 1}, nil, false},
		{"phone id", "tasks.action", map[string]any{"action": "P036", "kind": "do", "outcome": "done", "ms_to_result": 1}, nil, false},
		{"name as id", "tasks.action", map[string]any{"action": "Weekly update", "kind": "do", "outcome": "done", "ms_to_result": 1}, nil, false},
		{"bad kind", "tasks.action", map[string]any{"action": "T16", "kind": "private", "outcome": "done", "ms_to_result": 1}, nil, false},
		{"bad outcome", "tasks.action", map[string]any{"action": "T16", "kind": "do", "outcome": "private", "ms_to_result": 1}, nil, false},
		{"negative time", "tasks.action", map[string]any{"action": "T16", "kind": "do", "outcome": "done", "ms_to_result": -1}, nil, false},
		{"bad section", "tasks.section_seen", map[string]any{"section": "private", "rows": 1}, nil, false},
		{"bad rows", "tasks.section_seen", map[string]any{"section": "running", "rows": "3"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := productTelemetryProperties(tt.event, tt.input)
			if ok != tt.allowed || (ok && !reflect.DeepEqual(map[string]any(got), tt.want)) {
				t.Fatalf("got %#v, allowed %v; want %#v, allowed %v", got, ok, tt.want, tt.allowed)
			}
		})
	}
}

func TestRecordTasksTelemetry(t *testing.T) {
	telemetry, client := newRecordingTelemetry(t)
	app := &App{telemetry: telemetry}
	app.RecordTasksAction("T16", "do", "stopped", 12)
	app.RecordTasksAction("C08", "do", "failed", 3)
	app.RecordTasksSectionSeen("done_this_week", 2)
	app.RecordTasksAction("Weekly update", "do", "done", 1)
	app.RecordTasksSectionSeen("private", 1)
	if got := client.count(); got != 3 {
		t.Fatalf("recorded %d events, want 3 valid events", got)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	events := []string{}
	for _, message := range client.messages {
		capture := message.(posthog.Capture)
		events = append(events, capture.Event)
		for key := range capture.Properties {
			switch key {
			case "action", "kind", "outcome", "ms_to_result", "section", "rows",
				"app_version", "flavor", "build_channel", "os", "os_version", "locale", "telemetry_tier", "$process_person_profile", "$geoip_disable":
			default:
				t.Fatalf("%s carried %q", capture.Event, key)
			}
		}
	}
	if want := []string{"tasks.action", "tasks.action", "tasks.section_seen"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events %v, want %v", events, want)
	}
}

func TestTasksTelemetryRespectsConsent(t *testing.T) {
	telemetry, client := newRecordingTelemetry(t)
	app := &App{telemetry: telemetry}
	if err := app.SetTelemetryTier(TelemetryTierOff); err != nil {
		t.Fatal(err)
	}
	app.RecordTasksAction("T16", "do", "stopped", 1)
	app.RecordTasksSectionSeen("running", 1)
	telemetry.Capture("tasks.action", map[string]any{"action": "C03", "kind": "do", "outcome": "done", "ms_to_result": 1})
	if got := client.count(); got != 0 {
		t.Fatalf("consent off: captured %d events", got)
	}
}
