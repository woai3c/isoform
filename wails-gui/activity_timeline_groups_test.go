package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func newTimelineTestStore(t *testing.T) *ActivityStore {
	t.Helper()
	store, err := NewActivityStore(filepath.Join(t.TempDir(), ".yansu-agent"))
	if err != nil {
		t.Fatalf("NewActivityStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func saveTimelineTestSession(t *testing.T, store *ActivityStore, session ActivitySessionRow, events []activitymon.ActivityEvent) {
	t.Helper()
	if err := store.SaveSession(session, events, nil); err != nil {
		t.Fatalf("SaveSession %s: %v", session.ID, err)
	}
	if _, err := store.ReconcileTimelineRuns(); err != nil {
		t.Fatalf("ReconcileTimelineRuns after %s: %v", session.ID, err)
	}
}

func TestGetActivityTimelineGroups_MergesAcrossSessionBoundaryByApp(t *testing.T) {
	store := newTimelineTestStore(t)

	sessionA := ActivitySessionRow{
		ID:           "session-a",
		StartTime:    1000,
		EndTime:      2000,
		TriggerKind:  "periodic_flush",
		AppNames:     `["Codex","Claude"]`,
		EventCount:   2,
		TriageStatus: "pending",
		CreatedAt:    2000,
	}
	sessionB := ActivitySessionRow{
		ID:           "session-b",
		StartTime:    3000,
		EndTime:      4000,
		TriggerKind:  "idle_transition",
		AppNames:     `["Claude","Codex"]`,
		EventCount:   2,
		TriageStatus: "pending",
		CreatedAt:    4000,
	}

	saveTimelineTestSession(t, store, sessionA, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Send"},
	})
	saveTimelineTestSession(t, store, sessionB, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Reply"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Save"},
	})

	app := &App{activity: &activityMonitorState{store: store}}
	groups, err := app.GetActivityTimelineGroups(20, 0, 0, 0, "")
	if err != nil {
		t.Fatalf("GetActivityTimelineGroups: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("groups len = %d, want 3: %+v", len(groups), groups)
	}

	// Newest-first for the sidebar: Codex(B), Claude(A+B), Codex(A).
	if groups[1].AppName != "Claude" {
		t.Fatalf("middle group app = %q, want Claude", groups[1].AppName)
	}
	if groups[1].EventCount != 2 {
		t.Fatalf("Claude event count = %d, want 2", groups[1].EventCount)
	}
	if len(groups[1].SessionIDs) != 2 || groups[1].SessionIDs[0] != "session-a" || groups[1].SessionIDs[1] != "session-b" {
		t.Fatalf("Claude session IDs = %+v, want [session-a session-b]", groups[1].SessionIDs)
	}
	if len(groups[1].Events) != 2 {
		t.Fatalf("Claude loaded events = %d, want 2", len(groups[1].Events))
	}
	for _, group := range groups {
		if group.SummaryStatus != timelineSummaryStatusNone {
			t.Fatalf("group %s summary status = %q, want none", group.ID, group.SummaryStatus)
		}
	}
}

func TestGetActivityTimelineGroups_FiltersLowSignalSingletonsAndReconnectsAppRuns(t *testing.T) {
	store := newTimelineTestStore(t)

	session := ActivitySessionRow{
		ID:           "session-a",
		StartTime:    1000,
		EndTime:      4000,
		TriggerKind:  "periodic_flush",
		AppNames:     `["Codex","Yansu"]`,
		EventCount:   3,
		TriageStatus: "pending",
		CreatedAt:    4000,
	}
	saveTimelineTestSession(t, store, session, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.yansu", AppName: "Yansu"},
		{Timestamp: 3000, Kind: activitymon.EventKeyboard, AppBundleID: "com.codex", AppName: "Codex", KeyChars: []string{"⌘V"}},
	})

	app := &App{activity: &activityMonitorState{store: store}}
	groups, err := app.GetActivityTimelineGroups(20, 0, 0, 0, "")
	if err != nil {
		t.Fatalf("GetActivityTimelineGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups len = %d, want 1: %+v", len(groups), groups)
	}
	if groups[0].AppName != "Codex" {
		t.Fatalf("group app = %q, want Codex", groups[0].AppName)
	}
	if groups[0].EventCount != 2 {
		t.Fatalf("Codex event count = %d, want 2", groups[0].EventCount)
	}
	if len(groups[0].Events) != 2 {
		t.Fatalf("Codex loaded events = %d, want 2 (folded blip excluded)", len(groups[0].Events))
	}
}

