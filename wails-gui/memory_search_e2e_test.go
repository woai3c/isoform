package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchContextCandidatesHasNoBlindResultCountCap(t *testing.T) {
	cfgDir := t.TempDir()
	store, err := NewMemoryStore(cfgDir, MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	memoryDir := filepath.Join(cfgDir, "memory")
	const matchingFiles = 225
	for index := 0; index < matchingFiles; index++ {
		content := fmt.Sprintf(
			"# Context needle %03d\n\ncontextneedle detail %03d\n",
			index,
			index,
		)
		if err := os.WriteFile(
			filepath.Join(memoryDir, fmt.Sprintf("context-%03d.md", index)),
			[]byte(content),
			0o600,
		); err != nil {
			t.Fatalf("write context fixture %d: %v", index, err)
		}
	}
	if err := store.Sync("complete-context-test"); err != nil {
		t.Fatal(err)
	}

	complete, err := store.SearchContextCandidates(
		context.Background(),
		"contextneedle",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(complete) != matchingFiles {
		t.Fatalf(
			"complete context candidates=%d, want %d",
			len(complete),
			matchingFiles,
		)
	}
	bounded, err := store.Search(context.Background(), "contextneedle", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) > 20 {
		t.Fatalf("bounded search returned %d results", len(bounded))
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.SearchContextCandidates(
		canceledContext,
		"contextneedle",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled complete search error=%v, want context.Canceled", err)
	}
}

// End-to-end tests for MemoryStore.Search() running the full FTS-only code
// path (extractKeywords → buildFTSQueryWithOp AND/OR fallback → SQLite FTS5 →
// bm25RankToScore → mergeHybridResults → minScore filter).
//
// Incident regression: "where am i based" used to return zero hits because
//   1. stop words were not stripped before tokenising
//   2. the AND query demanded every word in a single chunk
//   3. text-only hits were capped at 0.3 by the hybrid-merge weighting
//      and then dropped by the 0.35 minScore filter
// These tests fail against the pre-fix implementation and pass against the
// current one.

const fixtureHalcyonMemory = `# Alex's location context — Halcyon home + Vesper Isle residency history

- **Updated**: 2027-09-18 16:40
- **Source**: conversation
- **Dimension**: deep_user_model

## Memory Points

- Alex is based in Halcyon, a fictional harbor city (starting point for weekend ferry trips)
- Alex splits time between Halcyon and Vesper Isle — confirmed as home and artist-residency locations
- Alex travels between Vesper Isle and Halcyon by overnight ferry
- Alex completed a six-week printmaking residency (驻留) in Vesper Isle from August 2 to September 12, 2027
`

const fixtureUnrelatedMemory = `# Cooking preferences

- Likes spicy food
- Prefers rice over noodles
- Morning coffee is non-negotiable
`

const fixtureRecipeMemory = `# Weeknight recipe notes

- 15-minute pasta with garlic and olive oil
- Roasted vegetables at 200C for 25 minutes
- Always salt pasta water generously
`

// Title is the only place the camel-cased compound "YouTube" appears — there
// is no standalone "you" or "youtube" token anywhere in the body. Used to
// pin the regression for short stop-word-overlapping queries like "You",
// which the unicode61 tokenizer fuses with the rest of "YouTube".
const fixtureYouTubeMemory = `# Zoe's YouTube study playlist

- **Updated**: 2026-04-17 09:15
- **Source**: conversation
- **Dimension**: topic_highlight

## Memory Points

- Zoe queues astronomy lectures and bicycle-repair tutorials for Sunday afternoons.
- Prefers concise explainers over livestreams when on a tablet.
- Currently following a series about balcony gardening.
`

// setupFixtureStore writes a minimal memory directory and opens a
// MemoryStore against it in FTS-only mode (no embedder). The first call to
// Search() will trigger an on-search Sync that discovers and indexes the
// fixture files.
func setupFixtureStore(t *testing.T) *MemoryStore {
	t.Helper()

	cfgDir := t.TempDir()
	memDir := filepath.Join(cfgDir, "memory")
	if err := os.MkdirAll(memDir, 0700); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}

	fixtures := map[string]string{
		"conversation-halcyon.md": fixtureHalcyonMemory,
		"cooking.md":              fixtureUnrelatedMemory,
		"recipes.md":              fixtureRecipeMemory,
		"zoe-youtube.md":          fixtureYouTubeMemory,
	}
	for name, body := range fixtures {
		if err := os.WriteFile(filepath.Join(memDir, name), []byte(body), 0600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	// Provider:"none" short-circuits resolveEmbeddingProvider even when
	// OPENAI_API_KEY is present in the test environment — so the store runs
	// the FTS-only path, which is what this incident was about.
	store, err := NewMemoryStore(cfgDir, MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

// containsPath reports whether any result's path matches want.
func containsPath(results []MemorySearchResult, want string) bool {
	for _, r := range results {
		if r.Path == want {
			return true
		}
	}
	return false
}

// paths returns the Path field of each result, for diagnostic output.
func paths(results []MemorySearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Path
	}
	return out
}

// ── Incident regression tests ──────────────────────────────────────────

// The original failing query shape is retained with a fictional location fixture.
// Pre-fix: returns zero results (AND query of 4 stop-word-laden tokens matches
// nothing). Post-fix: keywords reduce to "based", FTS finds the Halcyon file.
func TestSearch_WhereAmIBased_FindsHalcyonMemory(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "where am i based", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	for i, r := range results {
		t.Logf("result[%d] path=%s score=%.4f snippet=%q",
			i, r.Path, r.Score, snippetForLog(r.Snippet))
	}
	if len(results) == 0 {
		t.Fatalf("Search returned no results; expected Halcyon memory")
	}
	if !containsPath(results, "conversation-halcyon.md") {
		t.Errorf("Halcyon memory missing from results; got paths=%v", paths(results))
	}
	// Sanity: snippet of the top match should mention Halcyon so a human
	// reading the audit log can confirm the right chunk surfaced.
	if !strings.Contains(results[0].Snippet, "Halcyon") {
		t.Errorf("top result snippet should mention Halcyon; got %q", results[0].Snippet)
	}
}

// snippetForLog shortens a snippet so t.Log output stays readable.
func snippetForLog(s string) string {
	const max = 120
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Direct keyword — proves the FTS index is built correctly and the
// bm25 → score conversion doesn't zero-out results.
func TestSearch_BasedKeyword_FindsHalcyonMemory(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "based", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if !containsPath(results, "conversation-halcyon.md") {
		t.Errorf("Halcyon memory missing for 'based' query; got paths=%v", paths(results))
	}
}

// Content keyword — a word that only appears in the Halcyon memory.
// If this fails the FTS table or tokenizer is broken for non-ASCII or
// mixed-case input.
func TestSearch_HalcyonKeyword_FindsHalcyonMemory(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "Halcyon", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if !containsPath(results, "conversation-halcyon.md") {
		t.Errorf("Halcyon memory missing for 'Halcyon' query; got paths=%v", paths(results))
	}
}

// Negative control — queries with no match should return nothing (not noise).
// Guards against over-eager OR fallback dragging in unrelated memories.
func TestSearch_UnrelatedQuery_ReturnsNoHalcyonNoise(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "quantum physics", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if containsPath(results, "conversation-halcyon.md") {
		t.Errorf("Halcyon memory leaked into unrelated query; got paths=%v", paths(results))
	}
}

// Short query regression: typing "You" should reach the
// "Zoe's YouTube study playlist" memory. Before the fix the FTS
// tokenizer treated `YouTube` as the single token `youtube`, so a literal
// `"you"` query never matched it; combined with stop-word stripping
// reducing "you" → "" and the raw-fallback then issuing a literal AND
// query, the entry was unreachable through search. After the fix, single
// short tokens (≤ memoryShortQueryMaxLen runes) build an FTS5 prefix query
// (`"you" *`) which matches any token starting with "you" — including
// "youtube".
func TestSearch_ShortQuery_FindsYouTubeMemory(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "You", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	for i, r := range results {
		t.Logf("result[%d] path=%s score=%.4f snippet=%q",
			i, r.Path, r.Score, snippetForLog(r.Snippet))
	}
	if !containsPath(results, "zoe-youtube.md") {
		t.Fatalf("YouTube memory missing for short query %q; got paths=%v",
			"You", paths(results))
	}
}

// Sanity coverage of the same fix from a different angle: "Zo" (a stop-
// word-free 2-character query) should reach the YouTube memory because the
// title contains "Zoe". This case never hit stop-word stripping, so a
// failure here points at the prefix-query path itself, not the stop-word
// fallback.
func TestSearch_ShortPrefixQuery_FindsTitleMatch(t *testing.T) {
	store := setupFixtureStore(t)

	results, err := store.Search(context.Background(), "Zo", 10)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if !containsPath(results, "zoe-youtube.md") {
		t.Errorf("YouTube memory missing for prefix query %q; got paths=%v",
			"Zo", paths(results))
	}
}
