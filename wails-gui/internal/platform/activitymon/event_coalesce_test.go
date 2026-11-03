package activitymon

import (
	"testing"
	"time"
)

func TestCoalesceStateEventsKeepsLatestStatePerWindow(t *testing.T) {
	events := []ActivityEvent{
		{Timestamp: 1000, Kind: EventAppSwitch, AppName: "A"},
		{Timestamp: 1100, Kind: EventFocus, AppName: "A", FocusLabel: "first"},
		{Timestamp: 1200, Kind: EventAppSwitch, AppName: "B"},
		{Timestamp: 1300, Kind: EventClick, AppName: "B"},
		{Timestamp: 1400, Kind: EventFocus, AppName: "B", FocusLabel: "second"},
		{Timestamp: 1700, Kind: EventFocus, AppName: "B", FocusLabel: "third"},
	}

	got := coalesceStateEvents(events, 250*time.Millisecond)

	if len(got) != 4 {
		t.Fatalf("len(coalesced) = %d, want 4: %#v", len(got), got)
	}
	if got[0].AppName != "B" || got[0].Kind != EventAppSwitch {
		t.Fatalf("first coalesced state = %#v, want latest state in first window", got[0])
	}
	if got[1].Kind != EventClick {
		t.Fatalf("click event was not preserved at index 1: %#v", got)
	}
	if got[2].FocusLabel != "second" {
		t.Fatalf("second state = %#v, want focus label second", got[2])
	}
	if got[3].FocusLabel != "third" {
		t.Fatalf("third state = %#v, want focus label third", got[3])
	}
}

func TestCoalesceStateEventsPreservesInputEvents(t *testing.T) {
	events := []ActivityEvent{
		{Timestamp: 1000, Kind: EventKeyboard, KeyChars: []string{"a"}},
		{Timestamp: 1010, Kind: EventKeyboard, KeyChars: []string{"b"}},
		{Timestamp: 1020, Kind: EventClick, ClickLabel: "Save"},
	}

	got := coalesceStateEvents(events, 250*time.Millisecond)

	if len(got) != len(events) {
		t.Fatalf("len(coalesced) = %d, want %d", len(got), len(events))
	}
	for i := range events {
		if got[i].Kind != events[i].Kind {
			t.Fatalf("event %d kind = %q, want %q", i, got[i].Kind, events[i].Kind)
		}
	}
}