func TestReconcileTimelineRuns_IDStableAcrossIncrementalSaves(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Run"},
	})

	runs, err := store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs len = %d, want 1", len(runs))
	}
	firstID := runs[0].ID
	if runs[0].Status != timelineRunStatusOpen {
		t.Fatalf("run status = %q, want open", runs[0].Status)
	}

	// Same app continues across the periodic_flush session boundary: the run
	// must keep its identity and absorb the new events.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 3000, EndTime: 4000,
		TriggerKind: "periodic_flush", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 4000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Test"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Commit"},
	})

	runs, err = store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs len after continuation = %d, want 1: %+v", len(runs), runs)
	}
	run := runs[0]
	if run.ID != firstID {
		t.Fatalf("run id changed across saves: %q -> %q", firstID, run.ID)
	}
	if run.Status != timelineRunStatusOpen {
		t.Fatalf("continued run status = %q, want open", run.Status)
	}
	if run.EventCount != 4 {
		t.Fatalf("continued run event count = %d, want 4", run.EventCount)
	}
	if run.EndMs != 4000 || run.LastEventRowID <= run.FirstEventRowID {
		t.Fatalf("continued run tail not advanced: end=%d last=%d first=%d", run.EndMs, run.LastEventRowID, run.FirstEventRowID)
	}
	if len(run.SessionIDs) != 2 {
		t.Fatalf("continued run sessions = %+v, want 2 ids", run.SessionIDs)
	}
}

func TestReconcileTimelineRuns_TailBlipFoldsOnContinuation(t *testing.T) {
	store := newTimelineTestStore(t)

	// Batch 1 ends with an unlabeled blip from another app: it must not be
	// persisted as a run.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Codex","Yansu"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.yansu", AppName: "Yansu"},
	})

	runs, err := store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].AppKey != "com.codex" {
		t.Fatalf("runs after batch 1 = %+v, want single Codex run", runs)
	}
	codexID := runs[0].ID

	// Batch 2 continues in the original app: the blip is folded away and the
	// original run reconnects across it.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 3000, EndTime: 3000,
		TriggerKind: "idle_transition", AppNames: `["Codex"]`, EventCount: 1, TriageStatus: "pending", CreatedAt: 3000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventKeyboard, AppBundleID: "com.codex", AppName: "Codex", KeyChars: []string{"x"}},
	})

	runs, err = store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs after fold = %+v, want single Codex run", runs)
	}
	if runs[0].ID != codexID || runs[0].EventCount != 2 || runs[0].EndMs != 3000 {
		t.Fatalf("folded run = %+v, want id %s with 2 events ending 3000", runs[0], codexID)
	}
}

