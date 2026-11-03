package main

import (
	"sync"
	"time"
)

// quickChatDoubleTapWindow is the maximum interval between two
// consecutive taps of the trigger modifier key for the pair to be
// recognized as a "double tap". 400ms is a compromise: short enough
// to avoid firing on incidental single taps, long enough to tolerate
// a natural human cadence.
const quickChatDoubleTapWindow = 400 * time.Millisecond

// doubleTapDetector recognizes a double-tap of a single modifier key.
// Used only for the macOS default ("double-tap Option"); when the user
// switches to a normal modifier+key combo the detector is bypassed.
//
// blocked=true means another modifier is held alongside the target
// key (e.g. Shift+Option), in which case any in-flight tap is
// cancelled so we don't fight chord shortcuts.
//
// Concurrency: safe for a single producer (the OS event source) plus
// concurrent resetters; the mutex serializes all access.
type doubleTapDetector struct {
	mu      sync.Mutex
	window  time.Duration
	keyDown bool
	lastTap time.Time
}

func newDoubleTapDetector(window time.Duration) *doubleTapDetector {
	return &doubleTapDetector{window: window}
}

// observe records a key state transition and returns true iff it
// completes a recognized double-tap. The caller passes `now` rather
// than letting the detector read the clock so tests can drive it
// deterministically.
func (d *doubleTapDetector) observe(keyDown, blocked bool, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if blocked {
		d.keyDown = false
		return false
	}

	if keyDown && !d.keyDown {
		d.keyDown = true
		return false
	}
	if !keyDown && d.keyDown {
		d.keyDown = false
		if !d.lastTap.IsZero() && now.Sub(d.lastTap) <= d.window {
			d.lastTap = time.Time{}
			return true
		}
		d.lastTap = now
	}
	return false
}

// reset clears any in-flight tap state. Call when the listener
// restarts so a stale half-tap does not combine with a fresh one.
func (d *doubleTapDetector) reset() {
	d.mu.Lock()
	d.keyDown = false
	d.lastTap = time.Time{}
	d.mu.Unlock()
}

// quickChatHotkeyDetector is the singleton fed by the macOS event tap
// when the current binding is the double-tap-Option default.
var quickChatHotkeyDetector = newDoubleTapDetector(quickChatDoubleTapWindow)

// QuickChatInvokeEvent is the payload emitted to the renderer when the
// global hotkey fires. The renderer opens the overlay and uses bundleID
// to request app-specific suggestions.
type QuickChatInvokeEvent struct {
	FrontmostBundleID string `json:"frontmostBundleId"`
	// NativeWindow is true when a separate floating native window is
	// already being shown for this invocation. The renderer uses this
	// to avoid double-opening an in-app overlay.
	NativeWindow bool `json:"nativeWindow,omitempty"`
}

// quickChatHotkeyMu and quickChatApp let the platform hotkey hook reach
// the running App from a callback thread without taking the App's own
// lock. Both platforms register the App on Start and clear it on Stop.
var (
	quickChatHotkeyMu sync.Mutex
	quickChatApp      *App
)

// quickChatLifecycleMu serializes StartQuickChatHotkey / StopQuickChatHotkey
// against each other. The darwin tap-lifecycle C globals assume Start and
// Stop never run concurrently; this lock makes that hold even if the
// frontend invokes both bindings from different goroutines. It is distinct
// from quickChatHotkeyMu (which guards quickChatApp and is taken by the
// event-tap callback goroutines) so the callbacks never contend on it and
// there is no lock-ordering inversion.
var quickChatLifecycleMu sync.Mutex

// monitorError matches the shape used elsewhere in the wails-gui
// package so callers can type-check via errors.As without importing
// new types.
type monitorError struct{ msg string }

func (e *monitorError) Error() string { return e.msg }
