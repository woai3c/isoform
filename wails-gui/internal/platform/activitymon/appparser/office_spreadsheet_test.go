package appparser

import (
	"strings"
	"testing"
)

func sheetCell(text string) *AXNode {
	return &AXNode{Role: "AXCell", Value: text}
}

func sheetRow(cells ...*AXNode) *AXNode {
	return &AXNode{Role: "AXRow", Children: cells}
}

func sheetTable(rows ...*AXNode) *AXNode {
	return &AXNode{Role: "AXTable", Children: rows}
}

func sheetWindow(title string, table *AXNode, sheetTabs ...string) *AXNode {
	children := []*AXNode{table}
	if len(sheetTabs) > 0 {
		var tabs []*AXNode
		for _, name := range sheetTabs {
			tabs = append(tabs, &AXNode{Role: "AXRadioButton", Title: name})
		}
		children = append(children, &AXNode{Role: "AXTabGroup", Children: tabs})
	}
	return &AXNode{Role: "AXWindow", Title: title, Children: children}
}

func TestOfficeSpreadsheetParser_CanParse(t *testing.T) {
	p := &officeSpreadsheetParser{}
	for _, b := range []string{"com.microsoft.Excel", "com.apple.Numbers", "com.apple.iWork.Numbers"} {
		if !p.CanParse(b) {
			t.Errorf("CanParse(%q) = false, want true", b)
		}
	}
	if p.CanParse("com.microsoft.Word") {
		t.Errorf("Word must not match the spreadsheet parser")
	}
}

func TestOfficeSpreadsheetParser_TSVRendering(t *testing.T) {
	table := sheetTable(
		sheetRow(sheetCell("Name"), sheetCell("Quantity"), sheetCell("Price")),
		sheetRow(sheetCell("Apple"), sheetCell("3"), sheetCell("1.50")),
		sheetRow(sheetCell("Banana"), sheetCell("12"), sheetCell("0.20")),
	)
	tree := sheetWindow("Budget.xlsx - Excel", table)

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.microsoft.Excel", "Budget.xlsx - Excel", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.ContentType != "spreadsheet" {
		t.Errorf("contentType: got %q want spreadsheet", got.ContentType)
	}
	if got.Title != "Budget.xlsx" {
		t.Errorf("title: got %q want %q", got.Title, "Budget.xlsx")
	}
	wantBody := "Name\tQuantity\tPrice\nApple\t3\t1.50\nBanana\t12\t0.20"
	if got.Body != wantBody {
		t.Errorf("body mismatch:\n got %q\nwant %q", got.Body, wantBody)
	}
	if got.Metadata["rows"] != "3" || got.Metadata["cols"] != "3" {
		t.Errorf("rows/cols metadata: got rows=%q cols=%q want 3/3",
			got.Metadata["rows"], got.Metadata["cols"])
	}
	if got.Metadata["axCoverage"] != "full_grid" {
		t.Errorf("axCoverage: got %q want full_grid (Excel-style row>cell shape)",
			got.Metadata["axCoverage"])
	}
}

func TestOfficeSpreadsheetParser_PicksLargestTable(t *testing.T) {
	// A toolbar swatch picker also surfaces as AXTable with a few
	// AXRow/AXCell children. The parser must not let it outrank the
	// real grid.
	swatches := sheetTable(sheetRow(sheetCell("#fff")))
	real := sheetTable(
		sheetRow(sheetCell("A"), sheetCell("B")),
		sheetRow(sheetCell("1"), sheetCell("2")),
		sheetRow(sheetCell("3"), sheetCell("4")),
	)
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "Sheet1 - Excel",
		Children: []*AXNode{
			{Role: "AXToolbar", Children: []*AXNode{swatches}},
			real,
		},
	}

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.microsoft.Excel", "Sheet1 - Excel", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if !strings.Contains(got.Body, "A\tB") {
		t.Errorf("body should pick the larger grid, got %q", got.Body)
	}
	if strings.Contains(got.Body, "#fff") {
		t.Errorf("body must not include swatch table cells, got %q", got.Body)
	}
}

func TestOfficeSpreadsheetParser_CellEscaping(t *testing.T) {
	// Tab and newline inside a cell must be softened so downstream
	// consumers can split rows on \n and columns on \t safely.
	table := sheetTable(
		sheetRow(sheetCell("hello\tworld"), sheetCell("line1\nline2")),
	)
	tree := sheetWindow("Edge - Excel", table)

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.microsoft.Excel", "Edge - Excel", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if strings.Contains(got.Body, "hello\tworld") {
		t.Errorf("tab inside cell must be escaped, got %q", got.Body)
	}
	if strings.Contains(got.Body, "line1\nline2") {
		t.Errorf("newline inside cell must be escaped, got %q", got.Body)
	}
}

