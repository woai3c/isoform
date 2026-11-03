package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestMemoryHumanReviewTranslationAndRestoreRemainAvailable(t *testing.T) {
	for _, action := range []string{"live", "history", "all history", "archive"} {
		t.Run(action, func(t *testing.T) {
			app, dir := newForgetTestApp(t)
			original := "# Existing preference\n\n- **Source**: activity\n\n## Memory Points\n\n- Keeps a separate staging environment.\n"
			if err := os.WriteFile(filepath.Join(dir, "retained.md"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			history, err := memoryHistoryDir(dir, "retained")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(history, "1000.md"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := recordMemoryTombstone(dir, "forgotten", []string{"# Forgotten\n\nPrivate fact."}, time.Now(), memoryForgetSemantics{
				Version: memorySemanticVersion, Model: "offline-model", Hashes: []string{"forgotten-semantic-hash"},
			}); err != nil {
				t.Fatal(err)
			}
			runner := &memoryTranslationFakeRunner{ready: true, response: "# Translated preference\n\n## Memory Points\n\n- Uses an independent staging environment.\n"}
			// Even a prior semantic result matching a forgotten fact cannot
			// overrule this explicit transformation of a retained record.
			semanticContent := runner.response
			if action == "archive" {
				semanticContent = original
			}
			if err := cacheMemorySemanticHash(dir, "offline-model", semanticContent, "forgotten-semantic-hash"); err != nil {
				t.Fatal(err)
			}
			app.runner = runner
			app.memoryCanonicalizer = func(string, string) (string, error) {
				t.Error("explicit mutation requested observation canonicalization")
				return "", errors.New("canonicalization unavailable")
			}
			var result MemoryEntry
			switch action {
			case "live":
				result, err = app.TranslateMemoryEntryToCurrentLanguage("retained")
			case "history":
				result, err = app.TranslateMemoryVersionToCurrentLanguage("retained", 1000)
			case "all history":
				_, err = app.translateAllMemoryVersionsToLanguage("retained", "English")
				if err == nil {
					result.Content, err = app.GetMemoryVersion("retained", 1000)
				}
			case "archive":
				writeForgetArchiveFixture(t, dir, "retained-archive", "archived-unrelated", "Archived unrelated", original)
				_, err = app.RestoreArchivedMemory("retained-archive")
				if err == nil {
					result, err = app.GetMemoryEntry("archived-unrelated")
				}
			}
			if err != nil {
				t.Fatalf("explicit %s of unrelated record failed: %v", action, err)
			}
			if parseMemoryFrontmatter(result.Content).Source != "activity" {
				t.Fatalf("explicit %s changed original source metadata: %s", action, result.Content)
			}
			id := "retained"
			if action == "archive" {
				id = "archived-unrelated"
			}
			if suppressed, err := isMemoryForgotten(dir, id, result.Content, time.Now()); err != nil || suppressed {
				t.Fatalf("explicit %s became unusable after save: suppressed=%v err=%v", action, suppressed, err)
			}
		})
	}
}

func TestMemoryHumanReviewKeepsErasedIDAndVersionGuards(t *testing.T) {
	for _, marker := range []string{"id", "version"} {
		for _, action := range []string{"live", "history", "all history", "archive"} {
			t.Run(marker+"/"+action, func(t *testing.T) {
				app, dir := newForgetTestApp(t)
				original := "# Private historical statement\n\n- **Source**: activity\n\nA forgotten original observation.\n"
				if err := os.WriteFile(filepath.Join(dir, "retained.md"), []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				history, err := memoryHistoryDir(dir, "retained")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(history, "1000.md"), []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				forgottenID := "retained"
				prepared := memoryForgetSemantics{Version: memorySemanticVersion, Model: "offline-model"}
				if marker == "version" {
					forgottenID = "separate-forgotten-source"
					prepared.RemovedVersions = []string{memoryForgetHistoricalSelector("retained", original)}
				}
				if err := recordMemoryTombstone(dir, forgottenID, []string{original}, time.Now(), prepared); err != nil {
					t.Fatal(err)
				}
				app.runner = &memoryTranslationFakeRunner{ready: true, response: "# Translated private statement\n\nThe same private observation in different words.\n"}
				switch action {
				case "live":
					_, err = app.TranslateMemoryEntryToCurrentLanguage("retained")
				case "history":
					_, err = app.TranslateMemoryVersionToCurrentLanguage("retained", 1000)
				case "all history":
					_, err = app.translateAllMemoryVersionsToLanguage("retained", "English")
				case "archive":
					if err := os.Remove(filepath.Join(dir, "retained.md")); err != nil {
						t.Fatal(err)
					}
					writeForgetArchiveFixture(t, dir, "forgotten-archive", "retained", "Old private version", original)
					_, err = app.RestoreArchivedMemory("forgotten-archive")
				}
				if !errors.Is(err, errMemoryForgotten) {
					t.Fatalf("explicit %s restored erased %s: %v", action, marker, err)
				}
			})
		}
	}
}

type memoryHumanReviewTranslationRunner struct {
	*memoryTranslationFakeRunner
	beforeResponse func()
}

func (runner *memoryHumanReviewTranslationRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	runner.beforeResponse()
	return runner.memoryTranslationFakeRunner.ExecuteTask(ctx, input, callback)
}

func TestMemoryHumanReviewHistoryTranslationRevalidatesSelectedVersion(t *testing.T) {
	for _, action := range []string{"remove", "replace"} {
		t.Run(action, func(t *testing.T) {
			app, dir := newForgetTestApp(t)
			live := "# Current\n\nKeep this current preference.\n"
			if err := os.WriteFile(filepath.Join(dir, "retained.md"), []byte(live), 0600); err != nil {
				t.Fatal(err)
			}
			history, err := memoryHistoryDir(dir, "retained")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(history, "1000.md")
			if err := os.WriteFile(path, []byte("# Selected old version\n\nEarlier preference.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			app.runner = &memoryHumanReviewTranslationRunner{
				memoryTranslationFakeRunner: &memoryTranslationFakeRunner{ready: true, response: "# Translated historical preference\n\nA stale old preference.\n"},
				beforeResponse: func() {
					var err error
					switch action {
					case "remove":
						err = os.Remove(path)
					case "replace":
						err = os.WriteFile(path, []byte("# Different history\n\nA changed source version.\n"), 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
				},
			}
			if _, err := app.TranslateMemoryVersionToCurrentLanguage("retained", 1000); err == nil {
				t.Fatal("translation committed a historical version removed or changed during inference")
			}
			current, err := os.ReadFile(filepath.Join(dir, "retained.md"))
			if err != nil || string(current) != live {
				t.Fatalf("stale historical translation overwrote current memory: %q err=%v", current, err)
			}
		})
	}
}

func TestMobileBriefReviewNotRightRemovesOnlySelectedMemory(t *testing.T) {
	for _, offline := range []bool{false, true} {
		name := "online"
		if offline {
			name = "offline"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bridge, brief := mobileTestFeedbackBridge(t)
			bridge.app.activity = &activityMonitorState{store: newTestStore(t)}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			for id, content := range map[string]string{
				"mem-1":   "# Selected memory\n\n- **Source**: activity\n\n## Memory Points\n\n- Prefers quiet workspaces for focused writing.",
				"derived": "# Independent retained synthesis\n\n- **Source**: dream\n- **MergedFrom**: mem-1\n\n## Memory Points\n\n- Prefers quiet workspaces for focused writing.",
			} {
				if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			history, err := memoryHistoryDir(dir, "mem-1")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(history, "1000.md"), []byte("# Earlier selected version\n\nAn older observation."), 0600); err != nil {
				t.Fatal(err)
			}
			index, err := NewMemoryStore(filepath.Dir(dir), MemoryStoreConfig{Provider: "none"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { index.Close() })
			bridge.app.memoryStore = index
			if err := index.Sync("phone-single-record-test"); err != nil {
				t.Fatal(err)
			}
			if _, err := index.db.Exec(`UPDATE memory_chunks SET embedding = '[1,0]' WHERE path = 'derived.md'`); err != nil {
				t.Fatal(err)
			}
			if _, err := index.db.Exec(`INSERT INTO memory_embedding_cache(provider, model, provider_key, hash, embedding)
				SELECT 'test', 'test', 'test', hash, '[1,0]' FROM memory_chunks WHERE path = 'derived.md'`); err != nil {
				t.Fatal(err)
			}
			self := seedMemoryForgetSelf(t, bridge.app.activity.store, "mem-1", "An existing derived preference.")
			archive := writeForgetArchiveFixture(t, dir, "selected-archive", "mem-1", "Selected archived memory", "# Archived observation\n\nPrevious selected statement.")
			configureTestMemoryCanonicalizer(t, bridge.app)
			canonicalizer := bridge.app.memoryCanonicalizer
			calls := 0
			bridge.app.memoryCanonicalizer = func(prompt, model string) (string, error) {
				calls++
				if offline {
					return "", errors.New("offline")
				}
				return canonicalizer(prompt, model)
			}
			device := mobileTestDevice(mobileScopeBriefFeedback, mobileScopeMemoryFix)
			response := mobileTestChatCall(bridge, device, http.MethodPost,
				mobileTestFeedbackPath(brief, mobileTestItemKey(t, brief, "memory")),
				`{"idempotencyKey":"single-record-review","action":"not_right","reason":"Not me"}`)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"effect":"memory_removed"`) {
				t.Errorf("phone correction failed: %d %s", response.Code, response.Body.String())
			}
			if calls != 0 {
				t.Errorf("phone correction called semantic model %d times", calls)
			}
			if _, err := os.Stat(filepath.Join(dir, "mem-1.md")); !os.IsNotExist(err) {
				t.Errorf("selected memory remains: %v", err)
			}
			if _, err := os.Stat(history); !os.IsNotExist(err) {
				t.Errorf("single-record deletion did not clear its own history: %v", err)
			}
			for _, path := range []string{filepath.Join(dir, "derived.md"), archive} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("phone correction removed an unreviewed derivative/archive: %s: %v", path, err)
				}
			}
			var count int
			if err := bridge.app.activity.store.db.QueryRow(`SELECT count(*) FROM self_model_entries WHERE id = ?`, self.ID).Scan(&count); err != nil || count != 1 {
				t.Errorf("phone correction changed derived Self: count=%d err=%v", count, err)
			}
			if err := index.db.QueryRow(`SELECT count(*) FROM memory_chunks WHERE path = 'derived.md' AND embedding != ''`).Scan(&count); err != nil || count == 0 {
				t.Errorf("phone correction changed derived embeddings: count=%d err=%v", count, err)
			}
			if err := index.db.QueryRow(`SELECT count(*) FROM memory_embedding_cache`).Scan(&count); err != nil || count == 0 {
				t.Errorf("phone correction changed derived embedding cache: count=%d err=%v", count, err)
			}
			markers, err := loadMemoryTombstones(dir)
			if err != nil || len(markers) != 1 {
				t.Errorf("phone correction did not persist its local recurrence marker: %v, %v", markers, err)
			} else if memoryTombstoneHasKey(markers[0], memoryForgetIDKey("mem-1")) || markers[0].Semantics != nil {
				t.Errorf("phone correction created a cascade/model marker: %+v", markers[0])
			}
			injected, err := applyMemoryUserControls(dir, []MemorySearchResult{{Path: "derived.md", Source: "memory", Score: 1}})
			if err != nil || len(injected) != 1 {
				t.Errorf("retained derivative disappeared from injection: %+v, %v", injected, err)
			}
			candidates, err := NewMemoryDreamService(bridge.app).gatherCandidates(context.Background())
			found := false
			for _, candidate := range candidates {
				found = found || candidate.ID == "derived"
			}
			if err != nil || !found {
				t.Errorf("retained derivative disappeared from Dream: %+v, %v", candidates, err)
			}
		})
	}
}
