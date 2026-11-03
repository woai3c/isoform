package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

// These tests cover the safety-critical pure helpers. The Dream service
// itself is integration-level (touches the FS, SQLite, and Haiku) and is
// exercised manually from the UI; the helpers are where bugs would
// silently lose memories.

func TestNormalizeMemoryContent(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "User uses Go", "User uses Go", true},
		{"whitespace", "User  uses   Go\n", "user uses go", true},
		{"case", "User Uses Go", "USER USES GO", true},
		{"nfc-vs-nfd", "café", "café", true},
		{"distinct", "User uses Go", "User uses Rust", false},
		{"empty", "", "", true},
		{"empty-vs-nonempty", "", "User", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ga := normalizeMemoryContent(tc.a)
			gb := normalizeMemoryContent(tc.b)
			got := ga == gb
			if got != tc.want {
				t.Fatalf("normalize(%q)=%q normalize(%q)=%q equal=%v want=%v", tc.a, ga, tc.b, gb, got, tc.want)
			}
		})
	}
}

func TestSurvivorRankPrefersHistory(t *testing.T) {
	// hasHistory must dominate every other signal: the user has touched
	// this file, demoting it would silently throw away their edits.
	noHist := DreamMemoryCandidate{ID: "a", Dimension: "user_insight", UpdatedAt: 9_000_000_000_000}
	withHist := DreamMemoryCandidate{ID: "b", Dimension: "agentic_memory", UpdatedAt: 1_000, HasHistory: true}
	if survivorRank(withHist) <= survivorRank(noHist) {
		t.Fatalf("HasHistory must outrank everything; got %d ≤ %d", survivorRank(withHist), survivorRank(noHist))
	}
}

func TestSurvivorRankPrefersHigherDimension(t *testing.T) {
	insight := DreamMemoryCandidate{ID: "a", Dimension: "user_insight", UpdatedAt: 1_000}
	agent := DreamMemoryCandidate{ID: "b", Dimension: "agentic_memory", UpdatedAt: 9_000_000_000_000}
	// Even with vastly newer agent, the insight wins on dimension tier.
	if survivorRank(insight) <= survivorRank(agent) {
		t.Fatalf("user_insight must outrank agentic_memory; got %d ≤ %d", survivorRank(insight), survivorRank(agent))
	}
}

func TestSurvivorRankRecencyTiebreaker(t *testing.T) {
	// Recency is measured in days-since-epoch (see survivorRank doc),
	// so the timestamps must differ by ≥ 1 day for the tiebreaker to
	// fire. Using realistic UnixMilli values from 2026.
	const day = int64(24 * 60 * 60 * 1000)
	older := DreamMemoryCandidate{ID: "a", Dimension: "user_insight", UpdatedAt: 1_700_000_000_000}
	newer := DreamMemoryCandidate{ID: "b", Dimension: "user_insight", UpdatedAt: 1_700_000_000_000 + day}
	if survivorRank(newer) <= survivorRank(older) {
		t.Fatalf("recency must break ties; got %d ≤ %d", survivorRank(newer), survivorRank(older))
	}
}

func TestExactDuplicateGroupsBucketsAndPicksSurvivor(t *testing.T) {
	a := DreamMemoryCandidate{ID: "a", Content: "User uses Go", Normalized: "user uses go", Dimension: "agentic_memory", UpdatedAt: 100}
	b := DreamMemoryCandidate{ID: "b", Content: "user uses go", Normalized: "user uses go", Dimension: "user_insight", UpdatedAt: 50}
	c := DreamMemoryCandidate{ID: "c", Content: "USER USES GO", Normalized: "user uses go", Dimension: "agentic_memory", UpdatedAt: 200}
	d := DreamMemoryCandidate{ID: "d", Content: "Different fact", Normalized: "different fact", Dimension: "user_insight", UpdatedAt: 50}

	groups := exactDuplicateGroups([]DreamMemoryCandidate{a, b, c, d})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Survivor.ID != "b" {
		t.Fatalf("expected survivor=b (user_insight wins over agentic_memory regardless of recency); got %q", groups[0].Survivor.ID)
	}
	if len(groups[0].Duplicates) != 2 {
		t.Fatalf("expected 2 dupes; got %d", len(groups[0].Duplicates))
	}
}

