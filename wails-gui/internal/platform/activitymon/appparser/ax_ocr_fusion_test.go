package appparser

import (
	"strings"
	"testing"
)

func TestIsPartialAX(t *testing.T) {
	cases := []struct {
		name string
		in   *ParsedContent
		want bool
	}{
		{"nil_returns_false", nil, false},
		{"empty_metadata_returns_false", &ParsedContent{}, false},
		{"full_grid_returns_false", &ParsedContent{
			Metadata: map[string]string{"axCoverage": AXCoverageFullGrid},
		}, false},
		{"first_column_only_returns_true", &ParsedContent{
			Metadata: map[string]string{"axCoverage": AXCoverageFirstColumnOnly},
		}, true},
		{"outline_only_returns_true", &ParsedContent{
			Metadata: map[string]string{"axCoverage": AXCoverageOutlineOnly},
		}, true},
		{"metadata_only_returns_true", &ParsedContent{
			Metadata: map[string]string{"axCoverage": AXCoverageMetadataOnly},
		}, true},
		{"chat_list_only_returns_true", &ParsedContent{
			Metadata: map[string]string{"axCoverage": AXCoverageChatListOnly},
		}, true},
		{"unknown_coverage_returns_false", &ParsedContent{
			Metadata: map[string]string{"axCoverage": "novel_value"},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsPartialAX(c.in); got != c.want {
				t.Errorf("IsPartialAX = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeWithOCR_EmptyOCRReturnsBodyUnchanged(t *testing.T) {
	p := &ParsedContent{
		Body:     "headers\n2026-05-01",
		Metadata: map[string]string{"axCoverage": AXCoverageFirstColumnOnly},
	}
	for _, ocr := range []string{"", "   ", "\n\n\t"} {
		if got := MergeWithOCR(p, ocr); got != p.Body {
			t.Errorf("ocr=%q: got %q, want body unchanged", ocr, got)
		}
	}
}

func TestMergeWithOCR_NilParsedWithOCR(t *testing.T) {
	got := MergeWithOCR(nil, "some screen text")
	if !strings.HasPrefix(got, "[OCR: visible window content]\n") {
		t.Errorf("missing generic OCR header for nil parsed: %q", got)
	}
	if !strings.Contains(got, "some screen text") {
		t.Errorf("missing OCR body: %q", got)
	}
}

func TestMergeWithOCR_OutlineOnlyUsesSlideLabels(t *testing.T) {
	p := &ParsedContent{
		Body:     "1. Cover\n2. Q4 review  [active]",
		Metadata: map[string]string{"axCoverage": AXCoverageOutlineOnly},
	}
	got := MergeWithOCR(p, "Revenue grew 32%\nP0 incidents: 0")

	if !strings.Contains(got, "[AX: slide outline]") {
		t.Errorf("missing slide-outline AX header: %q", got)
	}
	if !strings.Contains(got, "[OCR: visible window content]") {
		t.Errorf("missing full-window OCR header: %q", got)
	}
	if !strings.Contains(got, "Q4 review  [active]") {
		t.Errorf("AX body dropped: %q", got)
	}
	if !strings.Contains(got, "Revenue grew 32%") {
		t.Errorf("OCR body dropped: %q", got)
	}
	// AX must appear before OCR — the LLM should read the trustworthy
	// signal first.
	if strings.Index(got, "[AX:") > strings.Index(got, "[OCR:") {
		t.Errorf("AX must precede OCR section: %q", got)
	}
}

func TestMergeWithOCR_FirstColumnUsesCellLabels(t *testing.T) {
	p := &ParsedContent{
		Body:     "date\tregion\nMay 1\t",
		Metadata: map[string]string{"axCoverage": AXCoverageFirstColumnOnly},
	}
	got := MergeWithOCR(p, "us-west\teast\n12\t34")

	if !strings.Contains(got, "[AX: headers + first column]") {
		t.Errorf("missing first-column AX header: %q", got)
	}
	if !strings.Contains(got, "[OCR: visible window content]") {
		t.Errorf("missing full-window OCR header: %q", got)
	}
}

func TestMergeWithOCR_EmptyBodyButHasParsedKeepsOCROnly(t *testing.T) {
	// A parser may return non-nil ParsedContent (e.g. ContentType set,
	// metadata populated) but with an empty Body — the merge should
	// still surface the OCR text rather than collapsing to "".
	p := &ParsedContent{
		ContentType: "spreadsheet",
		Metadata:    map[string]string{"axCoverage": AXCoverageFirstColumnOnly},
	}
	got := MergeWithOCR(p, "us-west\t12")
	if !strings.HasPrefix(got, "[OCR: visible window content]\n") {
		t.Errorf("missing OCR header when body empty: %q", got)
	}
}

func TestMergeWithOCR_ChatListOnlyUsesChatListAXHeader(t *testing.T) {
	p := &ParsedContent{
		Body:     "Currently in: Yansu-Team\n\nRecent chats:\n- A (4月27日): hi",
		Metadata: map[string]string{"axCoverage": AXCoverageChatListOnly},
	}
	got := MergeWithOCR(p, "Duration: 2m 47s\n@YanSu hello")

	if !strings.Contains(got, "[AX: chat list + open channel]") {
		t.Errorf("missing chat-list AX header: %q", got)
	}
	if !strings.Contains(got, "[OCR: visible window content]") {
		t.Errorf("missing full-window OCR header: %q", got)
	}
	if !strings.Contains(got, "Currently in: Yansu-Team") {
		t.Errorf("AX body dropped: %q", got)
	}
	if !strings.Contains(got, "Duration: 2m 47s") {
		t.Errorf("OCR body dropped: %q", got)
	}
}

func TestMergeWithOCR_UnknownCoverageFallsBackToGenericLabels(t *testing.T) {
	p := &ParsedContent{Body: "some body"}
	got := MergeWithOCR(p, "ocr text")
	if !strings.Contains(got, "[AX]") {
		t.Errorf("missing generic AX header: %q", got)
	}
	if !strings.Contains(got, "[OCR: visible window content]") {
		t.Errorf("missing generic OCR header: %q", got)
	}
}
