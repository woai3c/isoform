package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/engagement"
)

// The Notifications page shows each kind's latest notice as its real push
// line (sampleCopyKey, PROTOCOL.md 16.1). The frontend keeps those lines in
// its own catalogs (proactiveNotifications.samples, the key's dot written as
// an underscore); this test keeps both catalogs equal to the copy delivery
// uses, so a changed push line cannot leave Settings showing an old one.
func TestNotificationSampleCopyMatchesSettingsLocales(t *testing.T) {
	keys := []string{engagement.NotificationSampleActivityThread}
	for _, kind := range engagement.AllKinds() {
		if engagementKindSilent(kind) {
			continue
		}
		keys = append(keys, engagement.NotificationSampleCopyKey(kind, false))
	}
	for _, lang := range engagement.Languages {
		raw, err := os.ReadFile(filepath.Join("frontend", "src", "i18n", "locales", lang, "settings.json"))
		if err != nil {
			t.Fatalf("read %s settings.json: %v", lang, err)
		}
		var doc struct {
			ProactiveNotifications struct {
				Samples map[string]string `json:"samples"`
			} `json:"proactiveNotifications"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s settings.json: %v", lang, err)
		}
		samples := doc.ProactiveNotifications.Samples
		for _, key := range keys {
			want, ok := engagement.NotificationSampleCopy(key, lang)
			if !ok {
				t.Fatalf("%s: no copy for sample key %q", lang, key)
			}
			uiKey := strings.ReplaceAll(key, ".", "_")
			if got := samples[uiKey]; got != want.PushBody {
				t.Errorf("%s samples.%s = %q, want %q", lang, uiKey, got, want.PushBody)
			}
		}
		if len(samples) != len(keys) {
			t.Errorf("%s has %d samples, want %d (one per active kind and the Activity-thread variant)", lang, len(samples), len(keys))
		}
	}
}
