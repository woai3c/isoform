package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── buildFTSQueryWithOp ─────────────────────────────────────────────────

func TestBuildFTSQueryWithOp(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		operator string
		want     string
	}{
		{"empty", "", "AND", ""},
		{"whitespace only", "   \t\n", "AND", ""},
		{"single word AND", "halcyon", "AND", `"halcyon"`},
		{"single word OR", "halcyon", "OR", `"halcyon"`},
		{"multi word AND", "based halcyon", "AND", `"based" AND "halcyon"`},
		{"multi word OR", "based halcyon", "OR", `"based" OR "halcyon"`},
		{"strips punctuation", "where, am i based?", "AND", `"where" AND "am" AND "i" AND "based"`},
		{"preserves digits and underscores", "foo_bar 42", "AND", `"foo_bar" AND "42"`},
		// Quotes act as word separators (not letter/digit/underscore), so
		// `a"b c` tokenises into three words; the ReplaceAll escape is
		// defense-in-depth for a hypothetical future tokenizer.
		{"quotes split words", `a"b c`, "AND", `"a" AND "b" AND "c"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTSQueryWithOp(tt.raw, tt.operator)
			if got != tt.want {
				t.Errorf("buildFTSQueryWithOp(%q, %q) = %q; want %q",
					tt.raw, tt.operator, got, tt.want)
			}
		})
	}
}

func TestBuildFTSQueryDefaultsToAND(t *testing.T) {
	got := buildFTSQuery("based halcyon")
	want := `"based" AND "halcyon"`
	if got != want {
		t.Errorf("buildFTSQuery = %q; want %q", got, want)
	}
}

// ── buildFTSPrefixQuery ─────────────────────────────────────────────────

func TestBuildFTSPrefixQuery(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   string
	}{
		{"nil tokens", nil, ""},
		{"empty tokens", []string{}, ""},
		// Note the literal space before `*`: FTS5 parses `"you"*` as a
		// column-filter operator, not a prefix. The space form is the
		// documented prefix-token syntax.
		{"single token", []string{"you"}, `"you" *`},
		{"two tokens AND-joined", []string{"you", "tube"}, `"you" * AND "tube" *`},
		{"escapes quotes", []string{`a"b`}, `"a""b" *`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTSPrefixQuery(tt.tokens)
			if got != tt.want {
				t.Errorf("buildFTSPrefixQuery(%v) = %q; want %q",
					tt.tokens, got, tt.want)
			}
		})
	}
}

// ── isShortSingleToken ──────────────────────────────────────────────────

func TestIsShortSingleToken(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   bool
	}{
		{"nil → false", nil, false},
		{"empty → false", []string{}, false},
		{"single short ASCII", []string{"you"}, true},
		{"single at threshold", []string{"abcd"}, true},
		{"single over threshold", []string{"abcde"}, false},
		// Without the rune-aware check this would be 3 bytes vs the
		// 4-char threshold and pass; we want only one CJK character to
		// count as one rune, not three.
		{"single CJK rune", []string{"海"}, true},
		// Two CJK runes is still ≤ threshold.
		{"two CJK runes", []string{"海港"}, true},
		{"multiple tokens", []string{"you", "tube"}, false},
		{"empty token", []string{""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isShortSingleToken(tt.tokens)
			if got != tt.want {
				t.Errorf("isShortSingleToken(%v) = %v; want %v",
					tt.tokens, got, tt.want)
			}
		})
	}
}

// ── extractKeywords ─────────────────────────────────────────────────────

func TestExtractKeywordsStripsStopWords(t *testing.T) {
	// "where am i based" is the canonical failing query from the incident:
	// without stop-word stripping, the FTS AND query demands "where" + "am" +
	// "i" + "based" in one chunk, which no memory contains. After stripping,
	// only "based" remains and hits the Halcyon memory.
	got := extractKeywords("where am i based")
	want := "based"
	if got != want {
		t.Errorf("extractKeywords(%q) = %q; want %q", "where am i based", got, want)
	}
}

func TestExtractKeywordsEmptyForPureStopWords(t *testing.T) {
	// All-stopword queries collapse to empty — caller must fall back to raw.
	got := extractKeywords("what is the")
	if strings.TrimSpace(got) != "" {
		t.Errorf("extractKeywords(%q) = %q; want empty", "what is the", got)
	}
}

func TestExtractKeywordsPreservesContent(t *testing.T) {
	got := extractKeywords("Where am I based in Halcyon?")
	// Order preserved, lowercased, stop-words/punctuation removed.
	want := "based halcyon"
	if got != want {
		t.Errorf("extractKeywords(...) = %q; want %q", got, want)
	}
}

