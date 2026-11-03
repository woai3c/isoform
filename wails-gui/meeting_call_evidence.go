package main

import (
	"flag"
	"log"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/browsers"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/platform/activitymon"
)

// A call needs two pieces of evidence at once: a call window of the app (the
// meeting title hints or the window-count rule, Settings and audio-test
// windows left out; for a browser, a meeting page in front) and the app
// itself holding the microphone. Zoom and Teams open the microphone for their
// audio test and their Settings can keep it open, so a hold alone is no call;
// with no window to read (Accessibility not granted, or only the menu-bar
// item) nothing starts. Zoom merely running, or in front while another app
// (dictation into Claude, Codex, ChatGPT) holds the microphone, is never a
// call.

const (
	// meetingMicHoldWithWindowMinimum is how long the app must hold the
	// microphone while it shows a call window.
	meetingMicHoldWithWindowMinimum = 15 * time.Second
	// meetingMicHoldGap: a hold seen again within this counts as continuous.
	meetingMicHoldGap = 20 * time.Second
	// meetingCallEvidenceTTL limits the window lookups (Accessibility calls)
	// to one per app every few seconds.
	meetingCallEvidenceTTL = 10 * time.Second
)

// meetingSettingsWindowHints mark windows that never show a call: Settings,
// Preferences and the audio test.
var meetingSettingsWindowHints = []string{
	"settings", "preferences", "audio test", "test speaker", "test microphone", "test mic",
	"设置", "偏好设置", "设定",
}

