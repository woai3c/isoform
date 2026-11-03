//go:build !darwin

package activitymon

// CloseBrowserTabIfURL has no AppleScript to speak outside macOS.
func CloseBrowserTabIfURL(bundleID, fragment string) bool { return false }

// ActivateApp is a no-op outside macOS.
func ActivateApp(bundleID string) bool { return false }

// BrowserActiveTabRef is unavailable outside macOS.
func BrowserActiveTabRef(bundleID string) string { return "" }

// CloseBrowserTabRef is unavailable outside macOS.
func CloseBrowserTabRef(bundleID, ref, fragment string) bool { return false }

// DefaultBrowserBundleID is unknown outside macOS.
func DefaultBrowserBundleID() string { return "" }

// BrowserScriptable is false outside macOS.
func BrowserScriptable(bundleID string) bool { return false }
