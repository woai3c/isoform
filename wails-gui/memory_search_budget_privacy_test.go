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

type countedMemoryQueryEmbedder struct {
	activityEmbeddingTestProvider
	queries int
}

func (p *countedMemoryQueryEmbedder) EmbedQuery(context.Context, string) ([]float64, error) {
	p.queries++
	return []float64{1, 0}, nil
}

// Seed real FTS/vector tables and source files without invoking an embedder or
// the chunker. Multiple chunks of one source exercise the per-search read cache.
func seedMemoryReadBudgetCorpus(t *testing.T, store *MemoryStore, files, protected, chunksPerFile int) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	fts, err := tx.Prepare(`INSERT INTO memory_chunks_fts(text,id,path,source,model,start_line,end_line) VALUES(?,?,?,'memory','test',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fts.Close()
	chunks, err := tx.Prepare(`INSERT INTO memory_chunks(id,path,source,start_line,end_line,hash,model,text,embedding) VALUES(?,?,'memory',?,?,'test','test',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer chunks.Close()
	for i := range files {
		path := fmt.Sprintf("source-%04d.md", i)
		body := "# Safe source\n\nneedlework " + strings.Repeat("ordinary work detail ", 8)
		embedding := "[0.9,0.1]"
		if i < protected {
			body = "# Protected source\n\n- **Apps**: Diary\n\nneedlework"
			embedding = "[1,0]"
		}
		if err := os.WriteFile(filepath.Join(store.baseDir, path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		for chunk := range chunksPerFile {
			id := fmt.Sprintf("chunk-%04d-%d", i, chunk)
			startLine := chunk*10 + 1
			if _, err := fts.Exec(body, id, path, startLine, startLine+9); err != nil {
				t.Fatal(err)
			}
			if _, err := chunks.Exec(id, path, startLine, startLine+9, body, embedding); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Nonmatching index rows give the shared query useful FTS IDF while leaving
	// thousands of matching sources in the corpus for the broad-query case.
	for i := range files * chunksPerFile * 3 {
		if _, err := fts.Exec("roast vegetables dinner", fmt.Sprintf("noise-%d", i), "noise.md", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store.dirty = false
}

func TestSearchMemoryBoundsOrdinaryReadValidation(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		t.Run(fmt.Sprintf("hybrid=%v", hybrid), func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), "search"), MemoryStoreConfig{Provider: "none"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			seedMemoryReadBudgetCorpus(t, store, 2000, 0, 1)
			provider := &countedMemoryQueryEmbedder{}
			if hybrid {
				store.embedder = provider
			}
			allowRead, err := app.memoryReadFilter()
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			results, err := store.searchReadable(t.Context(), "needlework", 10, func(id, content string) bool {
				reads++
				return allowRead(id, content)
			})
			if err != nil || len(results) != 10 {
				t.Fatalf("broad search: results=%d err=%v", len(results), err)
			}
			maxReads := 100
			if hybrid {
				maxReads *= 2 // Independent keyword and vector windows can differ.
			}
			if reads < 10 || reads > maxReads {
				t.Fatalf("ordinary search read %d source files for 10 results; want at most %d", reads, maxReads)
			}
			if hybrid && provider.queries != 1 {
				t.Fatalf("query embedded %d times", provider.queries)
			}
		})
	}
}

func TestSearchMemoryWidensProtectedPrefixWithoutRereadingSources(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), "search"), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedMemoryReadBudgetCorpus(t, store, 100, 60, 3)
	provider := &countedMemoryQueryEmbedder{}
	store.embedder = provider
	app.memoryStore = store
	allowRead, err := app.memoryReadFilter()
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]int{}
	results, err := store.searchReadable(t.Context(), "needlework", 5, func(id, content string) bool {
		reads[id]++
		return allowRead(id, content)
	})
	if err != nil || len(results) != 5 {
		t.Fatalf("safe tail was not filled: results=%d err=%v", len(results), err)
	}
	if len(reads) <= 60 || len(reads) >= 100 {
		t.Fatalf("expected bounded widening past protected prefix, read %d sources", len(reads))
	}
	for id, count := range reads {
		if count != 1 {
			t.Fatalf("source %s validated %d times across chunks/pages", id, count)
		}
	}
	for _, result := range results {
		if strings.Contains(result.Snippet, "Protected source") {
			t.Fatal("protected source reached results")
		}
	}
	if provider.queries != 1 {
		t.Fatalf("widening embedded the query %d times", provider.queries)
	}
	if results, err := app.SearchMemory("needlework", 5); err != nil || len(results) != 5 {
		t.Fatalf("public SearchMemory did not refill: results=%d err=%v", len(results), err)
	}
}

func TestDedupReadersRequirePrivacyPolicy(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writePrivacyMemory(t, "private", "# Hidden source\n\n- **Apps**: Diary\n\n- private marker")
	results := []MemorySearchResult{{Path: "private.md", Source: string(MemorySourceMemory)}}
	resolve := func(path string) string { return filepath.Join(dir, path) }
	if got := dedupFromResultsWithReadFilter(results, MemorySourceMemory, 1000, resolve, nil); got != "" {
		t.Fatal("result reader accepted a missing privacy policy")
	}
	if got := loadDedupDirectorySummaryWithReadFilter(dir, 1000, nil); got != "" {
		t.Fatal("directory reader accepted a missing privacy policy")
	}
	app.closedEyes.enabled.Store(true)
	if got := app.loadRelevantMemorySummary(nil, "private", 1); got != "" {
		t.Fatal("fallback recall bypassed the App privacy policy")
	}
}

func TestKnowledgeRecallFallbackGatesBeforeRankingAndLimit(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	for id, content := range map[string]string{
		"private": "# kubernetes rollout\n\n- **Apps**: Diary\n\n- protected marker kubernetes rollout",
		"safe":    "# Cluster notes\n\n- kubernetes routine " + activityPrivacySecret,
	} {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := app.loadRelevantKnowledgeSummary(nil, "kubernetes rollout", 1)
	if !strings.Contains(got, "[id=safe]") || strings.Contains(got, "protected marker") || strings.Contains(got, activityPrivacySecret) {
		t.Fatalf("fallback did not gate before rank/limit/redaction: %s", got)
	}
}
