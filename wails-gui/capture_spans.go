package main

import (
	"sort"
	"sync"
	"time"

	"wails-gui/internal/browsers"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

// A chunk's app and microphone holders are read when it ends, five seconds
// after it began. What happened in between still counts: a protected app,
// an app the user listed under "Apps Isoform does not listen to" or a
// private browser window in front for part of the chunk, or a listed app on
// the microphone for part of it. The spans below follow those moments. They
// are fed by the activity monitor's events and by a sampler that runs every
// second while listening runs, so they hold without the monitor (Watch my
// screen off, or its permissions missing).

// captureSampleInterval is how often the sampler looks.
const captureSampleInterval = time.Second

// frontSpanKeep bounds the completed stretches a span remembers. A chunk is
// annotated a few seconds after it ends (the microphone chunk waits for its
// system audio sibling), and with a sample every second that is far fewer
// stretches than this.
const frontSpanKeep = 32

// frontSpanStretch is one completed stretch, [sinceMs, untilMs].
type frontSpanStretch struct {
	sinceMs int64
	untilMs int64
}

// frontSpan follows when something of one kind is going on: sinceMs is when
// the current stretch began (0 when none is), done the last completed
// stretches, oldest first.
type frontSpan struct {
	sinceMs int64
	done    []frontSpanStretch
}

func (f *frontSpan) note(matches bool, ts int64) {
	if matches {
		if f.sinceMs == 0 {
			f.sinceMs = ts
		}
		return
	}
	if f.sinceMs != 0 {
		f.remember(f.sinceMs, ts)
		f.sinceMs = 0
	}
}

// remember also accepts late exclusions. Merge in time order so repeated old
// delivery neither inverts a stretch nor evicts more recent privacy history.
func (f *frontSpan) remember(sinceMs, untilMs int64) {
	f.done = append(f.done, frontSpanStretch{sinceMs: sinceMs, untilMs: untilMs})
	sort.Slice(f.done, func(i, j int) bool { return f.done[i].sinceMs < f.done[j].sinceMs })
	merged := f.done[:0]
	for _, span := range f.done {
		if len(merged) > 0 && span.sinceMs <= merged[len(merged)-1].untilMs {
			last := &merged[len(merged)-1]
			last.untilMs = max(last.untilMs, span.untilMs)
		} else {
			merged = append(merged, span)
		}
	}
	f.done = merged
	if len(f.done) > frontSpanKeep {
		f.done = append(f.done[:0:0], f.done[len(f.done)-frontSpanKeep:]...)
	}
}

// overlaps reports whether it was going on at any moment from startMs to
// endMs: the current stretch, or any remembered one, reaches into it.
func (f frontSpan) overlaps(startMs, endMs int64) bool {
	if f.sinceMs != 0 && f.sinceMs <= endMs {
		return true
	}
	for _, d := range f.done {
		if d.sinceMs <= endMs && d.untilMs >= startMs {
			return true
		}
	}
	return false
}

// captureSpans are the spans of one app run.
type captureSpans struct {
	mu           sync.Mutex
	protected    frontSpan
	listed       frontSpan
	private      frontSpan
	listedHolder frontSpan
	unavailable  frontSpan
	// Newest sample or positive exclusion event; safe events never advance it.
	frontAtMs     int64
	frontSamples  []int64 // accepted successful samples, oldest first
	frontReleases []int64 // keep recovery boundaries through repeated public samples
}

// noteFront records a successful live focus sample. Only a strictly newer
// sample may release exclusions; equal timestamps favor the exclusion.
func (c *captureSpans) noteFront(bundleID string, private bool, ts int64) {
	_, sensitive := pii.IsSensitiveApp(bundleID)
	listed := userListedAppMatches(bundleID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts <= c.frontAtMs {
		if ts == c.frontAtMs {
			c.noteExclusionsLocked(sensitive, listed, private, ts)
		} else {
			c.rememberFrontLocked(sensitive, listed, private, ts)
		}
		return
	}
	c.frontAtMs = ts
	if len(c.frontSamples) == 0 || c.unavailable.sinceMs != 0 ||
		(c.protected.sinceMs != 0 && !sensitive) || (c.listed.sinceMs != 0 && !listed) || (c.private.sinceMs != 0 && !private) {
		c.frontReleases = append(c.frontReleases, ts)
		if len(c.frontReleases) > frontSpanKeep {
			c.frontReleases = append(c.frontReleases[:0:0], c.frontReleases[len(c.frontReleases)-frontSpanKeep:]...)
		}
	}
	c.frontSamples = append(c.frontSamples, ts)
	if len(c.frontSamples) > frontSpanKeep {
		c.frontSamples = append(c.frontSamples[:0:0], c.frontSamples[len(c.frontSamples)-frontSpanKeep:]...)
	}
	c.unavailable.note(false, ts)
	c.protected.note(sensitive, ts)
	c.listed.note(listed, ts)
	c.private.note(private, ts)
}

// noteActivityFront adds exclusions from an event's original time, not proof
// of current safe focus. Even a newly delivered title can be cached or delayed
// by AX enrichment. Events never release unknown or other privacy exclusions.
func (c *captureSpans) noteActivityFront(bundleID string, private bool, ts int64) {
	_, sensitive := pii.IsSensitiveApp(bundleID)
	listed := userListedAppMatches(bundleID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts < c.frontAtMs {
		c.rememberFrontLocked(sensitive, listed, private, ts)
		return
	}
	if !sensitive && !listed && !private {
		return
	}
	c.frontAtMs = ts
	c.noteExclusionsLocked(sensitive, listed, private, ts)
}

func (c *captureSpans) noteExclusionsLocked(sensitive, listed, private bool, ts int64) {
	if sensitive {
		c.protected.note(true, ts)
	}
	if listed {
		c.listed.note(true, ts)
	}
	if private {
		c.private.note(true, ts)
	}
}

// A late exclusion ends at the next accepted successful sample, not the latest
// one: already established public recovery must remain public. Without such a
// sample, the exclusion stays open even if a newer historical event arrived.
func (c *captureSpans) rememberFrontLocked(sensitive, listed, private bool, ts int64) {
	until, closed := c.nextFrontSampleLocked(ts)
	if sensitive {
		c.protected.remember(ts, until)
	}
	if listed {
		c.listed.remember(ts, until)
	}
	if private {
		c.private.remember(ts, until)
	}
	if !closed {
		c.noteExclusionsLocked(sensitive, listed, private, ts)
	}
}

func (c *captureSpans) nextFrontSampleLocked(ts int64) (int64, bool) {
	until, found := c.frontAtMs, false
	for _, samples := range [][]int64{c.frontSamples, c.frontReleases} {
		for _, sample := range samples {
			if sample > ts {
				until, found = min(until, sample), true
				break
			}
		}
	}
	return until, found
}

// noteUnavailable keeps existing exclusions open and excludes unknown focus.
func (c *captureSpans) noteUnavailable(ts int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts < c.frontAtMs {
		until, closed := c.nextFrontSampleLocked(ts)
		c.unavailable.remember(ts, until)
		if !closed {
			c.unavailable.note(true, ts)
		}
		return
	}
	c.frontAtMs = ts
	c.unavailable.note(true, ts)
}

func (c *captureSpans) focusUnavailableDuring(startMs, endMs int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unavailable.overlaps(startMs, endMs)
}

// noteHolders records whether a listed app held the microphone at ts.
func (c *captureSpans) noteHolders(holders micInputSnapshot, ts int64) {
	listed := holders.listedAppHolds()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listedHolder.note(listed, ts)
}

// during reports what went on from startMs to endMs: a protected app in
// front, a listed app in front or on the microphone, a private window in
// front.
func (c *captureSpans) during(startMs, endMs int64) (protected, listed, private bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protected.overlaps(startMs, endMs),
		c.listed.overlaps(startMs, endMs) || c.listedHolder.overlaps(startMs, endMs),
		c.private.overlaps(startMs, endMs)
}

// privateBrowserWindow reports whether the app in front is a browser whose
// window title says it is private. Safari's private windows carry no such
// title, so they cannot be told this way.
func privateBrowserWindow(front activitymon.AppInfo) bool {
	return browsers.IsBrowser(front.BundleID) && activitymon.IsPrivateBrowsing(front.WindowTitle)
}

// sampleCaptureState runs every second while listening runs: it feeds the
// spans from the app in front and the microphone holders, and follows the
// browser of a browser call letting go of the microphone between chunks.
func (a *App) sampleCaptureState(now time.Time) {
	if a == nil || a.IsClosedEyesModeEnabled() {
		return
	}
	front := meetingProbes.frontmost()
	holders := meetingProbes.micHolders()
	ts := now.UnixMilli()
	if front.FocusUnavailable {
		a.captureSpans.noteUnavailable(ts)
	} else {
		a.captureSpans.noteFront(front.BundleID, privateBrowserWindow(front), ts)
	}
	a.captureSpans.noteHolders(holders, ts)
	if a.activity != nil && !front.FocusUnavailable {
		a.followBrowserCallHold(front, holders, now)
	}
}

// followBrowserCallHold feeds the sampler's view of the browser of a browser
// call into noteBrowserCallHoldLocked. The tab's address is read (outside
// the monitor lock) only when the hold comes back after a gap and the
// browser is in front.
func (a *App) followBrowserCallHold(front activitymon.AppInfo, holders micInputSnapshot, now time.Time) {
	am := a.activity
	ts := now.UnixMilli()
	am.mu.Lock()
	bundle := am.meetingBundleID
	if !am.inMeeting || am.meetingBrowserMicReleased || holders.Unknown || !meetingdetect.IsBrowserBundle(bundle) {
		am.mu.Unlock()
		return
	}
	held := holders.holds(bundle) || (holders.Active && len(holders.BundleIDs) == 0)
	afterGap := held && am.meetingBrowserGapSinceMs != 0 && ts-am.meetingBrowserGapSinceMs >= browserCallBriefGap.Milliseconds()
	am.mu.Unlock()
	frontMeeting := ""
	if afterGap && bundlesMatch(front.BundleID, bundle) && browserWindowOpenToCalls(front) {
		frontMeeting = browserMeetingInFront(front, now)
	}
	am.mu.Lock()
	if am.inMeeting && am.meetingBundleID == bundle {
		a.noteBrowserCallHoldLocked(held, ts, frontMeeting)
	}
	am.mu.Unlock()
}
