package main

import (
	"fmt"
	"sync"
	"time"
)

// The drag overlay floats over System Settings while it shows the Screen
// Recording or Accessibility list, the two panes an app is dragged into.
// It used to stay up until its own back button was pressed: over the
// Notifications pane opened next, after the permission was on, and after
// System Settings was gone. Now it closes when another pane is opened from
// Isoform, once its permission is granted (this file), and when System
// Settings quits, stays in the background or moves to another pane (the
// overlay manager in permission_overlay_darwin.go).

const (
	permissionOverlayPollInterval = time.Second
	permissionOverlayWatchLimit   = 10 * time.Minute
)

// permissionOverlayNames names each pane the overlay is made for, in the
// words System Settings uses.
var permissionOverlayNames = map[string]map[string]string{
	"screen_recording": {"en": "Screen Recording", "zh-CN": "屏幕录制"},
	"accessibility":    {"en": "Accessibility", "zh-CN": "辅助功能"},
}

// hasPermissionOverlay reports whether opening key's pane shows the overlay.
func hasPermissionOverlay(key string) bool {
	_, ok := permissionOverlayNames[key]
	return ok
}

// permissionOverlayTitle is the overlay's line for key's pane, or "" for a
// pane that has no overlay, so the overlay can only ever name the pane it
// sits over.
func permissionOverlayTitle(key, lang, appName string) string {
	names, ok := permissionOverlayNames[key]
	if !ok {
		return ""
	}
	if normalizeUILanguage(lang) == "zh-CN" {
		return fmt.Sprintf("将 %s 拖到上方列表中，以允许%s", appName, names["zh-CN"])
	}
	return fmt.Sprintf("Drag %s to the list above to allow %s", appName, names["en"])
}

// permissionOverlayState numbers each overlay shown, so a watch ends when a
// newer overlay replaces its own or the overlay is dismissed.
type permissionOverlayState struct {
	mu  sync.Mutex
	gen uint64
}

var permissionOverlays permissionOverlayState

// show runs present under the lock as a new generation and returns it.
func (s *permissionOverlayState) show(present func()) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	present()
	return s.gen
}

// dismiss ends the current generation and runs hide.
func (s *permissionOverlayState) dismiss(hide func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	hide()
}

// dismissIf runs hide only while gen is still the overlay on screen.
func (s *permissionOverlayState) dismissIf(gen uint64, hide func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return false
	}
	s.gen++
	hide()
	return true
}

func (s *permissionOverlayState) current(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen == gen
}

// watchPermissionOverlay checks key's permission every interval while
// overlay gen is on screen and hides the overlay once it is granted, or
// after limit. It returns when the overlay is replaced or dismissed.
func watchPermissionOverlay(s *permissionOverlayState, gen uint64, key string, granted func(string) bool, hide func(), interval, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for {
		time.Sleep(interval)
		if !s.current(gen) {
			return
		}
		if granted(key) || !time.Now().Before(deadline) {
			s.dismissIf(gen, hide)
			return
		}
	}
}
