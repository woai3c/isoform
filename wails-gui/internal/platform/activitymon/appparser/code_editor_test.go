package appparser

import (
	"strings"
	"testing"
)

// TestCodeEditorParser_MonacoEditor exercises the happy path where the
// user has flipped editor.accessibilitySupport on (or a screen reader
// auto-activated it). Monaco then emits one AXStaticText per visible
// line under an AXGroup tagged with the "monaco-editor" class, and we
// stitch them back into the on-screen layout.
func TestCodeEditorParser_MonacoEditor(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "main.go — myproject",
		Children: []*AXNode{
			{Role: "AXGroup", DOMClassList: []string{"monaco-workbench"}, Children: []*AXNode{
				{Role: "AXGroup", DOMClassList: []string{"monaco-editor", "vs-dark"}, Children: []*AXNode{
					{Role: "AXStaticText", Value: "package main"},
					{Role: "AXStaticText", Value: ""},
					{Role: "AXStaticText", Value: "func main() {"},
					{Role: "AXStaticText", Value: "\tprintln(\"hi\")"},
					{Role: "AXStaticText", Value: "}"},
				}},
			}},
		},
	}

	p := &codeEditorParser{}
	got := p.ParseTree(tree, "com.microsoft.VSCode", "main.go — myproject", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "document" {
		t.Errorf("contentType: got %q want document", got.ContentType)
	}
	if got.Title != "main.go" {
		t.Errorf("title: got %q want main.go", got.Title)
	}
	for _, want := range []string{"package main", "func main()", "println(\"hi\")"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
	if got.Metadata["filename"] != "main.go" {
		t.Errorf("filename: got %q", got.Metadata["filename"])
	}
	if got.Metadata["workspace"] != "myproject" {
		t.Errorf("workspace: got %q", got.Metadata["workspace"])
	}
	// Editor body is present → axCoverage MUST NOT be set so
	// IsPartialAX returns false and downstream consumers skip the
	// OCR splice.
	if got.Metadata["axCoverage"] != "" {
		t.Errorf("axCoverage should be unset when editor body is present, got %q",
			got.Metadata["axCoverage"])
	}
	if IsPartialAX(got) {
		t.Error("IsPartialAX should be false when editor body is present")
	}
}

// TestCodeEditorParser_MetadataOnlyFallback covers the realistic
// default — VSCode ships with editor.accessibilitySupport="auto", which
// keeps the editor pane's text out of the AX tree unless a screen
// reader is detected. We still want a usable ParsedContent so the
// triager / quick-chat-context can fuse OCR text in via
// MergeWithOCR, gated on axCoverage=metadata_only.
func TestCodeEditorParser_MetadataOnlyFallback(t *testing.T) {
	// Only workbench chrome, no monaco-editor and no AXTextArea —
	// matches what we observed in the live dump (~50KB tree of
	// sidebar + status bar items, zero code lines).
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "wav_test_helpers_test.go — something",
		Children: []*AXNode{
			{Role: "AXGroup", DOMClassList: []string{"monaco-workbench"}, Children: []*AXNode{
				{Role: "AXStaticText", Value: "EXPLORER"},
				{Role: "AXStaticText", Value: "main"}, // git branch in status bar
			}},
		},
	}

	p := &codeEditorParser{}
	got := p.ParseTree(tree, "com.microsoft.VSCode",
		"wav_test_helpers_test.go — something", "")
	if got == nil {
		t.Fatal("expected ParsedContent for metadata-only fallback, got nil")
	}
	if got.Title != "wav_test_helpers_test.go" {
		t.Errorf("title: got %q", got.Title)
	}
	if got.Metadata["filename"] != "wav_test_helpers_test.go" {
		t.Errorf("filename: got %q", got.Metadata["filename"])
	}
	if got.Metadata["workspace"] != "something" {
		t.Errorf("workspace: got %q", got.Metadata["workspace"])
	}
	if got.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage: got %q want %q",
			got.Metadata["axCoverage"], AXCoverageMetadataOnly)
	}
	// The body still carries enough for a consumer that doesn't run
	// the fusion (or has no OCR available) to know which file the
	// user is editing.
	if !strings.Contains(got.Body, "wav_test_helpers_test.go") {
		t.Errorf("body should mention filename: %q", got.Body)
	}
	if !strings.Contains(got.Body, "something") {
		t.Errorf("body should mention workspace: %q", got.Body)
	}
	if !IsPartialAX(got) {
		t.Error("IsPartialAX should be true for metadata-only fallback so OCR splice runs")
	}
}

// TestCodeEditorParser_LongestAXTextAreaFallback covers the rare case
// where AX is enabled but the monaco-editor class anchor missed (some
// Electron builds drop the class when AXEnhancedUserInterface priming
// hasn't fully landed). The longest AXTextArea heuristic — same one
// terminal.go uses for the screen buffer — picks up the editor body.
func TestCodeEditorParser_LongestAXTextAreaFallback(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "config.json — myrepo",
		Children: []*AXNode{
			// Short AXTextArea — search box, would be picked if we
			// just grabbed the first one.
			{Role: "AXTextArea", Value: "find"},
			// Long AXTextArea — the actual editor body.
			{Role: "AXTextArea", Value: "{\n  \"port\": 5173,\n  \"host\": \"localhost\"\n}"},
		},
	}

	p := &codeEditorParser{}
	got := p.ParseTree(tree, "com.microsoft.VSCode", "config.json — myrepo", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if !strings.Contains(got.Body, "\"port\": 5173") {
		t.Errorf("body should pick the longer AXTextArea: %q", got.Body)
	}
	if got.Metadata["axCoverage"] != "" {
		t.Errorf("axCoverage should be unset when body was extracted, got %q",
			got.Metadata["axCoverage"])
	}
}

