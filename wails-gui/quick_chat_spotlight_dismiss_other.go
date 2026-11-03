//go:build !darwin

package main

// setSpotlightDismissOnKey is darwin-only: it arms the macOS event tap's
// dismiss-on-typing flag (option C — typing in another app dismisses the
// non-activating pill). No-op on Windows/Linux, where the spotlight focus
// model is handled separately.
func setSpotlightDismissOnKey(_ bool) {}
