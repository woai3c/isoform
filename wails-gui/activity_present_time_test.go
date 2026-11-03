package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// seedPresentSession stores one capture session; extra events (a lock, a
// screensaver switch) ride along with timestamps of their own, the way the
// buffer carries a lock into the session that is open when the screen locks.
func seedPresentSession(t *testing.T, app *App, id string, start, end time.Time, extra ...activitymon.ActivityEvent) {
	t.Helper()
	events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: start.UnixMilli(), AppName: "Finder", AppBundleID: "com.apple.finder"}}
	events = append(events, extra...)
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: id, StartTime: start.UnixMilli(), EndTime: end.UnixMilli(), AppNames: `["Finder"]`, TriageStatus: "done"}, events, nil); err != nil {
		t.Fatal(err)
	}
}

func presentTime(t *testing.T, app *App, dayMs int64) (int64, []activityPresentGap) {
	t.Helper()
	store := app.activity.store
	sessions, err := store.modelSessionsOverlapping(dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	total, gaps, err := app.activityPresentTimeMs(context.Background(), store, app.filterActivityModelSessions(store, sessions), dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	return total, gaps
}

func TestActivityPresentTimeCountsAShortGapWithTheScreenOn(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
	seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
	total, gaps := presentTime(t, app, dayMs)
	if want := (12 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time = %v, want %v (sessions plus the 3 minute reading gap)", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	if len(gaps) != 1 || gaps[0].beforeSession != "s1" || gaps[0].afterSession != "s2" {
		t.Fatalf("gaps = %+v, want the s1 to s2 gap", gaps)
	}
	summary, err := app.GetDailySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalActiveMs != total {
		t.Fatalf("day line %d != present time %d", summary.TotalActiveMs, total)
	}
}

func TestActivityPresentTimeSkipsAGapWithTheScreenOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		event  activitymon.ActivityEvent
		counts bool
	}{
		{"a lock", activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "locked"}, false},
		{"sleep", activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "sleep"}, false},
		{"the screensaver", activitymon.ActivityEvent{Kind: activitymon.EventAppSwitch, AppName: "ScreenSaverEngine", AppBundleID: "com.apple.screensaver.engine"}, false},
		{"the login window", activitymon.ActivityEvent{Kind: activitymon.EventFocus, AppName: "loginwindow", AppBundleID: "com.apple.loginwindow"}, false},
		{"an unlock", activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "unlocked"}, true},
		{"an idle mark", activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "idle"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newThreadTestApp(t)
			day, dayMs := threadTestDay(t)
			event := tc.event
			event.Timestamp = at(day, 9, 6).UnixMilli()
			seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
			seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12), event)
			total, gaps := presentTime(t, app, dayMs)
			want := (9 * time.Minute).Milliseconds()
			if tc.counts {
				want = (12 * time.Minute).Milliseconds()
			}
			if total != want {
				t.Fatalf("present time with %s in the gap = %v, want %v", tc.name, time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
			}
			if tc.counts != (len(gaps) == 1) {
				t.Fatalf("gap counted = %t with %s inside it", len(gaps) == 1, tc.name)
			}
		})
	}
	// A lock is recorded as the last event of the session before the gap, so
	// it lands exactly on the gap's left edge; the stretch after it is
	// screen-off time and must not count either.
	t.Run("a lock ending the earlier session", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		lock := activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "locked", Timestamp: at(day, 9, 5).UnixMilli()}
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5), lock)
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
		total, gaps := presentTime(t, app, dayMs)
		if want := (9 * time.Minute).Milliseconds(); total != want {
			t.Fatalf("present time after an edge lock = %v, want %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
		}
		if len(gaps) != 0 {
			t.Fatalf("gap after an edge lock counted: %+v", gaps)
		}
	})
	// The mirror image: when the buffer was empty at lock time, the lock
	// becomes the first event of the next session and lands on the gap's
	// right edge. The stretch before it was still screen-on, so it counts.
	t.Run("a lock starting the next session", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		lock := activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "locked", Timestamp: at(day, 9, 8).UnixMilli()}
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12), lock)
		total, gaps := presentTime(t, app, dayMs)
		if want := (12 * time.Minute).Milliseconds(); total != want {
			t.Fatalf("present time before an edge lock = %v, want %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
		}
		if len(gaps) != 1 {
			t.Fatalf("gap before an edge lock should count: %+v", gaps)
		}
	})
}

