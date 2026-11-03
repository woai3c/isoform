package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestFormatDedupEntry(t *testing.T) {
	t.Run("title and first three points", func(t *testing.T) {
		content := "# My Title\n\n- **Apps**: ignored metadata\n- point one\n- point two\n- point three\n- point four\n"
		got := formatDedupEntry("entry-1", content)
		if !strings.Contains(got, "- [id=entry-1] My Title") {
			t.Fatalf("missing id/title line:\n%s", got)
		}
		for _, want := range []string{"point one", "point two", "point three"} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "point four") {
			t.Fatalf("should only show first 3 points, got point four:\n%s", got)
		}
		if !strings.Contains(got, "... +1 more points") {
			t.Fatalf("missing overflow note:\n%s", got)
		}
		if strings.Contains(got, "ignored metadata") {
			t.Fatalf("`- **` metadata bullet should be excluded:\n%s", got)
		}
	})

	t.Run("falls back to id when no title", func(t *testing.T) {
		got := formatDedupEntry("fallback-id", "no heading here\n- a point\n")
		if !strings.Contains(got, "- [id=fallback-id] fallback-id") {
			t.Fatalf("expected id as title fallback:\n%s", got)
		}
	})
}

// writeKnowledgeFile writes a knowledge entry and stamps its mtime so recency
// ordering is deterministic in tests.
func writeKnowledgeFile(t *testing.T, dir, id, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", id, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", id, err)
	}
}

func countDedupEntries(s string) int {
	return strings.Count(s, "- [id=")
}

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestLoadRelevantKnowledgeSummary_KeywordRanking(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	base := time.Now()
	writeKnowledgeFile(t, dir, "kube-deploy", "# Kube Deploy\n\n- kubernetes deployment rollout strategy\n", base)
	writeKnowledgeFile(t, dir, "kube-pods", "# Kube Pods\n\n- kubernetes pod scheduling notes\n", base)
	writeKnowledgeFile(t, dir, "billing", "# Billing\n\n- invoice and payment reconciliation\n", base)

	query := "Debugging kubernetes cluster scaling"
	got := app.loadRelevantKnowledgeSummaryFallback(query, 2)

	if !strings.Contains(got, "[id=kube-deploy]") || !strings.Contains(got, "[id=kube-pods]") {
		t.Fatalf("expected both kubernetes entries in top-2:\n%s", got)
	}
	if strings.Contains(got, "[id=billing]") {
		t.Fatalf("irrelevant billing entry should be excluded by top-k:\n%s", got)
	}
	if countDedupEntries(got) != 2 {
		t.Fatalf("expected exactly 2 entries, got %d:\n%s", countDedupEntries(got), got)
	}
}

func TestLoadRelevantKnowledgeSummary_RecencyFallbackAndCap(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	base := time.Now()
	// None of these match the query — exercises the recency-ordered fallback.
	writeKnowledgeFile(t, dir, "old", "# Old\n\n- alpha note\n", base.Add(-3*time.Hour))
	writeKnowledgeFile(t, dir, "mid", "# Mid\n\n- beta note\n", base.Add(-2*time.Hour))
	writeKnowledgeFile(t, dir, "new", "# New\n\n- gamma note\n", base.Add(-1*time.Hour))

	got := app.loadRelevantKnowledgeSummaryFallback("zzz nomatch query", 2)
	if countDedupEntries(got) != 2 {
		t.Fatalf("expected cap of 2 entries, got %d:\n%s", countDedupEntries(got), got)
	}
	if !strings.Contains(got, "[id=new]") || !strings.Contains(got, "[id=mid]") {
		t.Fatalf("expected the two most-recent entries (new, mid):\n%s", got)
	}
	if strings.Contains(got, "[id=old]") {
		t.Fatalf("oldest entry should be dropped by recency cap:\n%s", got)
	}
	// "new" is most recent, so it must precede "mid".
	if strings.Index(got, "[id=new]") > strings.Index(got, "[id=mid]") {
		t.Fatalf("most-recent entry should sort first:\n%s", got)
	}
}

func TestLoadRelevantKnowledgeSummary_ExactTokenMatching(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	base := time.Now()
	writeKnowledgeFile(t, dir, "exact-app", "# Exact App\n\n- app launcher workflow\n", base.Add(-2*time.Hour))
	writeKnowledgeFile(t, dir, "partial-newer", "# Partial Newer\n\n- happy wrapper apply flow\n", base.Add(-1*time.Hour))

	got := app.loadRelevantKnowledgeSummaryFallback("app", 1)
	if !strings.Contains(got, "[id=exact-app]") {
		t.Fatalf("expected exact app token match to win:\n%s", got)
	}
	if strings.Contains(got, "[id=partial-newer]") {
		t.Fatalf("partial words like happy/apply/wrapper must not match app:\n%s", got)
	}
}

