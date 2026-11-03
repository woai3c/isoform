//go:build !darwin

package main

// clearWindowBackgroundForTransparency is a no-op on non-macOS
// platforms. See clear_window_bg_darwin.go for the rationale.
func clearWindowBackgroundForTransparency() {}

// configureSpotlightAppPresentation is a no-op on non-macOS platforms.
func configureSpotlightAppPresentation() {}

// setSpotlightTitle is a no-op on non-macOS platforms — only the
// darwin CGO layer needs the window title for filtering.
func setSpotlightTitle(string) {}