func TestExactDuplicateGroupsSkipsEmptyKeys(t *testing.T) {
	// Empty-normalized memories (e.g. frontmatter-only files) must not
	// collapse into one mega-group.
	a := DreamMemoryCandidate{ID: "a", Normalized: ""}
	b := DreamMemoryCandidate{ID: "b", Normalized: ""}
	groups := exactDuplicateGroups([]DreamMemoryCandidate{a, b})
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups for empty-key candidates; got %d", len(groups))
	}
}

func TestParseMergeResponseRejectsUnknownIDs(t *testing.T) {
	resp := parseMergeResponse(`{"delete":["a","z"],"synthesize":[]}`, map[string]bool{"a": true})
	if len(resp.DeleteIDs) != 1 || resp.DeleteIDs[0] != "a" {
		t.Fatalf("must drop unknown ids; got %#v", resp.DeleteIDs)
	}
}

func TestParseMergeResponseHandlesCodeFences(t *testing.T) {
	in := "Here is the result:\n```json\n{\"delete\":[\"a\"],\"synthesize\":[{\"title\":\"T\",\"content\":\"body\",\"dimension\":\"user_insight\"}]}\n```\nDone."
	resp := parseMergeResponse(in, map[string]bool{"a": true})
	if len(resp.DeleteIDs) != 1 || len(resp.Synthesize) != 1 {
		t.Fatalf("must parse JSON inside fences; got %#v", resp)
	}
	if resp.Synthesize[0].Dimension != "user_insight" {
		t.Fatalf("dimension lost; got %q", resp.Synthesize[0].Dimension)
	}
}

func TestParseMergeResponseEmptyOnMalformed(t *testing.T) {
	for _, in := range []string{"", "not json", "{", "{\"delete\":1}"} {
		resp := parseMergeResponse(in, map[string]bool{"a": true})
		if len(resp.DeleteIDs) != 0 || len(resp.Synthesize) != 0 {
			t.Fatalf("malformed input %q should yield empty; got %#v", in, resp)
		}
	}
}

func TestParseMergeResponseDropsEmptyContent(t *testing.T) {
	in := `{"delete":[],"synthesize":[{"title":"T","content":"   ","dimension":"user_insight"}]}`
	resp := parseMergeResponse(in, map[string]bool{})
	if len(resp.Synthesize) != 0 {
		t.Fatalf("must drop empty-content synthesis; got %#v", resp.Synthesize)
	}
}

func TestParseMergeResponseInvalidDimensionBlanked(t *testing.T) {
	in := `{"delete":[],"synthesize":[{"title":"T","content":"body","dimension":"NOT_A_VALID_DIM"}]}`
	resp := parseMergeResponse(in, map[string]bool{})
	if len(resp.Synthesize) != 1 || resp.Synthesize[0].Dimension != "" {
		t.Fatalf("invalid dimension must be blanked (caller fills); got %#v", resp.Synthesize)
	}
}

func TestExtractFactTokensSkipsFrontmatter(t *testing.T) {
	content := "# Title\n\n- **Source**: activity\n- **Updated**: 2026-05-15 10:00\n- **Dimension**: user_insight\n\nThe user prefers TypeScript over JavaScript for backend work."
	tokens := extractFactTokens(content)
	for _, fm := range []string{"source", "activity", "updated", "dimension"} {
		if _, ok := tokens[fm]; ok {
			t.Fatalf("frontmatter token %q must not appear; tokens=%v", fm, tokens)
		}
	}
	if _, ok := tokens["typescript"]; !ok {
		t.Fatalf("body token typescript must appear; tokens=%v", tokens)
	}
}

func TestVerifyDeletionSafetyAcceptsFullCoverage(t *testing.T) {
	sources := []string{"The user prefers TypeScript over JavaScript for backend work and avoids the any type."}
	survivors := []string{"User prefers TypeScript over JavaScript for backend; avoids any type."}
	if !verifyDeletionSafety(sources, survivors, deletionSafetyThreshold) {
		t.Fatal("survivor preserves all fact tokens; safety check must accept")
	}
}

func TestVerifyDeletionSafetyRejectsLossyMerge(t *testing.T) {
	// Two distinct memories merged into a one-line summary that drops
	// most of the facts.
	sources := []string{
		"User uses Go for backend services with PostgreSQL database and Redis cache.",
		"User strictly forbids the any type in TypeScript and prefers unknown instead.",
	}
	survivors := []string{"User likes typed languages."}
	if verifyDeletionSafety(sources, survivors, deletionSafetyThreshold) {
		t.Fatal("lossy synthesis must be rejected")
	}
}