func TestActivityPresentTimeSkipsAGapOverTenMinutes(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
	seedPresentSession(t, app, "s2", at(day, 9, 20), at(day, 9, 25))
	total, gaps := presentTime(t, app, dayMs)
	if want := (10 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time across a 15 minute gap = %v, want sessions only %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	if len(gaps) != 0 {
		t.Fatalf("15 minute gap counted: %+v", gaps)
	}
	// Ten minutes exactly still counts.
	seedPresentSession(t, app, "s3", at(day, 10, 0), at(day, 10, 5))
	seedPresentSession(t, app, "s4", at(day, 10, 15), at(day, 10, 20))
	total, gaps = presentTime(t, app, dayMs)
	if want := (30 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time with a 10 minute gap = %v, want %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	if len(gaps) != 1 {
		t.Fatalf("the 10 minute gap should count once: %+v", gaps)
	}
}

func TestActivityPresentTimeClipsSessionsToTheDay(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	// A session that began yesterday counts only its inside-the-day span, and
	// the gap after its clipped end counts like any other.
	seedPresentSession(t, app, "s0", day.Add(-10*time.Minute), at(day, 0, 5))
	seedPresentSession(t, app, "s1", at(day, 0, 8), at(day, 0, 12))
	total, gaps := presentTime(t, app, dayMs)
	if want := (12 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time across midnight = %v, want %v (5 clipped + 4 + 3 gap)", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	if len(gaps) != 1 {
		t.Fatalf("the gap after the clipped session should count: %+v", gaps)
	}
}

func TestActivityPresentTimeNestedSessionsCannotHideAGap(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	// A session nested inside another (chat ingest beside capture): the real
	// gap opens when the outer session ends, not when the nested one does.
	seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 30))
	seedPresentSession(t, app, "s2", at(day, 9, 10), at(day, 9, 20))
	seedPresentSession(t, app, "s3", at(day, 9, 35), at(day, 9, 40))
	total, gaps := presentTime(t, app, dayMs)
	if want := (50 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time with a nested session = %v, want %v (spans plus the 5 minute gap)", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	if len(gaps) != 1 || gaps[0].beforeSession != "s1" || gaps[0].afterSession != "s3" {
		t.Fatalf("gaps = %+v, want the 9:30 to 9:35 gap off the outer session", gaps)
	}
}

func TestActivityPresentTimeClosedEyesNeverCounts(t *testing.T) {
	t.Run("a pause inside the gap", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
		app.saveObservationPauses([]observationPause{{Start: at(day, 9, 6).UnixMilli(), End: at(day, 9, 7).UnixMilli()}})
		total, gaps := presentTime(t, app, dayMs)
		if want := (9 * time.Minute).Milliseconds(); total != want {
			t.Fatalf("present time across a Closed Eyes pause = %v, want sessions only %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
		}
		if len(gaps) != 0 {
			t.Fatalf("gap during Closed Eyes counted: %+v", gaps)
		}
	})
	t.Run("a pause elsewhere does not", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
		app.saveObservationPauses([]observationPause{{Start: at(day, 10, 0).UnixMilli(), End: at(day, 10, 5).UnixMilli()}})
		total, _ := presentTime(t, app, dayMs)
		if want := (12 * time.Minute).Milliseconds(); total != want {
			t.Fatalf("present time with an unrelated pause = %v, want %v", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
		}
	})
	t.Run("a pause still open covers the gap", func(t *testing.T) {
		// The presence check treats an open pause as covering every later gap.
		open := activityPresenceBreak{pauses: []observationPause{{Start: 10}}}
		if !open.broken(12, 20) {
			t.Fatal("an open pause must break presence")
		}
		// End to end the read gate withholds every session overlapping the
		// pause, so only what ended before it remains on the day line.
		app := newThreadTestApp(t)
		day, _ := threadTestDay(t)
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
		app.saveObservationPauses([]observationPause{{Start: at(day, 9, 6).UnixMilli()}})
		summary, err := app.GetDailySummary(day.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		if want := (5 * time.Minute).Milliseconds(); summary.TotalActiveMs != want {
			t.Fatalf("day line across an open pause = %v, want %v (the session before the pause)", time.Duration(summary.TotalActiveMs)*time.Millisecond, time.Duration(want)*time.Millisecond)
		}
	})
	t.Run("an unreadable pause log fails closed", func(t *testing.T) {
		// One layer down, the presence check treats every gap as paused.
		if !(activityPresenceBreak{pausesErr: errors.New("broken log")}).broken(1, 2) {
			t.Fatal("an unreadable pause log must break presence")
		}
		// End to end the read gate fails closed first: with a broken log no
		// session is readable, so the day line is 0 rather than a guess.
		app := newThreadTestApp(t)
		day, _ := threadTestDay(t)
		seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 5))
		seedPresentSession(t, app, "s2", at(day, 9, 8), at(day, 9, 12))
		if err := app.store.SetUIState(context.Background(), observationPausesKey, "{broken"); err != nil {
			t.Fatal(err)
		}
		summary, err := app.GetDailySummary(day.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		if summary.TotalActiveMs != 0 {
			t.Fatalf("day line with a broken pause log = %v, want 0 (fail closed)", time.Duration(summary.TotalActiveMs)*time.Millisecond)
		}
	})
}

