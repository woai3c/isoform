package appparser

import (
	"fmt"
	"strings"
)

// finderBundleIDs lists the macOS Finder.
var finderBundleIDs = map[string]bool{
	"com.apple.finder": true,
}

// finderMaxBrowseListed caps the count of non-selected filenames included in
// the parsed body for directory listing output. Capturing the full file list
// is rarely useful for AI context (a Downloads folder may have hundreds of
// items) and risks bloating the prompt — the selected files always render
// in full, since that's the load-bearing user-intent signal.
const finderMaxBrowseListed = 50

type finderParser struct{}

func init() {
	Register(&finderParser{})
}

func (p *finderParser) CanParse(bundleID string) bool {
	return finderBundleIDs[bundleID]
}

func (p *finderParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "finder",
		Label:       "Finder",
		ContentType: "document",
		Bundles:     []string{"com.apple.finder"},
	}
}

// ParseTree extracts the active Finder window's directory context. The
// output is structured so AI consumers can answer two questions:
//
//  1. "What is the user looking at?" — the current directory's displayName,
//     pulled from the AXWindow title.
//  2. "What did the user pick?" — the filenames whose AXRow / AXGroup is
//     marked Selected. This is the load-bearing signal; merely browsing a
//     directory is weak context, but a specific selection signals intent.
//
// Three view modes are handled, each with a different AX layout. The
// detector picks whichever container is present, which is mutually
// exclusive in practice — Finder only renders one view per leaf window.
//
//   - List view: AXOutline[identifier="ListView"] contains AXRow
//     [subrole="AXOutlineRow"] nodes, each with an AXTextField whose
//     Value is the filename. Selection rides on the row's Selected flag.
//   - Icon view: AXList[identifier="IconView"][subrole="AXCollectionList"]
//     contains AXGroup nodes whose Identifier is the filename. Selection
//     rides on the group's Selected flag. NSCollectionView lazily renders
//     only the viewport-visible cells, so for long directories the AX
//     tree only exposes a fraction of the files.
//   - Column view: AXBrowser[identifier="ColumnView"] is opaque under the
//     macOS 26 SwiftUI rewrite — the AX tree exposes no per-file nodes
//     even when the column is populated. We degrade to "current directory
//   - selection count" using the status-bar text instead, since that's
//     the only file-level signal Finder still surfaces in this view.
//
// The Finder sidebar uses the same AXRow role as List view file rows, and
// the user's current sidebar location is also marked Selected — so a
// naive "find AXRow[selected=true]" sweep would treat "下载" (the sidebar
// chip) as a file. We always anchor the file walk under the view-specific
// container (ListView / IconView / ColumnView identifier), never the
// window root.
//
// Returns nil when no recognized view container is found (e.g. user is in
// a Finder window with no editor pane focused, such as a Get Info or
// Connect-to-Server dialog).
func (p *finderParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	dirName := strings.TrimSpace(windowTitle)
	if dirName == "" {
		dirName = strings.TrimSpace(window.Title)
	}

	view, allFiles, selectedFiles := finderExtractFiles(window)
	status := finderStatusBarText(window)

	if view == "" && dirName == "" && status == "" {
		return nil
	}

	body := finderRenderBody(dirName, view, allFiles, selectedFiles, status)
	if strings.TrimSpace(body) == "" {
		return nil
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "finder",
	}
	if view != "" {
		meta["view"] = view
	}
	if dirName != "" {
		meta["directory"] = dirName
	}
	if status != "" {
		meta["statusBar"] = status
	}
	if len(allFiles) > 0 {
		meta["visibleFileCount"] = fmt.Sprintf("%d", len(allFiles))
	}
	if len(selectedFiles) > 0 {
		meta["selectedCount"] = fmt.Sprintf("%d", len(selectedFiles))
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       dirName,
		Channel:     dirName,
		Body:        body,
		Metadata:    meta,
	}
}

// finderExtractFiles detects the active view and pulls (all visible
// filenames, selected filenames) out of it. Empty slices when the view
// can't be identified — Column view returns ("column", nil, nil) so the
// caller still tags metadata appropriately even with no file names.
func finderExtractFiles(window *AXNode) (view string, all, selected []string) {
	if listRoot := FindFirst(window, func(n *AXNode) bool {
		return n.Role == "AXOutline" && n.Identifier == "ListView"
	}); listRoot != nil {
		all, selected = finderListViewFiles(listRoot)
		return "list", all, selected
	}
	if iconRoot := FindFirst(window, func(n *AXNode) bool {
		return n.Role == "AXList" && n.Identifier == "IconView"
	}); iconRoot != nil {
		all, selected = finderIconViewFiles(iconRoot)
		return "icon", all, selected
	}
	if FindFirst(window, func(n *AXNode) bool {
		return n.Role == "AXBrowser" && n.Identifier == "ColumnView"
	}) != nil {
		return "column", nil, nil
	}
	return "", nil, nil
}

