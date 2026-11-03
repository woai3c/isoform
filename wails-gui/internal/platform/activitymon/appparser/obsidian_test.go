package appparser

import (
	"strings"
	"testing"
)

// Helper: build the chrome shell Obsidian wraps every workspace in
// (workspace → workspace-split.mod-root → workspace-tabs.mod-top) so each
// test focuses on the leaf shape that matters. The active leaf is whatever
// the test passes in.
func obsidianWorkspaceTree(activeLeaf *AXNode, extraLeaves ...*AXNode) *AXNode {
	tabs := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-tabs", "mod-active", "mod-top"},
		Children:     append([]*AXNode{activeLeaf}, extraLeaves...),
	}
	return &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"workspace"},
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"workspace-split", "mod-root"},
						Children:     []*AXNode{tabs},
					},
				},
			},
		},
	}
}

// TestObsidianParser_EditModeExtractsCMContent locks in the primary
// happy path: the active leaf hosts a CodeMirror editor whose content
// AXTextArea (class `cm-content`) carries the raw markdown source.
func TestObsidianParser_EditModeExtractsCMContent(t *testing.T) {
	leaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf", "mod-active"},
		Children: []*AXNode{
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"inline-title"},
				Value:        "tmp",
			},
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"cm-content", "cm-lineWrapping"},
				Value:        "​\n# Heading\n\nFirst paragraph.\n\nSecond paragraph.",
			},
		},
	}
	tree := obsidianWorkspaceTree(leaf)

	parsed := (&obsidianParser{}).ParseTree(tree, "md.obsidian",
		"tmp - obsidian - Obsidian 1.12.7", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for edit-mode leaf")
	}
	if parsed.Title != "tmp" {
		t.Errorf("title = %q, want %q", parsed.Title, "tmp")
	}
	if parsed.Metadata["mode"] != "edit" {
		t.Errorf("metadata.mode = %q, want %q", parsed.Metadata["mode"], "edit")
	}
	if parsed.Channel != "obsidian" {
		t.Errorf("channel = %q, want %q", parsed.Channel, "obsidian")
	}
	if parsed.Metadata["vault"] != "obsidian" {
		t.Errorf("metadata.vault = %q, want %q", parsed.Metadata["vault"], "obsidian")
	}
	for _, want := range []string{"# Heading", "First paragraph.", "Second paragraph."} {
		if !strings.Contains(parsed.Body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, parsed.Body)
		}
	}
	if strings.Contains(parsed.Body, "​") {
		t.Errorf("zero-width space leaked into body:\n%q", parsed.Body)
	}
	if parsed.ContentType != "document" {
		t.Errorf("contentType = %q, want %q", parsed.ContentType, "document")
	}
}

// TestObsidianParser_ReadingModeExtractsPreviewView reproduces the
// markdown-preview-view shape: cm-content is gone, the rendered HTML
// nodes show up as AXStaticText under a `markdown-preview-view` group.
func TestObsidianParser_ReadingModeExtractsPreviewView(t *testing.T) {
	leaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf", "mod-active"},
		Children: []*AXNode{
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"inline-title"},
				Value:        "tmp",
			},
			{
				Role:         "AXGroup",
				DOMClassList: []string{"markdown-reading-view"},
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"markdown-preview-view", "markdown-rendered"},
						Children: []*AXNode{
							// Obsidian renders the note title as the leading H1
							// inside the preview, mirroring the inline-title.
							// The parser must dedup it against the title.
							{Role: "AXStaticText", Value: "tmp"},
							{Role: "AXStaticText", Value: "Body paragraph one."},
							{Role: "AXStaticText", Value: "​"},
							{Role: "AXStaticText", Value: "Body paragraph two."},
						},
					},
				},
			},
		},
	}
	tree := obsidianWorkspaceTree(leaf)

	parsed := (&obsidianParser{}).ParseTree(tree, "md.obsidian",
		"tmp - obsidian - Obsidian 1.12.7", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for reading-mode leaf")
	}
	if parsed.Metadata["mode"] != "reading" {
		t.Errorf("metadata.mode = %q, want %q", parsed.Metadata["mode"], "reading")
	}
	if !strings.Contains(parsed.Body, "Body paragraph one.") ||
		!strings.Contains(parsed.Body, "Body paragraph two.") {
		t.Errorf("body missing expected paragraphs:\n%s", parsed.Body)
	}
	// The "tmp" H1 echo must not show up as a body line — it would duplicate
	// the Title field and turn every reading-mode capture into <title>\n<body>.
	for line := range strings.SplitSeq(parsed.Body, "\n") {
		if strings.TrimSpace(line) == "tmp" {
			t.Errorf("title echo leaked into body line: %q\n--- body ---\n%s",
				line, parsed.Body)
		}
	}
	if strings.Contains(parsed.Body, "​") {
		t.Errorf("zero-width placeholder leaked into reading-mode body:\n%q", parsed.Body)
	}
}

