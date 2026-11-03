package appparser

import (
	"strings"
	"testing"
)

// TestNotesParser_PrefersTextAreaOverWebAreaNoise reproduces the modern
// Notes failure mode: the entire window is wrapped in an AXWebArea whose
// AXStaticText descendants are sidebar dates / hint cells (not the note
// body). The actual body lives in an AXTextArea. Previously the WebArea
// path won and returned the sidebar dates as "body", which blocked the
// AXTextArea fallback. Now AXTextArea takes priority.
func TestNotesParser_PrefersTextAreaOverWebAreaNoise(t *testing.T) {
	body := "switch # 大气层系统开机\n1. 右手柄插入破解器插片\n2. 充电口插入破解器"
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			// WebArea wrapping sidebar date strings — noise that the old
			// implementation would have promoted to body.
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "2025/1/19"},
					{Role: "AXStaticText", Value: "按下或选择 Return 键来打开备忘录"},
				},
			},
			// The real note canvas.
			{Role: "AXTextArea", Value: body},
		},
	}

	parsed := (&notesParser{}).ParseTree(tree, "com.apple.Notes", "switch # 大气层系统开机", "")
	if parsed == nil {
		t.Fatalf("ParseTree returned nil; expected body extracted from AXTextArea")
	}
	if !strings.Contains(parsed.Body, "大气层系统开机") {
		t.Errorf("Body should contain note canvas text, got: %q", parsed.Body)
	}
	if strings.Contains(parsed.Body, "2025/1/19") {
		t.Errorf("Body must not include sidebar dates, got: %q", parsed.Body)
	}
	if parsed.Title != "switch # 大气层系统开机" {
		t.Errorf("Title=%q, expected first line of body", parsed.Title)
	}
}

// TestNotesParser_PicksLongerTextAreaSkippingSearch verifies that when
// multiple AXTextArea nodes exist (toolbar search + note canvas), the
// longer one wins. The search field's content would otherwise leak in.
func TestNotesParser_PicksLongerTextAreaSkippingSearch(t *testing.T) {
	canvas := strings.Repeat("note body line\n", 20)
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "搜索"}, // search field with short query
			{Role: "AXTextArea", Value: canvas},
		},
	}
	parsed := (&notesParser{}).ParseTree(tree, "com.apple.Notes", "Note", "")
	if parsed == nil || !strings.Contains(parsed.Body, "note body line") {
		t.Fatalf("expected canvas body, got: %+v", parsed)
	}
	if strings.Contains(parsed.Body, "搜索") {
		t.Errorf("search field text leaked into body: %q", parsed.Body)
	}
}

// TestNotesParser_FallsBackToWebAreaWhenNoTextArea covers legacy WebKit
// Notes where the body is purely AXStaticText nodes inside AXWebArea.
func TestNotesParser_FallsBackToWebAreaWhenNoTextArea(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "Trip Plan"},
					{Role: "AXStaticText", Value: "Day 1: arrive Tokyo"},
					{Role: "AXStaticText", Value: "Day 2: visit Senso-ji"},
				},
			},
		},
	}
	parsed := (&notesParser{}).ParseTree(tree, "com.apple.Notes", "Trip Plan", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "Senso-ji") {
		t.Errorf("expected fallback path to collect static texts, got: %q", parsed.Body)
	}
	if parsed.Title != "Trip Plan" {
		t.Errorf("Title=%q want %q", parsed.Title, "Trip Plan")
	}
}

// TestNotesParser_AcceptsTextAreaDescription covers the rich-text case
// where AXTextArea's Value is empty and the body is exposed via
// Description (observed on some macOS builds with format=rich).
func TestNotesParser_AcceptsTextAreaDescription(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Description: "Shopping list\nmilk\nbread\neggs"},
		},
	}
	parsed := (&notesParser{}).ParseTree(tree, "com.apple.Notes", "Shopping list", "")
	if parsed == nil || !strings.Contains(parsed.Body, "bread") {
		t.Fatalf("expected body from AXTextArea.Description, got: %+v", parsed)
	}
}

func TestNotesParser_ReturnsNilForEmptyTree(t *testing.T) {
	tree := &AXNode{Role: "AXWindow", Children: []*AXNode{
		{Role: "AXTextArea"}, // no Value, no Description
	}}
	if got := (&notesParser{}).ParseTree(tree, "com.apple.Notes", "", ""); got != nil {
		t.Errorf("expected nil when no body content, got: %+v", got)
	}
}
