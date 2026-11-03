package appparser

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// asanaBundleIDs covers Asana's macOS desktop builds. Verified live
// against the .dmg distributed from asana.com/download as of 2026:
// the wrapper ships under `com.electron.asana`. Add any new identifiers
// here when a future build changes them.
var asanaBundleIDs = map[string]bool{
	"com.electron.asana": true,
}

const asanaAppName = "asana"

// asanaMaxRows caps the task list output. Asana's portfolio / project
// views can render hundreds of rows; the downstream prompt only needs
// the recent window.
const asanaMaxRows = 80

// asanaTaskItemRE pulls one task out of a MyTasksWidget AXGroup
// description. Asana packs the entire visible task list into a single
// screen-reader hint of the form:
//
//	"標記為完成 開啟任務對話框「<name>」 <project> <date range> 標記為完成 開啟任務對話框「<name>」 …"
//
// Or the English-locale equivalent with ASCII straight quotes and the
// "Mark complete / Open task dialog" verb phrases. We anchor on the
// quoted name and capture trailing context up to the next verb phrase
// (or end of string) so the project + date stay attached to the right
// row even when the verb phrase locale doesn't match what we expect.
//
// The character class `[「"\x{201C}]` accepts the Chinese full-width
// open quote, the ASCII straight quote, and the en-US curly open quote
// — all three appear depending on locale + which build of Asana renders
// the page. The closing class mirrors that for the close quotes.
var asanaTaskItemRE = regexp.MustCompile(
	`[「"\x{201C}]([^」"\x{201D}]+)[」"\x{201D}]\s+(.+?)(?:\s+(?:標記為完成|标记为完成|Mark complete)\b|$)`,
)

// asanaWidgetClassPrefix matches any class on Asana's home-dashboard
// widgets — observed values include `MyTasksWidgetTaskList`,
// `MyTasksWidgetContent-upcomingTasks`, `MyTasksWidgetContent-overdue`,
// `MyTasksWidgetContent-completed`. The prefix is stable enough to
// anchor on; the suffix changes per tab.
const asanaWidgetClassPrefix = "MyTasksWidget"

type asanaParser struct{}

func init() {
	Register(&asanaParser{})
}

func (p *asanaParser) CanParse(bundleID string) bool {
	return asanaBundleIDs[bundleID]
}

func (p *asanaParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        asanaAppName,
		Label:       "Asana",
		ContentType: "task_list",
		Bundles:     []string{"com.electron.asana"},
		Notes: "Task content is hidden from macOS AX on every list view we've probed " +
			"(home dashboard, My Tasks, project detail). AX surfaces window title + " +
			"column hints + occasionally project name and due date. OCR fills in " +
			"Latin / numeric content (project names, dates) reasonably well; Chinese " +
			"task names and section headers stay unreliable due to Vision's " +
			"recognition floor on small CJK in dark-mode UIs — toggle the 2x upscale " +
			"in the probe panel to verify on your own content.",
	}
}

