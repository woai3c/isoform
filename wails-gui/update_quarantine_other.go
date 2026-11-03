//go:build !darwin

package main

import "os/exec"

// stripQuarantineRecursive is a no-op outside macOS: com.apple.quarantine
// and Gatekeeper are darwin-only concepts.
func stripQuarantineRecursive(string) (int, error) {
	return 0, nil
}

// startDetachedShell only backs the darwin relaunch path; Windows restarts
// the new binary directly and Linux has no Gatekeeper-style relaunch dance.
func startDetachedShell(script string) error {
	return exec.Command("bash", "-c", script).Start()
}