// TestObsidianParser_NoActiveLeafReturnsNil covers the "vault chrome only"
// state (e.g. graph view focused, settings open, no markdown tab) — the
// parser must yield nil so the activity pipeline falls back to raw AX text
// instead of advertising a fake document.
func TestObsidianParser_NoActiveLeafReturnsNil(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"workspace"},
				Children: []*AXNode{
					// File explorer sidebar with no active editor leaf.
					{Role: "AXOutline", Children: []*AXNode{
						{Role: "AXStaticText", Value: "some-file.md"},
					}},
				},
			},
		},
	}
	parsed := (&obsidianParser{}).ParseTree(tree, "md.obsidian",
		"obsidian - Obsidian 1.12.7", "")
	if parsed != nil {
		t.Errorf("expected nil for no-active-leaf, got %+v", parsed)
	}
}

// TestObsidianParser_SidebarPluginContentExcluded reproduces the
// Notebook Navigator interaction we found on the live dump: a sibling
// `workspace-leaf` (no `mod-active`) under the right sidedock indexes
// other notes' content as AXStaticText. Parser must not mix sidebar
// content into the active note's body.
func TestObsidianParser_SidebarPluginContentExcluded(t *testing.T) {
	activeLeaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf", "mod-active"},
		Children: []*AXNode{
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"inline-title"},
				Value:        "tmp",
			},
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"cm-content"},
				Value:        "Active note body.",
			},
		},
	}
	sidebarLeaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf"}, // no mod-active
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"nn-file-preview"},
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "Other note preview snippet."},
				},
			},
		},
	}
	tree := obsidianWorkspaceTree(activeLeaf, sidebarLeaf)

	parsed := (&obsidianParser{}).ParseTree(tree, "md.obsidian",
		"tmp - obsidian - Obsidian 1.12.7", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "Active note body.") {
		t.Errorf("body missing active note content: %q", parsed.Body)
	}
	if strings.Contains(parsed.Body, "Other note preview snippet.") {
		t.Errorf("sidebar plugin content leaked into body: %q", parsed.Body)
	}
}

// TestObsidianVaultName covers windowTitle parsing edge cases.
func TestObsidianVaultName(t *testing.T) {
	cases := map[string]string{
		"tmp - obsidian - Obsidian 1.12.7":       "obsidian",
		"My Note - My - Vault - Obsidian 1.12.7": "My - Vault",
		"tmp - Obsidian 1.12.7":                  "",
		"":                                       "",
		"unstructured single segment":            "",
	}
	for in, want := range cases {
		if got := obsidianVaultName(in); got != want {
			t.Errorf("obsidianVaultName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestObsidianParser_EditModeBlankRunsNormalizeThroughRegistry guards
// the CodeMirror cm-content fingerprint: notes with several visually-empty
// lines between paragraphs would otherwise leak a long run of "\n" into
// ParsedContent.Body. Obsidian still strips its U+200B placeholders, while
// the registry applies the blank-run normalization shared by all parsers.
func TestObsidianParser_EditModeBlankRunsNormalizeThroughRegistry(t *testing.T) {
	// Reproduces the bilibili-style note from the DevTools panel:
	// each bullet line is followed by a U+200B placeholder line, then
	// several literal blank lines before the next bullet. With the old
	// cleaner this rendered as a 6-line gap; we expect a single empty
	// line between paragraphs.
	raw := "- \n​\nFirst paragraph\nSecond line\n​\n\n\n\n- \n​\nNext paragraph"
	leaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf", "mod-active"},
		Children: []*AXNode{
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"inline-title"},
				Value:        "tmp",
			},
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"cm-content"},
				Value:        raw,
			},
		},
	}
	tree := obsidianWorkspaceTree(leaf)

	parsed := ParseTree(tree, "", "md.obsidian",
		"tmp - obsidian - Obsidian 1.12.7", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for edit-mode leaf")
	}
	if strings.Contains(parsed.Body, "​") {
		t.Errorf("zero-width space leaked into body:\n%q", parsed.Body)
	}
	if strings.Contains(parsed.Body, "\n\n\n") {
		t.Errorf("blank-line run not collapsed; body retained 3+ consecutive newlines:\n%q",
			parsed.Body)
	}
	for _, want := range []string{"First paragraph", "Second line", "Next paragraph"} {
		if !strings.Contains(parsed.Body, want) {
			t.Errorf("body missing %q after blank-run collapse\n--- body ---\n%s",
				want, parsed.Body)
		}
	}
}

// TestObsidianTitle_FallsBackToWindowTitle covers the case where the
// inline-title element isn't exposed (e.g. an old build, an unusual
// view): we still extract a usable title from the window title prefix.
func TestObsidianTitle_FallsBackToWindowTitle(t *testing.T) {
	leaf := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"workspace-leaf", "mod-active"},
		Children: []*AXNode{
			{
				Role:         "AXTextArea",
				DOMClassList: []string{"cm-content"},
				Value:        "some body",
			},
		},
	}
	tree := obsidianWorkspaceTree(leaf)
	parsed := (&obsidianParser{}).ParseTree(tree, "md.obsidian",
		"My Note - obsidian - Obsidian 1.12.7", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if parsed.Title != "My Note" {
		t.Errorf("title = %q, want %q", parsed.Title, "My Note")
	}
}