func TestReconcileTimelineRuns_TailBlipBecomesOwnRun(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Codex","Yansu"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.yansu", AppName: "Yansu"},
	})

	// The blip's app keeps producing events: the blip becomes the head of a
	// real run, with the run id minted from the blip's original row id.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 3000, EndTime: 4000,
		TriggerKind: "idle_transition", AppNames: `["Yansu"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 4000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.yansu", AppName: "Yansu", ClickLabel: "Open"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.yansu", AppName: "Yansu", ClickLabel: "Save"},
	})

	runs, err := store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want Codex + Yansu", runs)
	}
	yansu := runs[0]
	if yansu.AppKey != "com.yansu" {
		t.Fatalf("newest run app = %q, want com.yansu", yansu.AppKey)
	}
	if yansu.ID != timelineRunID(yansu.FirstEventRowID) {
		t.Fatalf("yansu run id %q does not match first event row id %d", yansu.ID, yansu.FirstEventRowID)
	}
	if yansu.EventCount != 3 || yansu.StartMs != 2000 {
		t.Fatalf("yansu run = %+v, want 3 events starting at the blip (2000)", yansu)
	}
	if yansu.Status != timelineRunStatusOpen || runs[1].Status != timelineRunStatusClosed {
		t.Fatalf("statuses = %q/%q, want open/closed", yansu.Status, runs[1].Status)
	}
}

func TestReconcileTimelineRuns_DeletesVanishedRunAndItsSummary(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Codex","Claude"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Send"},
	})

	// Simulate an invariant violation: a stale run row whose events no longer
	// derive to it, carrying a cached summary.
	bogus := ActivityTimelineRunRow{
		ID: "tg_999", FirstEventRowID: 999, LastEventRowID: 999,
		AppKey: "com.ghost", AppName: "Ghost",
		StartMs: 5000, EndMs: 5000, ActiveMs: 1000, EventCount: 1,
		SessionIDs: []string{"session-a"}, TriggerKinds: []string{"periodic_flush"},
		Status: timelineRunStatusOpen,
	}
	if err := insertTimelineRun(store.db, bogus, time.Now().UnixMilli()); err != nil {
		t.Fatalf("insert bogus run: %v", err)
	}
	if err := store.UpsertTimelineGroupSummary(
		ActivityTimelineGroup{ID: bogus.ID, AppKey: bogus.AppKey, AppName: bogus.AppName, StartTime: bogus.StartMs, EndTime: bogus.EndMs},
		ActivityTimelineGroupSummary{GroupID: bogus.ID, Title: "Ghost work"},
		"test-model", "prompt", "raw",
	); err != nil {
		t.Fatalf("upsert bogus summary: %v", err)
	}

	// New events force a reconcile whose window covers the bogus anchor.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 6000, EndTime: 6000,
		TriggerKind: "idle_transition", AppNames: `["Claude"]`, EventCount: 1, TriageStatus: "pending", CreatedAt: 6000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 6000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Reply"},
	})

	if run, err := store.GetTimelineRunByID("tg_999"); err != nil || run != nil {
		t.Fatalf("bogus run after reconcile = (%+v, %v), want deleted", run, err)
	}
	summaries, err := store.GetTimelineGroupSummaries([]string{"tg_999"})
	if err != nil {
		t.Fatalf("GetTimelineGroupSummaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("bogus summary survived reconcile: %+v", summaries)
	}
}

func TestDeriveTimelineRuns_ActiveMsGapCap(t *testing.T) {
	items := []timelineRunEvent{
		{RowID: 1, Timestamp: 0, SessionID: "s", AppKey: "com.codex", AppName: "Codex"},
		// 5 minutes idle inside the same app: counted as at most 60s.
		{RowID: 2, Timestamp: 5 * 60 * 1000, SessionID: "s", AppKey: "com.codex", AppName: "Codex"},
		{RowID: 3, Timestamp: 5*60*1000 + 500, SessionID: "s", AppKey: "com.codex", AppName: "Codex"},
	}
	runs := deriveTimelineRuns(items)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want 1", runs)
	}
	if want := timelineGroupActiveGapCapMs + 500; runs[0].ActiveMs != want {
		t.Fatalf("activeMs = %d, want %d (gap capped)", runs[0].ActiveMs, want)
	}

	single := deriveTimelineRuns(items[:1])
	if len(single) != 1 || single[0].ActiveMs != 1000 {
		t.Fatalf("singleton run = %+v, want activeMs floored to 1000", single)
	}
}

func TestClaimTimelineRunForSummary_CASAndReset(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "idle_transition", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Run"},
	})
	runs, err := store.GetLastTimelineRuns(1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("GetLastTimelineRuns: %v %+v", err, runs)
	}
	id := runs[0].ID

	from := []string{timelineSummaryStatusNone, timelineSummaryStatusFailedTransient}
	claimed, err := store.ClaimTimelineRunForSummary(id, from)
	if err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", claimed, err)
	}
	claimed, err = store.ClaimTimelineRunForSummary(id, from)
	if err != nil || claimed {
		t.Fatalf("second claim = (%v, %v), want (false, nil)", claimed, err)
	}

	n, err := store.ResetGeneratingTimelineRuns()
	if err != nil || n != 1 {
		t.Fatalf("ResetGeneratingTimelineRuns = (%d, %v), want (1, nil)", n, err)
	}
	run, err := store.GetTimelineRunByID(id)
	if err != nil || run == nil || run.SummaryStatus != timelineSummaryStatusNone {
		t.Fatalf("run after reset = %+v (%v), want summary status none", run, err)
	}

	if err := store.MarkTimelineRunSummaryDone(id, 2); err != nil {
		t.Fatalf("MarkTimelineRunSummaryDone: %v", err)
	}
	run, _ = store.GetTimelineRunByID(id)
	if run.SummaryStatus != timelineSummaryStatusDone || run.SummarizedEventCount != 2 {
		t.Fatalf("run after done = %+v, want done with 2 summarized events", run)
	}
}

func TestGetTimelineRunsDueForSummary_Eligibility(t *testing.T) {
	store := newTimelineTestStore(t)
	now := time.Now().UnixMilli()
	horizon := timelineAutoSummarizeHorizon.Milliseconds()
	quiet := timelineOpenRunQuiet.Milliseconds()

	insert := func(id string, firstRow int64, endMs int64, status, summaryStatus string, nextRetryAt int64, eventCount, summarized int) {
		t.Helper()
		run := ActivityTimelineRunRow{
			ID: id, FirstEventRowID: firstRow, LastEventRowID: firstRow,
			AppKey: "com.codex", AppName: "Codex",
			StartMs: endMs - 1000, EndMs: endMs, ActiveMs: 1000, EventCount: eventCount,
			SessionIDs: []string{"s"}, TriggerKinds: []string{"idle_transition"},
			Status: status,
		}
		if err := insertTimelineRun(store.db, run, now); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if _, err := store.db.Exec(
			`UPDATE activity_timeline_groups SET summary_status = ?, next_retry_at = ?, summarized_event_count = ? WHERE id = ?`,
			summaryStatus, nextRetryAt, summarized, id); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	insert("tg_1", 1, now-1000, timelineRunStatusClosed, timelineSummaryStatusNone, 0, 3, 0)                               // fresh, in horizon
	insert("tg_2", 2, now-horizon-1000, timelineRunStatusClosed, timelineSummaryStatusNone, 0, 3, 0)                       // out of horizon
	insert("tg_3", 3, now-2000, timelineRunStatusClosed, timelineSummaryStatusFailedTransient, now+60_000, 3, 0)           // retry not due
	insert("tg_4", 4, now-3000, timelineRunStatusClosed, timelineSummaryStatusFailedTransient, now-60_000, 3, 0)           // retry due
	insert("tg_5", 5, now-horizon-5000, timelineRunStatusClosed, timelineSummaryStatusDone, 0, 9, 4)                       // grew after done (no horizon)
	insert("tg_6", 6, now-1000, timelineRunStatusOpen, timelineSummaryStatusNone, 0, 3, 0)                                 // open, not quiet
	insert("tg_7", 7, now-quiet-1000, timelineRunStatusOpen, timelineSummaryStatusNone, 0, 3, 0)                           // open, quiet
	insert("tg_8", 8, now-4000, timelineRunStatusClosed, timelineSummaryStatusGenerating, 0, 3, 0)                         // already claimed
	insert("tg_9", 9, now-5000, timelineRunStatusClosed, timelineSummaryStatusFailedPermanent, 0, 3, 0)                    // parked
	insert("tg_10", 10, now-horizon-9000, timelineRunStatusClosed, timelineSummaryStatusFailedTransient, now-60_000, 3, 0) // retry due, out of horizon (still eligible)

	due, err := store.GetTimelineRunsDueForSummary(now, horizon, quiet, 10)
	if err != nil {
		t.Fatalf("GetTimelineRunsDueForSummary: %v", err)
	}
	got := make(map[string]bool, len(due))
	order := make([]string, 0, len(due))
	for _, run := range due {
		got[run.ID] = true
		order = append(order, run.ID)
	}
	for _, want := range []string{"tg_1", "tg_4", "tg_5", "tg_7", "tg_10"} {
		if !got[want] {
			t.Fatalf("eligible runs = %v, missing %s", order, want)
		}
	}
	for _, reject := range []string{"tg_2", "tg_3", "tg_6", "tg_8", "tg_9"} {
		if got[reject] {
			t.Fatalf("eligible runs = %v, must not include %s", order, reject)
		}
	}
	if len(due) != 5 {
		t.Fatalf("eligible runs = %v, want exactly 5", order)
	}
	// Newest end first.
	for i := 1; i < len(due); i++ {
		if due[i-1].EndMs < due[i].EndMs {
			t.Fatalf("order not newest-first: %v", order)
		}
	}

	limited, err := store.GetTimelineRunsDueForSummary(now, horizon, quiet, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("budget limit = %d runs (%v), want 2", len(limited), err)
	}
}

func TestMigrateActivityTimelineGroups_BackfillAndRekey(t *testing.T) {
	store := newTimelineTestStore(t)

	// Seed sessions/events WITHOUT reconciling — the migration must derive
	// runs from scratch.
	if err := store.SaveSession(ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 4000,
		TriggerKind: "periodic_flush", AppNames: `["Codex","Claude"]`, EventCount: 4, TriageStatus: "pending", CreatedAt: 4000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Send"},
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Reply"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Save"},
	}, nil); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// A summary cached under the legacy derived id, matching the Codex run
	// starting at 1000, plus an orphan that matches nothing.
	if err := store.UpsertTimelineGroupSummary(
		ActivityTimelineGroup{ID: "1000:com.codex:session-a", AppKey: "com.codex", AppName: "Codex", StartTime: 1000, EndTime: 1000},
		ActivityTimelineGroupSummary{GroupID: "1000:com.codex:session-a", Title: "Edit code in Codex"},
		"test-model", "prompt", "raw",
	); err != nil {
		t.Fatalf("upsert legacy summary: %v", err)
	}
	if err := store.UpsertTimelineGroupSummary(
		ActivityTimelineGroup{ID: "555:com.ghost:session-x", AppKey: "com.ghost", AppName: "Ghost", StartTime: 555, EndTime: 555},
		ActivityTimelineGroupSummary{GroupID: "555:com.ghost:session-x", Title: "Orphan"},
		"test-model", "prompt", "raw",
	); err != nil {
		t.Fatalf("upsert orphan summary: %v", err)
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := migrateActivityTimelineGroups(tx); err != nil {
		tx.Rollback()
		t.Fatalf("migrateActivityTimelineGroups: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	runs, err := store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("backfilled runs = %+v, want 3", runs)
	}
	if runs[0].Status != timelineRunStatusOpen || runs[1].Status != timelineRunStatusClosed || runs[2].Status != timelineRunStatusClosed {
		t.Fatalf("backfill statuses = %q/%q/%q, want open/closed/closed", runs[0].Status, runs[1].Status, runs[2].Status)
	}

	codex := runs[2] // oldest: Codex starting at 1000
	if codex.AppKey != "com.codex" || codex.StartMs != 1000 {
		t.Fatalf("oldest run = %+v, want Codex@1000", codex)
	}
	if codex.SummaryStatus != timelineSummaryStatusDone || codex.SummarizedEventCount != codex.EventCount {
		t.Fatalf("re-keyed run = %+v, want summary done covering all events", codex)
	}
	summaries, err := store.GetTimelineGroupSummaries([]string{codex.ID})
	if err != nil {
		t.Fatalf("GetTimelineGroupSummaries: %v", err)
	}
	// Stored titles read back in the past tense (activity review v1.0.5 V4),
	// so the legacy "Edit code in Codex" comes back as "Edited code in Codex".
	if summaries[codex.ID].Title != "Edited code in Codex" {
		t.Fatalf("re-keyed summary = %+v, want legacy title under new id", summaries)
	}

	var orphanCount int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM activity_timeline_group_summaries WHERE group_id = ?`,
		"555:com.ghost:session-x").Scan(&orphanCount); err != nil {
		t.Fatalf("count orphan: %v", err)
	}
	if orphanCount != 0 {
		t.Fatalf("orphan summary survived migration")
	}
}

