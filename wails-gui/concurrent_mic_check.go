package main

import "wails-gui/internal/meetingdetect"

// micInputBundleBufferSize bounds the newline-separated bundle IDs the
// platform check hands back; only a handful of processes ever hold the mic.
const micInputBundleBufferSize = 4096

// micInputSnapshot is who else is reading the microphone at one moment.
type micInputSnapshot struct {
	// Active is true when at least one other process is capturing audio input.
	Active bool
	// BundleIDs names those processes where the platform can tell (macOS
	// 14+); it is empty elsewhere.
	BundleIDs []string
	// Unknown is true when the platform could not tell who reads the
	// microphone at all (macOS before 14, a failed lookup, Linux).
	Unknown bool
}

// couldHold reports whether the app may be the one reading the microphone:
// it is among the named holders, or the holders are not named and someone
// reads it, or the platform cannot tell. Starting a call needs this next to
// its call window or title, so a meeting app in front while another app
// (dictation into Claude, Codex, ChatGPT) has the microphone is no call.
func (s micInputSnapshot) couldHold(bundleID string) bool {
	if s.Unknown {
		return true
	}
	if len(s.BundleIDs) > 0 {
		return s.holds(bundleID)
	}
	return s.Active
}

// meetingApp returns the supported native meeting app (Zoom, Microsoft
// Teams) among the processes holding the microphone. Those apps open the
// microphone only while a call (or their audio test) runs, so this is a
// call signal that needs no window access, and dictation into another app
// while Zoom merely sits in the menu bar never counts.
func (s micInputSnapshot) meetingApp() meetingdetect.Result {
	for _, bundleID := range s.BundleIDs {
		// A call app the user listed under "Apps Isoform does not listen
		// to": its calls are left out.
		if userListedAppMatches(bundleID) {
			continue
		}
		if result := meetingdetect.TranscriptionSupportedNativeMeetingForBundle(bundleID); result.IsMeeting {
			return result
		}
	}
	return meetingdetect.Result{}
}

// listedAppHolds reports whether an app the user listed under "Apps Isoform
// does not listen to" holds the microphone.
func (s micInputSnapshot) listedAppHolds() bool {
	for _, id := range s.BundleIDs {
		if userListedAppMatches(id) {
			return true
		}
	}
	return false
}