// ParseTree extracts the currently visible Asana surface. Asana's
// macOS app is an Electron wrapper around the web client, so the AX
// tree mirrors the React DOM.
//
// Two extraction paths, tried in order:
//
//  1. Home dashboard / widget view: Asana packs each widget's visible
//     tasks into a single AXGroup whose `description` is a screen-
//     reader hint listing every row's name + project + date. We anchor
//     on the `MyTasksWidget` class prefix to find these containers and
//     parse the hint via asanaTaskItemRE.
//
//  2. Project / list view (fallback): the active task pane is an
//     AXTable / AXList / AXOutline whose direct children are AXRow
//     wrappers per task. We pick the container with the most row-
//     shaped children carrying non-empty text and join their static
//     texts as tab-separated rows.
//
// When neither path finds tasks (settings page, search results, empty
// project) we degrade to a flat document made of the window's static
// texts so the probe panel still has something to display.
func (p *asanaParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}
	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}
	title := asanaResolveTitle(windowTitle)
	meta := map[string]string{
		"app":      asanaAppName,
		"bundleId": bundleID,
	}

	// Path 1: home-dashboard widgets.
	if widget := asanaExtractWidgetTasks(window); len(widget.rows) > 0 {
		rows := widget.rows
		if len(rows) > asanaMaxRows {
			rows = rows[:asanaMaxRows]
		}
		// Per-row layout exposes project + date but hides the task
		// name from the AX tree. Flag partial coverage so the
		// triager / quick-chat-context route through OCR fusion
		// to recover the names.
		if !widget.namesPresent {
			meta["axCoverage"] = AXCoverageMetadataOnly
		}
		return &ParsedContent{
			ContentType: "task_list",
			Title:       title,
			Body:        strings.Join(rows, "\n"),
			Metadata:    meta,
		}
	}

	// Path 2: project / list view with AXRow children.
	if container := asanaFindTaskList(window); container != nil {
		if rows := asanaCollectRows(container); len(rows) > 0 {
			return &ParsedContent{
				ContentType: "task_list",
				Title:       title,
				Body:        strings.Join(rows, "\n"),
				Metadata:    meta,
			}
		}
	}

	// Fallback: Asana hides task content from macOS AX on every list
	// view we've probed (home dashboard, "My Tasks" list, project
	// detail). Task names, dates, even section headers ("近期指派" /
	// "今天執行") never make it into the tree — only window chrome and
	// occasionally column headers do. Rather than emitting a fake
	// "document" of sidebar/tab text, flag axCoverage=metadata_only
	// so the activity triager routes through OCR fusion (the
	// only reliable way to recover task content).
	//
	// Body still carries what we have: the window title (which Asana
	// keeps in sync with the active view) and the column header hint
	// when an AXTable is present. Useful for "user is on Asana
	// looking at view X" reasoning without OCR.
	meta["axCoverage"] = AXCoverageMetadataOnly
	body := title
	if columns := asanaExtractTableColumns(window); columns != "" {
		if body != "" {
			body += "\nColumns: " + columns
		} else {
			body = "Columns: " + columns
		}
	}
	if body == "" {
		return nil
	}
	return &ParsedContent{
		ContentType: "task_list",
		Title:       title,
		Body:        body,
		Metadata:    meta,
	}
}

// asanaColumnHintPrefixRE matches the start of an Asana sort/options
// hint phrase that follows every column name in the StandardHeaderStructure
// row title. Asana packs the title as
// "<col1> <hint phrase 1> <col2> <hint phrase 2> ..." where the hint
// phrases are localised but always begin with one of these verbs:
//
//	zh-TW: 顯示任務名稱的排序選項, 顯示適用於「X」欄的選項
//	zh-CN: 显示...的排序选项, 显示适用于"X"列的选项
//	en:    Show options for X column, Sort ascending by X, Sort descending by X
//
// Splitting on " <verb>" lets us recover the first column name reliably
// even when it contains spaces ("Due date", "Last modified") — the
// previous strings.Fields + every-other-token heuristic broke for those
// because it assumed each column name was a single whitespace-delimited
// token.
//
// We anchor on a leading space so the verb doesn't match column names
// that happen to start with these letters (a hypothetical "Show me" column
// would still parse correctly because its hint follows it, not precedes).
// Latin verbs require a trailing space to avoid matching column names like
// "Showtime"; CJK verbs need no trailing space because CJK glyphs aren't
// space-delimited and the hint starts immediately after the verb.
var asanaColumnHintPrefixRE = regexp.MustCompile(`\s(?:顯示|显示|Show\s|Sort\s)`)

// asanaColumnHintMaxRunes caps the length of the extracted column hint so
// a header whose title doesn't contain any recognised verb (locale we
// haven't seen, sort hint Asana renamed) still produces a bounded label
// instead of dumping the entire screen-reader string.
const asanaColumnHintMaxRunes = 40

