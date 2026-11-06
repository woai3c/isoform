package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// R36: each brief the first-week rule held back gets its Chat card once, at
// the time it was ready. The card never pushes, never changes the moment, and
// carries no brief words; a quiet brief or another held kind gets no card.
func TestBriefChatCardBackfillPostsOnceAtReadyTime(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	ctx := context.Background()
	ready := time.Date(2026, 11, 5, 18, 25, 0, 0, time.UTC)
	seed := func(id, reason, kind string) {
		t.Helper()
		if _, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			ID: "moment-" + id, Kind: kind, SubjectID: id, DedupeKey: engagement.DedupeKey(engagement.Kind(kind), id),
			State: storage.EngagementStateSuppressed, Reason: reason, CreatedAt: ready.UnixMilli(),
			ExpiresAt: ready.Add(12 * time.Hour).UnixMilli(), Params: `{"slot":"evening"}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("brief-held", firstWeekDiscoveryReason, "brief_ready")
	seed("brief-quiet", firstWeekDiscoveryReason, "brief_ready")
	seed("brief-off", "kind off", "brief_ready")
	const words = "Secret morning words that must not reach a card"
	briefs := map[string]MobileBriefV1{
		"brief-held":  {BriefID: "brief-held", Slot: mobileBriefSlotEvening, NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: words}}},
		"brief-quiet": {BriefID: "brief-quiet", Slot: mobileBriefSlotEvening, NarrativeStatus: "quiet"},
	}
	briefFor := func(id string) (MobileBriefV1, bool) { b, ok := briefs[id]; return b, ok }

	if !app.runBriefChatCardBackfill(ctx, briefFor) {
		t.Fatal("the first backfill did not complete")
	}
	cards := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want one for the held brief only", len(cards))
	}
	if cards[0].CreatedAt != ready.UnixMilli() {
		t.Fatalf("card time = %d, want the brief's ready time %d", cards[0].CreatedAt, ready.UnixMilli())
	}
	if strings.Contains(cards[0].Content, words) || strings.Contains(cards[0].ToolInput, words) {
		t.Fatalf("a card carries brief words: %+v", cards[0])
	}
	for _, id := range []string{"moment-brief-held", "moment-brief-quiet", "moment-brief-off"} {
		row, err := rig.store.GetEngagementMoment(ctx, id)
		if err != nil || row == nil || row.State != storage.EngagementStateSuppressed {
			t.Fatalf("the backfill changed moment %s: %+v, %v", id, row, err)
		}
	}
	if done, _ := rig.store.GetUIState(ctx, briefChatBackfillKey); done != "true" {
		t.Fatalf("completion not recorded: %q", done)
	}
	// A second launch posts nothing: the flag is set, and the card is keyed by its brief.
	app.runBriefChatCardBackfill(ctx, briefFor)
	if again := mainChatStructuredRows(t, app, mainChatProactiveTool); len(again) != 1 {
		t.Fatalf("a second backfill repeated the card: %d", len(again))
	}
}

// ST86 gap, closed already: a brief the person asked for is stored in the
// on-request slot, and the startup re-announce only takes the morning and
// evening slots, so a manual brief is never re-announced and cannot buzz.
// A scheduled one still is.
func TestStartupReannounceKeepsAManualBriefSilent(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{engagement: rig.svc}
	app.announceBriefStoredBeforeEngagement(MobileBriefV1{BriefID: "brief-manual", Slot: mobileBriefSlotOnRequest, DisplaySlot: mobileBriefSlotEvening})
	app.announceBriefStoredBeforeEngagement(MobileBriefV1{BriefID: "brief-scheduled", Slot: mobileBriefSlotMorning})
	rows := engagementPendingMoments(t, rig)
	if len(rows) != 1 || rows[0].DedupeKey != "brief_ready:brief-scheduled" {
		t.Fatalf("pending moments = %+v, want only the scheduled brief", rows)
	}
	if got := engagementMomentParams(t, rows[0])[engagement.BriefRequestParam]; got != "" {
		t.Fatalf("a scheduled brief re-announced silently: %q", got)
	}
}