// finderListViewFiles walks the ListView AXOutline subtree. Each
// AXRow[subrole="AXOutlineRow"] represents one file; the filename is in
// the first descendant AXTextField's Value (the row's "name" column —
// the other columns hold size, kind, modification date, etc).
func finderListViewFiles(root *AXNode) (all, selected []string) {
	rows := FindAll(root, func(n *AXNode) bool {
		return n.Role == "AXRow" && n.Subrole == "AXOutlineRow"
	})
	for _, row := range rows {
		name := finderRowFileName(row)
		if name == "" {
			continue
		}
		all = append(all, name)
		if IsSelected(row) {
			selected = append(selected, name)
		}
	}
	return all, selected
}

// finderRowFileName returns the filename for a List-view row by looking
// for the first AXTextField under the row. List view always renders the
// name column as an editable field (even when the user is not renaming)
// to support inline rename — the static-text columns are size / kind /
// date.
func finderRowFileName(row *AXNode) string {
	field := FindFirst(row, func(n *AXNode) bool { return n.Role == "AXTextField" })
	if field == nil {
		return ""
	}
	return strings.TrimSpace(field.Value)
}

// finderIconViewFiles walks the IconView AXList subtree. Each file is an
// AXGroup whose Identifier is the filename; the inner AXImage carries the
// thumbnail. Selection rides on the AXGroup's Selected flag. Only the
// viewport-rendered cells appear here — NSCollectionView lazily mounts
// rows so for a folder with hundreds of items the tree may only contain
// a couple dozen visible at any moment.
func finderIconViewFiles(root *AXNode) (all, selected []string) {
	groups := FindAll(root, func(n *AXNode) bool {
		return n.Role == "AXGroup" && n.Identifier != ""
	})
	for _, g := range groups {
		name := strings.TrimSpace(g.Identifier)
		if name == "" {
			continue
		}
		all = append(all, name)
		if IsSelected(g) {
			selected = append(selected, name)
		}
	}
	return all, selected
}

// finderStatusBarText returns Finder's bottom-bar text, which always
// includes the directory's item count and (when applicable) the selected
// count + total selected size. This is the only file-level signal that
// survives in Column view, so we keep it for all views — it's also a
// useful sanity-check pair with our per-row selection counting.
//
// The status bar AXStaticText is not a direct child of the AXWindow;
// it's nested inside the window's split-group layout (verified against
// live Finder AX dumps). We scan the whole subtree but stop as soon as
// we find the first text containing the locale-specific item-count
// marker so we don't pay for a full descent when the status bar is the
// only candidate.
func finderStatusBarText(window *AXNode) string {
	var found string
	Traverse(window, func(n *AXNode) bool {
		if found != "" {
			return false
		}
		if n.Role != "AXStaticText" {
			return true
		}
		s := strings.TrimSpace(n.Value)
		if s == "" {
			s = strings.TrimSpace(n.Description)
		}
		// Status bar text always mentions item count — match both the
		// localized "项" and the English "items" to avoid locale-specific
		// regressions.
		if s != "" && (strings.Contains(s, "项") || strings.Contains(s, "items") ||
			strings.Contains(s, "item")) {
			found = s
			return false
		}
		return true
	})
	return found
}

// finderRenderBody composes the human-readable body. Sections, in order:
//
//   - Directory header
//   - Selection: every selected filename, in full (the load-bearing signal)
//   - Browse: up to finderMaxBrowseListed filenames when nothing is selected
//     (gives AI context about the directory without flooding the prompt;
//     when something is selected, the selection itself is enough context
//     and the full listing would be noise)
//   - Status bar text, when present
//
// Column view degrades to header + status bar only because no filenames
// are extractable.
func finderRenderBody(dirName, view string, all, selected []string, status string) string {
	var sb strings.Builder
	if dirName != "" {
		fmt.Fprintf(&sb, "Directory: %s\n", dirName)
	}
	switch {
	case len(selected) > 0:
		fmt.Fprintf(&sb, "\nSelected (%d):\n", len(selected))
		for _, f := range selected {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	case len(all) > 0:
		listed := all
		truncated := false
		if len(listed) > finderMaxBrowseListed {
			listed = listed[:finderMaxBrowseListed]
			truncated = true
		}
		fmt.Fprintf(&sb, "\nFiles (%d shown of %d visible):\n", len(listed), len(all))
		for _, f := range listed {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
		if truncated {
			sb.WriteString("- …\n")
		}
	case view == "column":
		// Column view exposes no file rows. The status bar is our only
		// remaining file-level signal — surface it explicitly so the AI
		// consumer can at least tell the user is in a Column-view Finder
		// window (and how many things might be selected).
		sb.WriteString("\n(Column view — per-file AX nodes are not exposed by macOS; see statusBar metadata.)\n")
	}
	if status != "" {
		fmt.Fprintf(&sb, "\nStatus: %s", status)
	}
	return sb.String()
}