func TestLoadRelevantKnowledgeSummary_EmptyQueryUsesBoundedFallback(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}

	base := time.Now()
	writeKnowledgeFile(t, dir, "one", "# One\n\n- first note\n", base)
	writeKnowledgeFile(t, dir, "two", "# Two\n\n- second note\n", base)

	got := app.loadRelevantKnowledgeSummary(nil, "   ", 1)
	if countDedupEntries(got) != 1 {
		t.Fatalf("empty query must respect the entry budget: %s", got)
	}
}

func TestMemoryDedupFromResults_FiltersOutKnowledgeHits(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	allowRead, err := app.memoryReadFilter()
	if err != nil {
		t.Fatal(err)
	}
	// The memory store indexes both the memory and knowledge dirs, so Search
	// returns a mix of sources. The memory dedup list must recall ONLY
	// memory-source entries — a knowledge hit must neither be read from the
	// memory dir nor crowd out the real memory entry. Driven off crafted
	// results so it pins the filter behaviour independent of FTS scoring.
	memDir := t.TempDir()
	knowDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(memDir, "kube-mem.md"),
		[]byte("# Kube Mem\n\n- user runs kubernetes clusters at work\n"), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowDir, "kube-know.md"),
		[]byte("# Kube Know\n\n- kubernetes rollout best practices\n"), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	// Knowledge hit ranked ABOVE the memory hit — the worst case for crowding.
	results := []MemorySearchResult{
		{Source: string(MemorySourceKnowledge), Path: "kube-know.md", Score: 0.9},
		{Source: string(MemorySourceMemory), Path: "kube-mem.md", Score: 0.8},
	}
	resolve := func(path string) string {
		// Mirror MemoryStore.resolveAbsPath for the memory source only.
		return filepath.Join(memDir, path)
	}

	got := dedupFromResultsWithReadFilter(results, MemorySourceMemory, 100_000, resolve, allowRead)
	if !strings.Contains(got, "[id=kube-mem]") {
		t.Fatalf("expected memory entry recalled:\n%s", got)
	}
	if strings.Contains(got, "[id=kube-know]") {
		t.Fatalf("knowledge entry must not leak into the memory dedup list:\n%s", got)
	}
	if countDedupEntries(got) != 1 {
		t.Fatalf("expected exactly 1 memory entry, got %d:\n%s", countDedupEntries(got), got)
	}
}

func TestDedupFromResults_FiltersOutMemoryHitsForKnowledge(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	allowRead, err := app.memoryReadFilter()
	if err != nil {
		t.Fatal(err)
	}
	memDir := t.TempDir()
	knowDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(memDir, "kube-mem.md"),
		[]byte("# Kube Mem\n\n- user runs kubernetes clusters at work\n"), 0600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(knowDir, "kube-know.md"),
		[]byte("# Kube Know\n\n- kubernetes rollout best practices\n"), 0600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}

	results := []MemorySearchResult{
		{Source: string(MemorySourceMemory), Path: "kube-mem.md", Score: 0.9},
		{Source: string(MemorySourceKnowledge), Path: "kube-know.md", Score: 0.8},
	}
	resolve := func(path string) string {
		return filepath.Join(knowDir, path)
	}

	got := dedupFromResultsWithReadFilter(results, MemorySourceKnowledge, 100_000, resolve, allowRead)
	if !strings.Contains(got, "[id=kube-know]") {
		t.Fatalf("expected knowledge entry recalled:\n%s", got)
	}
	if strings.Contains(got, "[id=kube-mem]") {
		t.Fatalf("memory entry must not leak into the knowledge dedup list:\n%s", got)
	}
	if countDedupEntries(got) != 1 {
		t.Fatalf("expected exactly 1 knowledge entry, got %d:\n%s", countDedupEntries(got), got)
	}
}

func TestLoadRelevantMemorySummary_FallbackNilStore(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	for i := range 3 {
		id := fmt.Sprintf("mem-%d", i)
		content := fmt.Sprintf("# Memory %d\n\n- detail line\n", i)
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}

	// nil store must fall back to the full scan rather than returning empty.
	got := app.loadRelevantMemorySummary(nil, "any query", 20)
	if countDedupEntries(got) != 3 {
		t.Fatalf("expected fallback to list all 3 memories, got %d:\n%s", countDedupEntries(got), got)
	}
	if got != app.loadExistingMemorySummaryWithBudget(100_000) {
		t.Fatalf("nil-store result should equal the full-scan fallback")
	}
}

