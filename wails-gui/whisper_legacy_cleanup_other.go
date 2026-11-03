//go:build !windows

package main

// cleanupLegacyWhisperZombies is a no-op on non-Windows platforms. The old
// whisper-server subprocess on macOS was reliably reaped by the parent
// (and the new build no longer spawns it), so there's nothing to clean.
func cleanupLegacyWhisperZombies() {}
