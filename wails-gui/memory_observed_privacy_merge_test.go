package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestMemoryObservedPreparationKeepsProtectedSourcesLocal(t *testing.T) {
	for _, closedEyes := range []bool{false, true} {
		name := "sensitive source"
		if closedEyes {
			name = "closed eyes"
		}
		t.Run(name, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			prior := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
			pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
			provenance := memoryReadProvenance{Version: 2, Kinds: []string{"conversation"}}
			if closedEyes {
				app.closedEyes.enabled.Store(true)
			} else {
				provenance.Apps = []string{"Diary"}
			}
			content := withMemoryReadProvenance(buildOKFMemoryMarkdown("private", "Private observation", time.Now(), memorySourceConversation, "user_insight", []string{"Private remembered preference"}), provenance)
			var calls atomic.Int32
			app.memoryCanonicalizer = func(string, string) (string, error) {
				calls.Add(1)
				return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
			}
			entry, err := app.saveMemoryEntry("private", content, false)
			if err != nil || entry.Content != content || calls.Load() != 0 {
				t.Fatalf("protected observation must persist locally without a model read: err=%v calls=%d", err, calls.Load())
			}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := recordMemoryTombstone(dir, entry.ID, []string{entry.Content}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := app.saveMemoryEntry(entry.ID, content, false); !errors.Is(err, errMemoryForgotten) || calls.Load() != 0 {
				t.Fatalf("model-read veto bypassed local observation suppression: err=%v calls=%d", err, calls.Load())
			}
		})
	}
}
