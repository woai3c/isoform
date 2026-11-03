package main

import (
	"testing"
	"time"
	"wails-gui/internal/platform/activitymon"
)

// The walk-through (activity review v1.0.0 P0 #2): "Isoform · DeepSeek
// Harness" began late yesterday and was 1h 10m of work today, but drew one
// bar from 12 AM to 2 PM and a "Picked back up · 12:00 AM". Its moment spans
// the night; its run holds three sittings in three capture sessions.
func seedCarriedThreadWalkthrough(t *testing.T) (*App, time.Time, time.Time, ActivityRow, ActivityRow) {
	t.Helper()
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	yesterday := today.AddDate(0, 0, -3)
	day := yesterday.AddDate(0, 0, 1)
	start, end := at(yesterday, 23, 30), at(day, 14, 0)
	row := seedThreadMoment(t, app, threadFixtureMoment{id: "harness", title: "Isoform · DeepSeek Harness", app: "Code", bundle: "com.example.code", start: start, end: end})
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id='session-harness'`); err != nil {
		t.Fatal(err)
	}
	sittings := []struct {
		id       string
		from, to time.Time
	}{
		{"harness-night", start, at(yesterday, 23, 50)},
		{"harness-morning", at(day, 9, 0), at(day, 9, 40)},
		{"harness-afternoon", at(day, 13, 30), end},
	}
	var sessionIDs []string
	var count int
	for _, sitting := range sittings {
		var events []activitymon.ActivityEvent
		for at := sitting.from; !at.After(sitting.to); at = at.Add(time.Minute) {
			events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: at.UnixMilli(), AppName: "Code", AppBundleID: "com.example.code"})
		}
		if err := app.activity.store.SaveSession(ActivitySessionRow{ID: sitting.id, StartTime: sitting.from.UnixMilli(), EndTime: sitting.to.UnixMilli(), AppNames: `["Code"]`, TriageStatus: "done"}, events, nil); err != nil {
			t.Fatal(err)
		}
		sessionIDs = append(sessionIDs, sitting.id)
		count += len(events)
	}
	var firstID, lastID int64
	if err := app.activity.store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id LIKE 'harness-%'`).Scan(&firstID, &lastID); err != nil {
		t.Fatal(err)
	}
	// 20 + 40 + 30 minutes of one-minute steps, plus the two capped away gaps.
	active := (92 * time.Minute).Milliseconds()
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=?,event_count=?,first_event_row_id=?,last_event_row_id=?,session_ids=? WHERE id='run-harness'`,
		active, count, firstID, lastID, composeJSON(sessionIDs)); err != nil {
		t.Fatal(err)
	}
	row.ActiveMsByApp = `{"Code":5520000}`
	if err := app.activity.store.UpdateActivity(row); err != nil {
		t.Fatal(err)
	}
	feed := seedThreadMoment(t, app, threadFixtureMoment{id: "feed", title: "Browse the GitHub feed", app: "Safari", start: at(day, 10, 0), end: at(day, 10, 30)})
	for _, id := range []string{row.ID, feed.ID} {
		if _, err := app.MoveActivityToThread(id, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []time.Time{yesterday, day} {
		if _, err := app.runActivityThreadPass(t.Context(), d.UnixMilli(), false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return app, yesterday, day, row, feed
}

func TestActivityThreadCarriedThreadStartsAtItsFirstStretchToday(t *testing.T) {
	app, yesterday, day, row, feed := seedCarriedThreadWalkthrough(t)
	dayMs := day.UnixMilli()
	got, err := app.GetDayThreads(dayMs, activityThreadDayEnd(dayMs))
	if err != nil {
		t.Fatal(err)
	}
	var harness, github *ActivityThreadView
	for i := range got.Threads {
		switch got.Threads[i].ID {
		case threadOf(t, app, row.ID):
			harness = &got.Threads[i]
		case threadOf(t, app, feed.ID):
			github = &got.Threads[i]
		}
	}
	if harness == nil || github == nil {
		t.Fatalf("threads = %+v", got.Threads)
	}
	// The thread still says where it began, so the row reads "From yesterday".
	if harness.StartMs >= dayMs {
		t.Fatalf("carried thread lost its start: %d", harness.StartMs)
	}
	if len(harness.Stretches) != 2 || len(harness.Gaps) != 1 {
		t.Fatalf("stretches=%+v gaps=%+v", harness.Stretches, harness.Gaps)
	}
	morning, afternoon := harness.Stretches[0], harness.Stretches[1]
	// Nothing starts at midnight: the first bar is the 9 AM sitting (the
	// capped gap minute before its first event belongs to it).
	if morning.Start == dayMs || morning.Start < at(day, 8, 59).UnixMilli() || morning.End != at(day, 9, 40).UnixMilli() {
		t.Fatalf("morning bar = %s-%s", time.UnixMilli(morning.Start), time.UnixMilli(morning.End))
	}
	if afternoon.Start < at(day, 13, 29).UnixMilli() || afternoon.End != at(day, 14, 0).UnixMilli() {
		t.Fatalf("afternoon bar = %s-%s", time.UnixMilli(afternoon.Start), time.UnixMilli(afternoon.End))
	}
	gap := harness.Gaps[0]
	if gap.Start != morning.End || gap.End != afternoon.Start || gap.ThreadID != github.ID || gap.Idle {
		t.Fatalf("the real interruption is the GitHub feed: %+v", gap)
	}
	// Bars carry the day's active time, and the stretches add up to the row.
	if morning.ActiveMs+afternoon.ActiveMs != harness.ActiveMs || harness.ActiveMs < (70*time.Minute).Milliseconds() || harness.ActiveMs > (73*time.Minute).Milliseconds() {
		t.Fatalf("active: row=%d stretches=%d+%d", harness.ActiveMs, morning.ActiveMs, afternoon.ActiveMs)
	}
	for _, stretch := range harness.Stretches {
		if stretch.End-stretch.Start > (45 * time.Minute).Milliseconds() {
			t.Fatalf("a bar spans idle time: %+v", stretch)
		}
	}

	// Yesterday's row ends with its last sitting, not at midnight.
	yesterdayMs := yesterday.UnixMilli()
	before, err := app.GetDayThreads(yesterdayMs, activityThreadDayEnd(yesterdayMs))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Threads) != 1 || len(before.Threads[0].Stretches) != 1 || before.Threads[0].Stretches[0].End != at(yesterday, 23, 50).UnixMilli() {
		t.Fatalf("yesterday = %+v", before.Threads)
	}
}

// Activity review v1.0.1: idle inside one moment splits its bar, but only
// other work or a locked or sleeping computer is an interruption.
func TestActivityThreadIdleInsideAMomentEndsItsStretch(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "locked"}[locked], func(t *testing.T) {
			testActivityThreadIdleInsideAMoment(t, locked)
		})
	}
}

func testActivityThreadIdleInsideAMoment(t *testing.T, locked bool) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	day = day.AddDate(0, 0, -1)
	dayMs = day.UnixMilli()
	row := seedThreadMoment(t, app, threadFixtureMoment{id: "launch", title: "Launch film storyboard", app: "Keynote", bundle: "com.apple.iwork.keynote", start: at(day, 11, 0), end: at(day, 12, 30)})
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id='session-launch'`); err != nil {
		t.Fatal(err)
	}
	var events []activitymon.ActivityEvent
	for _, span := range [][2]time.Time{{at(day, 11, 0), at(day, 11, 20)}, {at(day, 12, 10), at(day, 12, 30)}} {
		for at := span[0]; !at.After(span[1]); at = at.Add(time.Minute) {
			events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: at.UnixMilli(), AppName: "Keynote", AppBundleID: "com.apple.iwork.keynote"})
		}
	}
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "session-launch", StartTime: at(day, 11, 0).UnixMilli(), EndTime: at(day, 12, 30).UnixMilli(), AppNames: `["Keynote"]`, TriageStatus: "done"}, events, nil); err != nil {
		t.Fatal(err)
	}
	var firstID, lastID int64
	if err := app.activity.store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id='session-launch'`).Scan(&firstID, &lastID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=?,event_count=?,first_event_row_id=?,last_event_row_id=? WHERE id='run-launch'`, (41 * time.Minute).Milliseconds(), len(events), firstID, lastID); err != nil {
		t.Fatal(err)
	}
	if locked {
		lock := []activitymon.ActivityEvent{{Kind: activitymon.EventSessionState, Timestamp: at(day, 11, 21).UnixMilli(), StateName: "locked"}}
		if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "session-lock", StartTime: at(day, 11, 21).UnixMilli(), EndTime: at(day, 11, 22).UnixMilli(), AppNames: `[]`, TriageStatus: "done"}, lock, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
		t.Fatal(err)
	}
	thread := activityThreadViewForMoment(t, app, dayMs, row.ID)
	if len(thread.Stretches) != 2 || len(thread.Gaps) != 1 {
		t.Fatalf("50 minutes away stayed inside one bar: %+v", thread.Stretches)
	}
	if thread.Stretches[0].End != at(day, 11, 20).UnixMilli() || thread.Stretches[1].End != at(day, 12, 30).UnixMilli() || thread.Gaps[0].Label != "away" {
		t.Fatalf("stretches=%+v gaps=%+v", thread.Stretches, thread.Gaps)
	}
	if thread.Gaps[0].Idle == locked || (thread.LinkReason.ResumedWithinMinutes != nil) != locked {
		t.Fatalf("locked=%v gap=%+v reason=%+v", locked, thread.Gaps[0], thread.LinkReason)
	}
	if thread.Stretches[0].ActiveMs+thread.Stretches[1].ActiveMs != thread.ActiveMs {
		t.Fatalf("stretch time %d+%d != %d", thread.Stretches[0].ActiveMs, thread.Stretches[1].ActiveMs, thread.ActiveMs)
	}
}

