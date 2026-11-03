package appparser

import (
	"path/filepath"
	"strings"
)

// filepathBasename mirrors filepath.Base but normalises Windows-style
// backslashes first so VSCode's window.title template works on both
// macOS and (theoretically) Windows builds without diverging code.
func filepathBasename(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return filepath.Base(p)
}

// codeEditorBundleIDs covers VSCode and its forks that ship a near-
// identical Monaco-based UI. Cursor's bundle ID is the ToDesktop
// identifier the team picked; if a future Cursor build changes it,
// the AX Parser DevTools panel will report "no parser matched" and
// we update this map.
//
// Windows entries are the lowercase exe basename produced by
// FrontmostAppBundleID (see monitor_windows.go). They land in the same
// map because parser dispatch keys off a single string and the macOS /
// Windows identifiers can never collide.
var codeEditorBundleIDs = map[string]bool{
	"com.microsoft.VSCode":          true,
	"com.todesktop.230313mzl4w4u92": true, // Cursor
	// Windows exe basenames. Match what FrontmostAppBundleID emits
	// (lowercase). Without these the parser never fires on Windows,
	// AXCoverageMetadataOnly never gets set, and the activity popup
	// keeps the (chrome-only) UIA dump as the primary text signal
	// even though OCR is the only thing that actually carries the
	// Monaco buffer.
	"cursor.exe":          true, // Cursor
	"code.exe":            true, // VS Code
	"code - insiders.exe": true, // VS Code Insiders
	"windsurf.exe":        true, // Windsurf
	"trae.exe":            true, // Trae (ByteDance)
}

// codeEditorAppName tags the parser hits so downstream consumers can
// distinguish "developer editor" from generic document parsers.
const codeEditorAppName = "code_editor"

// codeEditorMaxLines caps the body so a huge open file doesn't flood
// the activity snapshot. The Monaco editor virtualises rendering so
// only the visible lines are in the AX tree anyway, but a wide
// resolution + small font can still emit a few hundred lines.
const codeEditorMaxLines = 200

type codeEditorParser struct{}

func init() {
	Register(&codeEditorParser{})
}

func (p *codeEditorParser) CanParse(bundleID string) bool {
	return codeEditorBundleIDs[bundleID]
}

func (p *codeEditorParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        codeEditorAppName,
		Label:       "VSCode / Cursor",
		ContentType: "document",
		Bundles: []string{
			"com.microsoft.VSCode",
			"com.todesktop.230313mzl4w4u92",
		},
		Notes: "Editor body is never exposed via macOS AX " +
			"(Monaco renders code to canvas, invisible to the AX bridge). " +
			"AX provides filename + workspace; OCR fills in the visible code.",
	}
}