func TestOfficeSpreadsheetParser_ActiveSheetFromTabGroup(t *testing.T) {
	table := sheetTable(
		sheetRow(sheetCell("a"), sheetCell("b")),
	)
	tree := sheetWindow("Multi - Numbers", table, "Summary", "Q1", "Q2")
	// Mark "Q2" as the active tab via Value="1" so we exercise the
	// AppKit "selected" proxy.
	tabGroup := tree.Children[1]
	tabGroup.Children[2].Value = "1"

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Numbers", "Multi — Numbers", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Metadata["activeSheet"] != "Q2" {
		t.Errorf("activeSheet: got %q want Q2", got.Metadata["activeSheet"])
	}
}

// numbersRow builds an AXRow whose only payload is description (the
// first cell value) — the cell-less shape that exercises the
// first_column_only fallback. Real Numbers 14 with the AX prime hack
// gated off exposes AXCell children directly (see
// TestOfficeSpreadsheetParser_NumbersFullGridFromCellTitle); this
// shape covers the historical/regression path.
func numbersRow(idx, firstCell string) *AXNode {
	return &AXNode{
		Role:        "AXRow",
		Subrole:     "AXTableRow",
		Title:       idx,
		Description: firstCell,
	}
}

// numbersColumn builds an AXColumn with a header label.
func numbersColumn(letter, header string) *AXNode {
	return &AXNode{
		Role:        "AXColumn",
		Title:       letter,
		Description: header,
	}
}

// TestOfficeSpreadsheetParser_NumbersFirstColumnOnly covers the
// fallback path: AXRow has no AXCell children at all, so cell data
// has to come from AXRow.description (first column) and
// AXColumn.description (headers). Real Numbers 14 used to land here
// because the AX prime hack stripped the cell tree; current builds
// with prime gated off hit the full_grid path. This test guards the
// fallback so a future Numbers / macOS regression is still rendered.
func TestOfficeSpreadsheetParser_NumbersFirstColumnOnly(t *testing.T) {
	table := &AXNode{
		Role:  "AXTable",
		Title: "sample_sales_data, 11 行, 7 列",
		Children: []*AXNode{
			numbersRow("1", "date"),
			numbersRow("2", "2026-05-01"),
			numbersRow("3", "2026-05-02"),
			numbersRow("4", "2026-05-03"),
			numbersColumn("A", "date"),
			numbersColumn("B", "region"),
			numbersColumn("C", "product"),
			numbersColumn("D", "channel"),
			numbersColumn("E", "units"),
			numbersColumn("F", "unit_price"),
			numbersColumn("G", "revenue"),
		},
	}
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "sample_sales_data",
		Children: []*AXNode{
			{Role: "AXSplitGroup", Children: []*AXNode{
				{Role: "AXScrollArea", Children: []*AXNode{
					{Role: "AXLayoutArea", Description: "工作表 1, 白色背景", Children: []*AXNode{table}},
				}},
			}},
		},
	}

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Numbers", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.ContentType != "spreadsheet" {
		t.Errorf("contentType: got %q want spreadsheet", got.ContentType)
	}
	if got.Title != "sample_sales_data" {
		t.Errorf("title: got %q want %q", got.Title, "sample_sales_data")
	}
	if got.Metadata["axCoverage"] != "first_column_only" {
		t.Errorf("axCoverage: got %q want first_column_only",
			got.Metadata["axCoverage"])
	}
	if got.Metadata["rows"] != "4" || got.Metadata["cols"] != "7" {
		t.Errorf("rows/cols: got rows=%q cols=%q want 4/7",
			got.Metadata["rows"], got.Metadata["cols"])
	}

	wantHeader := "date\tregion\tproduct\tchannel\tunits\tunit_price\trevenue"
	if !strings.HasPrefix(got.Body, wantHeader+"\n") {
		t.Errorf("body must start with column headers + newline:\n got %q\nwant prefix %q",
			got.Body, wantHeader)
	}
	for _, want := range []string{"2026-05-01", "2026-05-02", "2026-05-03"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing first-column value %q: %q", want, got.Body)
		}
	}
	// Each row line should carry padding tabs for the absent 6 columns
	// so the grid stays rectangular for downstream split-on-tab parsing.
	for _, line := range strings.Split(got.Body, "\n") {
		if cols := strings.Count(line, "\t"); cols != 6 {
			t.Errorf("line %q has %d tabs, want 6 (rectangular grid)", line, cols)
		}
	}
}

