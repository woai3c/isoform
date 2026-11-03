package appparser

import (
	"strings"
)

var linearWebHosts = []string{
	"linear.app",
}

// linearNativeBundleIDs covers the macOS Linear app. Like Notion the
// native client wraps the same web stack, so the same extraction logic
// applies for both surfaces.
var linearNativeBundleIDs = map[string]bool{
	"com.linear":            true,
	"com.linear.linear-mac": true,
}

type linearParser struct{}

func init() {
	Register(&linearParser{})
}

func (p *linearParser) CanParse(bundleID string) bool {
	return linearNativeBundleIDs[bundleID]
}

func (p *linearParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, linearWebHosts...)
}

func (p *linearParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "linear",
		Label:       "Linear",
		ContentType: "document",
		Bundles:     []string{"com.linear", "com.linear.linear-mac"},
		URLHosts:    append([]string(nil), linearWebHosts...),
	}
}

// ParseTree extracts the focused Linear surface. Linear URLs disambiguate
// the page kind:
//
//	/team/<TEAM>/active                   → task_list (board / cycle view)
//	/issue/<TEAM>-123/<slug>             → document  (single issue detail)
//	/project/<slug>                       → document  (project overview)
//	other                                 → falls through to generic body
//
// Returns nil for views we don't recognize so the browser chrome stripper
// still produces useful text instead of nothing.
func (p *linearParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
	if tree == nil {
		return nil
	}
	root := linearFindRoot(tree)
	if root == nil {
		return nil
	}

	source := "web"
	if linearNativeBundleIDs[bundleID] {
		source = "native"
	}

	if linearURLIsIssueDetail(url) || linearURLIsProject(url) {
		return linearParseDetail(root, bundleID, windowTitle, url, source)
	}
	return linearParseList(root, bundleID, windowTitle, url, source)
}

// linearFindRoot returns the AXWebArea (web) or window root (native).
func linearFindRoot(tree *AXNode) *AXNode {
	if web := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" }); web != nil {
		return web
	}
	if window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" }); window != nil {
		return window
	}
	return tree
}

func linearURLIsIssueDetail(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "/issue/")
}

func linearURLIsProject(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "/project/")
}

// linearParseDetail extracts a single issue or project page: title (first
// AXHeading) plus body text, skipping the activity sidebar / properties
// rail when we can identify them by class.
func linearParseDetail(root *AXNode, bundleID, windowTitle, url, source string) *ParsedContent {
	title := linearDetailTitle(root, windowTitle)

	var lines []string
	Traverse(root, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		// Skip the activity rail and properties panel — they're noisy and
		// duplicate metadata that shows up elsewhere.
		if HasClassContaining(n, "ActivityFeed") ||
			HasClassContaining(n, "PropertyList") ||
			HasClassContaining(n, "Sidebar") {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Value
			if t == "" {
				t = n.Description
			}
			t = strings.TrimSpace(t)
			if t != "" && t != title {
				lines = append(lines, t)
			}
		}
		return true
	})
	body := strings.Join(lines, "\n")
	if strings.TrimSpace(body) == "" {
		body = MainLandmarkText(root)
		if strings.TrimSpace(body) == "" {
			return nil
		}
	}
	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Body:        body,
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "linear",
			"source":   source,
			"page":     "detail",
		},
	}
}

// linearParseList extracts a board / cycle / triage view: a list of
// issues, each with an identifier, title, and status. Linear renders
// each row as an AXRow with multiple AXStaticText children.
func linearParseList(root *AXNode, bundleID, windowTitle, url, source string) *ParsedContent {
	rows := FindAll(root, func(n *AXNode) bool {
		return n.Role == "AXRow"
	})
	if len(rows) == 0 {
		return nil
	}
	var lines []string
	for _, row := range rows {
		texts := CollectStaticTexts(row)
		if len(texts) == 0 {
			continue
		}
		// Compress whitespace and trim duplicate cells.
		var cells []string
		seen := map[string]bool{}
		for _, t := range texts {
			t = strings.TrimSpace(t)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			cells = append(cells, t)
		}
		if len(cells) == 0 {
			continue
		}
		lines = append(lines, "- "+strings.Join(cells, " · "))
	}
	if len(lines) == 0 {
		return nil
	}
	title := strings.TrimSpace(windowTitle)
	return &ParsedContent{
		ContentType: "task_list",
		Title:       title,
		Body:        strings.Join(lines, "\n"),
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "linear",
			"source":   source,
			"page":     "list",
		},
	}
}

// linearDetailTitle prefers the first AXHeading on the page; falls back to
// the window title with the trailing " · Linear" stripped.
func linearDetailTitle(root *AXNode, windowTitle string) string {
	heading := FindFirst(root, func(n *AXNode) bool {
		return n.Role == "AXHeading"
	})
	if heading != nil {
		t := strings.TrimSpace(Text(heading))
		if t != "" {
			return t
		}
	}
	t := strings.TrimSpace(windowTitle)
	if idx := strings.LastIndex(t, " · "); idx > 0 {
		t = t[:idx]
	}
	return t
}
