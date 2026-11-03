package calendar

import (
	"testing"
	"time"
)

func readyPeek(now time.Time) PeekState {
	return PeekState{
		Enabled:           true,
		Granted:           true,
		Providers:         []string{"google"},
		ComputerUseReady:  true,
		BrowserScriptable: true,
		Idle:              PeekIdleAfter,
		BriefTimes:        BriefTimesFor(now, time.UTC, "07:00", "18:00"),
		LastPeek:          now.Add(-3 * time.Hour),
	}
}

func TestDecidePeekRules(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	cases := []struct {
		name   string
		mutate func(*PeekState)
		run    bool
		reason string
	}{
		{"idle user, all clear", func(*PeekState) {}, true, "idle"},
		{"switch off", func(s *PeekState) { s.Enabled = false }, false, "off"},
		{"calendars not granted in-app", func(s *PeekState) { s.Granted = false }, false, "calendars not granted"},
		{"nothing to read", func(s *PeekState) { s.Providers = nil }, false, "nothing to read"},
		{"no computer use", func(s *PeekState) { s.ComputerUseReady = false }, false, "computer use unavailable"},
		{"another reader holds the browser", func(s *PeekState) { s.ComputerUseBusy = true }, false, "computer use busy"},
		{"firefox", func(s *PeekState) { s.BrowserScriptable = false }, false, "browser not scriptable"},
		{"microphone in use", func(s *PeekState) { s.DeviceInUse = true }, false, "microphone or camera in use"},
		{"fullscreen", func(s *PeekState) { s.Fullscreen = true }, false, "fullscreen"},
		{"closed eyes", func(s *PeekState) { s.ClosedEyes = true }, false, "closed eyes"},
		{"locked", func(s *PeekState) { s.ScreenLocked = true }, false, "screen locked"},
		{"in a meeting", func(s *PeekState) { s.InMeeting = true }, false, "in a meeting"},
		{"peeked an hour ago", func(s *PeekState) { s.LastPeek = now.Add(-time.Hour) }, false, "too soon"},
		{"peeked exactly two hours ago", func(s *PeekState) { s.LastPeek = now.Add(-PeekMinGap) }, true, "idle"},
		{"never peeked", func(s *PeekState) { s.LastPeek = time.Time{} }, true, "idle"},
		{"user active", func(s *PeekState) { s.Idle = time.Minute }, false, "user active"},
		{"idle just under five minutes", func(s *PeekState) { s.Idle = PeekIdleAfter - time.Second }, false, "user active"},
	}
	for _, tc := range cases {
		state := readyPeek(now)
		tc.mutate(&state)
		got := DecidePeek(state, now)
		if got.Run != tc.run || got.Reason != tc.reason {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.name, got.Run, got.Reason, tc.run, tc.reason)
		}
		if got.Run && len(got.Providers) != 1 {
			t.Errorf("%s: providers %v", tc.name, got.Providers)
		}
	}
}

func TestDecidePeekAtBriefTime(t *testing.T) {
	// The window is the ten minutes before the build, so the brief reads
	// what the peek found; it still needs a minute of idle time.
	cases := []struct {
		at     string
		idle   time.Duration
		run    bool
		reason string
	}{
		{"2026-10-14T06:49:00Z", PeekBriefIdle, false, "user active"},
		{"2026-10-14T06:50:00Z", PeekBriefIdle, true, "brief time"},
		{"2026-10-14T06:51:00Z", PeekBriefIdle, true, "brief time"},
		{"2026-10-14T06:59:00Z", PeekBriefIdle, true, "brief time"},
		{"2026-10-14T07:00:00Z", PeekBriefIdle, false, "user active"},
		{"2026-10-14T07:05:00Z", PeekBriefIdle, false, "user active"},
		{"2026-10-14T17:55:00Z", PeekBriefIdle, true, "brief time"},
		{"2026-10-14T17:55:00Z", 30 * time.Second, false, "user active"},
		{"2026-10-14T17:55:00Z", 0, false, "user active"},
	}
	for _, tc := range cases {
		now := mustTime(tc.at)
		state := readyPeek(now)
		state.Idle = tc.idle
		state.LastPeek = time.Time{}
		got := DecidePeek(state, now)
		if got.Run != tc.run || got.Reason != tc.reason {
			t.Errorf("%s idle %v: got (%v, %q), want (%v, %q)", tc.at, tc.idle, got.Run, got.Reason, tc.run, tc.reason)
		}
	}
	// The brief time never overrides the gap, the lock or closed eyes.
	now := mustTime("2026-10-14T06:55:00Z")
	state := readyPeek(now)
	state.Idle = PeekBriefIdle
	state.LastPeek = now.Add(-30 * time.Minute)
	if got := DecidePeek(state, now); got.Run {
		t.Fatalf("brief time must not override the two-hour gap: %+v", got)
	}
	state = readyPeek(now)
	state.Idle = PeekBriefIdle
	state.LastPeek = time.Time{}
	state.ScreenLocked = true
	if got := DecidePeek(state, now); got.Run {
		t.Fatalf("brief time must not override the lock: %+v", got)
	}
}

func TestBriefTimesForAndParseClock(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	times := BriefTimesFor(now, time.UTC, "07:00", "18:30", "bad", "25:00")
	if len(times) != 2 || times[0].Hour() != 7 || times[1].Hour() != 18 || times[1].Minute() != 30 {
		t.Fatalf("times %v", times)
	}
	if _, _, ok := ParseClock("7:00"); ok {
		t.Fatal("a one-digit hour is not a clock value")
	}
}
