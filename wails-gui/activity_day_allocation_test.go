package main

import (
	"testing"
	"time"
	"wails-gui/internal/platform/activitymon"
)

func TestActivityDayOwnedSparseAndContinuousTime(t *testing.T) {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })
	for _, date := range []struct {
		name       string
		month      time.Month
		day, hours int
	}{
		{"ordinary", time.October, 19, 24}, {"spring", time.March, 9, 23}, {"autumn", time.November, 2, 25},
	} {
		for _, mode := range []string{"sparse-sessions", "sparse-one-session", "continuous"} {
			sparse := mode != "continuous"
			name := date.name + "/" + mode
			t.Run(name, func(t *testing.T) {
				app := newThreadTestApp(t)
				next := time.Date(2025, date.month, date.day, 0, 0, 0, 0, zone)
				first := next.AddDate(0, 0, -1)
				start, end := next.Add(-time.Hour), next.Add(2*time.Hour)
				row := seedThreadMoment(t, app, threadFixtureMoment{id: "overnight", title: "Review public project", app: "Code", bundle: "com.example.code", start: start, end: end})
				expected := []int64{time.Hour.Milliseconds(), (2 * time.Hour).Milliseconds()}
				if sparse {
					// The source actually contains 10m before midnight and 20m
					// near the end, not a uniform 30m spread over three hours.
					if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET end_time=? WHERE id='session-overnight'`, start.Add(10*time.Minute).UnixMilli()); err != nil {
						t.Fatal(err)
					}
					if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET end_ms=?,active_ms=? WHERE id='run-overnight'`, start.Add(10*time.Minute).UnixMilli(), (10 * time.Minute).Milliseconds()); err != nil {
						t.Fatal(err)
					}
					tail := seedThreadMoment(t, app, threadFixtureMoment{id: "tail", title: row.Title, app: "Code", bundle: "com.example.code", start: end.Add(-20 * time.Minute), end: end})
					if _, err := app.activity.store.db.Exec(`DELETE FROM activities WHERE id=?`, tail.ID); err != nil {
						t.Fatal(err)
					}
					if err := app.activity.store.UpsertActivityRunLink("run-tail", row.ID, 1, end.UnixMilli()); err != nil {
						t.Fatal(err)
					}
					row.RunIDs = `["run-overnight","run-tail"]`
					row.ActiveMsByApp = `{"Code":1800000}`
					if err := app.activity.store.UpdateActivity(row); err != nil {
						t.Fatal(err)
					}
					if mode == "sparse-one-session" {
						if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET end_time=? WHERE id='session-overnight'`, end.UnixMilli()); err != nil {
							t.Fatal(err)
						}
						if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='["session-overnight"]' WHERE id='run-tail'`); err != nil {
							t.Fatal(err)
						}
						if _, err := app.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id='session-tail'`); err != nil {
							t.Fatal(err)
						}
					}
					expected = []int64{(10 * time.Minute).Milliseconds(), (20 * time.Minute).Milliseconds()}
				}
				if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
					t.Fatal(err)
				}
				var total int64
				for i, day := range []time.Time{first, next} {
					edge := activityThreadDayEnd(day.UnixMilli())
					if i == 1 && (edge-day.UnixMilli())/time.Hour.Milliseconds() != int64(date.hours) {
						t.Fatal("wrong DST day")
					}
					if _, err := app.runActivityThreadPass(t.Context(), day.UnixMilli(), false, time.Now()); err != nil {
						t.Fatal(err)
					}
					cards, err := app.GetActivitiesInRange(day.UnixMilli(), edge)
					if err != nil || len(cards) != 1 || cards[0].ActiveMs != expected[i] {
						t.Fatalf("cards day %d: %+v %v; want %d", i, cards, err, expected[i])
					}
					var appMs int64
					for _, usage := range cards[0].Apps {
						appMs += usage.ActiveMs
					}
					if appMs != expected[i] {
						t.Fatalf("app allocation=%d want %d", appMs, expected[i])
					}
					threads, err := app.GetDayThreads(day.UnixMilli(), edge)
					if err != nil || len(threads.Threads) != 1 || threads.Threads[0].ActiveMs != expected[i] || threads.Threads[0].Stretches[0].ActiveMs != expected[i] {
						t.Fatalf("thread day %d: %+v %v", i, threads, err)
					}
					draft, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: "activity", SourceID: row.ID, DayStartMs: day.UnixMilli()})
					if err != nil || draft.Prefill.DurationMinutes != int(expected[i]/time.Minute.Milliseconds()) {
						t.Fatalf("chat day %d: %+v %v", i, draft, err)
					}
					batch := app.PrepareTodayChatDrafts([]TodayChatContextRef{{SourceKind: "activity", SourceID: row.ID, DayStartMs: day.UnixMilli()}})
					if len(batch) != 1 || batch[0].Draft == nil || batch[0].Draft.Prefill.DurationMinutes != draft.Prefill.DurationMinutes {
						t.Fatalf("prepared batch disagrees: %+v", batch)
					}
					total += cards[0].ActiveMs
				}
				source, err := app.activity.store.GetActivityByID(row.ID)
				if err != nil || source == nil || source.StartMs != row.StartMs || source.EndMs != row.EndMs || source.ActiveMsByApp != row.ActiveMsByApp {
					t.Fatalf("day projection changed durable source: %+v %v", source, err)
				}
				if total != expected[0]+expected[1] {
					t.Fatalf("source activity not conserved: %d", total)
				}
				if sparse && total != (30*time.Minute).Milliseconds() {
					t.Fatal("sparse activity duplicated")
				}
			})
		}
	}
}

func TestActivityDayChatSelectionCannotInventSourceTime(t *testing.T) {
	app, row, day := seedPastActivityDay(t)
	for _, ref := range []TodayChatContextRef{
		{SourceKind: "activity", SourceID: row.ID, DayStartMs: day + time.Hour.Milliseconds()},
		{SourceKind: "activity", SourceID: row.ID, DayStartMs: time.UnixMilli(day).AddDate(0, 0, -7).UnixMilli()},
		{SourceKind: "memory", SourceID: row.ID, DayStartMs: day},
	} {
		if _, err := app.PrepareTodayChatDraft(ref); err == nil {
			t.Fatalf("accepted invalid day selection: %+v", ref)
		}
	}
}

func TestActivityDayAllocationWithheldTimeIsNotReassigned(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -3)
	start := day.Add(9 * time.Hour)
	seedThreadMoment(t, app, threadFixtureMoment{id: "visible", title: "Public review", app: "Code", start: start, end: start.Add(10 * time.Minute)})
	seedThreadMoment(t, app, threadFixtureMoment{id: "withheld", title: "Withheld source", app: "Code", start: start.Add(10 * time.Minute), end: start.Add(30 * time.Minute)})
	for _, query := range []string{
		`UPDATE activity_sessions SET end_time=start_time+1800000 WHERE id='session-visible'`,
		`UPDATE activity_timeline_groups SET session_ids='["session-visible"]',evidence_excluded=1 WHERE id='run-withheld'`,
		`DELETE FROM activity_sessions WHERE id='session-withheld'`,
	} {
		if _, err := app.activity.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	apps := activityMomentAppTimes{}
	spent, total, err := app.activityMomentTimeMs(t.Context(), day.UnixMilli(), activityThreadDayEnd(day.UnixMilli()), apps)
	if err != nil || spent["visible"] != (10*time.Minute).Milliseconds() || spent["withheld"] != 0 || spent[""] != (20*time.Minute).Milliseconds() || total != (30*time.Minute).Milliseconds() {
		t.Fatalf("withheld source reassigned: %+v total=%d err=%v", spent, total, err)
	}
	if len(apps["withheld"]) != 0 {
		t.Fatalf("withheld app allocation: %+v", apps)
	}
}

// Even a single long session is not proof of uniformly active work. The
// original run counter comes from capped, timestamped observations.
func TestActivityDaySparseCrossingRunReplaysSourceIntervals(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	first := today.AddDate(0, 0, -3)
	next := first.AddDate(0, 0, 1)
	start, end := next.Add(-time.Hour), next.Add(2*time.Hour)
	row := seedThreadMoment(t, app, threadFixtureMoment{id: "event-sparse", title: "Public release review", app: "Code", bundle: "com.example.code", start: start, end: end})
	var events []activitymon.ActivityEvent
	for i := 0; i <= 10; i++ {
		events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: start.Add(time.Duration(i) * time.Minute).UnixMilli(), AppName: "Code", AppBundleID: "com.example.code"})
	}
	for i := 19; i >= 0; i-- {
		events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: end.Add(-time.Duration(i) * time.Minute).UnixMilli(), AppName: "Code", AppBundleID: "com.example.code"})
	}
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id='session-event-sparse'`); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "session-event-sparse", StartTime: start.UnixMilli(), EndTime: end.UnixMilli(), AppNames: `["Code"]`, TriageStatus: "done"}, events, nil); err != nil {
		t.Fatal(err)
	}
	var firstID, lastID int64
	if err := app.activity.store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id='session-event-sparse'`).Scan(&firstID, &lastID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=1800000,event_count=?,first_event_row_id=?,last_event_row_id=? WHERE id='run-event-sparse'`, len(events), firstID, lastID); err != nil {
		t.Fatal(err)
	}
	row.ActiveMsByApp = `{"Code":1800000}`
	if err := app.activity.store.UpdateActivity(row); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
		t.Fatal(err)
	}
	var total int64
	for i, day := range []time.Time{first, next} {
		if _, err := app.runActivityThreadPass(t.Context(), day.UnixMilli(), false, time.Now()); err != nil {
			t.Fatal(err)
		}
		cards, err := app.GetActivitiesInRange(day.UnixMilli(), activityThreadDayEnd(day.UnixMilli()))
		want := int64(10*(i+1)) * time.Minute.Milliseconds()
		if err != nil || len(cards) != 1 || cards[0].ActiveMs != want {
			t.Fatalf("event day %d want %d: %+v %v", i, want, cards, err)
		}
		threads, err := app.GetDayThreads(day.UnixMilli(), activityThreadDayEnd(day.UnixMilli()))
		if err != nil || len(threads.Threads) != 1 || threads.Threads[0].ActiveMs != want {
			t.Fatalf("event threads: %+v %v", threads, err)
		}
		draft, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: "activity", SourceID: row.ID, DayStartMs: day.UnixMilli()})
		if err != nil || draft.Prefill.DurationMinutes != 10*(i+1) {
			t.Fatalf("event chat: %+v %v", draft, err)
		}
		total += cards[0].ActiveMs
	}
	if total != 1800000 {
		t.Fatalf("sparse source changed: %d", total)
	}
	// Matching numeric counters do not authorize borrowing undeclared frames.
	source, err := app.activity.store.GetTimelineRunByID("run-event-sparse")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "different-readable-source", StartTime: start.UnixMilli(), EndTime: end.UnixMilli(), AppNames: `["Code"]`, TriageStatus: "done"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	declared := source.SessionIDs
	source.SessionIDs = []string{"different-readable-source"}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, composeJSON(source.SessionIDs), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.activityRunDayActiveMs(t.Context(), *source, first.UnixMilli(), next.UnixMilli()); err == nil {
		t.Fatal("sparse source replay borrowed undeclared source sessions")
	}
	source.SessionIDs = declared
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, composeJSON(declared), source.ID); err != nil {
		t.Fatal(err)
	}
	if active, err := app.activity.store.activityRunDayActiveMs(t.Context(), *source, first.UnixMilli(), next.UnixMilli()); err != nil || active != 600000 {
		t.Fatalf("declared provenance did not recover: %d %v", active, err)
	}
}

