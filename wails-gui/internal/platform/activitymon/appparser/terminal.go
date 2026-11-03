package appparser

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"wails-gui/internal/platform/nativeprompt"
)

// terminalBundleIDs lists known terminal emulator bundle identifiers.
//
// Termius (com.termius-dmg.mac) is registered here so the parser
// claims the bundle and emits its AXCoverageMetadataOnly stub
// (see ParseTree's fallback below) instead of leaving CanParse=false
// — which would route the snapshot through the generic flat-text
// path and surface Termius's sidebar nav ("Hosts" / "Keychain" /
// "Snippets" / ...) as the "terminal content", misleading downstream
// consumers.
//
// The AXTextArea screen-buffer path further down does NOT extract
// anything for Termius: the terminal grid is an xterm.js <canvas>,
// invisible to the macOS AX bridge regardless of the
// AXManualAccessibility / AXEnhancedUserInterface prime flags.
// Verified 2026-05-25 against an open SSH session (Termius - Max
// Mini, ~80 nodes, max depth 12): only the Vaults sidebar nav and
// the active tab title reach AX; every line of terminal output
// (pwd / cd / ls listing / file names visible on screen) was absent
// from the dump. The downstream MergeWithOCR splice is the only
// reliable source for that content — same architectural limit as
// VSCode / Cursor's Monaco editor.
var terminalBundleIDs = map[string]bool{
	"com.apple.Terminal":     true,
	"com.googlecode.iterm2":  true,
	"com.mitchellh.ghostty":  true,
	"dev.warp.Warp-Stable":   true,
	"dev.warp.Warp":          true,
	"co.zeit.hyper":          true,
	"com.github.wez.wezterm": true,
	"net.kovidgoyal.kitty":   true,
	"com.cmuxterm.app":       true,
	"com.termius-dmg.mac":    true,
}

type terminalParser struct{}

func init() {
	Register(&terminalParser{})
}

func (p *terminalParser) CanParse(bundleID string) bool {
	return terminalBundleIDs[bundleID]
}

func (p *terminalParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "terminal",
		Label:       "Terminal emulators",
		ContentType: "terminal",
		Bundles: []string{
			"co.zeit.hyper",
			"com.apple.Terminal",
			"com.cmuxterm.app",
			"com.github.wez.wezterm",
			"com.googlecode.iterm2",
			"com.mitchellh.ghostty",
			"com.termius-dmg.mac",
			"dev.warp.Warp",
			"dev.warp.Warp-Stable",
			"net.kovidgoyal.kitty",
		},
	}
}

// iterm2BundleID is the bundle identifier for iTerm2. Pulled out as a
// constant because the terminal parser has a special path for it.
const iterm2BundleID = "com.googlecode.iterm2"

// iterm2GetContents returns the live contents of iTerm2's current
// session (window + tab). Pulled out as a package-level var so tests
// can swap in a deterministic fake without spawning osascript.
//
// iTerm2's AX tree only exposes the primary screen buffer +
// scrollback. When a full-screen TUI takes over (Claude Code, vim,
// htop, less, …) it draws into the alternate screen buffer, which the
// AX bridge never publishes — so the user sees rich TUI content on
// screen but the parser sees a stale "$ claude" prompt. iTerm2's
// AppleScript dictionary exposes `contents of current session`, which
// includes the alternate screen and gives us the live picture.
//
// 800 ms is a defensive cap — osascript normally returns in <100 ms
// even on a busy machine. We'd rather drop the iTerm2 enrichment than
// freeze the capture pipeline if Apple Events stall.
var iterm2GetContents = func() string {
	if !nativeprompt.Allowed() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e",
		`tell application "iTerm2" to tell current session of current window to get contents`)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// ParseTree pulls the terminal screen buffer out of the AX tree.
//
// Terminal emulators expose the live screen + scrollback as a single
// AXTextArea whose Value holds the full text. We can't use the flat-
// text walker for this: that path copies attribute strings through a
// 1 KiB stack buffer, and a typical terminal buffer overflows it and
// gets silently dropped — which is why the old TextParser-only
// implementation returned only iTerm2's chrome ("⌥⌘1", tab title,
// menu strings) and never the prompt. The tree walker uses
// CFStringGetMaximumSizeForEncoding + malloc, so AXTextArea.Value
// survives intact in the structured tree.
//
// Strategy: traverse for AXTextArea and pick the longest Value —
// the screen buffer dwarfs any chrome textarea (URL field, search
// box, etc.) in every terminal app we support.
func (p *terminalParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	var best string
	if tree != nil {
		Traverse(tree, func(n *AXNode) bool {
			if n.Role == "AXTextArea" && len(n.Value) > len(best) {
				best = n.Value
			}
			return true
		})
	}
	// iTerm2's AX tree omits the alternate screen buffer (where TUIs
	// like Claude Code / vim / htop live). Pull session contents via
	// AppleScript and prefer it when it has more text — it includes
	// both the alt buffer and any visible scrollback.
	if bundleID == iterm2BundleID {
		if s := iterm2GetContents(); len(s) > len(best) {
			best = s
		}
	}
	if out := terminalBuildContent(best, bundleID, windowTitle); out != nil {
		return out
	}
	// No buffer extractable via AX. Common for Electron / xterm.js-based
	// terminals like Termius — their canvas-rendered grid is invisible
	// to the macOS AX bridge, and the flat-text walker only picks up
	// sidebar chrome (Hosts / Keychain / Snippets) that would mislead
	// downstream consumers. Hand back a metadata-only stub so the
	// activity triager / quick-chat-context route through
	// MergeWithOCR instead — OCR can read the canvas pixels and is the
	// only reliable source for these apps. Matches the pattern
	// code_editor.go uses for VSCode / Cursor.
	return &ParsedContent{
		ContentType: "terminal",
		Title:       strings.TrimSpace(windowTitle),
		Body:        strings.TrimSpace(windowTitle),
		Metadata: map[string]string{
			"bundleId":   bundleID,
			"axCoverage": AXCoverageMetadataOnly,
		},
	}
}

// Parse is the legacy flat-text fallback. Kept so callers without a
// structured tree (older code paths, edge cases where tree collection
// failed) still produce something usable.
func (p *terminalParser) Parse(axText, bundleID, windowTitle, _ string) *ParsedContent {
	return terminalBuildContent(axText, bundleID, windowTitle)
}

// terminalBuildContent trims to the last 100 lines (the visible
// terminal area) and wraps the result in a ParsedContent.
func terminalBuildContent(raw, bundleID, windowTitle string) *ParsedContent {
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if body == "" {
		return nil
	}
	return &ParsedContent{
		ContentType: "terminal",
		Title:       windowTitle,
		Body:        body,
		Metadata:    map[string]string{"bundleId": bundleID},
	}
}
