package main

import (
	"context"
	"testing"
	"time"
)

// insertTestRun writes a minimal timeline-run row directly (bypassing reconcile)
// so the composer-ready query can be exercised in isolation.
func insertTestRun(t *testing.T, store *ActivityStore, id string, rowID, startMs int64, summaryStatus string, summarizedCount int) {
	t.Helper()
	_, err := store.db.Exec(`
		INSERT INTO activity_timeline_groups
			(id, first_event_row_id, last_event_row_id, app_key, app_name,
			 start_ms, end_ms, active_ms, event_count, session_ids, trigger_kinds,
			 status, summary_status, summary_attempts, next_retry_at, summarized_event_count,
			 created_at, updated_at)
		VALUES (?, ?, ?, 'app:test', 'TestApp', ?, ?, 1000, ?, '[]', '[]',
			'closed', ?, 0, 0, ?, 0, 0)`,
		id, rowID, rowID, startMs, startMs+1000, summarizedCount, summaryStatus, summarizedCount)
	if err != nil {
		t.Fatalf("insert test run %s: %v", id, err)
	}
}

func TestGetComposerReadyRuns_RoutesOncePerSummaryRevision(t *testing.T) {
	store := newTimelineTestStore(t)

	// done summary -> ready; none/generating -> not ready.
	insertTestRun(t, store, "tg_1", 1, 1000, "done", 5)
	insertTestRun(t, store, "tg_2", 2, 2000, "none", 0)
	insertTestRun(t, store, "tg_3", 3, 3000, "generating", 0)

	ready, err := store.GetComposerReadyRuns(10, 0, 0)
	if err != nil {
		t.Fatalf("GetComposerReadyRuns: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "tg_1" {
		t.Fatalf("ready = %v, want [tg_1]", runIDs(ready))
	}

	// Route tg_1 at its summarized count -> no longer ready.
	if err := store.UpsertActivityRunLink("tg_1", "act_1", 5, 100); err != nil {
		t.Fatalf("link: %v", err)
	}
	ready, _ = store.GetComposerReadyRuns(10, 0, 0)
	if len(ready) != 0 {
		t.Fatalf("after routing, ready = %v, want none", runIDs(ready))
	}

	// tg_1 grows (re-summarized with more events) -> ready again.
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET summarized_event_count = 9 WHERE id = 'tg_1'`); err != nil {
		t.Fatalf("grow: %v", err)
	}
	ready, _ = store.GetComposerReadyRuns(10, 0, 0)
	if len(ready) != 1 || ready[0].ID != "tg_1" {
		t.Fatalf("after growth, ready = %v, want [tg_1]", runIDs(ready))
	}
}

func TestGetComposerReadyRuns_TodayFirstThenChronological(t *testing.T) {
	store := newTimelineTestStore(t)
	const day = int64(24 * 60 * 60 * 1000)
	today := int64(10 * day) // arbitrary "today" start
	// Two older runs and two today runs, inserted out of order to prove the
	// ORDER BY (not insertion order) drives the result.
	insertTestRun(t, store, "old_b", 2, today-2*day+500, "done", 1)
	insertTestRun(t, store, "today_b", 4, today+2000, "done", 1)
	insertTestRun(t, store, "old_a", 1, today-3*day+500, "done", 1)
	insertTestRun(t, store, "today_a", 3, today+1000, "done", 1)

	ready, err := store.GetComposerReadyRuns(10, 0, today)
	if err != nil {
		t.Fatalf("GetComposerReadyRuns: %v", err)
	}
	got := runIDs(ready)
	want := []string{"today_a", "today_b", "old_a", "old_b"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want today-first then chronological %v", got, want)
		}
	}
}

func TestActivityCRUDAndSettle(t *testing.T) {
	store := newTimelineTestStore(t)

	a := ActivityRow{
		ID: "act_1", DayStartMs: 86_400_000, Status: activityStatusActive,
		Title: "Ship auth fix", Description: "desc", Category: "build",
		Entities: `[{"type":"repo","name":"api"}]`, RunIDs: `["tg_1"]`,
		ActiveMsByApp: `{"Codex":1000}`, StartMs: 1000, EndMs: 2000,
		CreatedAt: 10, UpdatedAt: 10,
	}
	if err := store.InsertActivity(a); err != nil {
		t.Fatalf("insert: %v", err)
	}

	open, err := store.GetOpenActivitiesForDay(86_400_000)
	if err != nil || len(open) != 1 || open[0].Title != "Ship auth fix" {
		t.Fatalf("open day = %+v err=%v", open, err)
	}

	got, _ := store.GetActivityByID("act_1")
	if got == nil || got.RunIDs != `["tg_1"]` {
		t.Fatalf("get by id = %+v", got)
	}

	// In-range fetch.
	inRange, _ := store.GetActivitiesInRange(0, 0)
	if len(inRange) != 1 {
		t.Fatalf("in range = %d, want 1", len(inRange))
	}

	// Settle a prior day: this bucket is on day 86_400_000, settle anything before day 2.
	n, err := store.SettleActivitiesBeforeDay(2*86_400_000, 999)
	if err != nil || n != 1 {
		t.Fatalf("settle = %d err=%v, want 1", n, err)
	}
	open, _ = store.GetOpenActivitiesForDay(86_400_000)
	if len(open) != 0 {
		t.Fatalf("after settle, open = %d, want 0 (settled excluded)", len(open))
	}
}

func TestSettleActivities_TimeoutBoundariesAndEvidencePreservation(t *testing.T) {
	store := newTimelineTestStore(t)
	const hour = int64(time.Hour / time.Millisecond)
	const now = 200 * hour
	cases := []struct {
		id         string
		status     string
		quietMs    int64
		pausedAt   int64
		wantStatus string
		wantPause  int64
	}{
		{"recent", activityStatusActive, 6*hour - 1, 0, activityStatusActive, 0},
		{"pause_boundary", activityStatusActive, 6 * hour, 0, activityStatusPaused, now},
		{"idle", activityStatusIdle, 7 * hour, 0, activityStatusPaused, now - hour},
		{"paused_recent", activityStatusPaused, 78*hour - 1, now - 72*hour + 1, activityStatusPaused, now - 72*hour + 1},
		{"abandon_boundary", activityStatusPaused, 78 * hour, now - 72*hour, activityStatusAbandoned, now - 72*hour},
		{"downtime", activityStatusActive, 80 * hour, 0, activityStatusAbandoned, now - 74*hour},
		{"settled", activityStatusSettled, 100 * hour, 0, activityStatusSettled, 0},
		{"abandoned", activityStatusAbandoned, 100 * hour, now - 94*hour, activityStatusAbandoned, now - 94*hour},
	}
	for _, tc := range cases {
		row := ActivityRow{
			ID: tc.id, Status: tc.status, StartMs: now - tc.quietMs - hour, EndMs: now - tc.quietMs,
			Title: "Inspect fixture archive", Description: "Observed storage usage", LatestChange: "Checked size",
			RunIDs: `["fixture-run"]`, Entities: `[{"type":"repo","name":"fixture"}]`,
			ActiveMsByApp: `{"Terminal":1000}`, CreatedAt: 123, UpdatedAt: now - 1, PausedAtMs: tc.pausedAt,
		}
		if err := store.InsertActivity(row); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := store.SettleActivities(now)
	if err != nil || changed != 4 {
		t.Fatalf("settle = %d, %v; want 4 changes", changed, err)
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, err := store.GetActivityByID(tc.id)
			if err != nil || got == nil {
				t.Fatalf("get settled activity: %v", err)
			}
			if got.Status != tc.wantStatus || got.PausedAtMs != tc.wantPause {
				t.Fatalf("state = %s paused_at=%d, want %s paused_at=%d", got.Status, got.PausedAtMs, tc.wantStatus, tc.wantPause)
			}
			if got.EndMs != now-tc.quietMs || got.StartMs != now-tc.quietMs-hour || got.CreatedAt != 123 ||
				got.RunIDs != `["fixture-run"]` || got.Title != "Inspect fixture archive" ||
				got.LatestChange != "Checked size" || got.ActiveMsByApp != `{"Terminal":1000}` {
				t.Fatalf("settling changed source evidence: %+v", got)
			}
		})
	}
	if changed, err := store.SettleActivities(now); err != nil || changed != 0 {
		t.Fatalf("repeated sweep = %d, %v; want no changes", changed, err)
	}
}

func TestSettleActivities_LinkedRunGrowthPostponesPause(t *testing.T) {
	store := newTimelineTestStore(t)
	const now = int64(100 * time.Hour / time.Millisecond)
	oldEnd := now - 7*int64(time.Hour/time.Millisecond)
	for i, status := range []string{activityStatusActive, activityStatusPaused} {
		id := "growing_" + status
		row := ActivityRow{ID: id, Status: status, EndMs: oldEnd, UpdatedAt: now - 1000}
		if status == activityStatusPaused {
			row.PausedAtMs = oldEnd + activityPauseAfterMs
		}
		if err := store.InsertActivity(row); err != nil {
			t.Fatal(err)
		}
		insertTestRun(t, store, id+"_run", int64(i+1), now-2000, "done", 1)
		if err := store.UpsertActivityRunLink(id+"_run", id, 1, now-1000); err != nil {
			t.Fatal(err)
		}
	}
	if changed, err := store.SettleActivities(now); err != nil || changed != 1 {
		t.Fatalf("settle growing runs = %d, %v; want paused activity resumed", changed, err)
	}
	for _, status := range []string{activityStatusActive, activityStatusPaused} {
		got, err := store.GetActivityByID("growing_" + status)
		if err != nil || got.Status != activityStatusActive || got.PausedAtMs != 0 {
			t.Fatalf("fresh linked evidence must keep activity open: %+v, %v", got, err)
		}
		if got.EndMs != oldEnd {
			t.Fatal("sweep rewrote the composer's evidence span")
		}
	}
}

func TestSettleActivities_ComposedResumptionResetsPauseClock(t *testing.T) {
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	const start = int64(100 * time.Hour / time.Millisecond)
	const hour = int64(time.Hour / time.Millisecond)
	row := ActivityRow{ID: "resume", Status: activityStatusActive, StartMs: start, EndMs: start, UpdatedAt: start}
	if err := store.InsertActivity(row); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.SettleActivities(start + 6*hour); err != nil || changed != 1 {
		t.Fatalf("initial pause: changed=%d err=%v", changed, err)
	}
	run := testRun("resumed-run", "Chat", start+7*hour, start+7*hour+1000, 1000, 1)
	insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{Title: "Inspect fixture archive"})
	_, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{Title: "Inspect fixture archive"}, composerResponse{
		Route: "join", JoinBucketID: row.ID, Bucket: composerBucketUpdate{Status: activityStatusActive},
	}, run.EndMs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetActivityByID(row.ID)
	if err != nil || got.Status != activityStatusActive || got.PausedAtMs != 0 {
		t.Fatalf("resumed state = %+v, %v", got, err)
	}
	if _, err := store.SettleActivities(run.EndMs + 6*hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleActivities(start + 78*hour); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetActivityByID(row.ID)
	if err != nil || got.Status != activityStatusPaused || got.PausedAtMs != run.EndMs+6*hour {
		t.Fatalf("resumption must reset abandonment deadline: %+v, %v", got, err)
	}
}

func TestSettleActivities_AbandonedExcludedFromResumableQueries(t *testing.T) {
	store := newTimelineTestStore(t)
	for i, status := range []string{activityStatusActive, activityStatusIdle, activityStatusPaused, activityStatusAbandoned, activityStatusSettled} {
		if err := store.InsertActivity(ActivityRow{ID: status, Status: status, DayStartMs: 1000, EndMs: int64(i + 2000)}); err != nil {
			t.Fatal(err)
		}
	}
	for name, query := range map[string]func() ([]ActivityRow, error){
		"day":       func() ([]ActivityRow, error) { return store.GetOpenActivitiesForDay(1000) },
		"unsettled": store.GetUnsettledActivities,
	} {
		rows, err := query()
		if err != nil || len(rows) != 3 {
			t.Fatalf("%s resumable rows = %+v, %v", name, rows, err)
		}
		for _, row := range rows {
			if !activityStatusResumable(row.Status) {
				t.Fatalf("%s included terminal state %s", name, row.Status)
			}
		}
	}
	latest, err := store.GetLatestOpenActivityBefore(1000, 3000)
	if err != nil || latest == nil || latest.Status != activityStatusPaused {
		t.Fatalf("latest resumable = %+v, %v", latest, err)
	}
}

func TestActivityComposerLoop_SettlesWithoutModelRunner(t *testing.T) {
	store := newTimelineTestStore(t)
	now := time.Now().UnixMilli()
	if err := store.InsertActivity(ActivityRow{ID: "no-runner", Status: activityStatusActive, EndMs: now - activityPauseAfterMs - 1000}); err != nil {
		t.Fatal(err)
	}
	tr := &ActivityTriager{app: &App{activity: &activityMonitorState{store: store}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The real loop's initial pass runs before waiting on its ticker. A
	// cancelled context exits that wait immediately; no model is installed.
	tr.runActivityComposerLoop(ctx)
	got, err := store.GetActivityByID("no-runner")
	if err != nil || got == nil || got.Status != activityStatusPaused {
		t.Fatalf("startup loop did not settle without a model: %+v, %v", got, err)
	}
}

func runIDs(runs []ActivityTimelineRunRow) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}
