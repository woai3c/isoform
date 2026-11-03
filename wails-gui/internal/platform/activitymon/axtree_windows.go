//go:build windows

package activitymon

// CollectFocusedWindowText returns the flat-text content of the foreground
// window's UI Automation tree. Mirrors the macOS AX walker — same role
// filter, same newline-joined output. Falls back to the foreground window
// title when the UIA subprocess is unavailable or returns nothing (which
// can happen for canvas-only apps, secure desktops, or before the
// PowerShell stack finishes warming up).
//
// The raw subprocess output is passed through cleanFlatText to suppress
// the three duplication patterns Chromium UIA produces that the PS
// window-N dedup can't catch (PUA icon glyphs, intra-string "X X"
// concatenation, far-apart line duplicates). See uia_text_clean_windows.go.
func CollectFocusedWindowText() string {
	if text := getUIA().CollectText(0); text != "" {
		return cleanFlatText(text)
	}
	_, title, _ := getForegroundWindowInfo()
	return title
}
