package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestDedupCaptureIncludesOnlyRenderedSourceSnapshots(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	insertTestSession(t, app.activity.store, "capture-source", "dismissed", 1000)
	store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), configDirName), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Now()
	original := map[string]string{}
	for _, source := range []MemorySource{MemorySourceMemory, MemorySourceKnowledge} {
		dir := store.baseDir
		if source == MemorySourceKnowledge {
			dir = store.knowledgeDir
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			id := fmt.Sprintf("%s-safe-%d", source, i)
			original[id] = fmt.Sprintf("# Recall %s %d\n\n- **Apps**: Notes\n- **Activity Session**: capture-source\n\n## Memory Points\n\n- capturelineage evidence %d %s %s", source, i, i, activityPrivacySecret, strings.Repeat("tail detail ", 80))
			writeKnowledgeFile(t, dir, id, original[id], base.Add(-time.Duration(i+1)*time.Hour))
		}
		writeKnowledgeFile(t, dir, string(source)+"-private", "# Protected recall\n\n- **Apps**: Diary\n\n- capturelineage protected marker", base)
		for i := range 20 {
			writeKnowledgeFile(t, dir, fmt.Sprintf("%s-unrelated-%d", source, i), fmt.Sprintf("# Cooking %s %d\n\n- Roast vegetables for dinner.", source, i), base.Add(-24*time.Hour))
		}
	}
	if err := store.Sync("dedup-source-capture"); err != nil {
		t.Fatal(err)
	}
	indexed, err := store.Search(t.Context(), "capturelineage", 200)
	if err != nil || len(indexed) < 6 {
		t.Fatalf("missing indexed fixtures: hits=%d err=%v", len(indexed), err)
	}
	for _, mode := range []struct {
		name   string
		store  *MemoryStore
		query  string
		source MemorySource
	}{
		{name: "indexed-memory", store: store, query: "capturelineage", source: MemorySourceMemory},
		{name: "indexed-knowledge", store: store, query: "capturelineage", source: MemorySourceKnowledge},
		{name: "directory-memory", query: "capturelineage", source: MemorySourceMemory},
		{name: "keyword-knowledge", query: "capturelineage", source: MemorySourceKnowledge},
		{name: "directory-knowledge", source: MemorySourceKnowledge},
	} {
		t.Run(mode.name, func(t *testing.T) {
			captured := map[string]string{}
			callbacks := 0
			got := app.loadModelDedupSummaryWithCapture(mode.store, mode.query, 0, 1, mode.source, func(id, content string) {
				callbacks++
				captured[id] = content
			})
			if callbacks != 1 || len(captured) != 1 || countDedupEntries(got) != 1 {
				t.Fatalf("capture disagreed with emitted entry slots: callbacks=%d captures=%d output=%s", callbacks, len(captured), got)
			}
			for id, content := range captured {
				if !strings.HasPrefix(id, string(mode.source)+"-safe-") || content != original[id] || !strings.Contains(got, "[id="+id+"]") {
					t.Fatalf("captured source was not the exact emitted file: id=%s content=%s output=%s", id, content, got)
				}
				if !strings.Contains(content, activityPrivacySecret) || !strings.Contains(content, "capture-source") {
					t.Fatal("capture lost raw source text or dependency metadata")
				}
			}
			if strings.Contains(got, activityPrivacySecret) || strings.Contains(got, "protected marker") || !strings.Contains(got, "[REDACTED:") {
				t.Fatalf("capture changed prompt privacy: %s", got)
			}
		})
	}
}

func TestDedupCaptureRespectsRenderedByteBudget(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	content := "# Exact source\n\n- first detail " + activityPrivacySecret + " " + strings.Repeat("full source tail ", 100)
	writePrivacyMemory(t, "source", content)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	allowRead, err := app.memoryReadFilter()
	if err != nil {
		t.Fatal(err)
	}
	for _, maxChars := range []int{0, 20, 180} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes=%d/indexed=%v", maxChars, indexed), func(t *testing.T) {
				captures := 0
				capture := func(id, raw string) {
					captures++
					if id != "source" || raw != content {
						t.Fatal("partial rendering captured a truncated or unrelated source")
					}
				}
				var got string
				if indexed {
					results := []MemorySearchResult{
						{Source: string(MemorySourceMemory), Path: "source.md"},
						{Source: string(MemorySourceMemory), Path: "source.md", StartLine: 100},
					}
					got = dedupFromResultsWithReadCapture(results, MemorySourceMemory, maxChars, func(path string) string { return filepath.Join(dir, path) }, allowRead, capture)
				} else {
					got = app.loadModelDedupSummaryWithCapture(nil, "", maxChars, 0, MemorySourceMemory, capture)
				}
				if maxChars == 20 && (got != "" || captures != 0) {
					t.Fatalf("unrendered source was captured: captures=%d output=%s", captures, got)
				}
				if maxChars == 180 && (captures != 1 || !strings.Contains(got, "context budget reached")) {
					t.Fatalf("partially rendered source missing or duplicated: captures=%d output=%s", captures, got)
				}
				if maxChars == 0 && (captures != 1 || strings.Contains(got, "context budget reached")) {
					t.Fatalf("complete source missing or captured per chunk: captures=%d output=%s", captures, got)
				}
				if strings.Contains(got, activityPrivacySecret) {
					t.Fatal("capture bypassed rendering redaction")
				}
			})
		}
	}

	knowledgePath, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeKnowledgeFile(t, knowledgePath, "source", content, time.Now())
	writeKnowledgeFile(t, knowledgePath, "tail", "# Unrendered tail\n\n- Later evidence.\n", time.Now().Add(-time.Hour))
	for _, maxChars := range []int{20, 180} {
		t.Run(fmt.Sprintf("bytes=%d/knowledge-fallback", maxChars), func(t *testing.T) {
			captures := 0
			got := app.loadModelDedupSummaryWithCapture(nil, "exact", maxChars, 3, MemorySourceKnowledge, func(id, raw string) {
				captures++
				if id != "source" || raw != content {
					t.Fatal("knowledge fallback captured an unrendered or truncated source")
				}
			})
			if maxChars == 20 && (got != "" || captures != 0) {
				t.Fatalf("unrendered knowledge was captured: captures=%d output=%s", captures, got)
			}
			if maxChars == 180 && (captures != 1 || !strings.Contains(got, "context budget reached")) {
				t.Fatalf("knowledge fallback ignored the byte budget: captures=%d output=%s", captures, got)
			}
			if strings.Contains(got, "Unrendered tail") || strings.Contains(got, activityPrivacySecret) {
				t.Fatal("knowledge fallback emitted unrendered or unredacted text")
			}
		})
	}
}
