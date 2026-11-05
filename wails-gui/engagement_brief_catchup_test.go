package main

import (
	"context"
	"testing"

	"wails-gui/internal/engagement"
)

// A brief stored while the Engagement service was not running is announced once
// the service starts: its brief-ready moment exists afterwards (R36).
func TestBriefStoredBeforeEngagementIsAnnouncedWhenItStarts(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	brief := MobileBriefV1{BriefID: "2026-11-05-morning", Slot: mobileBriefSlotMorning, NarrativeStatus: "ok", LocalDate: "2026-11-05"}
	// The return value says whether the caller must still deliver: a moment the
	// Engagement service accepted is delivered by it, so the answer is false.
	app.announceBriefStoredBeforeEngagement(brief)
	key := engagement.DedupeKey(engagement.KindBriefReady, brief.BriefID)
	exists, err := rig.store.EngagementMomentKeyExists(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("no brief-ready moment exists for the brief stored before the service started")
	}
	// Announcing again is harmless: the same moment answers, and no second one is made.
	open, err := rig.store.GetOpenEngagementMomentByKey(context.Background(), key)
	if err != nil || open == nil {
		t.Fatalf("no open moment for the brief before the repeat: %v", err)
	}
	app.announceBriefStoredBeforeEngagement(brief)
	again, err := rig.store.GetOpenEngagementMomentByKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil && again.ID != open.ID {
		t.Fatalf("a repeat announcement made a second moment: %s then %s", open.ID, again.ID)
	}
}

// A brief on demand, or one with no slot, is not a scheduled announcement.
func TestBriefCatchUpSkipsOnDemandBriefs(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	if app.announceBriefStoredBeforeEngagement(MobileBriefV1{BriefID: "2026-11-05-on_request-100000", Slot: mobileBriefSlotOnRequest}) {
		t.Fatal("an on-request brief was announced by the catch-up")
	}
}