func TestActivityDayUnprovenIntervalsRejectCacheAndRecover(t *testing.T) {
	app, _, day := seedPastActivityDay(t)
	end := activityThreadDayEnd(day)
	if _, err := app.GetActivitiesInRange(day, end); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=1800000 WHERE id='run-past-work'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetActivitiesInRange(day, end); err == nil {
		t.Fatal("replayed stale cache or guessed sparse activity without its source intervals")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=end_ms-start_ms WHERE id='run-past-work'`); err != nil {
		t.Fatal(err)
	}
	cards, err := app.GetActivitiesInRange(day, end)
	if err != nil || len(cards) != 1 || cards[0].ActiveMs != (30*time.Minute).Milliseconds() {
		t.Fatalf("source repair did not recover: %+v %v", cards, err)
	}
}

func TestActivityDayCounterFloorsAndCappedIntervalsConserve(t *testing.T) {
	today, _ := threadTestDay(t)
	edge := today.AddDate(0, 0, -2)
	items := []timelineRunEvent{
		{RowID: 1, Timestamp: edge.Add(-time.Hour).UnixMilli(), AppKey: "code", AppName: "Code"},
		{RowID: 2, Timestamp: edge.Add(-30 * time.Minute).UnixMilli(), AppKey: "other", AppName: "Other", LowSignal: true},
		{RowID: 3, Timestamp: edge.Add(time.Hour).UnixMilli(), AppKey: "code", AppName: "Code"},
	}
	source := deriveTimelineRuns(items)
	if len(source) != 1 || source[0].ActiveMs != 62000 {
		t.Fatalf("bad source control: %+v", source)
	}
	var total int64
	for i, day := range []time.Time{edge.AddDate(0, 0, -1), edge} {
		allocation := &timelineRunDayAllocation{start: day.UnixMilli(), end: activityThreadDayEnd(day.UnixMilli()), active: map[string]int64{}}
		replay := deriveTimelineRuns(items, allocation)
		if len(replay) != 1 || replay[0].ActiveMs != source[0].ActiveMs {
			t.Fatal("day allocation changed source counter")
		}
		want := int64(1000)
		if i == 1 {
			want = 61000
		}
		if got := allocation.active[replay[0].ID]; got != want {
			t.Fatalf("day %d: %d want %d; capped activity was spread over idle time", i, got, want)
		}
		total += allocation.active[replay[0].ID]
	}
	if total != source[0].ActiveMs {
		t.Fatal("compacted source time was not conserved")
	}
}