// TestSplitCodeEditorTitle covers the title-parsing edge cases the
// parser depends on. Failure here propagates into wrong filename /
// workspace metadata, so the table is the ground truth for what shapes
// VSCode emits.
func TestSplitCodeEditorTitle(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		filename  string
		workspace string
	}{
		{
			name:      "plain",
			title:     "main.go — myproject",
			filename:  "main.go",
			workspace: "myproject",
		},
		{
			name:      "unsaved buffer (leading dot)",
			title:     "● main.go — myproject",
			filename:  "main.go",
			workspace: "myproject",
		},
		{
			name:      "trailing [Workspace] decoration",
			title:     "main.go — myproject [Workspace]",
			filename:  "main.go",
			workspace: "myproject",
		},
		{
			name:      "welcome pane",
			title:     "Welcome — myproject",
			filename:  "Welcome",
			workspace: "myproject",
		},
		{
			name:      "no separator",
			title:     "VSCode",
			filename:  "",
			workspace: "",
		},
		{
			name:      "empty",
			title:     "",
			filename:  "",
			workspace: "",
		},
		// Windows VSCode / Cursor — regular hyphen separator and a
		// trailing app-name segment that must be stripped before the
		// split.
		{
			name:      "windows cursor",
			title:     "main.go - build-your-own-agentic-cli - Cursor",
			filename:  "main.go",
			workspace: "build-your-own-agentic-cli",
		},
		{
			name:      "windows vscode",
			title:     "app.tsx - myproject - Visual Studio Code",
			filename:  "app.tsx",
			workspace: "myproject",
		},
		{
			name:      "windows cursor with markdown file",
			title:     "article-zero-to-agent-cli.md - build-your-own-agentic-cli - Cursor",
			filename:  "article-zero-to-agent-cli.md",
			workspace: "build-your-own-agentic-cli",
		},
		{
			name:      "windows windsurf",
			title:     "config.yaml - infra - Windsurf",
			filename:  "config.yaml",
			workspace: "infra",
		},
		{
			name:      "windows unsaved buffer",
			title:     "● draft.md - notes - Cursor",
			filename:  "draft.md",
			workspace: "notes",
		},
		{
			name:      "windows no workspace",
			title:     "Welcome - Cursor",
			filename:  "Welcome",
			workspace: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, w := splitCodeEditorTitle(tc.title)
			if f != tc.filename {
				t.Errorf("filename: got %q want %q", f, tc.filename)
			}
			if w != tc.workspace {
				t.Errorf("workspace: got %q want %q", w, tc.workspace)
			}
		})
	}
}

// TestCodeEditorParser_RelativePathInTitle covers the case where the
// user has customised VSCode's window.title template to include
// ${activeEditorMedium} — the title's first segment is then a slash-
// separated relative path. The parser must split that into a
// `filename` basename plus a `relativePath` for the full segment so
// downstream prompts can pick whichever they need.
func TestCodeEditorParser_RelativePathInTitle(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "internal/platform/activitymon/appparser/code_editor.go — something",
	}
	p := &codeEditorParser{}
	got := p.ParseTree(tree, "com.microsoft.VSCode",
		"internal/platform/activitymon/appparser/code_editor.go — something", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.Metadata["filename"] != "code_editor.go" {
		t.Errorf("filename should be basename: got %q", got.Metadata["filename"])
	}
	if got.Metadata["relativePath"] != "internal/platform/activitymon/appparser/code_editor.go" {
		t.Errorf("relativePath: got %q", got.Metadata["relativePath"])
	}
	if got.Metadata["workspace"] != "something" {
		t.Errorf("workspace: got %q", got.Metadata["workspace"])
	}
	// Body for metadata-only fallback should prefer the relativePath
	// over the basename — it carries strictly more information.
	if !strings.Contains(got.Body, "internal/platform/activitymon/appparser/code_editor.go") {
		t.Errorf("body should contain relativePath: %q", got.Body)
	}
}

// TestCodeEditorParser_NoRelativePathWhenBasename guards the inverse:
// when the title contains only a basename (default VSCode template),
// relativePath MUST be unset so consumers can rely on its presence as
// a signal that the user opted into the verbose template.
func TestCodeEditorParser_NoRelativePathWhenBasename(t *testing.T) {
	tree := &AXNode{Role: "AXWindow", Title: "main.go — myproject"}
	p := &codeEditorParser{}
	got := p.ParseTree(tree, "com.microsoft.VSCode", "main.go — myproject", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.Metadata["filename"] != "main.go" {
		t.Errorf("filename: got %q", got.Metadata["filename"])
	}
	if _, ok := got.Metadata["relativePath"]; ok {
		t.Errorf("relativePath should be unset for basename-only title, got %q",
			got.Metadata["relativePath"])
	}
}

// TestCodeEditorParser_NoBodyNoTitle is the only path that returns
// nil — when the AX tree gave us nothing AND the window title doesn't
// parse to a filename/workspace. Defensive: prevents us from emitting
// an empty ParsedContent that would clutter the activity timeline.
func TestCodeEditorParser_NoBodyNoTitle(t *testing.T) {
	tree := &AXNode{Role: "AXWindow"}
	p := &codeEditorParser{}
	if got := p.ParseTree(tree, "com.microsoft.VSCode", "VSCode", ""); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
