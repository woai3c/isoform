package main

import (
	"testing"
	"time"
)

func TestCaptureByHourMatchesCaptureCounts(t *testing.T) {
	f := newCaptureCountsFixture(t)
	app := &App{activity: &activityMonitorState{store: f.store}}
	got, err := app.captureByHour(f.todayStart.Add(14 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	counts, err := app.captureCounts(f.todayStart, f.weekStart)
	if err != nil {
		t.Fatal(err)
	}
	var screens, speech int64
	for _, row := range got.Hours {
		screens += row.Screens
		speech += row.SpeechMinutes
	}
	for _, row := range counts.Sources {
		if row.Source == captureSourceScreens && screens != row.Today {
			t.Fatalf("screens %d != %d", screens, row.Today)
		}
		if row.Source == captureSourceAudioMinutes && speech != row.Today {
			t.Fatalf("speech %d != %d", speech, row.Today)
		}
	}
	if got.DayStart != f.todayStart.UnixMilli() || len(got.Hours) != 15 || got.Hours[0].Screens != 1 || got.Hours[0].SpeechMinutes != 1 || got.Hours[9].Screens != 3 || got.Hours[9].SpeechMinutes != 2 || got.Hours[1].Screens != 0 {
		t.Fatalf("hours: %+v", got)
	}
}

func TestCaptureByHourEmptyPausedAndNotReady(t *testing.T) {
	app := &App{activity: &activityMonitorState{store: newTestStore(t)}}
	got, err := app.GetCaptureByHour()
	if err != nil || got.Paused || got.NotReady || len(got.Hours) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	app.closedEyes.enabled.Store(true)
	app.activity = nil
	got, err = app.GetCaptureByHour()
	if err != nil || !got.Paused || got.NotReady || len(got.Hours) != 0 {
		t.Fatalf("paused: %+v %v", got, err)
	}
	app.closedEyes.enabled.Store(false)
	got, err = app.GetCaptureByHour()
	if err != nil || got.Paused || !got.NotReady || len(got.Hours) != 0 {
		t.Fatalf("not ready: %+v %v", got, err)
	}
}

func TestCaptureByHourFirstCaptureAndLocalHours(t *testing.T) {
	for _, zone := range []string{"America/Los_Angeles", "Asia/Kathmandu"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(zone, func(t *testing.T) {
			previous := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = previous })
			now := time.Date(2026, 10, 22, 14, 15, 0, 0, loc)
			app := &App{activity: &activityMonitorState{store: newTestStore(t)}}
			s := app.activity.store
			at := time.Date(2026, 10, 22, 9, 20, 0, 0, loc)
			if err := s.SaveSession(ActivitySessionRow{ID: "s", StartTime: at.UnixMilli(), EndTime: at.Add(time.Hour).UnixMilli(), AppNames: "[]"}, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO activity_snapshots (session_id, timestamp, file_path) VALUES ('s', ?, 'fixture.jpg')`, at.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			got, err := app.captureByHour(now)
			if err != nil || len(got.Hours) != 6 || got.Hours[0].HourStart != time.Date(2026, 10, 22, 9, 0, 0, 0, loc).UnixMilli() {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	loc, _ := time.LoadLocation("America/Los_Angeles")
	first := time.Date(2026, 11, 1, 1, 15, 0, 0, loc)
	second := first.Add(time.Hour)
	if observationHourStart(first).Equal(observationHourStart(second)) {
		t.Fatal("repeated DST hours collapsed")
	}
}
