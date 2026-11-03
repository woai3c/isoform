package main

import (
	"testing"

	"wails-gui/internal/engagement"
)

// The digest pass paces after releasing the service lock while Settings and the
// phone write the preference maps, so it must hold a private copy.
func TestEngagementPrefsCopyLockedSharesNoMapWithTheService(t *testing.T) {
	s := &engagementService{}
	s.prefs.Weights = map[engagement.Kind]float64{engagement.KindBriefReady: 1}
	s.prefs.Enabled = map[engagement.Kind]bool{engagement.KindBriefReady: true}
	s.mu.Lock()
	copied := s.prefsCopyLocked()
	s.mu.Unlock()

	copied.Enabled[engagement.KindBriefReady] = false
	copied.Weights[engagement.KindBriefReady] = 9
	if !s.prefs.Enabled[engagement.KindBriefReady] || s.prefs.Weights[engagement.KindBriefReady] != 1 {
		t.Fatal("the copy shares a map with the service preferences")
	}
	s.prefs.Enabled[engagement.KindBriefReady] = true
	s.prefs.Enabled[engagement.KindBriefMissed] = true
	if _, shared := copied.Enabled[engagement.KindBriefMissed]; shared {
		t.Fatal("a later write by the service shows in the copy")
	}
}
