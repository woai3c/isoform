//go:build windows

package activitymon

import "wails-gui/internal/browsers"

// Browser URL extraction is not yet implemented on Windows.

// IsBrowser returns true if the exe belongs to a known web browser
// (internal/browsers, the one list).
func IsBrowser(id string) bool { return browsers.FamilyOf(id) == browsers.WindowsBrowser }

// GetBrowserURL returns the URL of the active tab in the frontmost browser.
func GetBrowserURL(_ string) string { return "" }

// ReadBrowserURL has no native URL transport on this platform.
func ReadBrowserURL(bundleID string) BrowserURLRead {
	return BrowserURLRead{URL: GetBrowserURL(bundleID)}
}