// ── bm25RankToScore ─────────────────────────────────────────────────────

func TestBM25RankToScoreMonotonic(t *testing.T) {
	// SQLite FTS5's bm25() returns negative floats; more-negative = more
	// relevant. The mapping must preserve that ordering.
	ranks := []float64{-3.81, -3.50, -3.43, -2.90, -2.74, -2.56, -2.41}
	var prev float64 = math.Inf(1)
	for _, r := range ranks {
		got := bm25RankToScore(r)
		if got <= 0 || got >= 1 {
			t.Errorf("bm25RankToScore(%v) = %v; want in (0, 1)", r, got)
		}
		if got > prev {
			t.Errorf("monotonicity broken: rank=%v score=%v > prev=%v", r, got, prev)
		}
		prev = got
	}
}

func TestBM25RankToScoreBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		rank    float64
		wantMin float64
		wantMax float64
	}{
		{"zero rank → 0", 0, 0, 0},
		{"positive rank → 0", 1.5, 0, 0},
		{"+Inf → 0", math.Inf(1), 0, 0},
		{"-Inf → 0", math.Inf(-1), 0, 0},
		{"NaN → 0", math.NaN(), 0, 0},
		{"strong negative rank → near 1", -1e9, 0.999, 1},
		{"weak negative rank → below 0.5", -0.5, 0.3, 0.34},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bm25RankToScore(c.rank)
			if got < c.wantMin || got > c.wantMax {
				t.Errorf("bm25RankToScore(%v) = %v; want in [%v, %v]",
					c.rank, got, c.wantMin, c.wantMax)
			}
		})
	}
}

// Regression guard for the reviewer-flagged bug: previously every negative
// rank collapsed to score=1.0, erasing BM25 ordering.
func TestBM25RankToScorePreservesOrdering(t *testing.T) {
	weak := bm25RankToScore(-0.5)
	strong := bm25RankToScore(-5.0)
	if strong <= weak {
		t.Errorf("strong match (rank=-5) score=%v should exceed weak match (rank=-0.5) score=%v",
			strong, weak)
	}
	// Extra safety: the old buggy implementation returned 1.0 for anything
	// negative. If this fails, the regression has returned.
	if weak >= 0.999 {
		t.Errorf("weak match score should not saturate to 1.0; got %v (old bug?)", weak)
	}
}

// ── mergeHybridResults ──────────────────────────────────────────────────

func TestMergeHybridResultsBothSignals(t *testing.T) {
	vec := []MemorySearchResult{
		{Path: "a.md", StartLine: 1, EndLine: 10, Score: 0.8},
	}
	txt := []MemorySearchResult{
		{Path: "a.md", StartLine: 1, EndLine: 10, Score: 0.6},
	}
	out := mergeHybridResults(vec, txt, 0.7, 0.3)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged result, got %d", len(out))
	}
	want := 0.7*0.8 + 0.3*0.6
	if !approxEq(out[0].Score, want, 1e-9) {
		t.Errorf("dual-signal score = %v; want %v", out[0].Score, want)
	}
}

func TestMergeHybridResultsVectorOnlyKeepsVectorScore(t *testing.T) {
	vec := []MemorySearchResult{
		{Path: "v.md", StartLine: 1, EndLine: 5, Score: 0.82},
	}
	out := mergeHybridResults(vec, nil, 0.7, 0.3)
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	if !approxEq(out[0].Score, 0.82, 1e-9) {
		t.Errorf("vector-only score = %v; want 0.82 (not weighted)", out[0].Score)
	}
}

// Regression guard for reviewer point #2: before the fix, text-only hits were
// scored as `0.3 * textScore`, capping at 0.3 — always below the 0.35
// minScore filter, so FTS-only matches were silently dropped.
func TestMergeHybridResultsTextOnlyKeepsTextScore(t *testing.T) {
	txt := []MemorySearchResult{
		{Path: "t.md", StartLine: 1, EndLine: 5, Score: 0.72},
	}
	out := mergeHybridResults(nil, txt, 0.7, 0.3)
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	if !approxEq(out[0].Score, 0.72, 1e-9) {
		t.Errorf("text-only score = %v; want 0.72 (not weighted by textWeight=0.3)", out[0].Score)
	}
	if out[0].Score < memoryDefaultMinScore {
		t.Errorf("text-only score %v should exceed minScore %v after fix",
			out[0].Score, memoryDefaultMinScore)
	}
}

