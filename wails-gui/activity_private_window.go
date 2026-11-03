package main

import (
	"strings"

	"wails-gui/internal/platform/activitymon"
)

// privateWindowEventLocked reports whether an event happened in a private or
// incognito browser window: its own window title says so, or it carries no
// title and comes from the app whose last title said so. A titleless event
// from a browser whose window title is not known yet (it was just switched
// to) counts as private: the window may be. Caller holds am.mu.
func privateWindowEventLocked(am *activityMonitorState, evt activitymon.ActivityEvent) bool {
	if title := strings.TrimSpace(evt.WindowTitle); title != "" {
		return activitymon.IsPrivateBrowsing(title)
	}
	if evt.AppBundleID != "" && evt.AppBundleID != am.lastWindowTitleBundleID {
		return activitymon.IsBrowser(evt.AppBundleID)
	}
	return activitymon.IsPrivateBrowsing(am.lastWindowTitle)
}

// scrubPrivateWindowEvent is what is stored of an event: unchanged outside a
// private window; inside one, keystrokes are dropped, and every other event
// keeps only its kind, time, app and position, without the window title,
// element labels or any text.
func scrubPrivateWindowEvent(evt activitymon.ActivityEvent, private bool) (activitymon.ActivityEvent, bool) {
	if !private {
		return evt, true
	}
	switch evt.Kind {
	case activitymon.EventKeyboard:
		return activitymon.ActivityEvent{}, false
	case activitymon.EventClick, activitymon.EventFocus, activitymon.EventAppSwitch:
	default:
		// Speech and Isoform's own events do not come through the monitor.
		return evt, true
	}
	return activitymon.ActivityEvent{
		Timestamp:     evt.Timestamp,
		Kind:          evt.Kind,
		AppBundleID:   evt.AppBundleID,
		AppName:       evt.AppName,
		PrivateWindow: true,
		ClickX:        evt.ClickX,
		ClickY:        evt.ClickY,
		ClickRole:     evt.ClickRole,
		FocusRole:     evt.FocusRole,
	}, true
}

// privateFrontLocked reports whether the last window title seen says the
// window in front is private. Caller holds am.mu.
func privateFrontLocked(am *activityMonitorState) bool {
	return activitymon.IsPrivateBrowsing(am.lastWindowTitle)
}

// liveBrowserWindowPrivate reads the title of the browser window in front
// right now, for a capture that would otherwise go by a title cached from
// the last event: a private window can take focus without one.
func liveBrowserWindowPrivate(bundleID string) bool {
	if !activitymon.IsBrowser(bundleID) {
		return false
	}
	info := activitymon.FrontmostAppInfo()
	return info.BundleID == bundleID && activitymon.IsPrivateBrowsing(info.WindowTitle)
}

// browserWindowPrivate reports whether a browser's window in front is
// private: from the activity monitor's last title for that browser when it
// has one, otherwise read live.
func (a *App) browserWindowPrivate(bundleID string) bool {
	if a != nil && a.activity != nil {
		am := a.activity
		am.mu.Lock()
		title, from := am.lastWindowTitle, am.lastWindowTitleBundleID
		am.mu.Unlock()
		if from == bundleID && title != "" {
			return activitymon.IsPrivateBrowsing(title)
		}
	}
	return liveBrowserWindowPrivate(bundleID)
}

// trackWindowTitleLocked remembers the last window title and the app it came
// from. A titleless event from another app clears it, so one app's title (a
// private window's) is never read as another's. Caller holds am.mu.
func trackWindowTitleLocked(am *activityMonitorState, evt activitymon.ActivityEvent) {
	if evt.WindowTitle != "" {
		am.lastWindowTitle = evt.WindowTitle
		am.lastWindowTitleBundleID = evt.AppBundleID
		return
	}
	if evt.AppBundleID != "" && evt.AppBundleID != am.lastWindowTitleBundleID {
		am.lastWindowTitle = ""
		am.lastWindowTitleBundleID = evt.AppBundleID
	}
}
