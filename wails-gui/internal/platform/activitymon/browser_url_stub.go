//go:build !darwin && !windows

package activitymon

import "wails-gui/internal/browsers"

// IsBrowser returns true if the bundle ID belongs to a known web browser.
func IsBrowser(id string) bool { return browsers.FamilyOf(id) != browsers.NotABrowser }

// GetBrowserURL returns the URL of the active tab in the frontmost browser.
func GetBrowserURL(_ string) string { return "" }

// ReadBrowserURL has no native URL transport on this platform.
func ReadBrowserURL(bundleID string) BrowserURLRead {
	return BrowserURLRead{URL: GetBrowserURL(bundleID)}
}