func TestPurgeTimelineRunsOlderThan(t *testing.T) {
	store := newTimelineTestStore(t)
	now := time.Now().UnixMilli()

	old := ActivityTimelineRunRow{
		ID: "tg_1", FirstEventRowID: 1, LastEventRowID: 1,
		AppKey: "com.codex", AppName: "Codex",
		StartMs: now - 100_000, EndMs: now - 90_000, ActiveMs: 1000, EventCount: 1,
		SessionIDs: []string{"s"}, TriggerKinds: []string{"idle_transition"}, Status: timelineRunStatusClosed,
	}
	fresh := ActivityTimelineRunRow{
		ID: "tg_2", FirstEventRowID: 2, LastEventRowID: 2,
		AppKey: "com.codex", AppName: "Codex",
		StartMs: now - 1000, EndMs: now, ActiveMs: 1000, EventCount: 1,
		SessionIDs: []string{"s"}, TriggerKinds: []string{"idle_transition"}, Status: timelineRunStatusOpen,
	}
	for _, run := range []ActivityTimelineRunRow{old, fresh} {
		if err := insertTimelineRun(store.db, run, now); err != nil {
			t.Fatalf("insert %s: %v", run.ID, err)
		}
	}
	if err := store.UpsertTimelineGroupSummary(
		ActivityTimelineGroup{ID: old.ID, AppKey: old.AppKey, AppName: old.AppName, StartTime: old.StartMs, EndTime: old.EndMs},
		ActivityTimelineGroupSummary{GroupID: old.ID, Title: "Old work"},
		"test-model", "prompt", "raw",
	); err != nil {
		t.Fatalf("upsert old summary: %v", err)
	}

	if err := store.PurgeTimelineRunsOlderThan(now - 50_000); err != nil {
		t.Fatalf("PurgeTimelineRunsOlderThan: %v", err)
	}

	if run, _ := store.GetTimelineRunByID(old.ID); run != nil {
		t.Fatalf("old run survived purge: %+v", run)
	}
	if run, _ := store.GetTimelineRunByID(fresh.ID); run == nil {
		t.Fatalf("fresh run was purged")
	}
	summaries, err := store.GetTimelineGroupSummaries([]string{old.ID})
	if err != nil || len(summaries) != 0 {
		t.Fatalf("old summary after purge = %+v (%v), want gone", summaries, err)
	}
}

