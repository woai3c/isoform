//go:build darwin

package main

import (
	"time"

	"wails-gui/internal/platform/activitymon"
)

// darwinCalendarPeekDriver drives the default browser through the same
// helpers the activity monitor and computer use already rely on: NSWorkspace
// to open the URL, AppleScript for the tab identity, its URL and the close,
// the accessibility tree walker to read the page, CoreGraphics for the idle
// time.
type darwinCalendarPeekDriver struct{}

func newCalendarPeekDriver() calendarPeekDriver { return darwinCalendarPeekDriver{} }

func (darwinCalendarPeekDriver) openURL(url string) bool      { return activitymon.OpenURL(url) }
func (darwinCalendarPeekDriver) frontmostBundle() string      { return activitymon.FrontmostAppBundleID() }
func (darwinCalendarPeekDriver) isBrowser(bundle string) bool { return activitymon.IsBrowser(bundle) }
func (darwinCalendarPeekDriver) browserURL(bundle string) string {
	return activitymon.GetBrowserURL(bundle)
}
func (darwinCalendarPeekDriver) activeTabRef(bundle string) string {
	return activitymon.BrowserActiveTabRef(bundle)
}
func (darwinCalendarPeekDriver) closeTabRef(bundle, ref, fragment string) bool {
	return activitymon.CloseBrowserTabRef(bundle, ref, fragment)
}
func (darwinCalendarPeekDriver) readTree(bundle string) *activitymon.AXNode {
	return activitymon.CollectWindowTreeByBundle(bundle)
}
func (darwinCalendarPeekDriver) activate(bundle string) bool { return activitymon.ActivateApp(bundle) }
func (darwinCalendarPeekDriver) idle() time.Duration         { return activitymon.IdleTime() }
func (darwinCalendarPeekDriver) defaultBrowser() string      { return activitymon.DefaultBrowserBundleID() }
func (darwinCalendarPeekDriver) sleep(d time.Duration)       { time.Sleep(d) }