func TestDedupFromResultsWithBudgetIsNotCappedByEntryCount(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	allowRead, err := app.memoryReadFilter()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const entryCount = dedupRecallMaxEntries + 12
	results := make([]MemorySearchResult, 0, entryCount)
	for idx := range entryCount {
		name := fmt.Sprintf("memory-%02d", idx)
		if err := os.WriteFile(
			filepath.Join(dir, name+".md"),
			[]byte(fmt.Sprintf("# Memory %02d\n\n- evidence %02d\n", idx, idx)),
			0o600,
		); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		results = append(results, MemorySearchResult{
			Source: string(MemorySourceMemory),
			Path:   name + ".md",
			Score:  1 - float64(idx)/100,
		})
	}

	got := dedupFromResultsWithReadFilter(
		results,
		MemorySourceMemory,
		100_000,
		func(path string) string { return filepath.Join(dir, path) },
		allowRead,
	)
	if count := countDedupEntries(got); count != entryCount {
		t.Fatalf("character budget should admit all %d entries, got %d", entryCount, count)
	}
	if entryCount <= dedupRecallMaxEntries {
		t.Fatalf("fixture must exceed legacy entry cap")
	}
}

func TestRankAutomationDedupJobs_RelevanceAndFallbackOrder(t *testing.T) {
	jobs := []storage.CronJobRow{
		{Name: "daily standup", Description: "summarize calendar events"},
		{Name: "gmail digest", Description: "summarize today's gmail messages"},
		{Name: "slack review", Description: "read isoform-platform slack channel"},
	}

	// Relevant automation floats to the top; the rest are kept (not dropped)
	// because the list is under the rank cap — ranking, not filtering.
	got := rankAutomationDedupJobs(jobs, "latest gmail inbox messages")
	if got[0].Name != "gmail digest" {
		t.Fatalf("expected gmail job first, got %q", got[0].Name)
	}
	if len(got) != len(jobs) {
		t.Fatalf("under the cap every automation should survive (rank, not drop), got %d of %d: %#v", len(got), len(jobs), got)
	}

	noQuery := rankAutomationDedupJobs(jobs, "")
	if noQuery[0].Name != jobs[0].Name || noQuery[1].Name != jobs[1].Name {
		t.Fatalf("empty query should preserve existing order, got %#v", noQuery)
	}

	noMatch := rankAutomationDedupJobs(jobs, "kubernetes deployment rollout")
	if noMatch[0].Name != jobs[0].Name || noMatch[1].Name != jobs[1].Name {
		t.Fatalf("no-match query should preserve existing order, got %#v", noMatch)
	}
}

// TestRankAutomationDedupJobs_IDFBeatsBoilerplate guards the core fix: a verbose
// automation that overlaps the query only on boilerplate words must NOT outrank
// a terse automation that overlaps on a rare topical word.
func TestRankAutomationDedupJobs_IDFBeatsBoilerplate(t *testing.T) {
	jobs := []storage.CronJobRow{
		// Verbose, but only shares generic words with the query below.
		{Name: "daily-openclaw-version-updates", Description: "every morning search for the latest daily news and updates then summarize and push to the user"},
		// Shares the rare topical token "cmux" once; little else.
		{Name: "remove-cmux-panel", Description: "remove a cmux workspace panel"},
		// Pure boilerplate filler so "news/updates/summarize" are corpus-common.
		{Name: "daily-football-news", Description: "every morning search for the latest daily news and updates then summarize and push to the user"},
	}

	got := rankAutomationDedupJobs(jobs, "cleaning up a cmux workspace panel layout")
	if got[0].Name != "remove-cmux-panel" {
		t.Fatalf("rare topical match (cmux) should rank first over boilerplate-only overlap, got %q (%#v)", got[0].Name, got)
	}
}

// TestRankAutomationDedupJobs_CJKNotDiscriminated guards that a CJK-named/described
// automation ranks on a CJK query instead of being sunk for lacking ASCII tokens.
func TestRankAutomationDedupJobs_CJKNotDiscriminated(t *testing.T) {
	jobs := []storage.CronJobRow{
		{Name: "daily-tennis-news", Description: "search latest ATP WTA tennis news"},
		{Name: "今日谚语", Description: "每天推送一条今日谚语给用户"},
		{Name: "daily-football-news", Description: "search latest football news"},
	}

	got := rankAutomationDedupJobs(jobs, "帮我整理今日谚语合集")
	if got[0].Name != "今日谚语" {
		t.Fatalf("CJK query should rank the matching CJK automation first, got %q (%#v)", got[0].Name, got)
	}
}
