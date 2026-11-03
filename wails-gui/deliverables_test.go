package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDeliverables(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantSummary   string
		wantPaths     []string
		wantPathCount int
	}{
		{
			name:        "no deliverables block",
			input:       "Task completed successfully. Created 3 files.",
			wantSummary: "Task completed successfully. Created 3 files.",
			wantPaths:   nil,
		},
		{
			name: "single file",
			input: `Summary of work done.
<deliverables>
/tmp/output/report.pdf
</deliverables>`,
			wantSummary:   "Summary of work done.",
			wantPathCount: 1,
		},
		{
			name: "multiple files",
			input: `Created report and data.
<deliverables>
/home/user/report.pptx
/home/user/data.csv
/home/user/chart.png
</deliverables>`,
			wantSummary:   "Created report and data.",
			wantPathCount: 3,
		},
		{
			name: "empty deliverables block",
			input: `Done.
<deliverables>
</deliverables>`,
			wantSummary: "Done.",
			wantPaths:   nil,
		},
		{
			name: "deliverables with extra whitespace",
			input: `Done.
<deliverables>
  /path/to/file1.txt
  /path/to/file2.txt
</deliverables>`,
			wantSummary:   "Done.",
			wantPathCount: 2,
		},
		{
			name: "deliverables block in middle of text",
			input: `Start.
<deliverables>
/path/file.txt
</deliverables>
End.`,
			wantSummary:   "Start.\n\nEnd.",
			wantPathCount: 1,
		},
		{
			name: "caps at 10 paths",
			input: `Done.
<deliverables>
/f1
/f2
/f3
/f4
/f5
/f6
/f7
/f8
/f9
/f10
/f11
/f12
</deliverables>`,
			wantSummary:   "Done.",
			wantPathCount: 10,
		},
		{
			name: "blank lines inside deliverables filtered",
			input: `Done.
<deliverables>
/path/a.txt

/path/b.txt

</deliverables>`,
			wantSummary:   "Done.",
			wantPathCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, paths := parseDeliverables(tt.input)

			if tt.wantSummary != "" && summary != tt.wantSummary {
				t.Errorf("summary mismatch:\ngot:  %q\nwant: %q", summary, tt.wantSummary)
			}

			if tt.wantPaths != nil {
				if len(paths) != len(tt.wantPaths) {
					t.Errorf("paths count: got %d, want %d", len(paths), len(tt.wantPaths))
				}
			}

			if tt.wantPathCount > 0 && len(paths) != tt.wantPathCount {
				t.Errorf("paths count: got %d, want %d", len(paths), tt.wantPathCount)
			}

			// Verify deliverables block is stripped from summary.
			if strings.Contains(summary, "<deliverables>") {
				t.Error("summary still contains <deliverables> tag")
			}
		})
	}
}

func TestParseDeliverableRefs(t *testing.T) {
	t.Run("path with description", func(t *testing.T) {
		_, refs := parseDeliverableRefs(`Done.
<deliverables>
/home/user/report.pptx | Quarterly sales deck for the leadership review
/home/user/data.csv
</deliverables>`)
		if len(refs) != 2 {
			t.Fatalf("refs count: got %d, want 2", len(refs))
		}
		if refs[0].Path != "/home/user/report.pptx" {
			t.Errorf("path: got %q", refs[0].Path)
		}
		if refs[0].Description != "Quarterly sales deck for the leadership review" {
			t.Errorf("description: got %q", refs[0].Description)
		}
		if refs[1].Path != "/home/user/data.csv" || refs[1].Description != "" {
			t.Errorf("legacy line: got %+v", refs[1])
		}
	})

	t.Run("description keeps later pipes", func(t *testing.T) {
		_, refs := parseDeliverableRefs(`Done.
<deliverables>
/a.md | comparison of A | B variants
</deliverables>`)
		if len(refs) != 1 {
			t.Fatalf("refs count: got %d, want 1", len(refs))
		}
		if refs[0].Description != "comparison of A | B variants" {
			t.Errorf("description: got %q", refs[0].Description)
		}
	})

	t.Run("separator-only line dropped", func(t *testing.T) {
		_, refs := parseDeliverableRefs(`Done.
<deliverables>
| dangling description with no path
</deliverables>`)
		if len(refs) != 0 {
			t.Fatalf("refs count: got %d, want 0", len(refs))
		}
	})

	t.Run("description markdown stripped", func(t *testing.T) {
		_, refs := parseDeliverableRefs(`Done.
<deliverables>
/a.md | **Final** report with [sources](https://example.com)
</deliverables>`)
		if len(refs) != 1 {
			t.Fatalf("refs count: got %d, want 1", len(refs))
		}
		if refs[0].Description != "Final report with sources" {
			t.Errorf("description: got %q", refs[0].Description)
		}
	})

	t.Run("overlong description truncated", func(t *testing.T) {
		long := strings.Repeat("x", maxDeliverableDescriptionRunes+50)
		_, refs := parseDeliverableRefs("Done.\n<deliverables>\n/a.md | " + long + "\n</deliverables>")
		if len(refs) != 1 {
			t.Fatalf("refs count: got %d, want 1", len(refs))
		}
		if got := len([]rune(refs[0].Description)); got != maxDeliverableDescriptionRunes+1 {
			t.Errorf("description length: got %d runes, want %d (incl. ellipsis)", got, maxDeliverableDescriptionRunes+1)
		}
	})
}

