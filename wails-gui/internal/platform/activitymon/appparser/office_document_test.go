package appparser

import (
	"strings"
	"testing"
)

func officeDocTree(title, body string, decoyShortTexts ...string) *AXNode {
	textArea := &AXNode{Role: "AXTextArea", Value: body}
	children := []*AXNode{textArea}
	for _, s := range decoyShortTexts {
		children = append(children, &AXNode{Role: "AXTextArea", Value: s})
	}
	window := &AXNode{Role: "AXWindow", Title: title, Children: []*AXNode{
		{Role: "AXScrollArea", Children: children},
	}}
	return window
}

// officeWordMultiPageTree mirrors the real Word for Mac shape: one
// AXTextArea per page, each tagged with description="页面 N 内容".
func officeWordMultiPageTree(title string, pageBodies []string, pageDescLocale string) *AXNode {
	var pages []*AXNode
	for i, body := range pageBodies {
		desc := ""
		switch pageDescLocale {
		case "zh":
			desc = "页面 " + itoa(i+1) + " 内容"
		case "en":
			desc = "Page " + itoa(i+1) + " content"
		}
		pages = append(pages, &AXNode{Role: "AXTextArea", Value: body, Description: desc})
	}
	return &AXNode{Role: "AXWindow", Title: title, Children: []*AXNode{
		{Role: "AXScrollArea", Children: pages},
	}}
}

func TestOfficeDocumentParser_CanParse(t *testing.T) {
	p := &officeDocumentParser{}
	for _, b := range []string{"com.microsoft.Word", "com.apple.Pages", "com.apple.iWork.Pages"} {
		if !p.CanParse(b) {
			t.Errorf("CanParse(%q) = false, want true", b)
		}
	}
	if p.CanParse("com.microsoft.Excel") {
		t.Errorf("Excel must not match the document parser")
	}
}

func TestOfficeDocumentParser_WordHappyPath(t *testing.T) {
	body := "The quick brown fox jumps over the lazy dog.\nLine two."
	tree := officeDocTree("Q4 Plan.docx - Word", body, "search")

	p := &officeDocumentParser{}
	got := p.ParseTree(tree, "com.microsoft.Word", "Q4 Plan.docx - Word", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.ContentType != "document" {
		t.Errorf("contentType: got %q want document", got.ContentType)
	}
	if got.Body != strings.TrimSpace(body) {
		t.Errorf("body: got %q want %q", got.Body, strings.TrimSpace(body))
	}
	if got.Title != "Q4 Plan.docx" {
		t.Errorf("title: got %q want %q (app suffix must be stripped)", got.Title, "Q4 Plan.docx")
	}
	if got.Metadata["app"] != "word" {
		t.Errorf("metadata.app: got %q want word", got.Metadata["app"])
	}
}

func TestOfficeDocumentParser_PagesEmDash(t *testing.T) {
	// Pages uses an em dash in its window title.
	tree := officeDocTree("Essay — Pages", "Hello Pages.")
	p := &officeDocumentParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Pages", "Essay — Pages", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Title != "Essay" {
		t.Errorf("title: got %q want %q (em-dash suffix must be stripped)", got.Title, "Essay")
	}
	if got.Metadata["app"] != "pages" {
		t.Errorf("metadata.app: got %q want pages", got.Metadata["app"])
	}
}

func TestOfficeDocumentParser_PrefersLongestTextArea(t *testing.T) {
	// Toolbar/search AXTextAreas should never win over the document
	// canvas when no page-tagged description is present (Pages shape).
	tree := officeDocTree("Doc - Word", "the actual document body that is long enough to win", "x")
	p := &officeDocumentParser{}
	got := p.ParseTree(tree, "com.microsoft.Word", "Doc - Word", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if !strings.HasPrefix(got.Body, "the actual document body") {
		t.Errorf("body must come from the longest AXTextArea, got %q", got.Body)
	}
}

func TestOfficeDocumentParser_WordMultiPageZh(t *testing.T) {
	// Real Word for Mac shape: one AXTextArea per page, zh-CN
	// description. Pages must be concatenated and ordered by index.
	tree := officeWordMultiPageTree("当前 wails.docx - Word", []string{
		"first page body",
		"second page body",
		"third page body",
	}, "zh")

	p := &officeDocumentParser{}
	got := p.ParseTree(tree, "com.microsoft.Word", "当前 wails.docx - Word", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	for _, want := range []string{"first page body", "second page body", "third page body"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
	// Order must follow page index, not tree order.
	idxFirst := strings.Index(got.Body, "first page body")
	idxSecond := strings.Index(got.Body, "second page body")
	idxThird := strings.Index(got.Body, "third page body")
	if !(idxFirst < idxSecond && idxSecond < idxThird) {
		t.Errorf("page order wrong: first=%d second=%d third=%d", idxFirst, idxSecond, idxThird)
	}
	if got.Metadata["pages"] != "3" {
		t.Errorf("metadata.pages: got %q want 3", got.Metadata["pages"])
	}
}

func TestOfficeDocumentParser_WordMultiPageEn(t *testing.T) {
	// Same shape, en locale description.
	tree := officeWordMultiPageTree("Doc.docx - Word", []string{"alpha", "beta"}, "en")
	p := &officeDocumentParser{}
	got := p.ParseTree(tree, "com.microsoft.Word", "Doc.docx - Word", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if !strings.Contains(got.Body, "alpha") || !strings.Contains(got.Body, "beta") {
		t.Errorf("body missing pages: %q", got.Body)
	}
	if got.Metadata["pages"] != "2" {
		t.Errorf("metadata.pages: got %q want 2", got.Metadata["pages"])
	}
}

func TestOfficeDocumentParser_WordPagesOutOfOrder(t *testing.T) {
	// AX tree may surface pages out of order (e.g. user scrolled to
	// middle and AppKit emitted that subtree first). Output must still
	// be page-ordered.
	window := &AXNode{Role: "AXWindow", Title: "Doc - Word", Children: []*AXNode{
		{Role: "AXScrollArea", Children: []*AXNode{
			{Role: "AXTextArea", Value: "third", Description: "页面 3 内容"},
			{Role: "AXTextArea", Value: "first", Description: "页面 1 内容"},
			{Role: "AXTextArea", Value: "second", Description: "页面 2 内容"},
		}},
	}}
	p := &officeDocumentParser{}
	got := p.ParseTree(window, "com.microsoft.Word", "Doc - Word", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	idx1 := strings.Index(got.Body, "first")
	idx2 := strings.Index(got.Body, "second")
	idx3 := strings.Index(got.Body, "third")
	if !(idx1 < idx2 && idx2 < idx3) {
		t.Errorf("pages not sorted by index: first=%d second=%d third=%d", idx1, idx2, idx3)
	}
}

func TestOfficeDocumentParser_EmptyTreeReturnsNil(t *testing.T) {
	tree := &AXNode{Role: "AXWindow", Title: "Empty - Word"}
	p := &officeDocumentParser{}
	if got := p.ParseTree(tree, "com.microsoft.Word", "Empty - Word", ""); got != nil {
		t.Errorf("expected nil for empty tree, got %+v", got)
	}
}
