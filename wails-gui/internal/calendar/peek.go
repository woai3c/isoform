package calendar

import (
	"strconv"
	"strings"
	"time"
)

// Rules for reading a web calendar with the browser ("calendar peek"). A peek
// opens the user's browser, so it runs only when it cannot get in the way.
const (
	// PeekIdleAfter is how long the user must have been away from the
	// keyboard before a peek may open the browser.
	PeekIdleAfter = 5 * time.Minute
	// PeekBriefIdle is the shorter idle time accepted just before a brief
	// builds: the browser still never opens while someone is typing.
	PeekBriefIdle = time.Minute
	// PeekMinGap is the shortest time between two peeks.
	PeekMinGap = 2 * time.Hour
	// PeekBriefSlack is how long before a brief build time a peek counts as
	// being "at the brief time", so the brief sees fresh events.
	PeekBriefSlack = 10 * time.Minute
	// PeekRecentUse is how recently a web calendar must have been used in
	// the browser for a peek to read it.
	PeekRecentUse = 14 * 24 * time.Hour
)

// PeekState is everything the peek rules look at.
type PeekState struct {
	// Enabled is the "Read web calendars when the Mac is idle" switch, and
	// the Calendars switch above it.
	Enabled bool
	// Granted is whether the Calendars permission was granted in-app. The
	// peek is on by default, but only for someone who already said yes to
	// calendars here.
	Granted bool
	// Providers are the web calendars that need a peek (WebCalendarsToPeek).
	Providers []string
	// ComputerUseReady is whether the browser can be driven: Accessibility is
	// granted and the computer-use path is available.
	ComputerUseReady bool
	// ComputerUseBusy is whether another Isoform reader holds the browser.
	ComputerUseBusy bool
	// BrowserScriptable is whether the default browser can be asked for its
	// tab and to close it (Chromium browsers and Safari; not Firefox).
	BrowserScriptable bool
	ClosedEyes        bool
	ScreenLocked      bool
	// DeviceInUse is whether the microphone or camera is open in any app:
	// a call or a recording is the worst moment to steal the browser.
	DeviceInUse bool
	// Fullscreen is whether the front window fills the screen, as a
	// presentation or a video does.
	Fullscreen bool
	// Idle is the time since the last keyboard or mouse event.
	Idle time.Duration
	// BriefTimes are today's brief build instants (07:00 and 18:00 by
	// default); a peek may run in the ten minutes before one, so the brief
	// reads fresh events, with a shorter idle requirement.
	BriefTimes []time.Time
	LastPeek   time.Time
	// InMeeting blocks a peek: a meeting is the worst moment to steal the
	// browser.
	InMeeting bool
}

// PeekDecision says whether a peek may run now and why not otherwise. The
// reason is a short token for the log.
type PeekDecision struct {
	Run    bool
	Reason string
	// Providers are the calendars to read when Run is true.
	Providers []string
}

// DecidePeek applies the rules in order: the switch, calendars granted,
// something to read, the browser can be driven and is free and scriptable,
// not in closed-eyes mode, screen unlocked, not in a meeting, no microphone or
// camera in use, nothing fullscreen, at most one peek per PeekMinGap, and
// then either the user has been idle for PeekIdleAfter or it is the ten
// minutes before a brief and the user has been idle for PeekBriefIdle.
func DecidePeek(s PeekState, now time.Time) PeekDecision {
	switch {
	case !s.Enabled:
		return PeekDecision{Reason: "off"}
	case !s.Granted:
		return PeekDecision{Reason: "calendars not granted"}
	case len(s.Providers) == 0:
		return PeekDecision{Reason: "nothing to read"}
	case !s.ComputerUseReady:
		return PeekDecision{Reason: "computer use unavailable"}
	case s.ComputerUseBusy:
		return PeekDecision{Reason: "computer use busy"}
	case !s.BrowserScriptable:
		return PeekDecision{Reason: "browser not scriptable"}
	case s.ClosedEyes:
		return PeekDecision{Reason: "closed eyes"}
	case s.ScreenLocked:
		return PeekDecision{Reason: "screen locked"}
	case s.InMeeting:
		return PeekDecision{Reason: "in a meeting"}
	case s.DeviceInUse:
		return PeekDecision{Reason: "microphone or camera in use"}
	case s.Fullscreen:
		return PeekDecision{Reason: "fullscreen"}
	case !s.LastPeek.IsZero() && now.Sub(s.LastPeek) < PeekMinGap:
		return PeekDecision{Reason: "too soon"}
	}
	if s.Idle >= PeekIdleAfter {
		return PeekDecision{Run: true, Reason: "idle", Providers: s.Providers}
	}
	if s.Idle >= PeekBriefIdle {
		for _, t := range s.BriefTimes {
			if d := t.Sub(now); d > 0 && d <= PeekBriefSlack {
				return PeekDecision{Run: true, Reason: "brief time", Providers: s.Providers}
			}
		}
	}
	return PeekDecision{Reason: "user active"}
}

// BriefTimesFor turns "HH:MM" clock values into instants on now's local day.
// Values that do not parse are skipped.
func BriefTimesFor(now time.Time, loc *time.Location, clocks ...string) []time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	var out []time.Time
	for _, clock := range clocks {
		h, m, ok := ParseClock(clock)
		if !ok {
			continue
		}
		out = append(out, time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc))
	}
	return out
}

// ParseClock reads a 24-hour "HH:MM" value.
func ParseClock(clock string) (hour, minute int, ok bool) {
	clock = strings.TrimSpace(clock)
	if len(clock) != 5 || clock[2] != ':' {
		return 0, 0, false
	}
	h, errH := strconv.Atoi(clock[:2])
	m, errM := strconv.Atoi(clock[3:])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
