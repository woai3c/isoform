package main

import (
	"strings"
	"testing"

	"wails-gui/internal/sop"
)

func TestBuildSOPActivityMatchPrompt(t *testing.T) {
	acts := []ActivityRow{
		{ID: "act-1", Title: "Pull weekly metrics", Category: "work", StartMs: 1_700_000_000_000, EndMs: 1_700_003_600_000, Description: "Exported dashboards\nand cleaned data"},
		{ID: "act-2", Title: "Random browsing", Category: "leisure", StartMs: 1_700_010_000_000, EndMs: 1_700_011_000_000},
	}
	got := buildSOPActivityMatchPrompt("Every Monday I compile the weekly report", acts)

	for _, want := range []string{
		"act-1", "act-2",
		"Pull weekly metrics",
		"<<<DESCRIPTION",
		"Every Monday I compile the weekly report",
		`"activityIds"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// Multi-line descriptions must be collapsed so each candidate stays on
	// one line of the list.
	if strings.Contains(got, "Exported dashboards\nand cleaned data") {
		t.Error("candidate description not collapsed to one line")
	}
}

func TestBuildSOPDedupGatePrompt(t *testing.T) {
	existing := []sop.Entry{
		{ID: "weekly-report", Title: "Weekly metrics report", Trigger: "Every Monday", Description: "Compile and post weekly metrics"},
	}
	got := buildSOPDedupGatePrompt("Every Monday I compile the weekly report", existing)
	for _, want := range []string{
		"<<<CANDIDATE",
		"Every Monday I compile the weekly report",
		"weekly-report",
		`"verdict"`,
		`"updateId"`,
		`"skip"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gate prompt missing %q", want)
		}
	}
}

func TestOneLineClamp(t *testing.T) {
	if got := oneLineClamp("a\nb\t c", 100); got != "a b c" {
		t.Errorf("collapse failed: %q", got)
	}
	if got := oneLineClamp(strings.Repeat("x", 20), 5); got != "xxxxx..." {
		t.Errorf("clamp failed: %q", got)
	}
	if got := oneLineClamp("短描述内容", 2); got != "短描..." {
		t.Errorf("rune-safe clamp failed: %q", got)
	}
}
