//go:build !darwin

package main

// notificationExplicitlyDenied is macOS-specific. On Windows and Linux the
// notify package delivers real OS notifications, which the platform already
// suppresses when the user disables them — so there is nothing to gate here.
func notificationExplicitlyDenied() bool {
	return false
}
