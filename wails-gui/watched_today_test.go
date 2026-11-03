package main

import (
	"testing"
	"time"
)

func TestWatchedTodayCountsDistinctMinutesFromTheStartOfToday(t *testing.T) {
	loc := time.FixedZone("test", -7*3600)
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previous })
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, loc)
	app := &App{activity: &activityMonitorState{store: newTestStore(t)}}
	s := app.activity.store
	first := time.Date(2026, 10, 28, 9, 4, 10, 0, loc)
	if err := s.SaveSession(ActivitySessionRow{ID: "today", StartTime: first.UnixMilli(), EndTime: now.UnixMilli(), AppNames: "[]"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Two captures in the same minute count once; the earlier one sets the start.
	for _, at := range []time.Time{first, first.Add(20 * time.Second), first.Add(3 * time.Minute), first.Add(4 * time.Hour)} {
		if _, err := s.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path) VALUES ('today', ?, 'fixture.jpg')`, at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	// A capture from yesterday does not count.
	yesterday := time.Date(2026, 10, 27, 23, 50, 0, 0, loc)
	if _, err := s.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path) VALUES ('today', ?, 'old.jpg')`, yesterday.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got := app.watchedTodayAt(now, true)
	if got.StartedAt != first.UnixMilli() || got.Minutes != 3 || !got.ScreenRecording || got.Paused {
		t.Fatalf("%+v", got)
	}
	if off := app.watchedTodayAt(now, false); off.ScreenRecording {
		t.Fatalf("screen permission not reported: %+v", off)
	}
}

func TestWatchedTodayEmptyPausedAndNotReady(t *testing.T) {
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, loc)
	app := &App{activity: &activityMonitorState{store: newTestStore(t)}}
	if got := app.watchedTodayAt(now, true); got.StartedAt != 0 || got.Minutes != 0 || !got.ScreenRecording {
		t.Fatalf("empty day: %+v", got)
	}
	app.activity = nil
	if got := app.watchedTodayAt(now, false); got.Minutes != 0 || got.Paused {
		t.Fatalf("not ready: %+v", got)
	}
}