// ParseTree extracts the currently focused source file from a VSCode-
// derivative window.
//
// Strategy:
//  1. Find the editor surface — a subtree whose domClassList contains
//     "monaco-editor" — and collect its AXStaticText nodes. Monaco
//     emits one AXStaticText per visible line, so the join preserves
//     the on-screen layout.
//  2. If the class anchor misses (Electron sometimes drops the class
//     when AXEnhancedUserInterface priming hasn't fully landed), fall
//     back to the longest AXTextArea in the tree — same trick the
//     terminal parser uses for the screen buffer.
//  3. Pull the filename + workspace from the window title, which
//     VSCode formats as "<filename> — <workspace>" (with a leading
//     "● " for unsaved buffers).
func (p *codeEditorParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	filename, workspace := splitCodeEditorTitle(windowTitle)
	// VSCode's default window.title template emits just the basename
	// (${activeEditorShort}). Users who customize the template to use
	// ${activeEditorMedium} get the workspace-relative path here, in
	// which case the parser surfaces it as `relativePath` and demotes
	// `filename` to the basename so consumers can pick whichever they
	// need without re-parsing.
	relativePath := ""
	if strings.ContainsAny(filename, "/\\") {
		relativePath = filename
		filename = filepathBasename(filename)
	}

	body := extractMonacoEditorBody(tree)
	if body == "" {
		body = extractLongestAXTextArea(tree)
	}

	// Trim to the last N lines — Monaco surfaces only the visible
	// window so this rarely fires, but a high-DPI screen with tiny
	// font can blow past it.
	if body != "" {
		lines := strings.Split(body, "\n")
		if len(lines) > codeEditorMaxLines {
			lines = lines[len(lines)-codeEditorMaxLines:]
		}
		body = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	meta := map[string]string{
		"app":      codeEditorAppName,
		"bundleId": bundleID,
	}
	if filename != "" {
		meta["filename"] = filename
	}
	if relativePath != "" {
		meta["relativePath"] = relativePath
	}
	if workspace != "" {
		meta["workspace"] = workspace
	}

	title := filename
	if title == "" {
		title = windowTitle
	}

	// VSCode does NOT expose the editor pane's code through macOS AX,
	// full stop. Monaco renders code to canvas (since 2023) and the
	// Chromium AX bridge has no view into canvas content. Flipping
	// editor.accessibilitySupport=on only changes VSCode's internal
	// screen-reader UX (hidden textarea for the current line, ARIA
	// announcements) — it does not push the buffer into the AX tree
	// that AXUIElementCopyAttributeValue walks. Verified empirically
	// against VSCode 1.x with the setting both off and on: the tree
	// shape and content are identical.
	//
	// That's the realistic ceiling, not a bug. The activity pipeline
	// OCRs the frame in parallel, and downstream consumers fuse the
	// two via appparser.MergeWithOCR when IsPartialAX reports true.
	// We flag AXCoverageMetadataOnly so the fusion path runs, and
	// emit the filename + workspace as a structured Body so even
	// consumers that DON'T fuse (or when OCR is unavailable) still
	// know which file the user has open.
	if body == "" {
		if filename == "" && workspace == "" {
			return nil
		}
		meta["axCoverage"] = AXCoverageMetadataOnly
		// Prefer the relative path for the body — it carries strictly
		// more information than the basename and downstream prompts
		// can always trim it back down if needed.
		metaBody := relativePath
		if metaBody == "" {
			metaBody = filename
		}
		if workspace != "" {
			if metaBody != "" {
				metaBody += "  (" + workspace + ")"
			} else {
				metaBody = workspace
			}
		}
		return &ParsedContent{
			ContentType: "document",
			Title:       title,
			Body:        metaBody,
			Metadata:    meta,
		}
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Body:        body,
		Metadata:    meta,
	}
}

// extractMonacoEditorBody finds the first subtree marked as a Monaco
// editor and returns its joined static-text lines. Returns "" when
// no editor surface is found (Welcome page, Source Control view,
// extensions tab, …).
func extractMonacoEditorBody(tree *AXNode) string {
	if tree == nil {
		return ""
	}
	editor := FindFirst(tree, func(n *AXNode) bool {
		return HasClassContaining(n, "monaco-editor")
	})
	if editor == nil {
		return ""
	}
	texts := CollectStaticTexts(editor)
	if len(texts) == 0 {
		return ""
	}
	return strings.Join(texts, "\n")
}

// extractLongestAXTextArea returns the longest AXTextArea Value in the
// tree. Used as a fallback when the monaco-editor class anchor doesn't
// match — same heuristic as terminal.go.
func extractLongestAXTextArea(tree *AXNode) string {
	if tree == nil {
		return ""
	}
	var best string
	Traverse(tree, func(n *AXNode) bool {
		if n.Role == "AXTextArea" && len(n.Value) > len(best) {
			best = n.Value
		}
		return true
	})
	return best
}

// splitCodeEditorTitle pulls the filename + workspace out of a VSCode
// window title. Format examples:
//
//	"main.go — myproject"                      (macOS VSCode / Cursor)
//	"● main.go — myproject"                    (unsaved buffer)
//	"main.go — myproject [Workspace]"
//	"Welcome — myproject"
//	"main.go - myproject - Cursor"             (Windows Cursor)
//	"main.go - myproject - Visual Studio Code" (Windows VS Code)
//
// Returns ("", "") when the title doesn't match the pattern — callers
// fall back to windowTitle as-is.
func splitCodeEditorTitle(title string) (string, string) {
	t := strings.TrimSpace(title)
	t = strings.TrimPrefix(t, "● ")

	// macOS / em-dash form: "<filename> — <workspace>". A regular hyphen
	// inside the filename or workspace can't be mistaken for the
	// separator here, so this branch is unambiguous.
	if parts := strings.SplitN(t, " — ", 2); len(parts) == 2 {
		return finalizeCodeEditorParts(parts[0], parts[1])
	}

	// Windows form: "<filename> - <workspace> - <appName>". VSCode /
	// Cursor / Windsurf use a regular ASCII hyphen with spaces, AND
	// append the app name as the trailing segment. We strip that
	// trailing app name first so the remaining " - " split is safe.
	for _, suffix := range []string{
		" - Cursor",
		" - Visual Studio Code",
		" - Code",
		" - Windsurf",
		" - Trae",
	} {
		if strings.HasSuffix(t, suffix) {
			t = strings.TrimSuffix(t, suffix)
			if parts := strings.SplitN(t, " - ", 2); len(parts) == 2 {
				return finalizeCodeEditorParts(parts[0], parts[1])
			}
			// No workspace segment — title is just "<filename> - App".
			return strings.TrimSpace(t), ""
		}
	}

	return "", ""
}

// finalizeCodeEditorParts strips the " [Workspace]" decoration VSCode
// adds when multi-root workspaces are open, and trims whitespace.
func finalizeCodeEditorParts(filename, workspace string) (string, string) {
	filename = strings.TrimSpace(filename)
	workspace = strings.TrimSpace(workspace)
	if idx := strings.Index(workspace, " ["); idx > 0 {
		workspace = workspace[:idx]
	}
	return filename, workspace
}
