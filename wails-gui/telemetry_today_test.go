package main

import (
	"context"
	"reflect"
	"testing"
)

func TestTodayTelemetryProductContract(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		input   map[string]any
		want    map[string]any
		allowed bool
	}{
		{"desktop action", "today.action", map[string]any{"action": "T10", "kind": "ask", "outcome": "opened", "ms_to_result": 120, "title": "private title"}, map[string]any{"action": "T10", "kind": "ask", "outcome": "opened", "ms_to_result": 120}, true},
		{"phone action", "phone_today.action", map[string]any{"action": "P036", "kind": "ask", "outcome": "sent", "ms_to_result": 4}, map[string]any{"action": "P036", "kind": "ask", "outcome": "sent", "ms_to_result": 4}, true},
		{"section", "today.section_seen", map[string]any{"section": "needs_you", "rows": 3, "name": "private name"}, map[string]any{"section": "needs_you", "rows": 3}, true},
		{"typed turn", "chat.turn_sent", nil, map[string]any{"count": 1}, true},
		{"chip turn", "chat.turn_sent", map[string]any{"source": map[string]any{"surface": "today", "action": "T10", "text": "private words"}}, map[string]any{"count": 1, "source": map[string]any{"surface": "today", "action": "T10"}}, true},
		{"bad action", "today.action", map[string]any{"action": "Priya", "kind": "ask", "outcome": "sent", "ms_to_result": 1}, nil, false},
		{"wrong surface", "phone_today.action", map[string]any{"action": "T10", "kind": "ask", "outcome": "sent", "ms_to_result": 1}, nil, false},
		{"bad kind", "today.action", map[string]any{"action": "T10", "kind": "private", "outcome": "sent", "ms_to_result": 1}, nil, false},
		{"negative time", "today.action", map[string]any{"action": "T10", "kind": "ask", "outcome": "sent", "ms_to_result": -1}, nil, false},
		{"bad section", "today.section_seen", map[string]any{"section": "private", "rows": 1}, nil, false},
		{"invalid source omitted", "chat.turn_sent", map[string]any{"source": map[string]any{"surface": "private", "action": "T10"}}, map[string]any{"count": 1}, true},
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

func TestTodayTelemetryRespectsConsent(t *testing.T) {
	telemetry, client := newRecordingTelemetry(t)
	app := &App{telemetry: telemetry}
	if err := app.SetTelemetryTier(TelemetryTierOff); err != nil {
		t.Fatal(err)
	}
	telemetry.Capture("today.action", map[string]any{"action": "T10", "kind": "ask", "outcome": "sent", "ms_to_result": 1})
	telemetry.Capture("today.section_seen", map[string]any{"section": "before_you_talk", "rows": 1})
	telemetry.Capture("chat.turn_sent", map[string]any{"source": map[string]any{"surface": "today", "action": "T10"}})
	telemetry.Capture("phone_today.action", map[string]any{"action": "P036", "kind": "ask", "outcome": "sent", "ms_to_result": 1})
	if got := client.count(); got != 0 {
		t.Fatalf("consent off: captured %d events", got)
	}
}

func TestRecordTodayTelemetry(t *testing.T) {
	telemetry, client := newRecordingTelemetry(t)
	app := &App{telemetry: telemetry}
	app.RecordTodayAction("today", "T10", "ask", "opened", 10)
	app.RecordTodayAction("phone_today", "P036", "ask", "sent", 12)
	app.RecordTodaySectionSeen("before_you_talk", 1)
	app.RecordTodayAction("today", "private name", "ask", "sent", 1)
	app.RecordTodayAction("private surface", "T10", "ask", "sent", 1)
	if got := client.count(); got != 3 {
		t.Fatalf("recorded %d events, want 3 valid events", got)
	}
}

func TestTodayTurnSourceIsPerTurnAndContentFree(t *testing.T) {
	first := &todayTurnSource{}
	second := &todayTurnSource{}
	ctx := context.WithValue(context.Background(), todayTurnSourceKey{}, first)
	setTodayTurnSource(ctx, "private brief words")
	if got := todayTurnProperties(first); got != nil {
		t.Fatalf("invalid source: %#v", got)
	}
	setTodayTurnSource(ctx, "T10")
	want := map[string]any{"source": map[string]any{"surface": "today", "action": "T10"}}
	if got := todayTurnProperties(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("source: %#v", got)
	}
	if got := todayTurnProperties(second); got != nil {
		t.Fatalf("typed turn acquired another turn's source: %#v", got)
	}
}