func TestVerifyDeletionSafetyRejectsEmptySurvivors(t *testing.T) {
	if verifyDeletionSafety([]string{"some fact phrase"}, []string{}, deletionSafetyThreshold) {
		t.Fatal("empty survivors must be rejected — cluster must not vanish")
	}
}

// TestVerifyDeletionSafetyRejectsSymmetric8020 is the regression test for
// the original 80/20 hole. Two memories share most of their content but
// each carries a unique tail of content; a survivor-only check (no
// synthesis) must reject the deletion because the deleted memory's
// unique facts are not anywhere in the survivors.
//
// The numbers are deliberate. Filtering out stopwords and short words,
// the deleted memory ends up with ~14 fact tokens, of which ~9 (~64%)
// appear in the survivor. That's well under the 0.85 threshold so the
// check correctly rejects.
func TestVerifyDeletionSafetyRejectsSymmetric8020(t *testing.T) {
	deleted := []string{
		"User uses Go for backend services with PostgreSQL database and Redis cache. " +
			"Specifically uses gRPC for service communication with extensive Protobuf schemas.",
	}
	survivors := []string{
		"User uses Go for backend services with PostgreSQL database and Redis cache. " +
			"Uses GraphQL for service communication.",
	}
	if verifyDeletionSafety(deleted, survivors, deletionSafetyThreshold) {
		t.Fatal("80/20 symmetric case must be rejected; deleted's unique facts (gRPC, Protobuf, schemas) have no home")
	}
}

// TestVerifyDeletionSafetyRejectsUnionButNotSingleCoverage proves the
// per-survivor (not union) semantics. Two survivors together cover the
// deleted memory's tokens, but neither alone does — under union-of-
// survivors logic this would pass; under single-survivor logic it must
// fail, because "split between survivors" means no single survivor
// actually carries the deleted's content.
func TestVerifyDeletionSafetyRejectsUnionButNotSingleCoverage(t *testing.T) {
	deleted := []string{"alpha bravo charlie delta echo foxtrot golf hotel india juliet"}
	survivors := []string{
		"alpha bravo charlie delta echo",
		"foxtrot golf hotel india juliet",
	}
	if verifyDeletionSafety(deleted, survivors, deletionSafetyThreshold) {
		t.Fatal("split-coverage across multiple survivors must fail single-survivor safety check")
	}
}

// TestVerifyDeletionSafetyAcceptsCoveringSynthesis is the happy path for
// the symmetric 80/20 case: when the LLM produces a synthesis that
// genuinely contains every distinct fact, the safety check passes.
func TestVerifyDeletionSafetyAcceptsCoveringSynthesis(t *testing.T) {
	deleted := []string{
		"User uses Go for backend services with PostgreSQL database via gRPC communication.",
	}
	survivors := []string{
		// Survivor B (kept) carries one half of the shared content.
		"User uses Go for backend services with PostgreSQL database via GraphQL communication.",
		// Synthesized merger explicitly preserves the deleted's unique
		// "gRPC" fact alongside the shared backbone.
		"User uses Go for backend services with PostgreSQL database; communication uses gRPC and GraphQL.",
	}
	if !verifyDeletionSafety(deleted, survivors, deletionSafetyThreshold) {
		t.Fatal("synthesis that preserves the unique fact must pass safety check")
	}
}

func TestVerifyDeletionSafetyEmptyDeletedIsTrivialAccept(t *testing.T) {
	// No deletions ⇒ no facts to verify ⇒ trivially safe. Callers use
	// this to short-circuit the check when the LLM proposed
	// synthesize-only operations.
	if !verifyDeletionSafety(nil, []string{"anything"}, deletionSafetyThreshold) {
		t.Fatal("empty deleted list must be trivially safe")
	}
}

func TestCosineSimilarity64(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{1, 0, 0}
	if got := cosineSimilarity64(a, b); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("identical vectors: want 1, got %v", got)
	}
	c := []float64{0, 1, 0}
	if got := cosineSimilarity64(a, c); math.Abs(got) > 1e-9 {
		t.Fatalf("orthogonal vectors: want 0, got %v", got)
	}
	if got := cosineSimilarity64(nil, a); got != 0 {
		t.Fatalf("empty must return 0; got %v", got)
	}
}

func TestL2NormalizeCentroid(t *testing.T) {
	v := l2NormalizeCentroid([][]float64{{1, 0, 0}, {0, 1, 0}})
	if len(v) != 3 {
		t.Fatalf("want 3 dims; got %d", len(v))
	}
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	if math.Abs(norm-1.0) > 1e-9 {
		t.Fatalf("centroid must be L2-normalized; norm=%v", norm)
	}
	// Ragged input is skipped, not crashed on.
	v2 := l2NormalizeCentroid([][]float64{{1, 0}, {1, 2, 3}})
	if len(v2) != 2 {
		t.Fatalf("ragged input: want 2 dims; got %d", len(v2))
	}
}

