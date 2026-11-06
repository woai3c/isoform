package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// R40: one Chat card per brief. Several moments for one brief (a re-make, a
// re-send, the first-week backfill) leave one card, at the brief's first ready
// time, and each other brief keeps its own card.
func TestBriefChatCardOnePerBrief(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	ctx := context.Background()
	first := time.Date(2026, 11, 5, 6, 51, 0, 0, time.UTC)
	seed := func(id, brief string, at time.Time) {
		t.Helper()
		if _, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			ID: id, Kind: "brief_ready", SubjectID: brief, DedupeKey: engagement.DedupeKey(engagement.KindBriefReady, brief) + "/" + id,
			State: storage.EngagementStateSuppressed, Reason: firstWeekDiscoveryReason, CreatedAt: at.UnixMilli(),
			ExpiresAt: at.Add(12 * time.Hour).UnixMilli(), Params: `{"slot":"morning"}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("moment-1", "2026-11-05-morning", first)
	seed("moment-2", "2026-11-05-morning", first.Add(3*time.Hour))
	seed("moment-3", "2026-11-05-morning", first.Add(8*time.Hour+27*time.Minute))
	seed("moment-4", "2026-11-05-evening", first.Add(11*time.Hour))
	briefs := map[string]MobileBriefV1{
		"2026-11-05-morning": {BriefID: "2026-11-05-morning", Slot: mobileBriefSlotMorning, NarrativeStatus: "ok"},
		"2026-11-05-evening": {BriefID: "2026-11-05-evening", Slot: mobileBriefSlotEvening, NarrativeStatus: "ok"},
	}
	briefFor := func(id string) (MobileBriefV1, bool) { b, ok := briefs[id]; return b, ok }

	if !app.runBriefChatCardBackfill(ctx, briefFor) {
		t.Fatal("the backfill did not complete")
	}
	cards := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want one per brief (2)", len(cards))
	}
	if cards[0].CreatedAt != first.UnixMilli() {
		t.Fatalf("morning card time = %d, want its first ready time %d", cards[0].CreatedAt, first.UnixMilli())
	}
}

// R40 repair: a store that already holds several cards for one brief keeps the
// earliest card of each brief, once. A second run changes nothing.
func TestBriefChatCardRepairKeepsOneCardPerBrief(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	ctx := context.Background()
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 6, 6, 52, 0, 0, time.UTC)
	post := func(momentID, brief string, when time.Time) {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"kind": "brief_ready", "briefId": brief, "momentId": momentID})
		app.appendMainChatItem(ctx, main.LocalID, storage.StreamItemRow{ID: "proactive:" + momentID, Kind: "system",
			ToolName: mainChatProactiveTool, ToolInput: string(payload), CreatedAt: when.UnixMilli()}, mainChatSourceEngagement)
	}
	post("m1", "2026-11-06-morning", at)
	post("m2", "2026-11-06-morning", at.Add(3*time.Hour))
	post("m3", "2026-11-06-morning", at.Add(4*time.Hour))
	post("m4", "2026-11-06-evening", at.Add(12*time.Hour))
	if got := len(mainChatStructuredRows(t, app, mainChatProactiveTool)); got != 4 {
		t.Fatalf("seeded cards = %d, want 4", got)
	}

	if !app.dedupeBriefChatCards(ctx) {
		t.Fatal("the repair did not complete")
	}
	cards := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(cards) != 2 || cards[0].ID != "proactive:m1" || cards[1].ID != "proactive:m4" {
		t.Fatalf("after repair = %+v, want the earliest card of each brief", cards)
	}
	if done, _ := rig.store.GetUIState(ctx, briefChatCardDedupeKey); done != "true" {
		t.Fatalf("repair completion not recorded: %q", done)
	}
	app.dedupeBriefChatCards(ctx)
	if again := mainChatStructuredRows(t, app, mainChatProactiveTool); len(again) != 2 {
		t.Fatalf("a second repair changed the cards: %d", len(again))
	}
}
