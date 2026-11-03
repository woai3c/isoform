//go:build !darwin

package main

// setSpotlightFrameDarwin is a no-op stub on non-macOS platforms; the
// Windows fast path goes through setSpotlightFrameWin32, and Linux
// falls back to the Wails-based positioning in applySpotlightFrame.
func setSpotlightFrameDarwin(_ string, _, _ int, _ float64, _ bool) bool {
	return false
}