func TestDecodeChatDeliverableFiles(t *testing.T) {
	t.Run("current object form", func(t *testing.T) {
		files := decodeChatDeliverableFiles(`[{"path":"/a/b.md","name":"stale.md","description":"**The** [report](https://example.com)"}]`)
		if len(files) != 1 {
			t.Fatalf("files count: got %d, want 1", len(files))
		}
		if files[0].Name != "b.md" {
			t.Errorf("name not re-derived: got %q", files[0].Name)
		}
		if files[0].Description != "The report" {
			t.Errorf("description: got %q", files[0].Description)
		}
	})

	t.Run("current object form caps legacy descriptions", func(t *testing.T) {
		long := strings.Repeat("x", maxDeliverableDescriptionRunes+50)
		files := decodeChatDeliverableFiles(`[{"path":"/a/b.md","description":"` + long + `"}]`)
		if len(files) != 1 {
			t.Fatalf("files count: got %d, want 1", len(files))
		}
		if got := len([]rune(files[0].Description)); got != maxDeliverableDescriptionRunes+1 {
			t.Errorf("description length: got %d runes, want %d (incl. ellipsis)", got, maxDeliverableDescriptionRunes+1)
		}
	})

	t.Run("legacy string array form", func(t *testing.T) {
		files := decodeChatDeliverableFiles(`["/a/b.md","/c/d.csv"]`)
		if len(files) != 2 {
			t.Fatalf("files count: got %d, want 2", len(files))
		}
		if files[0].Path != "/a/b.md" || files[0].Name != "b.md" || files[0].Description != "" {
			t.Errorf("legacy file: got %+v", files[0])
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		if files := decodeChatDeliverableFiles("not json"); files != nil {
			t.Errorf("expected nil, got %+v", files)
		}
	})
}

func TestDeliverableSummaryLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{
			name:  "plain text passes through",
			input: "Task completed successfully.",
			max:   200,
			want:  "Task completed successfully.",
		},
		{
			name:  "bold markers stripped",
			input: "**Memory cleanup complete.** Here's the summary:",
			max:   200,
			want:  "Memory cleanup complete. Here's the summary:",
		},
		{
			name:  "heading skipped in favor of body text",
			input: "# Objective\n\nInspect and edit the teardown doc in VS Code.",
			max:   200,
			want:  "Inspect and edit the teardown doc in VS Code.",
		},
		{
			name:  "heading-only document falls back to heading text",
			input: "# Objective\n\n## Steps",
			max:   200,
			want:  "Objective",
		},
		{
			name:  "leading blank lines skipped",
			input: "\n\n  \nFirst real line.",
			max:   200,
			want:  "First real line.",
		},
		{
			name:  "link keeps label",
			input: "See [the report](https://example.com/r) for details.",
			max:   200,
			want:  "See the report for details.",
		},
		{
			name:  "inline code and italics unwrapped",
			input: "Wrote `main.go` with *three* helpers.",
			max:   200,
			want:  "Wrote main.go with three helpers.",
		},
		{
			name:  "list marker stripped",
			input: "- first item of the result",
			max:   200,
			want:  "first item of the result",
		},
		{
			name:  "blockquote marker stripped",
			input: "> quoted conclusion",
			max:   200,
			want:  "quoted conclusion",
		},
		{
			name:  "snake_case untouched",
			input: "Updated result_summary handling in cron_exec.",
			max:   200,
			want:  "Updated result_summary handling in cron_exec.",
		},
		{
			name:  "image-only first line falls back to later body text",
			input: "![](chart.png)\n\nRevenue grew 12% quarter over quarter.",
			max:   200,
			want:  "Revenue grew 12% quarter over quarter.",
		},
		{
			name:  "horizontal rule skipped",
			input: "---\nActual content after the rule.",
			max:   200,
			want:  "Actual content after the rule.",
		},
		{
			name:  "spaced horizontal rule skipped",
			input: "* * *\nActual content after the rule.",
			max:   200,
			want:  "Actual content after the rule.",
		},
		{
			name:  "markdown-chrome-only document yields empty",
			input: "![](a.png)\n---",
			max:   200,
			want:  "",
		},
		{
			name:  "heading fallback is markdown-stripped",
			input: "# **Bold** title only",
			max:   200,
			want:  "Bold title only",
		},
		{
			name:  "truncates to max runes",
			input: "abcdefghij",
			max:   5,
			want:  "abcde…",
		},
		{
			name:  "empty input",
			input: "",
			max:   200,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deliverableSummaryLine(tt.input, tt.max); got != tt.want {
				t.Errorf("deliverableSummaryLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateDeliverablePath(t *testing.T) {
	// Create temp directory structure for testing.
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "project")
	sessionDir := filepath.Join(tmpDir, "session")
	outsideDir := filepath.Join(tmpDir, "outside")

	for _, d := range []string{workDir, sessionDir, outsideDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Create test files.
	workFile := filepath.Join(workDir, "report.pdf")
	sessionFile := filepath.Join(sessionDir, "temp.csv")
	outsideFile := filepath.Join(outsideDir, "secret.txt")

	for _, f := range []string{workFile, sessionFile, outsideFile} {
		if err := os.WriteFile(f, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"file in workDir", workFile, true},
		{"file in sessionDir", sessionFile, true},
		{"file outside both dirs", outsideFile, false},
		{"nonexistent file", filepath.Join(workDir, "nope.txt"), false},
		{"path traversal attempt", filepath.Join(workDir, "..", "outside", "secret.txt"), false},
		{"empty path", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateDeliverablePath(tt.path, workDir, sessionDir)
			if got != tt.want {
				t.Errorf("validateDeliverablePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	// Test with empty dirs.
	t.Run("empty workDir still validates sessionDir", func(t *testing.T) {
		got := validateDeliverablePath(sessionFile, "", sessionDir)
		if !got {
			t.Error("expected true for file in sessionDir with empty workDir")
		}
	})

	t.Run("both dirs empty rejects everything", func(t *testing.T) {
		got := validateDeliverablePath(workFile, "", "")
		if got {
			t.Error("expected false with both dirs empty")
		}
	})
}

// touchedBy returns the canonical form of each path, as a run's tool tracking
// records it: the files the run read or wrote.
func touchedBy(t *testing.T, paths ...string) map[string]struct{} {
	t.Helper()
	touched := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		resolved, err := resolvedExistingPath(p)
		if err != nil {
			t.Fatal(err)
		}
		touched[resolved] = struct{}{}
	}
	return touched
}

func TestReadDeliverableFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files.
	smallFile := filepath.Join(tmpDir, "small.txt")
	if err := os.WriteFile(smallFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("reads valid file", func(t *testing.T) {
		files, skipped := readDeliverableFiles([]string{smallFile}, tmpDir, "", touchedBy(t, smallFile))
		if len(files) != 1 {
			t.Fatalf("expected 1 file, got %d", len(files))
		}
		if files[0].Filename != "small.txt" {
			t.Errorf("expected filename small.txt, got %s", files[0].Filename)
		}
		if string(files[0].Data) != "hello" {
			t.Errorf("expected data 'hello', got %q", string(files[0].Data))
		}
		if len(skipped) != 0 {
			t.Errorf("expected no skipped, got %v", skipped)
		}
	})

	t.Run("skips nonexistent file", func(t *testing.T) {
		files, skipped := readDeliverableFiles([]string{filepath.Join(tmpDir, "nope.txt")}, tmpDir, "", nil)
		if len(files) != 0 {
			t.Errorf("expected 0 files, got %d", len(files))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		// EvalSymlinks fails for nonexistent files, so the upload validator
		// rejects them before os.Stat.
		if !strings.Contains(skipped[0], "not read or written by this run") {
			t.Errorf("expected 'not read or written by this run' in skip reason, got %q", skipped[0])
		}
	})

	t.Run("skips file outside allowed dirs", func(t *testing.T) {
		outsideDir := t.TempDir()
		outsideFile := filepath.Join(outsideDir, "secret.txt")
		os.WriteFile(outsideFile, []byte("secret"), 0o644)

		files, skipped := readDeliverableFiles([]string{outsideFile}, tmpDir, "", nil)
		if len(files) != 0 {
			t.Errorf("expected 0 files, got %d", len(files))
		}
		if len(skipped) != 1 {
			t.Fatalf("expected 1 skipped, got %d", len(skipped))
		}
		if !strings.Contains(skipped[0], "not read or written by this run") {
			t.Errorf("expected 'not read or written by this run' in skip reason, got %q", skipped[0])
		}
	})

	t.Run("skips a file in the work folder that the run never read or wrote", func(t *testing.T) {
		files, skipped := readDeliverableFiles([]string{smallFile}, tmpDir, "", nil)
		if len(files) != 0 {
			t.Fatalf("uploaded an untouched file inside the work folder: %+v", files)
		}
		if len(skipped) != 1 || !strings.Contains(skipped[0], "not read or written by this run") {
			t.Fatalf("skipped = %v, want a 'not read or written' reason", skipped)
		}
	})

	t.Run("resolves relative paths against workDir", func(t *testing.T) {
		files, skipped := readDeliverableFiles([]string{"small.txt"}, tmpDir, "", touchedBy(t, smallFile))
		if len(files) != 1 {
			t.Fatalf("expected 1 file, got %d (skipped: %v)", len(files), skipped)
		}
		if files[0].Filename != "small.txt" {
			t.Errorf("expected filename small.txt, got %s", files[0].Filename)
		}
	})
}

// TestReadDeliverableFilesRefusesUnwrittenHomeFile is the item-2 regression:
// a run that names a sensitive home-directory file it did not write (e.g.
// ~/.ssh/id_ed25519) must never have it uploaded, even though the UI-open
// validator still allows the home directory. A file the run actually wrote,
// even outside its work folder, stays eligible.
func TestReadDeliverableFilesRefusesUnwrittenHomeFile(t *testing.T) {
	// A fake home that is NOT inside the work folder.
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(sshDir, "id_ed25519")
	if err := os.WriteFile(keyFile, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()

	// Named but not written by the run, and outside the work folder: refused.
	files, skipped := readDeliverableFiles([]string{keyFile}, workDir, "", nil)
	if len(files) != 0 {
		t.Fatalf("expected the unwritten ~/.ssh key to be refused, got %d files", len(files))
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "not read or written by this run") {
		t.Fatalf("expected a 'not read or written by this run' skip, got %v", skipped)
	}

	// A file the run wrote outside the work folder (e.g. ~/Documents/report):
	// eligible, because it is in the written set.
	report := filepath.Join(home, "report.pdf")
	if err := os.WriteFile(report, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedReport, err := filepath.EvalSymlinks(report)
	if err != nil {
		t.Fatal(err)
	}
	written := map[string]struct{}{resolvedReport: {}}
	files, skipped = readDeliverableFiles([]string{report}, workDir, "", written)
	if len(files) != 1 {
		t.Fatalf("expected the run-written report to be eligible, got %d files (skipped %v)", len(files), skipped)
	}
}

func TestBuildSkipNote(t *testing.T) {
	t.Run("empty skipped returns empty string", func(t *testing.T) {
		if got := buildSkipNote(nil); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("formats skip notes", func(t *testing.T) {
		skipped := []string{"file.txt -- too large", "other.csv -- not found"}
		got := buildSkipNote(skipped)
		if !strings.Contains(got, "file.txt -- too large") {
			t.Errorf("missing first skip note in %q", got)
		}
		if !strings.Contains(got, "other.csv -- not found") {
			t.Errorf("missing second skip note in %q", got)
		}
	})
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"report.pdf", "application/pdf"},
		{"data.csv", "text/csv"},
		{"image.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"doc.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := detectContentType(tt.filename)
			if got != tt.want {
				t.Errorf("detectContentType(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}
