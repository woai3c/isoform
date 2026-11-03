package main

import (
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// The transition owns this lock before a recorder drain or microphone prompt.
// Keep it held until the read returns or its bounded failure is recorded.
func observationRound1ReadWhilePauseLocked(t *testing.T, app *App, read func()) {
	t.Helper()
	app.closedEyes.mu.Lock()
	done := make(chan struct{})
	go func() { read(); close(done) }()
	returned := false
	select {
	case <-done:
		returned = true
	case <-time.After(2 * time.Second):
	}
	app.closedEyes.mu.Unlock()
	<-done
	if !returned {
		t.Error("read waited for the observation transition lock")
	}
}

func TestObservationRound1StatusReadsDoNotWaitForPauseTransition(t *testing.T) {
	app := newTimedPauseFixture(t)
	if err := app.PauseObservationFor(15); err != nil {
		t.Fatal(err)
	}
	deadline, _ := app.observationResumeAt()
	t.Run("doing now", func(t *testing.T) {
		var got DoingNow
		observationRound1ReadWhilePauseLocked(t, app, func() { got = app.GetDoingNow() })
		if got.Kind != doingNowPaused || got.PausedUntil != deadline.UnixMilli() || got.Microphone != "paused" || got.FrontApp != "" {
			t.Fatalf("timed pause: %+v", got)
		}
	})
	t.Run("settings overview", func(t *testing.T) {
		var got SettingsOverviewSees
		observationRound1ReadWhilePauseLocked(t, app, func() { got = app.settingsOverviewSees(0) })
		if got.Watching || got.PausedUntil == nil || *got.PausedUntil != deadline.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("timed pause: %+v", got)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		var got time.Time
		var timed bool
		observationRound1ReadWhilePauseLocked(t, app, func() { got, timed = app.observationResumeAt() })
		if !timed || !got.Equal(deadline) {
			t.Fatalf("deadline: %v, %v", got, timed)
		}
	})
}

func TestObservationRound1SuggestionsDoNotWaitForPauseTransition(t *testing.T) {
	app, now := observationSuggestionFixture(t)
	var got []AppSuggestion
	var err error
	observationRound1ReadWhilePauseLocked(t, app, func() {
		got, err = app.privateAppSuggestions(now, func() []SystemApp {
			return []SystemApp{{BundleID: "test.finance"}}
		}, func(SystemApp) string { return "finance" })
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("suggestions: %+v, %v", got, err)
	}
}

func TestObservationRound1UnreadableBrowserTitleHidesFrontApp(t *testing.T) {
	for _, tc := range []struct {
		name, bundle, title string
		hidden              bool
	}{
		{"Google Chrome", "com.google.Chrome", "", true},
		{"Google Chrome", "com.google.Chrome", "  \t", true},
		{"Safari", "com.apple.Safari", "", true},
		{"Google Chrome", "com.google.Chrome", "Public page", false},
		{"Editor", "test.editor", "", false},
	} {
		for _, kind := range []doingNowKind{doingNowWatching, doingNowWorking} {
			got := observationDoingNow(DoingNow{Kind: kind}, activitymon.AppInfo{AppName: tc.name, BundleID: tc.bundle, WindowTitle: tc.title}, BackgroundAudioStatus{}, false)
			want := tc.name
			if tc.hidden {
				want = ""
			}
			if got.FrontApp != want || got.FrontAppHidden != tc.hidden {
				t.Errorf("%s/%s/%q: %+v", kind, tc.name, tc.title, got)
			}
		}
	}
}

func TestObservationRound1CaptureExcludesProtectedSessions(t *testing.T) {
	f := newCaptureCountsFixture(t)
	if err := f.store.UpdateTriageStatus("today", "protected"); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: f.store}}
	counts, err := app.captureCounts(f.todayStart, f.weekStart)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]captureTally{
		captureSourceScreens:       {today: 1, week: 4},
		captureSourceTextRead:      {today: 0, week: 6},
		captureSourceAudioMinutes:  {today: 1, week: 4},
		captureSourceMeetings:      {today: 0, week: 1},
		captureSourceTyping:        {today: 1, week: 3},
		captureSourceCalendarPeeks: {today: 1, week: 1},
	}
	for _, row := range counts.Sources {
		if expected, ok := want[row.Source]; ok && (row.Today != expected.today || row.Week != expected.week) {
			t.Errorf("%s: %+v, want %+v", row.Source, row, expected)
		}
	}
	hourly, err := app.captureByHour(f.todayStart.Add(14 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var screens, speech int64
	for _, row := range hourly.Hours {
		screens += row.Screens
		speech += row.SpeechMinutes
		if row.HourStart == f.todayStart.Add(9*time.Hour).UnixMilli() && (row.Screens != 0 || row.SpeechMinutes != 0) {
			t.Errorf("protected-only hour: %+v", row)
		}
	}
	if screens != 1 || speech != 1 {
		t.Errorf("hourly totals include protected capture: screens %d, speech %d", screens, speech)
	}
	for _, row := range counts.Sources {
		if row.Source == captureSourceScreens && screens != row.Today || row.Source == captureSourceAudioMinutes && speech != row.Today {
			t.Errorf("hourly totals disagree with %s: %+v", row.Source, row)
		}
	}
}
