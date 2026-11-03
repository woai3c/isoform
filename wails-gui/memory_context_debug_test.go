package main

import (
	"fmt"
	"strings"
	"testing"
)

func memoryContextTestResults(count int) []MemorySearchResult {
	results := make([]MemorySearchResult, 0, count)
	for index := 0; index < count; index++ {
		results = append(results, MemorySearchResult{
			Path:      fmt.Sprintf("context-%03d.md", index),
			Source:    string(MemorySourceMemory),
			StartLine: 1,
			EndLine:   1,
			Score:     0.9,
			Snippet:   fmt.Sprintf("relevant context %03d", index),
		})
	}
	return results
}

func TestMemoryContextSelectionIncludesMoreThanLegacyCandidateCap(t *testing.T) {
	results := memoryContextTestResults(225)
	selection := buildMemoryContextSelection(
		results,
		memoryInjectMinScore,
		memoryInjectContextBudgetChars,
	)
	if selection.CandidateCount != 225 ||
		selection.SelectedCount != 225 ||
		selection.BudgetOmittedCount != 0 ||
		selection.SelectedChars <= 0 ||
		selection.SelectedChars > memoryInjectContextBudgetChars {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestMemoryContextSelectionAccountsForEveryOmission(t *testing.T) {
	results := []MemorySearchResult{
		{Path: "selected.md", Score: 0.9, Snippet: "selected"},
		{Path: "empty.md", Score: 0.9, Snippet: "   "},
		{Path: "below.md", Score: 0.1, Snippet: "below"},
		{Path: "truncated.md", Score: 0.8, Snippet: strings.Repeat("x", 100)},
		{Path: "omitted.md", Score: 0.8, Snippet: "omitted"},
	}
	selection := buildMemoryContextSelection(results, 0.35, 50)
	if selection.CandidateCount != 5 || selection.SelectedCount != 2 ||
		selection.EmptyCount != 1 || selection.BelowScoreCount != 1 ||
		selection.TruncatedCount != 1 || selection.BudgetOmittedCount != 1 ||
		selection.SelectedChars > 50 {
		t.Fatalf("selection=%+v", selection)
	}
	accounted := selection.SelectedCount + selection.EmptyCount +
		selection.BelowScoreCount + selection.BudgetOmittedCount
	if accounted != selection.CandidateCount {
		t.Fatalf("accounted=%d selection=%+v", accounted, selection)
	}
}

func TestCorrectedMemoryCandidatesDoNotLeaveArtificialCountGap(t *testing.T) {
	results := memoryContextTestResults(225)
	suppressed := make(map[string]struct{}, 220)
	for index := 0; index < 220; index++ {
		suppressed[strings.TrimSuffix(results[index].Path, ".md")] = struct{}{}
	}
	filtered := filterCorrectedAgencyLearningContext(results, suppressed)
	selection := buildMemoryContextSelection(filtered, 0.35, 10_000)
	if len(filtered) != 5 || selection.SelectedCount != 5 ||
		selection.Results[0].Path != "context-220.md" ||
		selection.Results[4].Path != "context-224.md" {
		t.Fatalf("filtered=%d selection=%+v", len(filtered), selection)
	}
}

func TestMemoryContextSelectionReceiptReportsProductionBudget(t *testing.T) {
	resolution := memoryContextResolution{
		DiscoveredCount:     230,
		CorrectedExclusions: 5,
		Selection: memoryContextSelection{
			CandidateCount:     225,
			SelectedCount:      210,
			SelectedChars:      47_990,
			BelowScoreCount:    10,
			TruncatedCount:     1,
			BudgetOmittedCount: 5,
		},
	}
	receipt := memoryContextSelectionReceipt(resolution)
	for _, expected := range []string{
		`discovered="230"`,
		`corrected-excluded="5"`,
		`selected="210"`,
		`selected-chars="47990"`,
		`character-budget="48000"`,
		`below-relevance="10"`,
		`truncated="1"`,
		`budget-omitted="5"`,
	} {
		if !strings.Contains(receipt, expected) {
			t.Fatalf("receipt %q missing %q", receipt, expected)
		}
	}
}

func TestMemoryContextDebugSnapshotIsReadOnlyAndReconciled(t *testing.T) {
	store := setupFixtureStore(t)
	rig := newHandoffTestRig(t)
	rig.app.memoryStore = store
	if _, err := rig.app.GetMemoryContextDebugSnapshot("   "); err == nil {
		t.Fatal("empty debug query unexpectedly succeeded")
	}
	snapshot, err := rig.app.GetMemoryContextDebugSnapshot("Halcyon")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != memoryContextDebugVersion || !snapshot.ReadOnly ||
		!snapshot.CountsReconcile || snapshot.Counts.Discovered == 0 ||
		snapshot.Counts.AfterCorrection != snapshot.Counts.Discovered ||
		snapshot.CharacterBudget != memoryInjectContextBudgetChars {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if !browserRPCAllowlist["GetMemoryContextDebugSnapshot"] {
		t.Fatal("Memory context debugger is not browser-RPC allowlisted")
	}
}
