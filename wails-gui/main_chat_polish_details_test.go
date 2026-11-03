package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestMainChatUnavailableThreadUpdateIsRoutine(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	item := mainChatProactive{MomentID: "stale", Kind: "decision_due", State: "pending", ActivityOutcomeSource: "missing", Headline: "Old update", Body: "Old update", ActionIDs: []string{"open", "mute_kind"}}
	id := app.postProactiveItem(item, "stale")
	if id == "" {
		t.Fatal("missing fixture")
	}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.LoadStreamItemsPage(main.LocalID, -1, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.ID != id {
			continue
		}
		var projected mainChatProactive
		if err := json.Unmarshal([]byte(row.ToolInput), &projected); err != nil {
			t.Fatal(err)
		}
		if projected.Kind != "thread_update_unavailable" || projected.State != "expired" || projected.Body != "This thread is no longer available." || len(projected.ActionIDs) != 0 || row.RowClass != string(MainChatRowRoutine) {
			t.Fatalf("stale update must join the routine fold: %+v, %+v", row, projected)
		}
		// The app does not show a withdrawn update (polish sweep
		// 2026-10-27), so it is neither waiting nor a routine update.
		overview, err := app.GetMainChatOverview()
		if err != nil || overview.Waiting != 0 || overview.Routine != 0 {
			t.Fatalf("stale update affected the counts: %+v, %v", overview, err)
		}
		waiting, err := app.ListMainChatWaitingItems("", 50)
		if err != nil || len(waiting.Items) != 0 {
			t.Fatalf("stale update stayed in waiting: %+v, %v", waiting, err)
		}
		if mainChatProactiveRowClass(item, now) != MainChatRowWaiting {
			t.Fatal("valid decision stopped waiting")
		}
		return
	}
	t.Fatal("stale update disappeared")
}

func TestMainChatEngagementMutedKindDoesNotWriteCard(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	moment := meetingPrepMoment(event, now, time.Local)
	if err := app.engagement.SetKindEnabled(t.Context(), moment.Kind, false); err != nil {
		t.Fatal(err)
	}
	if !app.postEngagementReport(moment, engagementText{Title: "Meeting starts soon"}) {
		t.Fatal("muted report should acknowledge the skip")
	}
	if len(mainChatProactiveItems(t, app)) != 0 {
		t.Fatal("muted kind wrote a card")
	}
	if err := app.engagement.SetKindEnabled(t.Context(), moment.Kind, true); err != nil {
		t.Fatal(err)
	}
	if !app.postEngagementReport(moment, engagementText{Title: "Meeting starts soon"}) || len(mainChatProactiveItems(t, app)) != 1 {
		t.Fatal("enabled kind did not write a card")
	}
	card := mainChatProactiveItems(t, app)[0]
	card.KindMuted = true
	if mainChatProactiveRowClass(card, time.Now()) != MainChatRowRoutine {
		t.Fatal("retained muted card must be routine")
	}
}

func TestEngagementMutedRetrySkipsChatReport(t *testing.T) {
	rig := newEngagementTestRig(t)
	reports := 0
	rig.svc.deliver.Report = func(engagement.Moment, engagementText) bool { reports++; return true }
	row, err := rig.svc.Submit(t.Context(), engagement.Moment{Kind: engagement.KindMeetingPrep, SubjectID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.store.SetEngagementMomentState(t.Context(), row.ID, storage.EngagementStateSent, "chat report", storage.EngagementMomentRow{Channel: "push", SentAt: rig.clock.UnixMilli(), HoldUntil: rig.clock.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := rig.svc.SetKindEnabled(t.Context(), engagement.KindMeetingPrep, false); err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(t.Context())
	rig.svc.Pass(t.Context())
	stored, err := rig.store.GetEngagementMoment(t.Context(), row.ID)
	if err != nil || stored == nil || stored.Reason == "chat report" || stored.Channel != "push" || stored.SentAt != rig.clock.UnixMilli() || reports != 0 {
		t.Fatalf("muted retry changed delivery or wrote a report: %+v, %v, reports=%d", stored, err, reports)
	}
}

func TestMainChatDisplayStateKeepsExpiryAndResumesMutedCards(t *testing.T) {
	now := time.Now()
	card := mainChatProactive{MomentID: "card", Kind: "meeting_prep", State: "pending", KindMuted: true, ExpiresAt: now.Add(time.Hour).UnixMilli()}
	row := mainChatViewCard(t, "card", "meeting_prep", "pending", now)
	row.ToolInput = mainChatViewPayload(t, card)
	display := card
	display.KindMuted, display.ExpiresAt = false, 0
	if got := mainChatRowClassForDisplay(row, mainChatViewPayload(t, display), now); got != MainChatRowWaiting {
		t.Fatalf("resumed card stayed muted: %s", got)
	}
	card.ExpiresAt = now.Add(-time.Hour).UnixMilli()
	row.ToolInput = mainChatViewPayload(t, card)
	if got := mainChatRowClassForDisplay(row, mainChatViewPayload(t, display), now); got != MainChatRowOther {
		t.Fatalf("display copy revived expired card: %s", got)
	}
	digest := mainChatProactive{Kind: "digest", State: "pending", Items: []mainChatProactive{{Kind: "meeting_prep", State: "pending", KindMuted: true}, {Kind: "thread_update_unavailable", State: "pending"}}}
	if got := mainChatProactiveRowClass(digest, now); got != MainChatRowRoutine {
		t.Fatalf("routine-only digest stayed a card: %s", got)
	}
	digest.Items = append(digest.Items, mainChatProactive{Kind: "meeting_prep", State: "pending"})
	if got := mainChatProactiveRowClass(digest, now); got != MainChatRowWaiting {
		t.Fatalf("digest lost actionable child: %s", got)
	}
}