// asanaExtractTableColumns pulls the first column header out of an Asana
// list view's table header row. Asana packs every column label plus its
// sort/options affordance into AXRow.title as a single screen-reader
// string of the form "<col1> <sort hint 1> <col2> <sort hint 2> ...".
// The hint phrases are localised and the column names themselves can
// contain spaces in English, so reliably splitting columns from hints
// requires recognising the hint-verb prefix.
//
// Returns the first column's name (e.g. "名稱" or "Task name") — enough
// for the LLM to label the view as "Asana list view sorted by <column>"
// without trying to reconstruct the full column list. The latter has
// proven brittle across locales / sort states.
//
// Returns "" when no header row is found, or when the title is empty.
func asanaExtractTableColumns(window *AXNode) string {
	var header *AXNode
	Traverse(window, func(n *AXNode) bool {
		if n.Role != "AXRow" {
			return true
		}
		if HasClassContaining(n, "StandardHeaderStructure") {
			header = n
			return false
		}
		return true
	})
	if header == nil {
		return ""
	}
	t := strings.TrimSpace(header.Title)
	if t == "" {
		return ""
	}
	if loc := asanaColumnHintPrefixRE.FindStringIndex(t); loc != nil {
		// Everything before the first hint verb is the first column name.
		if first := strings.TrimSpace(t[:loc[0]]); first != "" {
			return first
		}
	}
	// Hint pattern didn't match (unfamiliar locale, Asana renamed the
	// sort hint phrasing, …). Fall back to a rune-bounded prefix so the
	// label stays short and CJK doesn't get split mid-character.
	if utf8.RuneCountInString(t) <= asanaColumnHintMaxRunes {
		return t
	}
	runes := []rune(t)
	return strings.TrimSpace(string(runes[:asanaColumnHintMaxRunes]))
}

func asanaResolveTitle(windowTitle string) string {
	t := strings.TrimSpace(windowTitle)
	if t == "" {
		return ""
	}
	// Strip the dirty marker some builds prepend to indicate an
	// in-progress edit ("● 首頁 - Asana" → "首頁 - Asana").
	t = strings.TrimPrefix(t, "● ")
	// "<project / task> — Asana" — strip the suffix when present.
	if suffix, ok := strings.CutSuffix(t, " — Asana"); ok {
		return strings.TrimSpace(suffix)
	}
	// "<page> - Asana" (the hyphen variant Asana uses on the home page).
	if suffix, ok := strings.CutSuffix(t, " - Asana"); ok {
		return strings.TrimSpace(suffix)
	}
	return t
}

// asanaWidgetExtraction is the typed result of harvesting task rows
// from Asana's home-dashboard widgets. namesPresent is false when the
// per-row layout was found but the AX tree didn't expose any task
// names — caller flags axCoverage=metadata_only so the OCR pass fills
// them in.
type asanaWidgetExtraction struct {
	rows         []string
	namesPresent bool
}

// asanaExtractWidgetTasks finds every AXGroup tagged with a
// MyTasksWidget* class and pulls task rows out of it. Asana renders
// the dashboard in two distinct layouts (live A/B or accessibility-
// mode switch) and we handle both:
//
//  1. ARIA-hint layout: the widget's container AXGroup carries a
//     description string packing every row's name + project + date
//     for screen readers. We parse it with asanaTaskItemRE.
//
//  2. Per-row layout: each row is its own AXGroup tagged with
//     MyTasksWidgetTaskRow-task, with the task name absent from the
//     AX tree (rendered visually only) but project + date exposed on
//     child AXLink / AXButton nodes. We surface what we have and
//     return namesPresent=false so the caller can route the missing
//     names through OCR fusion.
func asanaExtractWidgetTasks(window *AXNode) asanaWidgetExtraction {
	out := asanaWidgetExtraction{}
	seen := map[string]bool{}

	addRow := func(line string) {
		if seen[line] {
			return
		}
		seen[line] = true
		out.rows = append(out.rows, line)
	}

	// Pass 1: ARIA-hint layout — pull rows out of widget descriptions.
	Traverse(window, func(n *AXNode) bool {
		if !HasClassContaining(n, asanaWidgetClassPrefix) {
			return true
		}
		desc := strings.TrimSpace(n.Description)
		if desc == "" {
			return true
		}
		for _, m := range asanaTaskItemRE.FindAllStringSubmatch(desc, -1) {
			name := strings.TrimSpace(m[1])
			if name == "" {
				continue
			}
			line := name
			if ctx := strings.TrimSpace(m[2]); ctx != "" {
				line += "\t" + ctx
			}
			addRow(line)
			out.namesPresent = true
		}
		return true
	})
	if len(out.rows) > 0 {
		return out
	}

	// Pass 2: per-row layout — harvest project + date from each row's
	// children. Task names are unavailable in this layout; rows are
	// emitted with a placeholder so downstream OCR fusion can fill in
	// the missing piece.
	Traverse(window, func(n *AXNode) bool {
		if !HasClassContaining(n, "MyTasksWidgetTaskRow") {
			return true
		}
		var name, project, date string
		// Task name occasionally leaks into a child static text or
		// the row's own title — try those before giving up.
		if t := strings.TrimSpace(n.Title); t != "" {
			name = t
		}
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			switch c.Role {
			case "AXLink":
				if HasClassContaining(c, "PotPillsContainer") || HasClassContaining(c, "PotTokenizerPill") {
					if d := strings.TrimSpace(c.Description); d != "" {
						project = d
					}
				}
			case "AXButton":
				if HasClassContaining(c, "DueDateContainer") {
					if t := strings.TrimSpace(c.Title); t != "" {
						date = t
					}
				}
			}
		}
		if name == "" {
			// Fall back to the first static text in the subtree — some
			// row variants do expose the name as a deep AXStaticText.
			texts := CollectStaticTexts(n)
			for _, t := range texts {
				t = strings.TrimSpace(t)
				if t != "" {
					name = t
					break
				}
			}
		}
		// Skip empty rows (nothing extracted at all).
		if name == "" && project == "" && date == "" {
			return false
		}
		var b strings.Builder
		if name != "" {
			b.WriteString(name)
			out.namesPresent = true
		} else {
			b.WriteString("(task name not in AX)")
		}
		if project != "" {
			b.WriteString("\t")
			b.WriteString(project)
		}
		if date != "" {
			b.WriteString("\t")
			b.WriteString(date)
		}
		addRow(b.String())
		// Don't recurse into the row — children already consumed.
		return false
	})
	return out
}