// TestActivityPresentTimeBeforeAndAfterOnAFixtureDay pins the change item 70
// makes to one day: the old line summed session spans, the new line adds the
// gaps that still count as present — here 13 of the 33 gap minutes.
func TestActivityPresentTimeBeforeAndAfterOnAFixtureDay(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	lock := activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "locked", Timestamp: at(day, 10, 52).UnixMilli()}
	seedPresentSession(t, app, "s1", at(day, 9, 0), at(day, 9, 20))
	seedPresentSession(t, app, "s2", at(day, 9, 22), at(day, 9, 40))
	seedPresentSession(t, app, "s3", at(day, 9, 45), at(day, 10, 0))
	seedPresentSession(t, app, "s4", at(day, 10, 20), at(day, 10, 40))
	seedPresentSession(t, app, "s5", at(day, 10, 41), at(day, 10, 50))
	seedPresentSession(t, app, "s6", at(day, 10, 55), at(day, 11, 10), lock)
	seedPresentSession(t, app, "s7", at(day, 11, 15), at(day, 11, 30))
	sessions, err := app.activity.store.modelSessionsOverlapping(dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	var oldLine int64
	for _, s := range sessions {
		oldLine += s.EndTime - s.StartTime
	}
	if want := (112 * time.Minute).Milliseconds(); oldLine != want {
		t.Fatalf("session-only line = %v, want %v", time.Duration(oldLine)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	total, gaps := presentTime(t, app, dayMs)
	if want := (125 * time.Minute).Milliseconds(); total != want {
		t.Fatalf("present time = %v, want %v (112 in sessions plus 13 in gaps)", time.Duration(total)*time.Millisecond, time.Duration(want)*time.Millisecond)
	}
	// Counted: 2, 5, 1 and 5 minute gaps. Skipped: the 20 minute gap and the
	// 5 minute gap with the lock inside.
	if len(gaps) != 4 {
		t.Fatalf("counted gaps = %+v, want 4", gaps)
	}
	summary, err := app.GetDailySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalActiveMs != total {
		t.Fatalf("day line %d != present time %d", summary.TotalActiveMs, total)
	}
}

func TestActivityMomentTimeSplitsGapsByThread(t *testing.T) {
	seedMembership := func(t *testing.T, app *App, dayMs int64, threadID string, activityIDs ...string) {
		t.Helper()
		if _, err := app.activity.store.db.Exec(`INSERT OR IGNORE INTO activity_threads (id, day_start_ms, title, created_at, updated_at) VALUES (?, ?, '', 0, 0)`, threadID, dayMs); err != nil {
			t.Fatal(err)
		}
		for _, id := range activityIDs {
			if _, err := app.activity.store.db.Exec(`INSERT INTO activity_thread_members (activity_id, thread_id, day_start_ms, source, updated_at) VALUES (?, ?, ?, 'rule', 0)`, id, threadID, dayMs); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedPair := func(t *testing.T, app *App, day time.Time) {
		t.Helper()
		seedThreadMoment(t, app, threadFixtureMoment{id: "m1", title: "Draft the isoform release note", app: "Claude", start: at(day, 9, 0), end: at(day, 9, 5)})
		seedThreadMoment(t, app, threadFixtureMoment{id: "m2", title: "Polish the isoform release note", app: "Claude", start: at(day, 9, 8), end: at(day, 9, 12)})
	}
	sumOf := func(spent map[string]int64) int64 {
		var sum int64
		for _, ms := range spent {
			sum += ms
		}
		return sum
	}
	fiveMin := (5 * time.Minute).Milliseconds()
	fourMin := (4 * time.Minute).Milliseconds()
	threeMin := (3 * time.Minute).Milliseconds()

	t.Run("one thread on both sides keeps the gap", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedPair(t, app, day)
		seedMembership(t, app, dayMs, "th_notes", "m1", "m2")
		spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
		if err != nil {
			t.Fatal(err)
		}
		if spent["m1"] != fiveMin+threeMin || spent["m2"] != fourMin {
			t.Fatalf("m1 = %v, m2 = %v; the whole gap belongs to their shared thread, reported at its earlier moment",
				time.Duration(spent["m1"])*time.Millisecond, time.Duration(spent["m2"])*time.Millisecond)
		}
		if sumOf(spent) != total {
			t.Fatalf("shares %d do not add up to the day line %d", sumOf(spent), total)
		}
	})
	t.Run("two threads split the gap in half", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedPair(t, app, day)
		seedMembership(t, app, dayMs, "th_notes", "m1")
		seedMembership(t, app, dayMs, "th_other", "m2")
		spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
		if err != nil {
			t.Fatal(err)
		}
		if spent["m1"] != fiveMin+threeMin/2 || spent["m2"] != fourMin+threeMin-threeMin/2 {
			t.Fatalf("m1 = %v, m2 = %v; the gap splits in half across their threads",
				time.Duration(spent["m1"])*time.Millisecond, time.Duration(spent["m2"])*time.Millisecond)
		}
		if sumOf(spent) != total {
			t.Fatalf("shares %d do not add up to the day line %d", sumOf(spent), total)
		}
	})
	t.Run("no thread on either side leaves the gap unthreaded", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedPair(t, app, day)
		spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
		if err != nil {
			t.Fatal(err)
		}
		if spent["m1"] != fiveMin+threeMin || spent["m2"] != fourMin {
			t.Fatalf("m1 = %v, m2 = %v; with neither side threaded the gap stays with the earlier moment, like a session does",
				time.Duration(spent["m1"])*time.Millisecond, time.Duration(spent["m2"])*time.Millisecond)
		}
		if sumOf(spent) != total {
			t.Fatalf("shares %d do not add up to the day line %d", sumOf(spent), total)
		}
	})
	t.Run("a momentless session takes its half under the empty id", func(t *testing.T) {
		app := newThreadTestApp(t)
		day, dayMs := threadTestDay(t)
		seedThreadMoment(t, app, threadFixtureMoment{id: "m1", title: "Draft the isoform release note", app: "Claude", start: at(day, 9, 0), end: at(day, 9, 5)})
		seedPresentSession(t, app, "glance", at(day, 9, 8), at(day, 9, 12))
		seedMembership(t, app, dayMs, "th_notes", "m1")
		spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
		if err != nil {
			t.Fatal(err)
		}
		if spent["m1"] != fiveMin+threeMin/2 || spent[""] != fourMin+threeMin-threeMin/2 {
			t.Fatalf("m1 = %v, unthreaded = %v; the gap splits between the thread and the momentless session",
				time.Duration(spent["m1"])*time.Millisecond, time.Duration(spent[""])*time.Millisecond)
		}
		if sumOf(spent) != total {
			t.Fatalf("shares %d do not add up to the day line %d", sumOf(spent), total)
		}
	})
}
