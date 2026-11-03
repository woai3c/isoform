package appparser

import (
	"strconv"
	"strings"
)

// Native macOS spreadsheet apps. Both expose their grid as AXTable
// nodes with AXRow > AXCell children. Excel for Mac and Numbers diverge
// on chrome scaffolding (Excel keeps a ribbon, Numbers a properties
// sidebar) but the data table itself accesses identically.
var officeSpreadsheetBundleIDs = map[string]string{
	"com.microsoft.Excel":     "excel",
	"com.apple.Numbers":       "numbers",
	"com.apple.iWork.Numbers": "numbers",
}

// officeSpreadsheetMaxRows caps the number of rows we materialise per
// snapshot. A real Excel sheet can have 1M+ rows — sampling the visible
// window (which AppKit AX surfaces preferentially) keeps body size
// bounded for snapshots even on power-user spreadsheets.
const officeSpreadsheetMaxRows = 200

// officeSpreadsheetMaxCols caps columns per row for the same reason.
const officeSpreadsheetMaxCols = 64

type officeSpreadsheetParser struct{}

func init() { Register(&officeSpreadsheetParser{}) }

func (p *officeSpreadsheetParser) CanParse(bundleID string) bool {
	_, ok := officeSpreadsheetBundleIDs[bundleID]
	return ok
}

func (p *officeSpreadsheetParser) DescribeAll() []ParserCatalogEntry {
	return []ParserCatalogEntry{
		{
			Name:        "office_spreadsheet_excel",
			Label:       "Microsoft Excel",
			ContentType: "document",
			Bundles:     []string{"com.microsoft.Excel"},
		},
		{
			Name:        "office_spreadsheet_numbers",
			Label:       "Apple Numbers",
			ContentType: "document",
			Bundles: []string{
				"com.apple.Numbers",
				"com.apple.iWork.Numbers",
			},
			// Numbers historically blocked per-cell AXValue, leaving
			// the parser to fall back to first_column_only coverage.
			// macOS 26+ exposes the full AXRow → AXCell grid like
			// Excel; older builds (or sheets with custom formats)
			// still drop us into the partial path. The pipeline
			// fuses OCR in either case via axCoverage detection.
			Notes: "Apple historically blocked per-cell AXValue. macOS 26+ exposes the full grid; older builds (or sheets with custom formats) may report axCoverage=first_column_only — OCR fills the missing cells.",
		},
	}
}

// ParseTree picks the largest AXTable in the window (by row × col
// count), serialises its visible cells as TSV, and packages the result
// as a "spreadsheet" ParsedContent. Body shape:
//
//	header1\theader2\theader3
//	row1col1\trow1col2\trow1col3
//	…
//
// Empty cells render as empty strings. Cells that themselves contain a
// tab or newline are escaped (tab → "    ", newline → " ") so the
// downstream consumer can split rows on \n and columns on \t without a
// dedicated parser.
func (p *officeSpreadsheetParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}
	appName, ok := officeSpreadsheetBundleIDs[bundleID]
	if !ok {
		return nil
	}
	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	table := officeSpreadsheetPickLargestTable(window)
	if table == nil {
		return nil
	}
	body, rows, cols, coverage := officeSpreadsheetRenderTSV(table)
	if strings.TrimSpace(body) == "" {
		return nil
	}

	title := officeStripAppSuffix(windowTitle, appName)
	if title == "" && window != nil {
		title = officeStripAppSuffix(window.Title, appName)
	}

	meta := map[string]string{
		"bundleId":   bundleID,
		"app":        appName,
		"rows":       strconv.Itoa(rows),
		"cols":       strconv.Itoa(cols),
		"axCoverage": coverage,
	}
	if sheet := officeSpreadsheetActiveSheet(window); sheet != "" {
		meta["activeSheet"] = sheet
	}

	return &ParsedContent{
		ContentType: "spreadsheet",
		Title:       title,
		Body:        body,
		Metadata:    meta,
	}
}