// The walk-through (P1 #5): "3h 3m at the computer, mostly Claude and
// loginwindow". Each lock left loginwindow in front until the next event.
func TestActivityLockScreenIsNeverAnAppOrActiveTime(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -1)
	ev := func(at time.Time, kind activitymon.EventKind, name, bundle string) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Kind: kind, Timestamp: at.UnixMilli(), AppName: name, AppBundleID: bundle}
	}
	claude := func(at time.Time) activitymon.ActivityEvent {
		return ev(at, activitymon.EventAppSwitch, "Claude", "com.anthropic.claudefordesktop")
	}
	lock := func(at time.Time) activitymon.ActivityEvent {
		return ev(at, activitymon.EventAppSwitch, "loginwindow", "com.apple.loginwindow")
	}
	sessions := []struct {
		id       string
		from, to time.Time
		events   []activitymon.ActivityEvent
	}{
		// 20 min in Claude, then 4 min at the lock screen before the session ends.
		{"morning", at(day, 9, 0), at(day, 9, 24), []activitymon.ActivityEvent{claude(at(day, 9, 0)), claude(at(day, 9, 10)), lock(at(day, 9, 20))}},
		// Back from lunch: a minute typing the password, then 30 min in Claude.
		{"afternoon", at(day, 13, 0), at(day, 13, 31), []activitymon.ActivityEvent{lock(at(day, 13, 0)), claude(at(day, 13, 1)), claude(at(day, 13, 20)), claude(at(day, 13, 31))}},
	}
	for _, session := range sessions {
		if err := app.activity.store.SaveSession(ActivitySessionRow{ID: session.id, StartTime: session.from.UnixMilli(), EndTime: session.to.UnixMilli(), AppNames: `["Claude","loginwindow"]`, TriageStatus: "done"}, session.events, nil); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := app.GetDailySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	want := (50 * time.Minute).Milliseconds()
	if summary.TotalActiveMs != want {
		t.Fatalf("active = %s, want 50m (lock screen excluded)", time.Duration(summary.TotalActiveMs)*time.Millisecond)
	}
	var apps int64
	for _, usage := range summary.AppBreakdown {
		if usage.AppName == "loginwindow" {
			t.Fatalf("loginwindow listed as an app: %+v", summary.AppBreakdown)
		}
		apps += usage.ActiveMs
	}
	if len(summary.AppBreakdown) == 0 || summary.AppBreakdown[0].AppName != "Claude" || apps > summary.TotalActiveMs {
		t.Fatalf("apps %+v sum %d > active %d", summary.AppBreakdown, apps, summary.TotalActiveMs)
	}
	weekly, err := app.GetWeeklySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stats := range weekly.DailyStats {
		if stats.Date == day.Format("2006-01-02") && stats.ActiveMs != summary.TotalActiveMs {
			t.Fatalf("Week says %d, Your day says %d", stats.ActiveMs, summary.TotalActiveMs)
		}
	}
}