// asanaFindTaskList picks the row-shaped container with the most direct
// row children. Used for project-detail / list views where Asana
// renders a proper AXTable / AXList. Scoring direct-child count instead
// of total descendants avoids picking the workspace-wide container that
// wraps the active view plus the navigation sidebar.
//
// Uses FindAll so the search prunes inside matched containers — the
// previous Traverse continued into every row's subtree, AND inside each
// row hasNonEmptyText ran its own nested Traverse, doubling the
// per-node visit cost. FindAll-with-prune also matches intent: when a
// table is nested inside another (rare in Asana but possible), only
// the outer one gets scored because that's the container
// asanaCollectRows will iterate over.
func asanaFindTaskList(window *AXNode) *AXNode {
	containers := FindAll(window, func(n *AXNode) bool {
		switch n.Role {
		case "AXTable", "AXList", "AXOutline":
			return true
		}
		return false
	})
	var best *AXNode
	bestScore := 0
	for _, n := range containers {
		score := 0
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			if c.Role == "AXRow" || c.Role == "AXGroup" {
				if hasNonEmptyText(c) {
					score++
				}
			}
		}
		if score > bestScore {
			bestScore = score
			best = n
		}
	}
	// Require at least 3 row-shaped children so we don't pick a small
	// toolbar or settings panel.
	if bestScore < 3 {
		return nil
	}
	return best
}

// asanaCollectRows joins each row's static texts with a tab separator
// so columnar fields (title, status, assignee, due) stay distinguishable
// downstream. Drops empty rows and applies the asanaMaxRows cap.
func asanaCollectRows(container *AXNode) []string {
	var rows []string
	for _, c := range container.Children {
		if c == nil {
			continue
		}
		texts := CollectStaticTexts(c)
		if len(texts) == 0 {
			continue
		}
		row := strings.TrimSpace(strings.Join(texts, "\t"))
		if row == "" {
			continue
		}
		rows = append(rows, row)
		if len(rows) >= asanaMaxRows {
			break
		}
	}
	return rows
}

// hasNonEmptyText reports whether the subtree contains any AXStaticText
// with a non-empty value — used to disqualify decorative wrappers from
// asanaFindTaskList's scoring.
func hasNonEmptyText(n *AXNode) bool {
	found := false
	Traverse(n, func(node *AXNode) bool {
		if node.Role == "AXStaticText" {
			if strings.TrimSpace(Text(node)) != "" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
