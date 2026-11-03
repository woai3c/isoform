package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestActivityDedupRecallAppliesSourcePrivacyBeforeBudget(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), configDirName), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := os.MkdirAll(store.knowledgeDir, 0700); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for _, source := range []struct {
		name, dir string
	}{
		{name: "memory", dir: store.baseDir},
		{name: "knowledge", dir: store.knowledgeDir},
	} {
		writeKnowledgeFile(t, source.dir, source.name+"-safe",
			"# Recall safe\n\n- **Apps**: Notes\n\n- dedupneedle "+strings.Repeat("x", 100)+" "+activityPrivacySecret+" "+strings.Repeat("tail ", 50)+"\n",
			base.Add(-time.Hour))
		writeKnowledgeFile(t, source.dir, source.name+"-protected",
			"# Excluded source\n\n- **Apps**: Diary\n\n- dedupneedle protected-source-marker\n",
			base)
		for i := range 20 {
			writeKnowledgeFile(t, source.dir, fmt.Sprintf("unrelated-%02d", i), "# Cooking\n\n- Roast vegetables for dinner.\n", base.Add(-2*time.Hour))
		}
	}
	if err := store.Sync("dedup-privacy"); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.Search(context.Background(), "dedupneedle", 200)
	if err != nil || len(baseline) < 4 {
		t.Fatalf("relevance fixtures missing: results=%v err=%v", paths(baseline), err)
	}
	for name, load := range map[string]func() string{
		"memory-relevant":          func() string { return app.loadRelevantMemorySummaryWithBudget(store, "dedupneedle", 180) },
		"knowledge-relevant":       func() string { return app.loadRelevantKnowledgeSummaryWithBudget(store, "dedupneedle", 180) },
		"memory-fallback":          func() string { return app.loadRelevantMemorySummaryWithBudget(store, "", 180) },
		"knowledge-fallback":       func() string { return app.loadRelevantKnowledgeSummaryWithBudget(store, "", 180) },
		"memory-full-directory":    func() string { return app.loadExistingMemorySummaryWithBudget(180) },
		"knowledge-full-directory": func() string { return app.loadExistingKnowledgeSummaryWithBudget(180) },
	} {
		t.Run(name, func(t *testing.T) {
			got := load()
			if strings.Contains(got, "Excluded source") || strings.Contains(got, "protected-source-marker") || strings.Contains(got, "sk-proj-") {
				t.Fatalf("dedup leaked excluded or partial secret content: %q", got)
			}
			if !strings.Contains(got, "Recall safe") || !strings.Contains(got, "[REDACTED:") || !strings.Contains(got, "context budget reached") {
				t.Fatalf("dedup did not redact allowed source before budgeting: %q", got)
			}
		})
	}
}