// officeSpreadsheetPickLargestTable walks the window and returns the
// AXTable with the largest (rows × cols) product. Excel and Numbers
// embed several smaller AXTables (toolbar swatches, named-range
// dropdowns) that must not outrank the active grid. Column count is
// estimated as max(actual AXCell children per row, AXColumn children
// of the table) so cell-less tables (the legacy Numbers fallback,
// see officeSpreadsheetRenderFirstColumnOnly) still rank correctly
// via their AXColumn metadata.
func officeSpreadsheetPickLargestTable(root *AXNode) *AXNode {
	// Collect every AXTable in the subtree, INCLUDING those nested
	// inside other AXTables. FindAll prunes matched subtrees to avoid
	// double-counting container matches, but Excel wraps its real
	// data grid inside a "sheet grid" AXTable whose cells each embed
	// the actual sub-table — using FindAll there would stop at the
	// wrapper and miss the 28 leaf data tables underneath.
	var tables []*AXNode
	Traverse(root, func(n *AXNode) bool {
		if n.Role == "AXTable" {
			tables = append(tables, n)
		}
		return true
	})
	var best *AXNode
	bestArea := 0
	for _, t := range tables {
		// Skip wrapper tables that contain nested AXTable nodes inside
		// their cells. Excel for Mac surfaces a top-level "网格" /
		// "sheet grid" AXTable whose 8×23 cells each embed the actual
		// data sub-table; selecting it would make cell-text extraction
		// recurse into the nested grid and return the same first-cell
		// value for every outer cell. The genuine data grid is a leaf
		// AXTable with no nested AXTable children.
		if hasNestedAXTable(t) {
			continue
		}
		rows := ChildrenByRole(t, "AXRow")
		if len(rows) == 0 {
			// Some trees nest rows under an AXGroup wrapper; fall back
			// to a full FindAll so we don't undercount.
			rows = FindAll(t, func(n *AXNode) bool { return n.Role == "AXRow" })
		}
		if len(rows) == 0 {
			continue
		}
		maxCols := 0
		for _, r := range rows {
			cells := ChildrenByRole(r, "AXCell")
			if len(cells) == 0 {
				cells = FindAll(r, func(n *AXNode) bool { return n.Role == "AXCell" })
			}
			if len(cells) > maxCols {
				maxCols = len(cells)
			}
		}
		// Cell-less fallback: when AXRow has zero AXCell children
		// (rare, see officeSpreadsheetRenderFirstColumnOnly) the AXRow
		// loop above leaves maxCols at 0. Count AXColumn children of
		// the table so the area estimate isn't zero, otherwise
		// toolbar swatch tables outrank the real grid.
		if maxCols == 0 {
			cols := ChildrenByRole(t, "AXColumn")
			if len(cols) == 0 {
				cols = FindAll(t, func(n *AXNode) bool { return n.Role == "AXColumn" })
			}
			maxCols = len(cols)
		}
		area := len(rows) * maxCols
		if area > bestArea {
			bestArea = area
			best = t
		}
	}
	return best
}

// hasNestedAXTable reports whether `t` contains another AXTable
// anywhere in its descendant subtree. Used by
// officeSpreadsheetPickLargestTable to reject Excel's outer "sheet
// grid" wrapper, which has no real cell content of its own — each
// of its cells embeds a copy of the actual data sub-table.
func hasNestedAXTable(t *AXNode) bool {
	nested := false
	Traverse(t, func(n *AXNode) bool {
		if nested {
			return false
		}
		if n != t && n.Role == "AXTable" {
			nested = true
			return false
		}
		return true
	})
	return nested
}