func isMeetingSettingsWindow(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	for _, hint := range meetingSettingsWindowHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// meetingCallWindow returns the call shown by the app's windows, Settings
// and audio-test windows left out, or an empty result.
func meetingCallWindow(bundleID, appName string, windows []activitymon.AppWindow) meetingdetect.Result {
	visible := make([]meetingdetect.VisibleWindow, 0, len(windows))
	for _, w := range windows {
		if isMeetingSettingsWindow(w.Title) {
			continue
		}
		visible = append(visible, meetingdetect.VisibleWindow{BundleID: bundleID, AppName: appName, Title: w.Title})
	}
	if len(visible) == 0 {
		return meetingdetect.Result{}
	}
	return meetingdetect.IsMeetingAppActive(visible)
}

// meetingCallEvidence decides whether a meeting app's hold on the
// microphone is a call: only with a call window, held past an audio test's
// first seconds. Settings, an audio test, an idle window or no readable
// window at all is no call.
func meetingCallEvidence(callWindow bool, held time.Duration) bool {
	return callWindow && held >= meetingMicHoldWithWindowMinimum
}

// meetingMicHoldTracker follows how long the same meeting app has held the
// microphone and caches the window check.
type meetingMicHoldTracker struct {
	mu          sync.Mutex
	app         string
	since       time.Time
	last        time.Time
	evaluatedAt time.Time
	evaluated   meetingdetect.Result
}

// callOnMicrophone returns the supported meeting app that holds the
// microphone and shows call evidence, or an empty result.
func (t *meetingMicHoldTracker) callOnMicrophone(snapshot micInputSnapshot, now time.Time, lookup func(meetingdetect.Result) (string, []activitymon.AppWindow)) meetingdetect.Result {
	holder := snapshot.meetingApp()
	t.mu.Lock()
	defer t.mu.Unlock()
	if !holder.IsMeeting {
		t.app = ""
		return meetingdetect.Result{}
	}
	if holder.MeetingName != t.app || now.Sub(t.last) > meetingMicHoldGap {
		t.app = holder.MeetingName
		t.since = now
		t.evaluatedAt = time.Time{}
	}
	t.last = now
	if !t.evaluatedAt.IsZero() && now.Sub(t.evaluatedAt) < meetingCallEvidenceTTL {
		return t.evaluated
	}
	appName, windows := lookup(holder)
	call := meetingCallWindow(holder.BundleID, appName, windows).IsMeeting
	result := meetingdetect.Result{}
	if meetingCallEvidence(call, now.Sub(t.since)) {
		result = holder
		result.AppName = appName
		if result.AppName == "" {
			result.AppName = holder.MeetingName
		}
	}
	t.evaluatedAt = now
	t.evaluated = result
	return result
}

var meetingMicHolds = &meetingMicHoldTracker{}

// meetingProbes are the system lookups call detection reads; tests stand in
// for them.
var meetingProbes = struct {
	frontmost   func() activitymon.AppInfo
	runningApps func() []activitymon.RunningApp
	appWindows  func(appName string) []activitymon.AppWindow
	micHolders  func() micInputSnapshot
	browserURL  func(bundleID string) activitymon.BrowserURLRead
}{activitymon.FrontmostAppInfo, activitymon.ListRunningApps, activitymon.ListAppWindows, otherMicInputApps, meetingBrowserURL}

// meetingBrowserURL fails closed in every Go test binary, including helper
// children. A missing stub must fail the test before native browser automation.
func meetingBrowserURL(bundleID string) activitymon.BrowserURLRead {
	if flag.Lookup("test.v") != nil {
		panic("native meeting browser probe reached from a test binary; install a stub")
	}
	return activitymon.ReadBrowserURL(bundleID)
}

// browserMeetingTTL: the watcher and the capture loop both ask for the
// front tab's address; one lookup serves a few seconds.
const browserMeetingTTL = 4 * time.Second

type browserMeetingLookup struct {
	at      time.Time
	meeting string
}

var browserMeetings = struct {
	mu   sync.Mutex
	last map[string]browserMeetingLookup
	// Keep ownership of late workers explicit so test cleanup can join them.
	pending sync.WaitGroup
}{last: map[string]browserMeetingLookup{}}

// browserURLTimeout bounds the wait for a browser to answer for its tab's
// address: a busy browser never holds up the watcher or the capture loop.
const browserURLTimeout = time.Second

// browserMeetingInFront returns the call the browser's tab in front is on,
// from its address (meetingdetect.BrowserMeetingForURL), or "" when it is on
// no call page or its address cannot be read in time. A lookup is kept per
// browser window title, so switching tabs asks again.
func browserMeetingInFront(front activitymon.AppInfo, now time.Time) string {
	key := front.BundleID + "\x00" + front.WindowTitle
	browserMeetings.mu.Lock()
	if hit, ok := browserMeetings.last[key]; ok && now.Sub(hit.at) >= 0 && now.Sub(hit.at) < browserMeetingTTL {
		browserMeetings.mu.Unlock()
		return hit.meeting
	}
	browserMeetings.mu.Unlock()
	// Capture before the goroutine: a timed-out caller may restore its probes
	// before this goroutine is scheduled.
	answer := startBrowserURLProbe(front.BundleID)
	result := activitymon.BrowserURLRead{}
	select {
	case result = <-answer:
	case <-time.After(browserURLTimeout):
		result.TimedOut = true
	}
	if result.TimedOut {
		log.Printf("[meeting-watcher] %s did not give its tab's address in %s", front.BundleID, browserURLTimeout)
	}
	if result.InFlight {
		log.Printf("[meeting-watcher] %s browser lookup is still in flight", front.BundleID)
	}
	if result.TimedOut || result.InFlight {
		result.URL = ""
	}
	meeting := meetingdetect.BrowserMeetingForURL(result.URL)
	browserMeetings.mu.Lock()
	browserMeetings.last = map[string]browserMeetingLookup{key: {at: now, meeting: meeting}}
	browserMeetings.mu.Unlock()
	return meeting
}

// startBrowserURLProbe owns its probe for the entire asynchronous lookup.
// The buffered result lets a late probe finish after the caller times out.
func startBrowserURLProbe(bundleID string) <-chan activitymon.BrowserURLRead {
	probe := meetingProbes.browserURL
	answer := make(chan activitymon.BrowserURLRead, 1)
	browserMeetings.pending.Add(1)
	go func() {
		defer browserMeetings.pending.Done()
		answer <- probe(bundleID)
	}()
	return answer
}

// browserWindowOpenToCalls reports whether the browser window in front may
// be on a call: a private (incognito) window's address is never read and
// its calls never start, and neither are those of a window whose title
// cannot be read. Safari titles a private window with the page title alone
// and offers no other sign of one, so Safari's tabs are never read and no
// call starts in Safari.
func browserWindowOpenToCalls(front activitymon.AppInfo) bool {
	if browsers.FamilyOf(front.BundleID) == browsers.Safari {
		return false
	}
	title := strings.TrimSpace(front.WindowTitle)
	return title != "" && !activitymon.IsPrivateBrowsing(title)
}

// frontCall returns the call the app in front shows while it holds the
// microphone: a browser by its tab's address, any other app by its window
// title (IsMeetingEvent).
func frontCall(front activitymon.AppInfo, holders micInputSnapshot, now time.Time) meetingdetect.Result {
	if !holders.couldHold(front.BundleID) || userListedAppMatches(front.BundleID) {
		return meetingdetect.Result{}
	}
	if meetingdetect.IsBrowserBundle(front.BundleID) {
		if !browserWindowOpenToCalls(front) {
			return meetingdetect.Result{}
		}
		if name := browserMeetingInFront(front, now); name != "" {
			return meetingdetect.Result{IsMeeting: true, MeetingName: name, BundleID: front.BundleID, AppName: front.AppName}
		}
		return meetingdetect.Result{}
	}
	return meetingdetect.IsMeetingEvent(front.BundleID, front.WindowTitle)
}

// callOnMicrophone is the tracker's check with the live window lookup.
func callOnMicrophone(snapshot micInputSnapshot, now time.Time) meetingdetect.Result {
	return meetingMicHolds.callOnMicrophone(snapshot, now, lookupMeetingAppWindows)
}

// lookupMeetingAppWindows names the running app behind a mic holder and
// lists its windows.
func lookupMeetingAppWindows(holder meetingdetect.Result) (string, []activitymon.AppWindow) {
	for _, ra := range meetingProbes.runningApps() {
		if bundlesMatch(ra.BundleID, holder.BundleID) {
			return ra.Name, meetingProbes.appWindows(ra.Name)
		}
	}
	return "", nil
}

// withMicHolders records on a chunk who held the microphone when it was
// captured, and which meeting app among them shows a call.
func withMicHolders(chunk capturedAudioChunk, holders micInputSnapshot, now time.Time) capturedAudioChunk {
	chunk.MicHolders = holders
	chunk.CaptureEvidenceSet = true
	chunk.OtherMicUser = chunk.Source == backgroundListeningSourceMicrophone && holders.Active
	if meetingdetect.IsBrowserBundle(chunk.CapturedApp.BundleID) && holders.couldHold(chunk.CapturedApp.BundleID) && browserWindowOpenToCalls(chunk.CapturedApp) {
		chunk.BrowserMeeting = browserMeetingInFront(chunk.CapturedApp, now)
	}
	if meeting := holders.meetingApp(); meeting.IsMeeting {
		chunk.MeetingAppOnMic = meeting.MeetingName
		chunk.MeetingAppOnMicBundleID = meeting.BundleID
		if call := callOnMicrophone(holders, now); call.IsMeeting {
			chunk.MeetingCallOnMic = call.MeetingName
		}
	}
	return chunk
}

// bundlesMatch reports whether two bundle IDs name the same app, a helper
// process ("com.microsoft.teams2.helper") included.
func bundlesMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// holds reports whether a process of the given app holds the microphone.
// Safari reads it through the shared WebKit media process.
func (s micInputSnapshot) holds(bundleID string) bool {
	for _, id := range s.BundleIDs {
		if bundlesMatch(id, bundleID) {
			return true
		}
		if strings.HasPrefix(bundleID, "com.apple.Safari") && strings.HasPrefix(id, "com.apple.WebKit") {
			return true
		}
	}
	return false
}

// micHeldByMeeting reports whether the meeting's own app holds the
// microphone in this chunk: the evidence that keeps a call alive. Where the
// platform cannot name the processes on the microphone, any other process
// on it counts. System audio alone never does: music after a call is not
// the call.
func micHeldByMeeting(chunk capturedAudioChunk, meetingApp, meetingBundleID string) bool {
	if chunk.MeetingAppOnMic != "" && chunk.MeetingAppOnMic == meetingApp {
		return true
	}
	if chunk.MicHolders.holds(meetingBundleID) {
		return true
	}
	return chunk.MicHolders.Active && len(chunk.MicHolders.BundleIDs) == 0
}

// callChunkOnMicrophoneLocked reports whether a chunk captured during the
// call belongs to it: the call's own app holds the microphone, or held it
// moments ago with no other named app on it since (a device switch). Once
// the app lets go, the call is over as far as its audio goes, so dictation
// into Claude right after a Zoom call is never recorded into it. A browser
// call is keyed to the whole browser, which also holds the microphone for
// dictation in any other tab, so once its hold has lapsed past
// meetingMicHoldGap it never counts again for that call. Where the platform
// cannot tell who reads the microphone, every chunk belongs to the call.
// Caller holds am.mu.
func (a *App) callChunkOnMicrophoneLocked(chunk capturedAudioChunk) bool {
	am := a.activity
	if chunk.MicHolders.Unknown {
		return true
	}
	ts := time.Now().UnixMilli()
	if !chunk.CapturedAt.IsZero() {
		ts = chunk.CapturedAt.UnixMilli()
	}
	held := micHeldByMeeting(chunk, am.meetingAppName, am.meetingBundleID)
	if meetingdetect.IsBrowserBundle(am.meetingBundleID) {
		a.noteBrowserCallHoldLocked(held, ts, chunk.BrowserMeeting)
		if am.meetingBrowserMicReleased {
			return false
		}
	}
	last := am.lastMeetingAudioMs
	if last == 0 {
		last = am.meetingStartMs
	}
	if held {
		// The watcher's audio stickiness reads this stamp.
		if ts > am.lastMeetingAudioMs {
			am.lastMeetingAudioMs = ts
		}
		return true
	}
	if ts-last <= meetingMicHoldGap.Milliseconds() {
		return len(chunk.MicHolders.BundleIDs) == 0
	}
	return false
}

const (
	// browserCallBriefGap: a browser call's page letting go of the
	// microphone for less than this (a device switch) is still the call.
	browserCallBriefGap = 3 * time.Second
	// browserCallGapRelease: without the microphone this long, a browser
	// call is over.
	browserCallGapRelease = 10 * time.Second
)

// noteBrowserCallHoldLocked follows the browser of a browser call on the
// microphone, from the sampler (every second) and from chunk ends; an
// observation older than the last one is ignored, since chunks arrive late.
// A call page keeps its address after a hangup ("You left the meeting"), so
// the hold is what tells, measured in time:
//   - a gap shorter than browserCallBriefGap is the call (a device switch);
//   - a hold that comes back after a longer gap is the call only with the
//     call page in front (frontMeeting, from the tab's address); with any
//     other page in front it is dictation, and the call is over;
//   - browserCallGapRelease without the hold ends the call.
//
// Caller holds am.mu.
func (a *App) noteBrowserCallHoldLocked(held bool, ts int64, frontMeeting string) {
	am := a.activity
	if am.meetingBrowserMicReleased || ts < am.meetingBrowserObservedMs {
		return
	}
	am.meetingBrowserObservedMs = ts
	if held {
		gapSince := am.meetingBrowserGapSinceMs
		am.meetingBrowserGapSinceMs = 0
		if gapSince != 0 && ts-gapSince >= browserCallBriefGap.Milliseconds() && frontMeeting != am.meetingAppName {
			am.meetingBrowserMicReleased = true
			log.Printf("[meeting-audio] %s took the microphone back %s after the call let go of it, with no call page in front: the call is over", am.meetingBundleID, time.Duration(ts-gapSince)*time.Millisecond)
			return
		}
		if ts > am.lastMeetingAudioMs {
			am.lastMeetingAudioMs = ts
		}
		return
	}
	if am.meetingBrowserGapSinceMs == 0 {
		am.meetingBrowserGapSinceMs = ts
	}
	if ts-am.meetingBrowserGapSinceMs >= browserCallGapRelease.Milliseconds() {
		am.meetingBrowserMicReleased = true
	}
}