// TestOfficeSpreadsheetParser_NumbersBeatsToolbarSwatch covers the
// pickLargestTable cell-less branch: a cell-less data table (rows
// without AXCell children but rich AXColumn metadata) must outrank
// a tiny Excel-style toolbar swatch table that has AXCell children
// but only one row/cell. Same fallback shape as
// TestOfficeSpreadsheetParser_NumbersFirstColumnOnly.
func TestOfficeSpreadsheetParser_NumbersBeatsToolbarSwatch(t *testing.T) {
	swatch := sheetTable(sheetRow(sheetCell("#fff")))
	dataTable := &AXNode{
		Role:  "AXTable",
		Title: "data, 3 行, 4 列",
		Children: []*AXNode{
			numbersRow("1", "h"),
			numbersRow("2", "v1"),
			numbersRow("3", "v2"),
			numbersColumn("A", "h"),
			numbersColumn("B", "x"),
			numbersColumn("C", "y"),
			numbersColumn("D", "z"),
		},
	}
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "Doc",
		Children: []*AXNode{
			{Role: "AXToolbar", Children: []*AXNode{swatch}},
			dataTable,
		},
	}
	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Numbers", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Metadata["axCoverage"] != "first_column_only" {
		t.Errorf("expected first_column_only path, got %q", got.Metadata["axCoverage"])
	}
	if strings.Contains(got.Body, "#fff") {
		t.Errorf("toolbar swatch leaked into body: %q", got.Body)
	}
	if !strings.Contains(got.Body, "h\tx\ty\tz") {
		t.Errorf("body missing data table headers: %q", got.Body)
	}
}

// TestOfficeSpreadsheetParser_NumbersFullGridFromCellTitle covers the
// Numbers 14 path: AXRow exposes AXCell children but each cell has
// no Value / Description / children — the displayed value lives in
// AXTitle. Header cells append a locale suffix the parser must strip.
//
// This is the "happy path" that activates once the AX prime fix lets
// the cell tree show through (see app_needs_prime in
// internal/platform/activitymon/axtree_tree_darwin.go). Before the
// prime fix, Numbers' AXRow had zero AXCell children and the parser
// fell through to first_column_only.
func TestOfficeSpreadsheetParser_NumbersFullGridFromCellTitle(t *testing.T) {
	// Build a Numbers-shaped cell whose value is in Title only.
	titleCell := func(title string) *AXNode {
		return &AXNode{Role: "AXCell", Title: title}
	}
	table := &AXNode{
		Role:  "AXTable",
		Title: "sample, 3 行, 3 列",
		Children: []*AXNode{
			// Header row — first cell has the row-header suffix.
			{Role: "AXRow", Subrole: "AXTableRow", Children: []*AXNode{
				titleCell("date, 行表头单元格"),
				titleCell("region"),
				titleCell("units"),
			}},
			{Role: "AXRow", Subrole: "AXTableRow", Children: []*AXNode{
				titleCell("2026-05-01, 行表头单元格"),
				titleCell("North"),
				titleCell("18"),
			}},
			{Role: "AXRow", Subrole: "AXTableRow", Children: []*AXNode{
				titleCell("2026-05-02, 行表头单元格"),
				titleCell("South"),
				titleCell("9"),
			}},
		},
	}
	tree := sheetWindow("sample - Numbers", table)

	p := &officeSpreadsheetParser{}
	got := p.ParseTree(tree, "com.apple.Numbers", "sample - Numbers", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Metadata["axCoverage"] != "full_grid" {
		t.Errorf("axCoverage: got %q want full_grid", got.Metadata["axCoverage"])
	}
	wantBody := "date\tregion\tunits\n2026-05-01\tNorth\t18\n2026-05-02\tSouth\t9"
	if got.Body != wantBody {
		t.Errorf("body mismatch:\n got %q\nwant %q", got.Body, wantBody)
	}
}

func TestOfficeSpreadsheetStripCellTitleSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-05-01, 行表头单元格", "2026-05-01"},
		{"date, 列表头单元格", "date"},
		{"Sales, row header cell", "Sales"},
		{"Region, column header cell", "Region"},
		{"North", "North"}, // no suffix → unchanged
		{"", ""},
		// Comma in real data with no header suffix — must not be stripped.
		{"Foo, Inc.", "Foo, Inc."},
		// Corner cell: both row header AND column header — Numbers
		// appends BOTH suffixes. Must strip both in any order.
		{"date, 列表头单元格, 行表头单元格", "date"},
		{"date, 行表头单元格, 列表头单元格", "date"},
		{"Q1, column header cell, row header cell", "Q1"},
	}
	for _, c := range cases {
		if got := officeSpreadsheetStripCellTitleSuffix(c.in); got != c.want {
			t.Errorf("strip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