func TestGetActivityTimelineGroups_KeywordFiltersRuns(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "idle_transition", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "RefactorParser"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "RunTests"},
	})
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 3000, EndTime: 4000,
		TriggerKind: "idle_transition", AppNames: `["Notes"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 4000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "GroceryList"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "Checkout"},
	})

	app := &App{activity: &activityMonitorState{store: store}}
	groups, err := app.GetActivityTimelineGroups(20, 0, 0, 0, "RefactorParser")
	if err != nil {
		t.Fatalf("GetActivityTimelineGroups keyword: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("keyword groups = %+v, want only the Codex run", groups)
	}
	if groups[0].AppName != "Codex" {
		t.Fatalf("keyword group app = %q, want Codex", groups[0].AppName)
	}

	none, err := app.GetActivityTimelineGroups(20, 0, 0, 0, "NoSuchThing")
	if err != nil {
		t.Fatalf("GetActivityTimelineGroups no-match keyword: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("no-match keyword groups = %+v, want empty", none)
	}
}

func TestGetActivityTimelineGroupsForSessions(t *testing.T) {
	store := newTimelineTestStore(t)

	// Two periodic_flush sessions continuing one Codex run, plus an
	// unrelated Notes session forming its own run.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-a", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 2000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Run"},
	})
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-b", StartTime: 3000, EndTime: 4000,
		TriggerKind: "periodic_flush", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 4000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 3000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Test"},
		{Timestamp: 4000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Commit"},
	})
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-c", StartTime: 5000, EndTime: 6000,
		TriggerKind: "idle_transition", AppNames: `["Notes"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 6000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 5000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "Open"},
		{Timestamp: 6000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "Write"},
	})

	app := &App{activity: &activityMonitorState{store: store}}

	// The non-primary member session must still resolve to its run, and the
	// group payload must carry events so the detail view works on arrival.
	groups, err := app.GetActivityTimelineGroupsForSessions([]string{"session-b"})
	if err != nil {
		t.Fatalf("GetActivityTimelineGroupsForSessions: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want exactly the Codex run", groups)
	}
	if groups[0].AppName != "Codex" || len(groups[0].SessionIDs) != 2 {
		t.Fatalf("group = %+v, want Codex run spanning both sessions", groups[0])
	}
	if groups[0].PrimarySessionID != "session-a" {
		t.Fatalf("primary session = %q, want session-a", groups[0].PrimarySessionID)
	}
	if len(groups[0].Events) != 4 {
		t.Fatalf("events = %d, want 4", len(groups[0].Events))
	}

	// Multiple ids spanning runs dedupe to distinct runs, newest first.
	groups, err = app.GetActivityTimelineGroupsForSessions([]string{"session-a", "session-b", "session-c"})
	if err != nil {
		t.Fatalf("GetActivityTimelineGroupsForSessions multi: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("multi groups = %+v, want Notes + Codex", groups)
	}
	if groups[0].AppName != "Notes" || groups[1].AppName != "Codex" {
		t.Fatalf("multi order = %q/%q, want Notes/Codex", groups[0].AppName, groups[1].AppName)
	}

	// Unknown session ids resolve to an empty list, not an error.
	groups, err = app.GetActivityTimelineGroupsForSessions([]string{"missing"})
	if err != nil {
		t.Fatalf("GetActivityTimelineGroupsForSessions missing: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("missing-session groups = %+v, want empty", groups)
	}
}

// Regression for the 2026-07 frozen-reconcile incident: a delayed session
// commit carrying older-stamped events creates a run whose first_event_row_id
// is past both start_ms-newest runs. Anchoring on start_ms then re-derives
// that run every pass and dies on the first_event_row_id UNIQUE constraint,
// aborting the whole reconcile transaction until the collision derives away
// (~12h of no timeline updates in the wild). Anchors must follow row-id order.
func TestReconcileTimelineRuns_StaleTimestampSessionDoesNotFreezeReconcile(t *testing.T) {
	store := newTimelineTestStore(t)

	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-x", StartTime: 5000, EndTime: 6000,
		TriggerKind: "periodic_flush", AppNames: `["Codex"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 6000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 5000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Edit"},
		{Timestamp: 6000, Kind: activitymon.EventClick, AppBundleID: "com.codex", AppName: "Codex", ClickLabel: "Save"},
	})
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-y", StartTime: 7000, EndTime: 8000,
		TriggerKind: "idle_transition", AppNames: `["Claude"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 8000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 7000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Ask"},
		{Timestamp: 8000, Kind: activitymon.EventClick, AppBundleID: "com.claude", AppName: "Claude", ClickLabel: "Send"},
	})
	// The stale straggler: persisted last (highest event row ids) but stamped
	// before both existing runs.
	saveTimelineTestSession(t, store, ActivitySessionRow{
		ID: "session-z", StartTime: 1000, EndTime: 2000,
		TriggerKind: "periodic_flush", AppNames: `["Notes"]`, EventCount: 2, TriageStatus: "pending", CreatedAt: 9000,
	}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "Open"},
		{Timestamp: 2000, Kind: activitymon.EventClick, AppBundleID: "com.notes", AppName: "Notes", ClickLabel: "Type"},
	})

	// Steady state: repeated reconciles over unchanged data must be no-ops,
	// not UNIQUE-constraint failures or spurious deletions.
	for i := 0; i < 3; i++ {
		if _, err := store.ReconcileTimelineRuns(); err != nil {
			t.Fatalf("steady-state reconcile #%d: %v", i+1, err)
		}
	}
	runs, err := store.GetLastTimelineRuns(10)
	if err != nil {
		t.Fatalf("GetLastTimelineRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d (%+v), want all 3 preserved", len(runs), runs)
	}
	byApp := map[string]bool{}
	for _, run := range runs {
		byApp[run.AppName] = true
	}
	for _, app := range []string{"Codex", "Claude", "Notes"} {
		if !byApp[app] {
			t.Fatalf("run for %s vanished after reconcile: %+v", app, runs)
		}
	}
}