func TestMergeHybridResultsMixedSources(t *testing.T) {
	// "dual" appears in both; "vecOnly" only in vector; "txtOnly" only in text.
	vec := []MemorySearchResult{
		{Path: "dual.md", StartLine: 1, EndLine: 1, Score: 0.9},
		{Path: "vecOnly.md", StartLine: 1, EndLine: 1, Score: 0.5},
	}
	txt := []MemorySearchResult{
		{Path: "dual.md", StartLine: 1, EndLine: 1, Score: 0.8},
		{Path: "txtOnly.md", StartLine: 1, EndLine: 1, Score: 0.7},
	}
	out := mergeHybridResults(vec, txt, 0.7, 0.3)
	if len(out) != 3 {
		t.Fatalf("expected 3 merged results (dual+vecOnly+txtOnly), got %d", len(out))
	}
	byPath := map[string]float64{}
	for _, r := range out {
		byPath[r.Path] = r.Score
	}
	wantDual := 0.7*0.9 + 0.3*0.8
	if !approxEq(byPath["dual.md"], wantDual, 1e-9) {
		t.Errorf("dual.md score = %v; want %v", byPath["dual.md"], wantDual)
	}
	if !approxEq(byPath["vecOnly.md"], 0.5, 1e-9) {
		t.Errorf("vecOnly.md score = %v; want 0.5", byPath["vecOnly.md"])
	}
	if !approxEq(byPath["txtOnly.md"], 0.7, 1e-9) {
		t.Errorf("txtOnly.md score = %v; want 0.7", byPath["txtOnly.md"])
	}
}

// ── MMR diversity ranking ──────────────────────────────────────────────

func referenceApplyMMR(results []MemorySearchResult, lambda float64) []MemorySearchResult {
	ordered := append([]MemorySearchResult(nil), results...)
	sortByScore(ordered)
	if len(ordered) <= 1 || lambda == 1 {
		return ordered
	}
	if lambda < 0 {
		lambda = 0
	}
	if lambda > 1 {
		lambda = 1
	}

	tokenSets := make([]map[string]bool, len(ordered))
	minScore, maxScore := ordered[0].Score, ordered[0].Score
	for i, result := range ordered {
		tokenSets[i] = tokenize(result.Snippet)
		minScore = math.Min(minScore, result.Score)
		maxScore = math.Max(maxScore, result.Score)
	}
	scoreRange := maxScore - minScore
	if scoreRange <= 0 {
		scoreRange = 1
	}

	selected := make([]int, 0, len(ordered))
	isSelected := make([]bool, len(ordered))
	for len(selected) < len(ordered) {
		bestIdx := -1
		bestScore := math.Inf(-1)
		for idx, result := range ordered {
			if isSelected[idx] {
				continue
			}
			maxSimilarity := 0.0
			for _, selectedIdx := range selected {
				maxSimilarity = math.Max(
					maxSimilarity,
					memoryJaccardSimilarity(tokenSets[idx], tokenSets[selectedIdx]),
				)
			}
			relevance := (result.Score - minScore) / scoreRange
			score := lambda*relevance - (1-lambda)*maxSimilarity
			if bestIdx < 0 || score > bestScore {
				bestIdx = idx
				bestScore = score
			}
		}
		selected = append(selected, bestIdx)
		isSelected[bestIdx] = true
	}

	reranked := make([]MemorySearchResult, len(selected))
	for idx, selectedIdx := range selected {
		reranked[idx] = ordered[selectedIdx]
	}
	return reranked
}

func resultPaths(results []MemorySearchResult) []string {
	paths := make([]string, len(results))
	for idx, result := range results {
		paths[idx] = result.Path
	}
	return paths
}

func TestApplyMMRContextMatchesReference(t *testing.T) {
	input := []MemorySearchResult{
		{Path: "alpha-primary.md", Score: 0.97, Snippet: "alpha launch review decision"},
		{Path: "alpha-near-copy.md", Score: 0.91, Snippet: "alpha launch review decision notes"},
		{Path: "finance.md", Score: 0.83, Snippet: "invoice cash collection forecast"},
		{Path: "calendar.md", Score: 0.71, Snippet: "calendar meeting scheduling follow up"},
		{Path: "alpha-secondary.md", Score: 0.64, Snippet: "alpha review implementation details"},
		{Path: "travel.md", Score: 0.55, Snippet: "flight hotel trip planning"},
	}

	got, err := applyMMRContext(context.Background(), input, 0.7)
	if err != nil {
		t.Fatalf("applyMMRContext: %v", err)
	}
	want := referenceApplyMMR(input, 0.7)
	if fmt.Sprint(resultPaths(got)) != fmt.Sprint(resultPaths(want)) {
		t.Fatalf("optimized MMR differs from reference\nwant: %v\ngot:  %v", resultPaths(want), resultPaths(got))
	}
}

