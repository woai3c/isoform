package appparser

import (
	"strings"
	"testing"
)

// TestTerminalParser_CanvasTerminalFallsBackToMetadataOnly covers
// Electron / xterm.js terminals (Termius is the load-bearing example).
// They render the grid to canvas which is invisible to AX, so the
// AXTextArea-search path returns empty and the flat-text walker would
// otherwise leak sidebar chrome (Hosts / Keychain / Snippets) into the
// body. Verify the parser instead emits a metadata-only stub so the
// triager / quick-chat-context route to OCR fusion.
func TestTerminalParser_CanvasTerminalFallsBackToMetadataOnly(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			// Chrome — what Termius actually exposes when the active
			// pane is an SSH session. No AXTextArea anywhere.
			{Role: "AXStaticText", Value: "Hosts"},
			{Role: "AXStaticText", Value: "Keychain"},
			{Role: "AXStaticText", Value: "Snippets"},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.termius-dmg.mac", "Termius - Max Mini", "")
	if parsed == nil {
		t.Fatalf("expected metadata_only stub, got nil")
	}
	if parsed.ContentType != "terminal" {
		t.Errorf("contentType: got %q want terminal", parsed.ContentType)
	}
	if parsed.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage: got %q want %q",
			parsed.Metadata["axCoverage"], AXCoverageMetadataOnly)
	}
	if !IsPartialAX(parsed) {
		t.Error("IsPartialAX should be true so OCR fusion runs")
	}
	for _, leak := range []string{"Hosts", "Keychain", "Snippets"} {
		if strings.Contains(parsed.Body, leak) {
			t.Errorf("chrome leaked into terminal body: %q contains %q",
				parsed.Body, leak)
		}
	}
	if parsed.Body != "Termius - Max Mini" {
		t.Errorf("body: got %q want window title", parsed.Body)
	}
}

// TestTerminalParser_TreeExtractsLongValue reproduces the iTerm2 bug:
// the flat-text walker copies attribute strings through a 1 KiB stack
// buffer, so a real terminal scrollback (multi-KB) is silently dropped
// from axText. The tree walker uses dynamic malloc and keeps the
// Value intact — verify the parser pulls from there.
func TestTerminalParser_TreeExtractsLongValue(t *testing.T) {
	stub := iterm2GetContents
	t.Cleanup(func() { iterm2GetContents = stub })
	iterm2GetContents = func() string { return "" }

	bigValue := strings.Repeat("alex@halcyon ~ $ ls\nfile1 file2\n", 200) // ~7 KB
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXScrollArea",
				Children: []*AXNode{
					{Role: "AXTextArea", Description: "shell", Value: bigValue},
				},
			},
			// Chrome surfaces that the old parser used to surface instead.
			{Role: "AXStaticText", Value: "⌥⌘1"},
			{Role: "AXStaticText", Value: "✳ Check yetone's recent commits in repo"},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.googlecode.iterm2", "Terminal", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "alex@halcyon ~ $ ls") {
		t.Errorf("terminal scrollback not extracted from AXTextArea: %q", parsed.Body)
	}
	if strings.Contains(parsed.Body, "⌥⌘1") {
		t.Errorf("chrome label leaked into terminal body: %q", parsed.Body)
	}
}

// TestTerminalParser_PicksLongestTextArea verifies that when multiple
// AXTextArea nodes coexist (e.g. a search field alongside the screen
// buffer), the parser keeps the screen buffer.
func TestTerminalParser_PicksLongestTextArea(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "find"}, // toolbar search field
			{Role: "AXTextArea", Value: strings.Repeat("real terminal line\n", 50)},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.apple.Terminal", "", "")
	if parsed == nil || !strings.Contains(parsed.Body, "real terminal line") {
		t.Fatalf("expected screen buffer, got %+v", parsed)
	}
	if strings.Contains(parsed.Body, "find") {
		t.Errorf("toolbar search text leaked into body: %q", parsed.Body)
	}
}

// TestTerminalParser_LegacyTextFallback locks in that callers without
// a tree still get useful output via the flat-text path.
func TestTerminalParser_LegacyTextFallback(t *testing.T) {
	parsed := (&terminalParser{}).Parse("user@host ~ $ pwd\n/home/user", "com.apple.Terminal", "Terminal", "")
	if parsed == nil || !strings.Contains(parsed.Body, "/home/user") {
		t.Fatalf("flat-text fallback regressed: %+v", parsed)
	}
}

// TestTerminalParser_ITerm2PrefersAppleScriptOverAX reproduces the
// alt-screen blindness: iTerm2's AX tree only shows the primary
// buffer, so a TUI like Claude Code looks like a stale "$ claude"
// prompt to the parser. The osascript path is supposed to override
// the AX value when it returns more content.
func TestTerminalParser_ITerm2PrefersAppleScriptOverAX(t *testing.T) {
	stub := iterm2GetContents
	defer func() { iterm2GetContents = stub }()
	iterm2GetContents = func() string {
		return "$ claude\nClaude Code v2.1\n❯ hello\n⏺ hi from Claude"
	}
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			// AX exposes only the pre-TUI primary buffer.
			{Role: "AXTextArea", Value: "$ claude"},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.googlecode.iterm2", "iTerm2", "")
	if parsed == nil || !strings.Contains(parsed.Body, "hi from Claude") {
		t.Fatalf("AppleScript content not used: %+v", parsed)
	}
	if !strings.Contains(parsed.Body, "Claude Code v2.1") {
		t.Errorf("body missing TUI body line: %q", parsed.Body)
	}
}

// TestTerminalParser_ITerm2FallsBackToAXOnEmptyAppleScript covers the
// degraded case where osascript fails or times out — we must still
// produce whatever AX gave us instead of returning nil.
func TestTerminalParser_ITerm2FallsBackToAXOnEmptyAppleScript(t *testing.T) {
	stub := iterm2GetContents
	defer func() { iterm2GetContents = stub }()
	iterm2GetContents = func() string { return "" }
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "$ pwd\n/home/user"},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.googlecode.iterm2", "", "")
	if parsed == nil || !strings.Contains(parsed.Body, "/home/user") {
		t.Fatalf("AX fallback regressed: %+v", parsed)
	}
}

// TestTerminalParser_NonITermDoesNotCallAppleScript guards against
// accidentally invoking osascript for other terminals — they don't
// have the iTerm2 dictionary and we'd waste a process per capture.
func TestTerminalParser_NonITermDoesNotCallAppleScript(t *testing.T) {
	stub := iterm2GetContents
	defer func() { iterm2GetContents = stub }()
	called := false
	iterm2GetContents = func() string { called = true; return "should-not-appear" }

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "$ uname"},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.apple.Terminal", "", "")
	if parsed == nil || strings.Contains(parsed.Body, "should-not-appear") {
		t.Fatalf("non-iTerm path should not consult AppleScript: %+v", parsed)
	}
	if called {
		t.Errorf("iterm2GetContents was invoked for bundleID com.apple.Terminal")
	}
}

// TestTerminalParser_TrimsToLast100Lines covers the existing visible-
// area truncation contract.
func TestTerminalParser_TrimsToLast100Lines(t *testing.T) {
	var sb strings.Builder
	for range 250 {
		sb.WriteString("line ")
		sb.WriteString("X\n")
	}
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: sb.String()},
		},
	}
	parsed := (&terminalParser{}).ParseTree(tree, "com.googlecode.iterm2", "", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if got := strings.Count(parsed.Body, "\n") + 1; got > 100 {
		t.Errorf("output should be capped at 100 lines, got %d", got)
	}
}