func TestIsImageUnsupportedModelError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Verbatim from the 2026-07 incident: an OpenAI-compatible text-only
		// upstream rejecting the image_url content-part type.
		{"image_url variant", errors.New("execute timeline group summary: anthropic: HTTP 400: {\"error\":{\"message\":\"Failed to deserialize the JSON body into the target type: messages[0]: unknown variant `image_url`, expected `text` at line 1 column 351562\",\"type\":\"invalid_request_error\"}}"), true},
		{"spelled-out no image support", errors.New("HTTP 400: this model does not support image input"), true},
		{"unrelated 400", errors.New("HTTP 400: max_tokens is too large"), false},
		{"image mention without 400", errors.New("HTTP 429: image_url rate limited"), false},
		{"network error", errors.New("dial tcp: i/o timeout"), false},
	}
	for _, tc := range cases {
		if got := isImageUnsupportedModelError(tc.err); got != tc.want {
			t.Errorf("%s: isImageUnsupportedModelError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMigrateTimelineSummaryImageFailureRequeue(t *testing.T) {
	store := newTimelineTestStore(t)
	now := time.Now().UnixMilli()
	horizon := timelineAutoSummarizeHorizon.Milliseconds()

	// A run parked as failed_permanent well outside the auto-summarize horizon
	// (the incident's shape: attempts burned on the image-rejecting upstream).
	parked := ActivityTimelineRunRow{
		ID: "tg_1", FirstEventRowID: 1, LastEventRowID: 1,
		AppKey: "com.codex", AppName: "Codex",
		StartMs: now - horizon - 2000, EndMs: now - horizon - 1000, ActiveMs: 1000, EventCount: 3,
		SessionIDs: []string{"s"}, TriggerKinds: []string{"idle_transition"},
		Status: timelineRunStatusClosed,
	}
	if err := insertTimelineRun(store.db, parked, now); err != nil {
		t.Fatalf("insert parked run: %v", err)
	}
	if _, err := store.db.Exec(
		`UPDATE activity_timeline_groups SET summary_status = ?, summary_attempts = 4, next_retry_at = ? WHERE id = ?`,
		timelineSummaryStatusFailedPermanent, now+3_600_000, parked.ID); err != nil {
		t.Fatalf("park run: %v", err)
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := migrateTimelineSummaryImageFailureRequeue(tx); err != nil {
		t.Fatalf("migrateTimelineSummaryImageFailureRequeue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	runs, err := store.GetLastTimelineRuns(1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("GetLastTimelineRuns: %v %+v", err, runs)
	}
	if runs[0].SummaryStatus != timelineSummaryStatusFailedTransient || runs[0].SummaryAttempts != 0 || runs[0].NextRetryAt != 0 {
		t.Fatalf("requeued run = status %q attempts %d retry %d, want failed_transient/0/0",
			runs[0].SummaryStatus, runs[0].SummaryAttempts, runs[0].NextRetryAt)
	}

	// The requeued run must actually reach the worker: retry-due runs are
	// eligible even though the run ended before the auto-summarize horizon.
	due, err := store.GetTimelineRunsDueForSummary(now, horizon, timelineOpenRunQuiet.Milliseconds(), 10)
	if err != nil {
		t.Fatalf("GetTimelineRunsDueForSummary: %v", err)
	}
	if len(due) != 1 || due[0].ID != parked.ID {
		t.Fatalf("due = %+v, want the requeued run", due)
	}
}

func TestTimelineRunIsWorkDropsSystemRuns(t *testing.T) {
	state := ActivityTimelineEvent{ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventSessionState}}
	typing := ActivityTimelineEvent{ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventAppSwitch}}
	cases := []struct {
		name   string
		run    ActivityTimelineRunRow
		events []ActivityTimelineEvent
		want   bool
	}{
		{"work", ActivityTimelineRunRow{AppKey: "com.microsoft.VSCode", AppName: "VS Code"}, []ActivityTimelineEvent{typing}, true},
		{"unknown app", ActivityTimelineRunRow{AppKey: "unknown", AppName: "Unknown"}, []ActivityTimelineEvent{typing}, false},
		{"loginwindow", ActivityTimelineRunRow{AppKey: "com.apple.loginwindow", AppName: "loginwindow"}, []ActivityTimelineEvent{typing}, false},
		{"only state changes", ActivityTimelineRunRow{AppKey: "com.microsoft.VSCode", AppName: "VS Code"}, []ActivityTimelineEvent{state, state}, false},
		{"state change inside work", ActivityTimelineRunRow{AppKey: "com.microsoft.VSCode", AppName: "VS Code"}, []ActivityTimelineEvent{state, typing}, true},
	}
	for _, tc := range cases {
		if got := timelineRunIsWork(tc.run, tc.events); got != tc.want {
			t.Errorf("%s: timelineRunIsWork = %v, want %v", tc.name, got, tc.want)
		}
	}
}