func TestApplyMMRContextDeterministicForTies(t *testing.T) {
	permutations := [][]MemorySearchResult{
		{
			{Path: "c.md", Score: 0.8, Snippet: "same tokens"},
			{Path: "a.md", Score: 0.8, Snippet: "same tokens"},
			{Path: "b.md", Score: 0.8, Snippet: "same tokens"},
		},
		{
			{Path: "b.md", Score: 0.8, Snippet: "same tokens"},
			{Path: "c.md", Score: 0.8, Snippet: "same tokens"},
			{Path: "a.md", Score: 0.8, Snippet: "same tokens"},
		},
	}

	for idx, input := range permutations {
		got, err := applyMMRContext(context.Background(), input, 0.7)
		if err != nil {
			t.Fatalf("permutation %d: %v", idx, err)
		}
		if gotPaths := fmt.Sprint(resultPaths(got)); gotPaths != "[a.md b.md c.md]" {
			t.Fatalf("permutation %d produced unstable order %s", idx, gotPaths)
		}
	}
}

func TestApplyMMRContextReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := applyMMRContext(ctx, []MemorySearchResult{
		{Path: "a.md", Score: 1, Snippet: "alpha"},
		{Path: "b.md", Score: 0.9, Snippet: "beta"},
	}, 0.7)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

type cancelAfterChecksContext struct {
	context.Context
	remaining int
	cancel    context.CancelFunc
}

func (c *cancelAfterChecksContext) Err() error {
	if c.remaining <= 0 {
		c.cancel()
	} else {
		c.remaining--
	}
	return c.Context.Err()
}

func TestApplyMMRContextChecksCancellationDuringRanking(t *testing.T) {
	results := make([]MemorySearchResult, 40)
	for idx := range results {
		results[idx] = MemorySearchResult{
			Path:    fmt.Sprintf("candidate-%02d.md", idx),
			Score:   1 - float64(idx)/100,
			Snippet: fmt.Sprintf("shared workflow distinct-%d", idx),
		}
	}
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterChecksContext{
		Context:   baseCtx,
		remaining: len(results) + 8,
		cancel:    cancel,
	}

	_, err := applyMMRContext(ctx, results, 0.7)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation during ranking, got %v", err)
	}
}

