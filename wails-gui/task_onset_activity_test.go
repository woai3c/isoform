package main

import (
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestBuildTaskOnsetEvidenceFromEventsUsesOnlyPrefixAndRedactsPII(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Timestamp:   5_000,
			Kind:        activitymon.EventClick,
			AppName:     "Mail",
			WindowTitle: "Sent",
			ClickLabel:  "Send final result",
		},
		{
			Timestamp:   2_000,
			Kind:        activitymon.EventKeyboard,
			AppName:     "Notes",
			WindowTitle: "Incident",
			TypedText:   "Contact person@example.com about the outage",
		},
		{
			Timestamp:   1_500,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Notes",
			WindowTitle: "Incident",
		},
	}

	evidence := buildTaskOnsetEvidenceFromEvents(events, 1_000, 3_000)
	if len(evidence) != 2 {
		t.Fatalf("evidence=%d, want 2: %+v", len(evidence), evidence)
	}
	if evidence[0].OccurredAt != 1_500 || evidence[1].OccurredAt != 2_000 {
		t.Fatalf("evidence order=%+v", evidence)
	}
	combined := evidence[0].Summary + "\n" + evidence[1].Summary
	if strings.Contains(combined, "person@example.com") {
		t.Fatalf("evidence retained PII: %s", combined)
	}
	if strings.Contains(combined, "Send final result") {
		t.Fatalf("evidence retained post-cutoff result: %s", combined)
	}
}

func TestBuildTaskOnsetEvidenceFromEventsPreservesOverlappingSignals(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{
			Timestamp:   1_000,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Terminal",
			WindowTitle: "incident",
		},
		{
			Timestamp:   1_500,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Mail",
			WindowTitle: "customer",
		},
		{
			Timestamp:   2_000,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Terminal",
			WindowTitle: "incident",
		},
	}

	evidence := buildTaskOnsetEvidenceFromEvents(events, 1_000, 2_500)
	if len(evidence) != 3 {
		t.Fatalf("evidence=%d, want all interleaved signals: %+v", len(evidence), evidence)
	}
}
