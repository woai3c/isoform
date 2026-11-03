package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
)

func newMemoryInjectionPrivacyFixture(t *testing.T) *App {
	t.Helper()
	app := newPrivacyReadTestApp(t)
	dir, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(dir, MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app.memoryStore = store
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	// A mixed corpus keeps the matched term above the injection relevance
	// threshold without altering production ranking.
	for i := range 20 {
		if err := os.WriteFile(filepath.Join(store.baseDir, fmt.Sprintf("cooking-%02d.md", i)), []byte(fixtureUnrelatedMemory), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func TestMemoryInjectionAppliesReadTimeSourcePrivacy(t *testing.T) {
	for _, fixture := range []struct {
		name, source string
		protect      func(*App)
	}{
		{name: "memory app protected after indexing", source: "memory", protect: func(app *App) {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}},
		{name: "knowledge app protected after indexing", source: "knowledge", protect: func(app *App) {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}},
		{name: "source captured during observation pause", source: "memory", protect: func(app *App) {
			app.saveObservationPauses([]observationPause{{Start: 100_000, End: 160_000}})
		}},
		{name: "closed eyes skips injection", source: "memory", protect: func(app *App) {
			app.closedEyes.enabled.Store(true)
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			app := newMemoryInjectionPrivacyFixture(t)
			insertTestSession(t, app.activity.store, "injection-source", "done", 100_000)
			dir := app.memoryStore.baseDir
			if fixture.source == "knowledge" {
				var err error
				dir, err = knowledgeDir()
				if err != nil {
					t.Fatal(err)
				}
			}
			content := "# Injection recall\n\n- **Activity Session**: injection-source\n\ninjectionprivacyneedle protected-source-payload\n"
			if err := os.WriteFile(filepath.Join(dir, "source.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := app.memoryStore.Sync("injection-privacy-fixture"); err != nil {
				t.Fatal(err)
			}
			const prompt = "Please help with this task."
			baseline := app.injectMemoryContext(context.Background(), prompt, "injectionprivacyneedle")
			if !strings.Contains(baseline, "protected-source-payload") {
				t.Fatal("fixture did not inject the source before its privacy changed")
			}
			fixture.protect(app)
			got := app.injectMemoryContext(context.Background(), prompt, "injectionprivacyneedle")
			if got != prompt {
				t.Fatalf("protected recall entered model prompt: %q", got)
			}
		})
	}
}

func TestMemoryInjectionRedactsRefreshedUserEdit(t *testing.T) {
	app := newMemoryInjectionPrivacyFixture(t)
	writeUserControlMemory(t, app.memoryStore.baseDir, "edited", "Contact preference", "activity", "injectioneditneedle original observation")
	if err := app.memoryStore.Sync("injection-edit-fixture"); err != nil {
		t.Fatal(err)
	}
	// The explicit edit replaces the indexed observation at the injection
	// boundary. Redacting only the search result would miss this fresh body.
	writeUserControlMemory(t, app.memoryStore.baseDir, "edited", "Contact preference", "user_edit", "Use alice@example.com for injectioneditneedle revised preference")
	got := app.injectMemoryContext(context.Background(), "Please help.", "injectioneditneedle")
	if !strings.Contains(got, "revised preference") || !strings.Contains(got, "attribution=user_edit") {
		t.Fatalf("explicit edit no longer wins over indexed observation: %q", got)
	}
	if strings.Contains(got, "alice@example.com") || !strings.Contains(got, "[REDACTED:") {
		t.Fatalf("fresh user edit entered model prompt without redaction: %q", got)
	}
}

func TestMemoryInjectionChecksExactRefreshedUserEdit(t *testing.T) {
	app := newMemoryInjectionPrivacyFixture(t)
	writeUserControlMemory(t, app.memoryStore.baseDir, "edited", "Contact preference", "user_edit", "original observation")
	results := []MemorySearchResult{{Path: "edited.md", Source: "memory", Snippet: "original observation", Score: 1}}
	filter := cachedMemorySearchReadFilter(app.memoryStore, func(_ string, content string) bool { return app.memorySourceAllowsRead(content) })
	results, err := filter(results)
	if err != nil || len(results) != 1 {
		t.Fatalf("safe index fixture: %v %v", results, err)
	}
	// The current file changes between indexed admission and explicit-edit
	// refresh. The original path allow-set cannot authorize its new bytes.
	content := "# Contact preference\n\n- **Source**: user_edit\n- **Apps**: Bitwarden\n\nprivate refreshed content\n"
	if err := os.WriteFile(filepath.Join(app.memoryStore.baseDir, "edited.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	results, err = applyMemoryUserControls(app.memoryStore.baseDir, results, func(_ string, content string) bool { return app.memorySourceAllowsRead(content) })
	if err != nil || len(results) != 0 {
		t.Fatalf("refreshed private body inherited earlier permission: %v %v", results, err)
	}
}