func TestApplyMMRContextRanksProductionSizedCandidateSetWithinBudget(t *testing.T) {
	results := make([]MemorySearchResult, 400)
	for idx := range results {
		var snippet strings.Builder
		fmt.Fprintf(&snippet, "shared workflow cohort-%d candidate-%d ", idx%40, idx)
		for tokenIdx := range 30 {
			fmt.Fprintf(&snippet, "topic-%d-%d ", idx%17, tokenIdx)
		}
		results[idx] = MemorySearchResult{
			Path:    fmt.Sprintf("candidate-%03d.md", idx),
			Score:   1 - float64(idx)/1000,
			Snippet: snippet.String(),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	reranked, err := applyMMRContext(ctx, results, 0.7)
	if err != nil {
		t.Fatalf("rank 400 candidates within triage budget: %v (elapsed %v)", err, time.Since(started))
	}
	if len(reranked) != len(results) {
		t.Fatalf("expected %d ranked candidates, got %d", len(results), len(reranked))
	}
}

func TestFinalizeMemorySearchResultsPreservesDiversityThroughLimit(t *testing.T) {
	input := []MemorySearchResult{
		{Path: "alpha-primary.md", Score: 1.0, Snippet: "alpha launch decision review"},
		{Path: "alpha-copy.md", Score: 0.99, Snippet: "alpha launch decision review"},
		{Path: "finance.md", Score: 0.8, Snippet: "invoice cash collection forecast"},
		{Path: "low-signal.md", Score: 0.1, Snippet: "miscellaneous archive"},
	}

	got, err := finalizeMemorySearchResults(
		context.Background(),
		input,
		true,
		memorySearchOptions{applyMMR: true, resultLimit: 2},
	)
	if err != nil {
		t.Fatalf("finalizeMemorySearchResults: %v", err)
	}
	if gotPaths := fmt.Sprint(resultPaths(got)); gotPaths != "[alpha-primary.md finance.md]" {
		t.Fatalf("MMR order was lost before limiting: %s", gotPaths)
	}
}

func TestFinalizeMemorySearchResultsCanceledMMRFallsBackToRelevance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := []MemorySearchResult{
		{Path: "lower.md", Score: 0.7, Snippet: "lower"},
		{Path: "higher.md", Score: 0.9, Snippet: "higher"},
	}

	got, err := finalizeMemorySearchResults(
		ctx,
		input,
		true,
		memorySearchOptions{applyMMR: true},
	)
	if err != nil {
		t.Fatalf("bounded fallback should return relevance results: %v", err)
	}
	if gotPaths := fmt.Sprint(resultPaths(got)); gotPaths != "[higher.md lower.md]" {
		t.Fatalf("unexpected fallback order: %s", gotPaths)
	}

	_, err = finalizeMemorySearchResults(
		ctx,
		input,
		true,
		memorySearchOptions{applyMMR: true, strictErrors: true},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("strict search should return cancellation, got %v", err)
	}
}

// ── Query-construction pipeline tests ──────────────────────────────────
// These tests exercise the *string* pipeline that Search() runs before
// hitting SQLite: extractKeywords → fallback → buildFTSQueryWithOp.
// They do NOT open a DB — real end-to-end coverage lives in the
// integration tests (TestSearch_*) in memory_search_e2e_test.go.

func TestQueryConstructionPipeline(t *testing.T) {
	// Simulates the Search() prologue: extract keywords, fall back if empty,
	// build AND then OR.
	query := "where am i based"
	keywords := extractKeywords(query)
	if keywords == "" {
		keywords = query
	}
	if keywords != "based" {
		t.Fatalf("keyword extraction degraded: got %q; want %q", keywords, "based")
	}

	and := buildFTSQueryWithOp(keywords, "AND")
	or := buildFTSQueryWithOp(keywords, "OR")
	if and != `"based"` {
		t.Errorf("AND query = %q; want %q", and, `"based"`)
	}
	if or != `"based"` {
		t.Errorf("OR query = %q; want %q (single token is identical under both ops)", or, `"based"`)
	}
}

func TestQueryConstructionPipelineMultiKeyword(t *testing.T) {
	// "where was I last in Halcyon" → keywords "last halcyon".
	keywords := extractKeywords("where was I last in Halcyon")
	if keywords != "last halcyon" {
		t.Fatalf("keywords = %q; want %q", keywords, "last halcyon")
	}
	and := buildFTSQueryWithOp(keywords, "AND")
	or := buildFTSQueryWithOp(keywords, "OR")
	if and != `"last" AND "halcyon"` {
		t.Errorf("AND = %q", and)
	}
	if or != `"last" OR "halcyon"` {
		t.Errorf("OR = %q", or)
	}
}

func TestMemoryStoreSearchNaturalLanguageQueryReturnsHalcyonMemory(t *testing.T) {
	configDir := t.TempDir()
	store, err := NewMemoryStore(configDir, MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	memoryDir := filepath.Join(configDir, "memory")
	halcyonContent := strings.Join([]string{
		"# Alex's location context",
		"",
		"- **Updated**: 2027-09-18 16:40",
		"- **Source**: conversation",
		"- **Dimension**: deep_user_model",
		"",
		"## Memory Points",
		"",
		"- Alex is based in Halcyon, a fictional harbor city.",
		"- Halcyon is the starting point for weekend ferry trips to Vesper Isle.",
	}, "\n")
	if err := os.WriteFile(
		filepath.Join(memoryDir, "conversation-halcyon.md"),
		[]byte(halcyonContent),
		0o600,
	); err != nil {
		t.Fatalf("write Halcyon memory: %v", err)
	}

	// Give FTS5 a small realistic corpus so bm25() has useful IDF signal.
	for i := range 80 {
		path := filepath.Join(memoryDir, fmt.Sprintf("distractor-%02d.md", i))
		content := fmt.Sprintf(
			"# Unrelated note %02d\n\nThis note covers design systems, automation, and project planning.\n",
			i,
		)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write distractor %d: %v", i, err)
		}
	}

	if err := store.Sync("test"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	results, err := store.Search(context.Background(), "where am i based", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Path == "conversation-halcyon.md" &&
			strings.Contains(strings.ToLower(r.Snippet), "halcyon") {
			return
		}
	}

	t.Fatalf("Search(%q) did not return Halcyon memory; got %#v", "where am i based", results)
}

// ── helpers ─────────────────────────────────────────────────────────────

func approxEq(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}