func TestL2NormalizeCentroidEmpty(t *testing.T) {
	if got := l2NormalizeCentroid(nil); got != nil {
		t.Fatalf("nil input must return nil; got %v", got)
	}
	if got := l2NormalizeCentroid([][]float64{{0, 0, 0}, {0, 0, 0}}); got != nil {
		t.Fatalf("all-zero input must return nil; got %v", got)
	}
}

func TestUnionFindClustersTransitively(t *testing.T) {
	uf := newUnionFind()
	uf.union("a", "b")
	uf.union("b", "c")
	uf.union("d", "e")
	if uf.find("a") != uf.find("c") {
		t.Fatal("a and c must be in the same set via b")
	}
	if uf.find("a") == uf.find("d") {
		t.Fatal("a and d must be in distinct sets")
	}
}

func TestStripMemoryFrontmatter(t *testing.T) {
	content := "# Title\n\n- **Source**: activity\n- **Dimension**: user_insight\n\nBody starts here.\n\n## Section\n\nMore body."
	got := stripMemoryFrontmatter(content)
	if !strings.HasPrefix(strings.TrimSpace(got), "Body starts here.") {
		t.Fatalf("strip should leave body intact; got %q", got)
	}
	if strings.Contains(got, "Source") {
		t.Fatalf("strip should drop frontmatter; got %q", got)
	}
}

func TestParseDailyTime(t *testing.T) {
	cases := []struct {
		in           string
		hour, minute int
		ok           bool
	}{
		{"03:00", 3, 0, true},
		{"23:59", 23, 59, true},
		{"00:00", 0, 0, true},
		{"3:00", 3, 0, true},
		{"24:00", 0, 0, false},
		{"03:60", 0, 0, false},
		{"abc", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range cases {
		h, m, ok := parseDailyTime(tc.in)
		if ok != tc.ok {
			t.Fatalf("%q: ok=%v want=%v", tc.in, ok, tc.ok)
		}
		if ok && (h != tc.hour || m != tc.minute) {
			t.Fatalf("%q: got %02d:%02d want %02d:%02d", tc.in, h, m, tc.hour, tc.minute)
		}
	}
}

func TestComputeNextRunMsAtAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previousLocal })
	for _, day := range []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
	} {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			last := DreamRun{StartedAt: day.Add(30 * time.Minute).UnixMilli()}
			now := day.Add(4 * time.Hour)
			got := time.UnixMilli(computeNextRunMsAt(DreamSettings{DailyTime: "00:30"}, true, last, now)).In(loc)
			want := day.AddDate(0, 0, 1).Add(30 * time.Minute)
			if !got.Equal(want) {
				t.Fatalf("next run = %s, want %s", got, want)
			}
		})
	}
}

func TestIsDateOnlyMemoryID(t *testing.T) {
	cases := map[string]bool{
		"2026-05-15":    true,
		"2026-05-15-x":  false,
		"memory-12345":  false,
		"":              false,
		"YYYY-MM-DD":    false,
		"2026-05-15.md": false, // trailing extension not part of id
	}
	for in, want := range cases {
		if got := isDateOnlyMemoryID(in); got != want {
			t.Fatalf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestMemoryHasExpired(t *testing.T) {
	pastISO := "# T\n\n- **ExpiresAt**: 2020-01-01T00:00:00Z\n\nbody"
	futureISO := "# T\n\n- **ExpiresAt**: 3000-01-01T00:00:00Z\n\nbody"
	noExpiry := "# T\n\n- **Source**: manual\n\nbody"
	pastHuman := "# T\n\n- **ExpiresAt**: 2020-01-01 00:00\n\nbody"

	if !memoryHasExpired(DreamMemoryCandidate{Content: pastISO}) {
		t.Fatal("past ISO expiry must expire")
	}
	if !memoryHasExpired(DreamMemoryCandidate{Content: pastHuman}) {
		t.Fatal("past human-form expiry must expire")
	}
	if memoryHasExpired(DreamMemoryCandidate{Content: futureISO}) {
		t.Fatal("future expiry must not expire")
	}
	if memoryHasExpired(DreamMemoryCandidate{Content: noExpiry}) {
		t.Fatal("absent ExpiresAt must not expire")
	}
}
