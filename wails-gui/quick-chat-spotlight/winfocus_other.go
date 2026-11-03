//go:build !windows

package main

// forceWindowFocus is Windows-only. On other platforms Wails' own
// activation behaviour is enough — macOS/Linux WMs don't have the
// same foreground-lock semantics.
func forceWindowFocus(title string) {}

// setSpotlightFrameWin32 is the Win32 SetWindowPos fast path used to
// apply size + position atomically (see the Windows implementation for
// rationale). Other platforms fall back to Wails' two-step
// WindowSetSize + WindowSetPosition — macOS' NSWindow.setFrame is
// already atomic enough that the user doesn't see an intermediate
// frame, and Linux WMs vary too widely to special-case.
func setSpotlightFrameWin32(title string, w, h int) bool { return false }
