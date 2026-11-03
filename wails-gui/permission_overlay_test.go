package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Only the two panes an app is dragged into get the overlay; opening any
// other pane closes it (openPermissionSettingsFromRect).
func TestPermissionOverlayOnlyForTheDragPanes(t *testing.T) {
	for _, key := range mainChatSetupPermissionOrder {
		want := key == "screen_recording" || key == "accessibility"
		if got := hasPermissionOverlay(key); got != want {
			t.Errorf("hasPermissionOverlay(%q) = %v, want %v", key, got, want)
		}
	}
}

// The overlay names the pane it sits over, in the user's language, and has
// no line at all for a pane without an overlay.
func TestPermissionOverlayTitleNamesItsPane(t *testing.T) {
	cases := []struct{ key, lang, want string }{
		{"accessibility", "en", "Drag Isoform to the list above to allow Accessibility"},
		{"screen_recording", "en", "Drag Isoform to the list above to allow Screen Recording"},
		{"accessibility", "zh-CN", "将 Isoform 拖到上方列表中，以允许辅助功能"},
		{"notifications", "en", ""},
		{"calendars", "zh-CN", ""},
	}
	for _, c := range cases {
		if got := permissionOverlayTitle(c.key, c.lang, "Isoform"); got != c.want {
			t.Errorf("permissionOverlayTitle(%q, %q) = %q, want %q", c.key, c.lang, got, c.want)
		}
	}
	if strings.Contains(permissionOverlayTitle("screen_recording", "en", "Isoform"), "Accessibility") {
		t.Error("the Screen Recording overlay names Accessibility")
	}
}

func TestPermissionOverlayClosesOnceGranted(t *testing.T) {
	var s permissionOverlayState
	var granted atomic.Bool
	var hidden atomic.Int32
	gen := s.show(func() {})
	done := make(chan struct{})
	go func() {
		watchPermissionOverlay(&s, gen, "accessibility", func(key string) bool {
			return key == "accessibility" && granted.Load()
		}, func() { hidden.Add(1) }, time.Millisecond, time.Minute)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	if hidden.Load() != 0 {
		t.Fatal("the overlay closed before the permission was granted")
	}
	granted.Store(true)
	waitForWatch(t, done)
	if hidden.Load() != 1 || s.current(gen) {
		t.Fatalf("hidden %d times, still current %v; want closed once", hidden.Load(), s.current(gen))
	}
}

func TestPermissionOverlayClosesAfterTheLimit(t *testing.T) {
	var s permissionOverlayState
	var hidden atomic.Int32
	gen := s.show(func() {})
	done := make(chan struct{})
	go func() {
		watchPermissionOverlay(&s, gen, "screen_recording", func(string) bool { return false },
			func() { hidden.Add(1) }, time.Millisecond, 15*time.Millisecond)
		close(done)
	}()
	waitForWatch(t, done)
	if hidden.Load() != 1 {
		t.Fatalf("hidden %d times after the limit, want 1", hidden.Load())
	}
}

// A newer overlay (or a dismissal) ends the old watch, which must never
// close the overlay that replaced its own.
func TestPermissionOverlayWatchLeavesANewerOverlayAlone(t *testing.T) {
	var s permissionOverlayState
	var granted atomic.Bool
	var hidden atomic.Int32
	old := s.show(func() {})
	done := make(chan struct{})
	go func() {
		watchPermissionOverlay(&s, old, "accessibility", func(string) bool { return granted.Load() },
			func() { hidden.Add(1) }, time.Millisecond, time.Minute)
		close(done)
	}()
	newer := s.show(func() {})
	granted.Store(true)
	waitForWatch(t, done)
	if hidden.Load() != 0 || !s.current(newer) {
		t.Fatalf("the old watch hid the newer overlay (hidden %d, newer current %v)", hidden.Load(), s.current(newer))
	}
	if s.dismissIf(old, func() { hidden.Add(1) }) {
		t.Fatal("dismissIf closed a replaced overlay")
	}
	s.dismiss(func() { hidden.Add(1) })
	if hidden.Load() != 1 || s.current(newer) {
		t.Fatal("dismiss did not close the overlay on screen")
	}
}

func waitForWatch(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not return")
	}
}
