package appparser

import (
	"strings"
	"testing"
)

// TestNotionParser_SwiftUITextAreaWithBullets reproduces the failure
// mode from the bug report: macOS Notion (SwiftUI rewrite) exposes the
// whole page as a single AXTextArea whose accessibility label is
// "Add icon Add cover Add comment Todo list • backend: ... • frontend:
// react • ...". The old parser only looked at AXStaticText and got
// nothing, so Notion captures produced empty parsed content.
func TestNotionParser_SwiftUITextAreaWithBullets(t *testing.T) {
	pageLabel := "Add icon Add cover Add comment Todo list " +
		"• backend : python pydantic fastapi based application, storage infos to sqlite " +
		"• frontend: react " +
		"• should support layered tag system " +
		"• should support Eisenhower-Matrix system (2-layer) " +
		"• hello " +
		"• List"

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: pageLabel},
		},
	}

	parsed := (&notionParser{}).ParseTree(tree, "notion.id", "Todo list", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content for SwiftUI Notion layout")
	}
	for _, expected := range []string{
		"backend : python pydantic fastapi based application",
		"frontend: react",
		"should support layered tag system",
		"should support Eisenhower-Matrix system (2-layer)",
		"hello",
	} {
		if !strings.Contains(parsed.Body, expected) {
			t.Errorf("body missing expected block %q\n--- body ---\n%s", expected, parsed.Body)
		}
	}
	if strings.Contains(parsed.Body, "Add icon") ||
		strings.Contains(parsed.Body, "Add cover") ||
		strings.Contains(parsed.Body, "Add comment") {
		t.Errorf("toolbar chrome leaked into body:\n%s", parsed.Body)
	}
}

// TestNotionParser_LegacyAXStaticTextStillWorks pins the legacy Electron
// / web-Notion path so the SwiftUI fallback doesn't regress it.
func TestNotionParser_LegacyAXStaticTextStillWorks(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"notion-page-content"},
						Children: []*AXNode{
							{Role: "AXStaticText", Value: "First paragraph."},
							{Role: "AXStaticText", Value: "Second paragraph."},
						},
					},
				},
			},
		},
	}
	parsed := (&notionParser{}).ParseTree(tree, "notion.id", "My Page", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "First paragraph.") ||
		!strings.Contains(parsed.Body, "Second paragraph.") {
		t.Errorf("static-text content missing: %q", parsed.Body)
	}
}

// TestNotionParser_DedupesMirroredText guards against a SwiftUI build
// that exposes the same block via both an AXTextArea and a nested
// AXStaticText — the line must not appear twice.
func TestNotionParser_DedupesMirroredText(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:  "AXTextArea",
				Value: "shared content line",
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "shared content line"},
				},
			},
		},
	}
	parsed := (&notionParser{}).ParseTree(tree, "notion.id", "Note", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if c := strings.Count(parsed.Body, "shared content line"); c != 1 {
		t.Errorf("expected single occurrence, got %d: %q", c, parsed.Body)
	}
}

// TestNotionParser_PicksPageWebAreaOverTabBar reproduces the bug found
// when running ax-tree-dump on a live native Notion window: the app
// exposes two sibling AXWebArea nodes — one with title="Tab Bar"
// (top tabs) and one whose title matches the page name (the editor).
// FindFirst grabbed the tab bar and the parser only emitted "panda" +
// "Todo list" instead of the page body. The fix picks the AXWebArea
// whose title matches the windowTitle.
func TestNotionParser_PicksPageWebAreaOverTabBar(t *testing.T) {
	tabBar := &AXNode{
		Role:  "AXWebArea",
		Title: "Tab Bar",
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "panda"},
			{Role: "AXStaticText", Value: "Todo list"},
		},
	}
	pageEditor := &AXNode{
		Role:  "AXWebArea",
		Title: "Todo list",
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "backend : python pydantic fastapi"},
			{Role: "AXStaticText", Value: "frontend: react"},
		},
	}
	tree := &AXNode{
		Role:     "AXWindow",
		Title:    "Todo list",
		Children: []*AXNode{tabBar, pageEditor},
	}
	parsed := (&notionParser{}).ParseTree(tree, "notion.id", "Todo list", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "backend : python") ||
		!strings.Contains(parsed.Body, "frontend: react") {
		t.Errorf("page editor content missing — parser likely picked Tab Bar:\n%s", parsed.Body)
	}
}

// TestNotionParser_CursorListenerDoesNotSwallowPage reproduces the
// second bug: the modern Notion app wraps the whole editor in an
// outer container named `notion-cursor-listener` (the document-level
// pointer event listener). A substring match against "notion-cursor"
// would silently drop the entire page body. Verify that page text
// under that container still makes it into the output.
func TestNotionParser_CursorListenerDoesNotSwallowPage(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"notion-cursor-listener"},
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "real page body line"},
				},
			},
		},
	}
	parsed := (&notionParser{}).ParseTree(tree, "notion.id", "Note", "")
	if parsed == nil || !strings.Contains(parsed.Body, "real page body line") {
		t.Fatalf("body under notion-cursor-listener was incorrectly skipped: %+v", parsed)
	}
}

// TestStripNotionHeaderChrome locks in the prefix peeling.
func TestStripNotionHeaderChrome(t *testing.T) {
	cases := map[string]string{
		"Add icon Add cover Add comment Todo list": "Todo list",
		"Add icon Todo list":                       "Todo list",
		"Add cover Add comment Todo list":          "Todo list",
		"Add comment Todo list":                    "Todo list",
		"Todo list":                                "Todo list",
		"":                                         "",
		"Add icon":                                 "",
	}
	for in, want := range cases {
		if got := stripNotionHeaderChrome(in); got != want {
			t.Errorf("stripNotionHeaderChrome(%q) = %q, want %q", in, got, want)
		}
	}
}
