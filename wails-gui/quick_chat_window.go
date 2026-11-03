package main

import (
	_ "embed"
	"sync"
)

// quickChatHTMLDoc is the full HTML document rendered inside the
// Quick Chat floating panel. It is shared across platforms: macOS
// loads it into a WKWebView (see quick_chat_window_darwin.go); Windows
// loads the same bytes into a WebView2 host (see
// quick_chat_window_windows.go).
//
// Keep this file build-tag free so the asset embeds into every binary
// — the platform-specific window shells consume it.
//
//go:embed quick-chat-window-assets/quick-chat.html
var quickChatHTMLDoc string

// quickChatWindow* state lives at package scope because the JS bridge
// callbacks (submit / close / request-suggestions) need to reach the
// running App from a thread that isn't necessarily the main goroutine,
// and they only carry the user-typed prompt — not the focused-app
// bundleID we showed the panel for. The bundle ID and App pointer are
// stashed here when ShowQuickChatWindow is called and read back when
// the user clicks a chip.
var (
	quickChatWindowMu     sync.Mutex
	quickChatWindowApp    *App
	quickChatWindowOpen   bool
	lastQuickChatBundleID string
)

// QuickChatSubmitEvent is the payload emitted to the renderer when
// the user submits a prompt from the floating panel. The renderer
// uses it to open a fresh standalone-chat session and immediately
// send the prompt.
type QuickChatSubmitEvent struct {
	Prompt            string `json:"prompt"`
	FrontmostBundleID string `json:"frontmostBundleId,omitempty"`
	FrontmostAppName  string `json:"frontmostAppName,omitempty"`
}