// officeSpreadsheetRenderTSV serialises the chosen AXTable into a TSV
// body and reports row/col counts plus which AX coverage path was
// taken: "full_grid" when AXRow → AXCell exposes the entire grid
// (Excel and Numbers both fall here), "first_column_only" when AXRow
// has no AXCell children at all and we have to render from
// AXColumn.description + AXRow.description.
//
// The first_column_only path remains as a fallback. Numbers used to
// systematically land here when the AX prime hack in
// tree_walk_app_to_buf was applied unconditionally — toggling
// AXEnhancedUserInterface on Numbers stripped every AXCell off the
// rows and forced the cell-less render. After app_needs_prime gated
// prime to Electron/Chromium apps only, Numbers exposes the full
// AXRow → AXCell tree and hits full_grid like Excel. Keep the
// fallback in case a future macOS / Numbers release regresses again.
//
// Counts are clamped to officeSpreadsheetMaxRows × officeSpreadsheetMaxCols
// so metadata.rows / metadata.cols reflect what was actually emitted.
func officeSpreadsheetRenderTSV(table *AXNode) (body string, rowCount, colCount int, coverage string) {
	rows := ChildrenByRole(table, "AXRow")
	if len(rows) == 0 {
		rows = FindAll(table, func(n *AXNode) bool { return n.Role == "AXRow" })
	}
	if len(rows) == 0 {
		return "", 0, 0, ""
	}

	// Probe one row for AXCell children. Empty across all rows means
	// we're in cell-less mode (see officeSpreadsheetRenderFirstColumnOnly)
	// and render via AXColumn descriptions + AXRow descriptions.
	// Otherwise fall through to the full-grid path — current Numbers
	// and Pages both expose AXCell children once app_needs_prime gates
	// the Electron prime workaround away from native AppKit.
	hasCells := false
	for _, r := range rows {
		if len(ChildrenByRole(r, "AXCell")) > 0 ||
			FindFirst(r, func(n *AXNode) bool { return n.Role == "AXCell" }) != nil {
			hasCells = true
			break
		}
	}
	if !hasCells {
		body, rowCount, colCount = officeSpreadsheetRenderFirstColumnOnly(table, rows)
		return body, rowCount, colCount, "first_column_only"
	}

	if len(rows) > officeSpreadsheetMaxRows {
		rows = rows[:officeSpreadsheetMaxRows]
	}
	var sb strings.Builder
	maxCols := 0
	for ri, r := range rows {
		cells := ChildrenByRole(r, "AXCell")
		if len(cells) == 0 {
			cells = FindAll(r, func(n *AXNode) bool { return n.Role == "AXCell" })
		}
		if len(cells) > officeSpreadsheetMaxCols {
			cells = cells[:officeSpreadsheetMaxCols]
		}
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
		for ci, c := range cells {
			if ci > 0 {
				sb.WriteByte('\t')
			}
			sb.WriteString(officeSpreadsheetCellText(c))
		}
		if ri < len(rows)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String(), len(rows), maxCols, "full_grid"
}

// officeSpreadsheetRenderFirstColumnOnly is the fallback for tables
// whose AXRow children expose no AXCell at all. Only two AX hooks
// remain in this state:
//
//   - AXColumn.description = column header label (e.g. "date", "region")
//   - AXRow.description    = the FIRST cell value of that row
//
// We render a TSV whose first line is the column header strip and
// whose subsequent lines are the per-row first-cell values, padded
// with empty cells to keep the column count consistent. Consumers
// can detect the partial coverage via metadata.axCoverage.
//
// This path is rare in practice. Numbers and Pages used to land
// here permanently — the AX prime hack in tree_walk_app_to_buf
// (originally meant for Chromium WebView) stripped every AXCell
// off their AXRow children. Once app_needs_prime gated prime to
// Electron-only, both apps moved to the full-grid path. Keep this
// renderer as a safety net for future macOS / Numbers regressions.
func officeSpreadsheetRenderFirstColumnOnly(table *AXNode, rows []*AXNode) (string, int, int) {
	cols := ChildrenByRole(table, "AXColumn")
	if len(cols) == 0 {
		cols = FindAll(table, func(n *AXNode) bool { return n.Role == "AXColumn" })
	}
	if len(cols) > officeSpreadsheetMaxCols {
		cols = cols[:officeSpreadsheetMaxCols]
	}
	if len(rows) > officeSpreadsheetMaxRows {
		rows = rows[:officeSpreadsheetMaxRows]
	}

	var sb strings.Builder
	if len(cols) > 0 {
		for i, c := range cols {
			if i > 0 {
				sb.WriteByte('\t')
			}
			sb.WriteString(officeSpreadsheetEscape(strings.TrimSpace(c.Description)))
		}
		if len(rows) > 0 {
			sb.WriteByte('\n')
		}
	}
	for ri, r := range rows {
		sb.WriteString(officeSpreadsheetEscape(strings.TrimSpace(r.Description)))
		// Pad with empty columns so each row reports the full width;
		// downstream consumers split on \t and would otherwise see a
		// ragged grid.
		for i := 1; i < len(cols); i++ {
			sb.WriteByte('\t')
		}
		if ri < len(rows)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String(), len(rows), len(cols)
}

// officeSpreadsheetCellText returns a cell's display text. AppKit
// surfaces cell content in different places depending on the host:
//
//   - Excel for Mac wraps the visible value inside the cell as
//     `AXGroup{Value="作业编号"} > AXTextArea{Value="作业编号",
//     Description="B4，纯色填充 ..."}`. The cell itself carries no
//     Value/Description. We must walk into the AXGroup / AXTextArea
//     to find the real text, and crucially must NOT fall back to
//     AXTextArea.Description because that field is cell-coordinate +
//     style metadata ("B4, solid fill, blue-grey"), not user content.
//   - Numbers 14 puts the value on the cell's AXTitle as a leaf — no
//     children, Value/Description are nil. Header cells append a
//     locale suffix ("2026-05-01, 行表头单元格" / "Sales, row header
//     cell") which we strip back to the bare value.
//   - Older AppKit grid apps put the value on the cell directly.
//   - Some apps still surface AXStaticText leaves (legacy path).
//
// Strategy: prefer the cell's own Value, then walk for the first
// AXTextArea / AXGroup / AXStaticText carrying a non-empty Value
// (AXMenuItem filter buttons are skipped), then fall back to the
// cell's own Title (Numbers path) and finally Description (legacy).
// Tabs and newlines inside the value are softened so TSV rendering
// stays line-/column-safe.
func officeSpreadsheetCellText(cell *AXNode) string {
	if cell == nil {
		return ""
	}
	if v := strings.TrimSpace(cell.Value); v != "" {
		return officeSpreadsheetEscape(v)
	}

	var found string
	Traverse(cell, func(n *AXNode) bool {
		if found != "" {
			return false
		}
		if n == cell {
			return true
		}
		switch n.Role {
		case "AXMenuItem", "AXButton":
			// Filter chevrons and inline controls hold UI labels
			// ("未应用任何筛选器"), not cell content. Skip the subtree.
			return false
		case "AXTextArea", "AXGroup", "AXStaticText":
			if v := strings.TrimSpace(n.Value); v != "" {
				found = v
				return false
			}
		}
		return true
	})
	// Numbers fallback: cell.Title carries the displayed value when
	// the cell has no children and Value/Description are nil. The
	// header-cell variant appends a locale suffix that we strip.
	if found == "" {
		found = officeSpreadsheetStripCellTitleSuffix(strings.TrimSpace(cell.Title))
	}
	// Final defensive fallback: cell.Description rarely carries the
	// real value but some legacy AppKit grids do route through it.
	// (Excel for Mac does NOT — see comment above.)
	if found == "" {
		found = strings.TrimSpace(cell.Description)
	}
	return officeSpreadsheetEscape(found)
}

// officeSpreadsheetStripCellTitleSuffix removes the localized "row
// header cell" / "column header cell" annotation Numbers appends to
// a header cell's AXTitle. Loops because the corner cell of a grid
// is BOTH a row header and a column header, producing a double
// suffix: "date, 列表头单元格, 行表头单元格" → "date".
// Non-header cells return unchanged.
func officeSpreadsheetStripCellTitleSuffix(title string) string {
	// Locale suffixes observed in the wild — extend as new locales
	// surface. Match against the trailing ", <suffix>" only.
	suffixes := []string{
		", 行表头单元格", ", 列表头单元格", // zh-CN
		", row header cell", ", column header cell", // en-US
	}
	for {
		s := strings.TrimSpace(title)
		if s == "" {
			return ""
		}
		stripped := false
		for _, suf := range suffixes {
			if strings.HasSuffix(s, suf) {
				title = strings.TrimSuffix(s, suf)
				stripped = true
				break
			}
		}
		if !stripped {
			return s
		}
	}
}

// officeSpreadsheetEscape replaces TSV-breaking characters inside a
// cell value: tab → four spaces, CR/LF → single space.
func officeSpreadsheetEscape(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer("\t", "    ", "\r\n", " ", "\n", " ", "\r", " ")
	return r.Replace(s)
}

// officeSpreadsheetActiveSheet returns the name of the currently
// selected sheet tab when one can be derived from the AX tree.
//
// Excel and Numbers both expose sheet tabs as AXRadioButton (Numbers)
// or AXButton (Excel) children of an AXTabGroup at the bottom of the
// window. The selected tab has a non-zero AXValue when accessible —
// since our AXNode serialisation only carries Value as a string, we
// fall back to "the first tab whose Title looks like a sheet name".
// Best effort: returns "" when nothing matches.
func officeSpreadsheetActiveSheet(window *AXNode) string {
	tabs := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXTabGroup" })
	if tabs == nil {
		return ""
	}
	// Prefer a tab with Value == "1" (AppKit's accessible-selected
	// signal); fall back to the first labelled tab.
	for _, t := range tabs.Children {
		if t == nil {
			continue
		}
		switch t.Role {
		case "AXRadioButton", "AXButton":
		default:
			continue
		}
		if t.Value == "1" && strings.TrimSpace(t.Title) != "" {
			return strings.TrimSpace(t.Title)
		}
	}
	for _, t := range tabs.Children {
		if t == nil {
			continue
		}
		if strings.TrimSpace(t.Title) != "" {
			return strings.TrimSpace(t.Title)
		}
	}
	return ""
}
