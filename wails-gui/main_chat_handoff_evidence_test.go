package main

import (
	"os"
	"path/filepath"
	"testing"

	"wails-gui/internal/handoff"
)

func TestHandoffMainChatStructuredEvidenceKeepsVerifierPathBoundary(t *testing.T) {
	home := setHandoffVerifierHome(t)
	workdir := filepath.Join(home, "work")
	for _, relative := range []string{"work/report.md", ".ssh/notes.md", ".isoform-agent/state.json", "elsewhere/report.md"} {
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for alias, target := range map[string]string{
		"private-alias.md": filepath.Join(home, ".ssh", "notes.md"),
		"safe-alias.md":    filepath.Join(workdir, "report.md"),
	} {
		if err := os.Symlink(target, filepath.Join(workdir, alias)); err != nil {
			t.Fatal(err)
		}
	}
	safePath, err := resolvedExistingPath(filepath.Join(workdir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		workdir  string
		evidence handoff.ResultEvidence
		wantURL  string
	}{
		{name: "private folder", workdir: workdir, evidence: handoff.ResultEvidence{Path: filepath.Join(home, ".ssh", "notes.md")}},
		{name: "private working folder", workdir: filepath.Join(home, ".ssh"), evidence: handoff.ResultEvidence{Path: "notes.md"}},
		{name: "private app state", workdir: workdir, evidence: handoff.ResultEvidence{Path: filepath.Join(home, ".isoform-agent", "state.json")}},
		{name: "private symlink", workdir: workdir, evidence: handoff.ResultEvidence{Path: "private-alias.md"}},
		{name: "outside working folder", workdir: workdir, evidence: handoff.ResultEvidence{Path: filepath.Join(home, "elsewhere", "report.md")}},
		{name: "safe workspace artifact", workdir: workdir, evidence: handoff.ResultEvidence{Path: "report.md"}, wantURL: safePath},
		{name: "safe symlink canonicalized", workdir: workdir, evidence: handoff.ResultEvidence{Path: "safe-alias.md"}, wantURL: safePath},
		{name: "permitted URL unchanged", workdir: workdir, evidence: handoff.ResultEvidence{URL: "https://example.com/report?view=full#appendix"}, wantURL: "https://example.com/report?view=full#appendix"},
		{name: "credential URL refused", workdir: workdir, evidence: handoff.ResultEvidence{URL: "https://example.com/report?token=secret-token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := mainChatTestApp(t)
			entry := handoff.Entry{
				ID: "result", Title: "The report", Status: handoff.StatusNeedsReview,
				Workdir: tc.workdir, UpdatedAt: 1,
				Result: &handoff.Result{Status: handoff.StatusCompleted, Evidence: []handoff.ResultEvidence{tc.evidence}},
			}
			// Result is a rejected worker assertion. Posting the outcome must
			// not turn its refused file path into a clickable deliverable.
			app.postHandoffResult(entry, "The result needs review.")
			items := mainChatProactiveItems(t, app)
			if len(items) != 1 || items[0].Work == nil {
				t.Fatalf("missing work card: %+v", items)
			}
			links := items[0].Work.Deliverables
			if tc.wantURL == "" {
				if len(links) != 0 {
					t.Fatalf("rejected evidence became a deliverable: %+v", links)
				}
				return
			}
			if len(links) != 1 || links[0].URL != tc.wantURL {
				t.Fatalf("deliverables = %+v, want %q", links, tc.wantURL)
			}
		})
	}
}
