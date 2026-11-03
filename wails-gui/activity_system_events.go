package main

import (
	"sync/atomic"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// activityCaptureSuppressed pauses screen and accessibility capture while
// Isoform itself drives the screen (the web calendar peek), so the page it
// opened is never recorded as the user's own activity.
var activityCaptureSuppressed atomic.Bool

func setActivityCaptureSuppressed(on bool) { activityCaptureSuppressed.Store(on) }

// recordSystemActivityEvent adds an event Isoform itself produced (a web
// calendar read, a device starting, the screen locking) to the activity
// buffer, so it lands in the current session and shows in the Activity log.
// It is dropped while observing is paused, like every other event, and when
// the activity store has not been set up.
func (a *App) recordSystemActivityEvent(evt activitymon.ActivityEvent) {
	if a == nil || a.IsClosedEyesModeEnabled() {
		return
	}
	if a.activity == nil {
		a.initActivityMonitor()
	}
	if a.activity == nil || a.activity.buf == nil {
		return
	}
	if evt.Timestamp == 0 {
		evt.Timestamp = time.Now().UnixMilli()
	}
	if evt.AppBundleID == "" && evt.AppName == "" {
		info := activitymon.FrontmostAppInfo()
		evt.AppBundleID = info.BundleID
		evt.AppName = info.AppName
	}
	a.activity.buf.Push(evt)
}
