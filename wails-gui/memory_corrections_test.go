package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── LoadRecentCorrections ──

func TestLoadRecentCorrections_MissingFilesIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadRecentCorrections(dir, 10)
	if err != nil {
		t.Fatalf("LoadRecentCorrections: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestLoadRecentCorrections_MergesAndSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()

	// Write rejections (oldest, middle).
	rejPath := filepath.Join(dir, memoryRejectionsLogName)
	writeJSONLines(t, rejPath, []any{
		MemoryRejectionEntry{MemoryID: "r1", Title: "I like chestnuts", Reason: "friend said it", RejectedAt: 1000},
		MemoryRejectionEntry{MemoryID: "r2", Title: "I love watermelon", Reason: "video", RejectedAt: 3000},
	})
	// Write reassignments (newest).
	reaPath := filepath.Join(dir, memoryReassignmentsLogName)
	writeJSONLines(t, reaPath, []any{
		MemoryReassignmentEntry{MemoryID: "x1", Title: "Said by Mei", CorrectPrincipalName: "Mei", Reason: "WeChat", ReassignedAt: 2000},
		MemoryReassignmentEntry{MemoryID: "x2", Title: "Newest", CorrectPrincipalName: "Zhang San", Reason: "Telegram", ReassignedAt: 5000},
	})

	got, err := LoadRecentCorrections(dir, 10)
	if err != nil {
		t.Fatalf("LoadRecentCorrections: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// Sorted newest first.
	want := []int64{5000, 3000, 2000, 1000}
	for i, w := range want {
		if got[i].OccurredAt != w {
			t.Errorf("got[%d].OccurredAt = %d, want %d", i, got[i].OccurredAt, w)
		}
	}
	// Kinds preserved.
	if got[0].Kind != CorrectionKindReassign || got[3].Kind != CorrectionKindReject {
		t.Errorf("kinds wrong: first=%s last=%s", got[0].Kind, got[3].Kind)
	}
	if got[0].TargetName != "Zhang San" {
		t.Errorf("reassign target lost: %+v", got[0])
	}
}

func TestLoadRecentCorrections_LimitsToMaxN(t *testing.T) {
	dir := t.TempDir()
	rejPath := filepath.Join(dir, memoryRejectionsLogName)
	var rows []any
	for i := 1; i <= 100; i++ {
		rows = append(rows, MemoryRejectionEntry{MemoryID: "r", Title: "t", RejectedAt: int64(i)})
	}
	writeJSONLines(t, rejPath, rows)

	got, err := LoadRecentCorrections(dir, 7)
	if err != nil {
		t.Fatalf("LoadRecentCorrections: %v", err)
	}
	if len(got) != 7 {
		t.Errorf("len = %d, want 7", len(got))
	}
	if got[0].OccurredAt != 100 {
		t.Errorf("newest = %d, want 100", got[0].OccurredAt)
	}
}

func TestLoadRecentCorrections_DefaultsToFiftyWhenZero(t *testing.T) {
	dir := t.TempDir()
	rejPath := filepath.Join(dir, memoryRejectionsLogName)
	var rows []any
	for i := 1; i <= 80; i++ {
		rows = append(rows, MemoryRejectionEntry{MemoryID: "r", Title: "t", RejectedAt: int64(i)})
	}
	writeJSONLines(t, rejPath, rows)

	got, _ := LoadRecentCorrections(dir, 0)
	if len(got) != 50 {
		t.Errorf("default cap broken: %d", len(got))
	}
}

func TestLoadRecentCorrections_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	rejPath := filepath.Join(dir, memoryRejectionsLogName)
	if err := os.WriteFile(rejPath, []byte(
		`{"memoryId":"good1","title":"a","rejectedAt":100}`+"\n"+
			`not-json garbage line`+"\n"+
			`{"memoryId":"good2","title":"b","rejectedAt":200}`+"\n",
	), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadRecentCorrections(dir, 10)
	if err != nil {
		t.Fatalf("LoadRecentCorrections: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (corrupt line skipped)", len(got))
	}
}

func TestLoadRecentCorrections_HandlesEmptyLines(t *testing.T) {
	dir := t.TempDir()
	rejPath := filepath.Join(dir, memoryRejectionsLogName)
	if err := os.WriteFile(rejPath, []byte(
		`{"memoryId":"r1","title":"t","rejectedAt":100}`+"\n\n\n"+
			`{"memoryId":"r2","title":"t","rejectedAt":200}`+"\n",
	), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := LoadRecentCorrections(dir, 10)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// ── FormatCorrectionsPromptBlock ──

func TestFormatCorrectionsPromptBlock_EmptyInputProducesEmptyOutput(t *testing.T) {
	got := FormatCorrectionsPromptBlock(nil)
	if got != "" {
		t.Errorf("expected empty output for empty input, got %q", got)
	}
}

func TestFormatCorrectionsPromptBlock_ContainsOnlyStructuralGuidance(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "I like chestnuts", Reason: "friend said it"},
		{Kind: CorrectionKindReassign, Title: "I am sad", Reason: "movie character", TargetName: "Mei"},
	}
	got := FormatCorrectionsPromptBlock(corrections)
	for _, want := range []string{"## Past user corrections", "REJECTED", "REASSIGNED", "current source", "speaker"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	for _, private := range []string{"chestnuts", "friend said it", "I am sad", "movie character", "Mei"} {
		if strings.Contains(got, private) {
			t.Errorf("private correction text %q reached prompt: %s", private, got)
		}
	}
}

func TestFormatCorrectionsPromptBlock_HandlesEmptyTitleAndReason(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, OccurredAt: 1},
	}
	got := FormatCorrectionsPromptBlock(corrections)
	if !strings.Contains(got, "REJECTED") || strings.Contains(got, "(no title)") || strings.Contains(got, "(no reason given)") {
		t.Errorf("empty private fields changed structural guidance: %s", got)
	}
}

// ── NormalizeForMatch ──

func TestNormalizeForMatch_EnglishLowercasedAndSplit(t *testing.T) {
	got := NormalizeForMatch("I LOVE Chestnuts!! they're great.")
	want := []string{"i", "love", "chestnuts", "they", "re", "great"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeForMatch_CJKEachCharIsToken(t *testing.T) {
	got := NormalizeForMatch("我喜欢栗子。")
	want := []string{"我", "喜", "欢", "栗", "子"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeForMatch_MixedScript(t *testing.T) {
	got := NormalizeForMatch("Yansu 学了 React")
	want := []string{"yansu", "学", "了", "react"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeForMatch_EmptyInputReturnsEmpty(t *testing.T) {
	if got := NormalizeForMatch("   "); len(got) != 0 {
		t.Errorf("whitespace input should give empty tokens, got %v", got)
	}
}

// ── JaccardSimilarity ──

func TestJaccardSimilarity_IdenticalIsOne(t *testing.T) {
	tokens := []string{"a", "b", "c"}
	if got := JaccardSimilarity(tokens, tokens); got != 1.0 {
		t.Errorf("identical sets = %v, want 1.0", got)
	}
}

func TestJaccardSimilarity_DisjointIsZero(t *testing.T) {
	if got := JaccardSimilarity([]string{"a", "b"}, []string{"c", "d"}); got != 0.0 {
		t.Errorf("disjoint = %v, want 0", got)
	}
}

func TestJaccardSimilarity_PartialOverlap(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"b", "c", "d"}
	got := JaccardSimilarity(a, b)
	// intersection={b,c}=2, union={a,b,c,d}=4 → 0.5
	if got != 0.5 {
		t.Errorf("got %v, want 0.5", got)
	}
}

func TestJaccardSimilarity_EmptyIsZero(t *testing.T) {
	if got := JaccardSimilarity(nil, []string{"a"}); got != 0 {
		t.Errorf("nil vs non-nil = %v, want 0", got)
	}
}

// ── FindMatchingCorrection ──

func TestFindMatchingCorrection_MatchesTitleByJaccard(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "I like chestnuts"},
	}
	got := FindMatchingCorrection(corrections, "I really like chestnuts", nil)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Score < CorrectionMatchThreshold {
		t.Errorf("score %v below threshold", got.Score)
	}
}

func TestFindMatchingCorrection_NoMatchReturnsNil(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "I like chestnuts"},
	}
	got := FindMatchingCorrection(corrections, "Yansu uses React with TypeScript", nil)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestFindMatchingCorrection_MatchesAgainstAnyPoint(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "I love watermelon"},
	}
	got := FindMatchingCorrection(corrections, "Summer favorites", []string{
		"User installed nginx",
		"I love watermelon",
	})
	if got == nil {
		t.Fatal("expected match against point")
	}
}

func TestFindMatchingCorrection_PicksBestScoreAcrossCorrections(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "I like chestnuts a lot"},
		{Kind: CorrectionKindReject, Title: "I like chestnuts"}, // higher overlap
	}
	got := FindMatchingCorrection(corrections, "I like chestnuts", nil)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Correction.Title != "I like chestnuts" {
		t.Errorf("wrong correction picked: %q", got.Correction.Title)
	}
}

func TestFindMatchingCorrection_FallsBackToReasonWhenTitleEmpty(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "", Reason: "user likes chestnuts"},
	}
	got := FindMatchingCorrection(corrections, "User likes chestnuts a lot", nil)
	if got == nil {
		t.Fatal("expected reason-based match")
	}
}

func TestFindMatchingCorrection_EmptyCandidateIsNoMatch(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "anything"},
	}
	if got := FindMatchingCorrection(corrections, "", nil); got != nil {
		t.Errorf("empty candidate should not match, got %+v", got)
	}
}

func TestFindMatchingCorrection_CJKMatches(t *testing.T) {
	corrections := []CorrectionSummary{
		{Kind: CorrectionKindReject, Title: "我喜欢栗子"},
	}
	got := FindMatchingCorrection(corrections, "我喜欢栗子和苹果", nil)
	if got == nil {
		t.Fatal("expected CJK match")
	}
}

// ── helpers ──

func writeJSONLines(t *testing.T, path string, rows []any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
