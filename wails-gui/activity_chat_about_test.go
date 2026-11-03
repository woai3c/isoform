package main

import (
	"context"
	"testing"
	"time"
)

// "Chat about this" on an Activity moment prepares a Today chat draft for
// {activity, id}. A moment older than the sources' week-long lookback is read
// by id with the same gates, so every day's rows can be talked about.
func TestTodayChatPreparesAnActivityMomentOlderThanTheLookback(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	at := time.Now().Add(-todayChatActivityLookback - 3*24*time.Hour)
	const title = "Atlas launch review with Alex Chen"
	saveTodayDraftActivity(t, app, "old-moment", title, at, 25)
	ref := TodayChatContextRef{SourceKind: "activity", SourceID: "old-moment"}

	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Prefill.Kind != "activity" || draft.Prefill.Title != title || draft.Prefill.DurationMinutes != 25 {
		t.Fatalf("prefill = %+v", draft.Prefill)
	}
	sent, err := app.buildTodayChatContext(context.Background(), ref)
	if err != nil || sent.Counts.Activities < 1 || sent.Counts != draft.Counts {
		t.Fatalf("Send re-reads the moment: send=%+v preview=%+v %v", sent.Counts, draft.Counts, err)
	}
	// The Day view prepares its rows in one batch when it loads.
	batch := app.PrepareTodayChatDrafts([]TodayChatContextRef{ref})
	if len(batch) != 1 || batch[0].Draft == nil || batch[0].Draft.Prefill.Title != title || batch[0].Draft.Counts != draft.Counts {
		t.Fatalf("batch = %+v", batch)
	}

	// The same gates as recent moments: a private window is never read.
	saveTodayDraftActivity(t, app, "old-private", "Private Browsing", at, 10)
	if _, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: "activity", SourceID: "old-private"}); err == nil {
		t.Fatal("a private moment was prepared for chat")
	}
	if _, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: "activity", SourceID: "missing"}); err == nil {
		t.Fatal("a missing moment was prepared for chat")
	}
	// A summary ref keeps its own meaning: it never falls back to an id read.
	if _, ok := app.todayChatActivityByID(context.Background(), ""); ok {
		t.Fatal("an empty id resolved")
	}
}

func TestTodayChatActivityByIDRespectsObservationPauses(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	at := time.Now().Add(-todayChatActivityLookback - 24*time.Hour)
	saveTodayDraftActivity(t, app, "paused-moment", "Atlas notes", at, 20)
	if _, ok := app.todayChatActivityByID(context.Background(), "paused-moment"); !ok {
		t.Fatal("an ordinary old moment must resolve")
	}
	pause := observationPause{Start: at.Add(-30 * time.Minute).UnixMilli(), End: at.Add(time.Hour).UnixMilli()}
	app.saveObservationPauses([]observationPause{pause})
	if _, ok := app.todayChatActivityByID(context.Background(), "paused-moment"); ok {
		t.Fatal("a moment recorded while observing was paused was read")
	}
}
