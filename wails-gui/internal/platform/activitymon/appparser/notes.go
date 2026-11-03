package appparser

import (
	"strings"
)

// notesBundleIDs lists the macOS Notes app.
var notesBundleIDs = map[string]bool{
	"com.apple.Notes": true,
}

type notesParser struct{}

func init() {
	Register(&notesParser{})
}

func (p *notesParser) CanParse(bundleID string) bool {
	return notesBundleIDs[bundleID]
}

func (p *notesParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "notes",
		Label:       "Apple Notes",
		ContentType: "document",
		Bundles:     []string{"com.apple.Notes"},
	}
}

// ParseTree extracts the active note from Notes.app.
//
// macOS Notes exposes the active note body as an AXTextArea on macOS 11+
// (the editable canvas, whether or not WebKit renders it underneath).
// Pre-WebKit-era builds and some web-context fallbacks expose the body as
// AXStaticText nodes nested under an AXWebArea. We try AXTextArea first
// because the WebArea on recent macOS often wraps the whole window —
// including the sidebar's date strings — and the static-text walk turns
// that sidebar noise into a non-empty "body" that then blocks the
// fallback. The sidebar is an AXOutline; we use the window title for the
// note title and derive the folder from whichever sidebar row has a
// readable label.
func (p *notesParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	var title, body string

	// Primary path: pick the AXTextArea whose body text is longest. The
	// toolbar search box is also an AXTextArea but only carries whatever
	// the user typed (usually short or empty), so length is a good proxy
	// for "this is the note canvas, not a chrome control". We accept the
	// content from Value or Description because rich-text notes sometimes
	// expose the AT-string-rendered body via Description instead of Value.
	if best := notesPickTextAreaBody(window); best != "" {
		title, body = notesSplitTitleBody(best)
	}

	// Fallback: legacy WebKit-only Notes — body shows up as AXStaticText
	// nested in an AXWebArea.
	if body == "" {
		webArea := FindFirst(window, func(n *AXNode) bool {
			return n.Role == "AXWebArea"
		})
		if webArea != nil {
			title, body = notesExtractFromWebArea(webArea)
		}
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	if title == "" {
		title = windowTitle
	}

	folder := notesActiveFolder(window)

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "notes",
	}
	if folder != "" {
		meta["folder"] = folder
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       strings.TrimSpace(title),
		Channel:     folder,
		Body:        strings.TrimSpace(body),
		Metadata:    meta,
	}
}

// notesPickTextAreaBody returns the longest body text across all AXTextArea
// nodes under window, sourcing each candidate from Value (preferred) or
// Description. Returns "" when no AXTextArea carries text. Length-based
// selection is intentional: it discriminates the note canvas from the
// toolbar search field without relying on identifiers that vary across
// macOS releases.
func notesPickTextAreaBody(window *AXNode) string {
	var best string
	Traverse(window, func(n *AXNode) bool {
		if n.Role != "AXTextArea" {
			return true
		}
		s := n.Value
		if s == "" {
			s = n.Description
		}
		if len(s) > len(best) {
			best = s
		}
		return true
	})
	return best
}

// notesExtractFromWebArea walks the WebArea subtree collecting visible text.
// Treats the first non-empty static text as the title (Notes renders the
// note title in a larger heading element that's still an AXStaticText).
func notesExtractFromWebArea(root *AXNode) (string, string) {
	var lines []string
	Traverse(root, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Value
			if t == "" {
				t = n.Description
			}
			if t != "" {
				lines = append(lines, t)
			}
		}
		return true
	})
	if len(lines) == 0 {
		return "", ""
	}
	title := strings.TrimSpace(lines[0])
	body := strings.Join(lines, "\n")
	return title, body
}

// notesSplitTitleBody treats the first line of a plain-text note as the
// title (matching Notes.app behavior when rendering list previews).
func notesSplitTitleBody(s string) (string, string) {
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[:i]), s
	}
	return strings.TrimSpace(s), s
}

// notesActiveFolder returns the name of the currently-selected folder in
// the Notes sidebar. Best-effort: we pick the first AXRow that has a
// readable static-text label, which is usually the first folder rendered.
// A proper "selected" signal would require AXSelected to be threaded
// through our AXNode serialization.
func notesActiveFolder(window *AXNode) string {
	outline := FindFirst(window, func(n *AXNode) bool {
		return n.Role == "AXOutline"
	})
	if outline == nil {
		return ""
	}
	rows := FindAll(outline, func(n *AXNode) bool { return n.Role == "AXRow" })
	for _, row := range rows {
		text := FindFirst(row, func(n *AXNode) bool {
			return n.Role == "AXStaticText" && (n.Value != "" || n.Title != "")
		})
		if text == nil {
			continue
		}
		name := text.Value
		if name == "" {
			name = text.Title
		}
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	return ""
}
