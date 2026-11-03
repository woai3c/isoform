package appparser

import (
	"strings"
)

// notionWebHosts lists the hostnames at which Notion is served. Personal
// workspaces sit on notion.so, customer-published sites on notion.site.
var notionWebHosts = []string{
	"notion.so",
	"*.notion.site",
}

// notionNativeBundleIDs lists the macOS Notion app. Notion ships an
// Electron wrapper, so the AX tree mirrors the web DOM — the same
// extraction logic works for both.
var notionNativeBundleIDs = map[string]bool{
	"notion.id": true,
}

type notionParser struct{}

func init() {
	Register(&notionParser{})
}

func (p *notionParser) CanParse(bundleID string) bool {
	return notionNativeBundleIDs[bundleID]
}

func (p *notionParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, notionWebHosts...)
}

func (p *notionParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "notion",
		Label:       "Notion",
		ContentType: "document",
		Bundles:     []string{"notion.id"},
		URLHosts:    append([]string(nil), notionWebHosts...),
	}
}

// ParseTree extracts the focused Notion page: title plus body text from
// the page's main content area. Notion exposes a generally well-labeled
// AX tree under an AXWebArea (in browsers) or under the top window
// (in the Electron app) — in both we look for the editor surface, which
// is identifiable by the `notion-page-content` class.
//
// Returns nil when the editor isn't present (database list view, settings
// page, share dialog, etc.) so the capture falls back to the generic
// browser chrome stripper for those cases.
func (p *notionParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
	if tree == nil {
		return nil
	}

	root := notionFindRoot(tree, windowTitle)
	if root == nil {
		return nil
	}

	pageContent := FindFirst(root, func(n *AXNode) bool {
		return HasClassContaining(n, "notion-page-content")
	})
	if pageContent == nil {
		// Some Notion views (e.g. focus mode, legacy DOM) don't tag the
		// container — fall back to the first AXWebArea / AXGroup with
		// substantial text content under the root.
		pageContent = root
	}

	title := notionPageTitle(root, windowTitle)
	body := notionRenderBody(pageContent, title)
	if strings.TrimSpace(body) == "" {
		// Notion redesigned page surfaces drop the notion-page-content
		// container in some embedded views — fall back to the page's
		// AXMain landmark so we still capture useful content.
		body = MainLandmarkText(root)
		if strings.TrimSpace(body) == "" {
			return nil
		}
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "notion",
	}
	source := "web"
	if notionNativeBundleIDs[bundleID] {
		source = "native"
	}
	meta["source"] = source

	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Body:        body,
		URL:         url,
		Metadata:    meta,
	}
}

// notionFindRoot returns the subtree we should walk for content.
//
// The native macOS Notion app exposes multiple AXWebArea siblings — one
// for the top tab bar (title "Tab Bar") and one for the actual page
// editor (title matches the page name, e.g. "Todo list"). Naively
// picking the first AXWebArea gets the tab bar and the parser ends up
// extracting "panda" + the tab label instead of the page body. Match
// the window title first, then fall back to the AXWebArea with the most
// descendants (the editor dwarfs the tab bar).
func notionFindRoot(tree *AXNode, windowTitle string) *AXNode {
	webAreas := FindAll(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if len(webAreas) > 0 {
		if windowTitle != "" {
			for _, w := range webAreas {
				if w.Title == windowTitle {
					return w
				}
			}
		}
		best := webAreas[0]
		bestCount := notionCountNodes(best)
		for _, w := range webAreas[1:] {
			if c := notionCountNodes(w); c > bestCount {
				best = w
				bestCount = c
			}
		}
		return best
	}
	if window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" }); window != nil {
		return window
	}
	return tree
}

// notionCountNodes returns the size of a subtree (number of nodes).
// Cheap enough to call once per AXWebArea candidate when picking the
// editor surface.
func notionCountNodes(n *AXNode) int {
	if n == nil {
		return 0
	}
	count := 1
	for _, c := range n.Children {
		count += notionCountNodes(c)
	}
	return count
}

