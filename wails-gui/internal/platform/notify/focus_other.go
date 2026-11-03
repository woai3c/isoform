//go:build !windows && !darwin

package notify

// IsAppForeground returns true if the app window is the foreground window.
// On platforms without native detection (Linux), we fall back to a
// conservative default (true) so the caller uses in-app toast.
func IsAppForeground() bool {
	return true
}
