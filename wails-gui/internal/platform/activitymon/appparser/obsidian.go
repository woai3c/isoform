package appparser

import "strings"

// obsidianBundleIDs lists the macOS Obsidian app. Obsidian ships an Electron
// wrapper so the AX tree mirrors the renderer DOM — class names from the
// running app's CSS are exposed via domClassList and drive our extraction.
var obsidianBundleIDs = map[string]bool{
	"md.obsidian": true,
}

type obsidianParser struct{}

func init() {
	Register(&obsidianParser{})
}

func (p *obsidianParser) CanParse(bundleID string) bool {
	return obsidianBundleIDs[bundleID]
}

func (p *obsidianParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "obsidian",
		Label:       "Obsidian",
		ContentType: "document",
		Bundles:     []string{"md.obsidian"},
	}
}

// ParseTree extracts the active note's title and body from Obsidian.
//
// Two AX shapes are possible depending on the view mode of the active leaf:
//
//   - Edit mode (Live Preview or Source mode share the same AX shape):
//     the editor surface is an AXTextArea with class `cm-content` whose
//     Value carries the raw markdown source for the rows in CodeMirror's
//     current viewport. CM6 lazily mounts ~1 viewport ± a small buffer of
//     lines into the DOM, so for long notes the parser only sees what the
//     user can currently see — this is a CodeMirror constraint we cannot
//     work around from the AX side.
//   - Reading mode: the editor pane is replaced by a `markdown-preview-view`
//     subtree containing rendered HTML. Each visible block becomes an
//     AXStaticText. Same viewport-only limitation applies.
//
// Both modes coexist with the same chrome (left/right sidebars, ribbon,
// status bar). To avoid mixing the active note's content with sidebar
// plugins that index the vault (e.g. Notebook Navigator, which exposes
// every file's preview as AXStaticText under its own subtree), we anchor
// every extraction under the `workspace-leaf.mod-active` element — that's
// the leaf Obsidian itself treats as focused.
//
// Returns nil when the active leaf isn't a markdown note (vault settings,
// graph view, canvas, empty new-tab) so the snapshot falls back to raw
// accessibility text.
func (p *obsidianParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	leaf := obsidianFindActiveLeaf(tree)
	if leaf == nil {
		return nil
	}

	title := obsidianTitle(leaf, windowTitle)
	body := obsidianEditModeBody(leaf)
	mode := "edit"
	if body == "" {
		body = obsidianReadingModeBody(leaf, title)
		mode = "reading"
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "obsidian",
		"source":   "native",
		"mode":     mode,
	}

	vault := obsidianVaultName(windowTitle)
	if vault != "" {
		meta["vault"] = vault
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Channel:     vault,
		Body:        body,
		Metadata:    meta,
	}
}

// obsidianFindActiveLeaf returns the focused workspace-leaf — Obsidian's
// term for an editor pane. Anchoring under this subtree filters out
// sidebars, ribbon controls, and the file explorer, plus any vault-wide
// plugin (e.g. Notebook Navigator) that indexes other notes' content
// into its own panel and would otherwise duplicate body text.
func obsidianFindActiveLeaf(tree *AXNode) *AXNode {
	return FindFirst(tree, func(n *AXNode) bool {
		return HasClass(n, "workspace-leaf") && HasClass(n, "mod-active")
	})
}

// obsidianTitle returns the note's title. The `inline-title` AXTextArea
// (the editable H1 at the top of the editor canvas) carries the active
// note's name in both edit and reading modes, so we prefer it. Falls back
// to the leading segment of the window title, which Obsidian formats as
// "<File> - <Vault> - Obsidian <Version>".
func obsidianTitle(leaf *AXNode, windowTitle string) string {
	inlineTitle := FindFirst(leaf, func(n *AXNode) bool {
		return n.Role == "AXTextArea" && HasClass(n, "inline-title")
	})
	if inlineTitle != nil {
		if t := strings.TrimSpace(inlineTitle.Value); t != "" {
			return t
		}
	}
	if windowTitle != "" {
		if idx := strings.Index(windowTitle, " - "); idx > 0 {
			return strings.TrimSpace(windowTitle[:idx])
		}
		return strings.TrimSpace(windowTitle)
	}
	return ""
}

// obsidianEditModeBody returns the raw markdown source from CodeMirror's
// content area, or "" if the active leaf isn't in edit mode. Live Preview
// and Source mode expose the same `cm-content` AXTextArea, so this single
// path covers both. The leading run of zero-width spaces (U+200B) that
// CM6 inserts as placeholders for empty lines gets stripped — keeping
// them would force the body text to start with invisible characters that
// confuse downstream consumers.
func obsidianEditModeBody(leaf *AXNode) string {
	cm := FindFirst(leaf, func(n *AXNode) bool {
		return n.Role == "AXTextArea" && HasClass(n, "cm-content")
	})
	if cm == nil {
		return ""
	}
	return obsidianCleanText(cm.Value)
}

// obsidianReadingModeBody concatenates the AXStaticText leaves under the
// `markdown-preview-view` subtree, dropping the title echo (Obsidian
// renders the note title as the first H1 in the preview, duplicating the
// inline-title) and stripping the U+200B placeholders that show up for
// blank-line paragraphs.
func obsidianReadingModeBody(leaf *AXNode, title string) string {
	preview := FindFirst(leaf, func(n *AXNode) bool {
		return HasClass(n, "markdown-preview-view")
	})
	if preview == nil {
		return ""
	}

	var lines []string
	seen := map[string]bool{}
	titleTrim := strings.TrimSpace(title)
	Traverse(preview, func(n *AXNode) bool {
		if n == nil || n.Role == "AXScrollBar" {
			return false
		}
		if n.Role != "AXStaticText" {
			return true
		}
		t := n.Value
		if t == "" {
			t = n.Description
		}
		t = obsidianCleanText(t)
		if t == "" || t == titleTrim {
			return true
		}
		if seen[t] {
			return true
		}
		seen[t] = true
		lines = append(lines, t)
		return true
	})
	return strings.Join(lines, "\n")
}

// obsidianCleanText strips the zero-width spaces (U+200B) Obsidian uses
// as visual placeholders for empty paragraphs and trims surrounding
// whitespace. A text node that contained only a placeholder collapses to
// empty so callers can skip it.
func obsidianCleanText(s string) string {
	s = strings.ReplaceAll(s, "​", "")
	return strings.TrimSpace(s)
}

// obsidianVaultName extracts the vault name from the window title.
// Obsidian formats the window title as "<File> - <Vault> - Obsidian
// <Version>". Returns "" when the title doesn't match this shape.
func obsidianVaultName(windowTitle string) string {
	parts := strings.Split(windowTitle, " - ")
	if len(parts) < 3 {
		return ""
	}
	// The last segment is always "Obsidian <Version>"; everything between
	// the first segment (file) and the last (app banner) is the vault. We
	// re-join in case the vault name itself contained " - ".
	vault := strings.Join(parts[1:len(parts)-1], " - ")
	return strings.TrimSpace(vault)
}