// notionPageTitle pulls the page title from the most reliable source we
// can find. Priority order, each gracefully falling through:
//  1. `notion-page-block-title` / `notion-page-block` element (current
//     and legacy class names — Notion has renamed it across redesigns).
//  2. First AXHeading on the page (Notion renders the title as h1).
//  3. The leading segment of the window title (Notion sets it to
//     "Page name — Workspace").
func notionPageTitle(root *AXNode, windowTitle string) string {
	for _, classNeedle := range []string{"notion-page-block-title", "notion-page-block"} {
		titleEl := FindFirst(root, func(n *AXNode) bool {
			return HasClassContaining(n, classNeedle)
		})
		if titleEl == nil {
			continue
		}
		if text := strings.TrimSpace(Text(titleEl)); text != "" {
			return text
		}
		texts := CollectStaticTexts(titleEl)
		if len(texts) > 0 {
			if first := strings.TrimSpace(texts[0]); first != "" {
				return first
			}
		}
	}
	if heading := FindFirst(root, func(n *AXNode) bool { return n.Role == "AXHeading" }); heading != nil {
		if text := strings.TrimSpace(Text(heading)); text != "" {
			return text
		}
	}
	if windowTitle != "" {
		left := strings.Split(windowTitle, " — ")[0]
		return strings.TrimSpace(left)
	}
	return ""
}

// notionRenderBody walks the page content collecting visible text while
// skipping presence chips, comment threads, and other noisy chrome that
// Notion sprinkles around the editor.
//
// Sources, in priority order:
//   - AXStaticText (legacy Electron / web Notion — DOM mirrors)
//   - AXTextArea  (macOS SwiftUI Notion 2025+ — each block is a textarea
//     whose accessibility label carries the block text, sometimes with
//     toolbar chrome ("Add icon Add cover Add comment") prepended and
//     multiple blocks collapsed onto one line using "•" bullets)
//
// Identical-string dedup keeps a single block from appearing twice when
// both an AXTextArea and a nested AXStaticText expose the same text.
func notionRenderBody(root *AXNode, title string) string {
	var lines []string
	seen := map[string]bool{}
	addOne := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == title || s == "•" || seen[s] {
			return
		}
		seen[s] = true
		lines = append(lines, s)
	}
	// Some SwiftUI Notion builds bullet-join several blocks into one
	// label string; split on "•" so each block lands on its own line.
	addBlocks := func(s string) {
		if strings.Contains(s, "•") {
			for part := range strings.SplitSeq(s, "•") {
				addOne(part)
			}
			return
		}
		addOne(s)
	}

	Traverse(root, func(n *AXNode) bool {
		if n == nil || n.Role == "AXScrollBar" {
			return false
		}
		// Skip chrome subtrees that pollute body extraction.
		//
		// `notion-cursor` is deliberately NOT in this list: the modern
		// Notion app wraps the whole page editor in an outer container
		// named `notion-cursor-listener` (the document-level pointer
		// event listener), and a substring match against "notion-cursor"
		// would silently drop the entire page body. The pre-rewrite
		// cursor avatars used class names like `notion-cursor-icon`
		// which we no longer see in the current AX dump, so skipping
		// them isn't load-bearing.
		if HasClassContaining(n, "notion-presence") ||
			HasClassContaining(n, "notion-discussion") ||
			HasClassContaining(n, "notion-sidebar") ||
			HasClassContaining(n, "notion-topbar") ||
			HasClassContaining(n, "notion-print-ignore") ||
			HasClassContaining(n, "pseudoBefore") ||
			HasClassContaining(n, "pseudoAfter") {
			return false
		}
		// Visually hidden elements: ARIA-live region announcements
		// ("Converted to Bulleted list") and "Skip to content" links
		// render at h<=1 / w<=0. Negative X/Y is NOT a hidden signal —
		// Notion windows on secondary displays sit at negative screen
		// coordinates, so judging by X<0/Y<0 would drop the entire
		// page body for users with a left-side external monitor.
		if n.Frame != nil && (n.Frame.W <= 0 || n.Frame.H <= 1) {
			return false
		}
		switch n.Role {
		case "AXStaticText":
			t := n.Value
			if t == "" {
				t = n.Description
			}
			addOne(t)
		case "AXTextArea":
			t := n.Value
			if t == "" {
				t = n.Description
			}
			if t == "" {
				t = n.Title
			}
			t = stripNotionHeaderChrome(t)
			addBlocks(t)
		}
		return true
	})
	return strings.Join(lines, "\n")
}

// stripNotionHeaderChrome removes toolbar prefixes that the SwiftUI
// Notion app leaks into the top-level AXTextArea's accessibility label
// (the buttons in the page header — "Add icon", "Add cover",
// "Add comment" — get concatenated in front of the actual page text).
func stripNotionHeaderChrome(s string) string {
	s = strings.TrimSpace(s)
	// Iteratively peel known chrome tokens in case only a subset is
	// present in any given build / page state.
	prefixes := []string{
		"Add icon Add cover Add comment",
		"Add icon Add cover",
		"Add icon Add comment",
		"Add cover Add comment",
		"Add icon",
		"Add cover",
		"Add comment",
	}
	for {
		trimmed := false
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				s = strings.TrimSpace(s[len(p):])
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	return s
}
