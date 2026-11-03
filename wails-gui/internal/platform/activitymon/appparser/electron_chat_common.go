package appparser

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
)

// ElectronChatSpec configures a per-app Chromium chat parser. Slack,
// Discord, Feishu, Teams, and similar Electron chat clients share the
// same underlying shape:
//
//   - the WebView wraps a virtualized message list (HTML <ol> → AXList
//     or AXGroup, depending on app);
//   - each message is a per-row wrapper (AXGroup with app-specific
//     identifying class/identifier) holding a sender, timestamp, and
//     body;
//   - Chromium gates the WebView AX tree until something sets
//     AXEnhancedUserInterface=true on the application element (handled
//     in tree_walk_app_to_buf — see appparser README).
//
// Per-app code only differs in how to locate the message wrappers,
// how to pluck fields out of each wrapper, and how to derive the
// channel/DM name. Everything else (gated-tree detection + sentinel,
// dedup, body rendering, metadata) is the same.
type ElectronChatSpec struct {
	// AppName is written to Metadata["app"] and used in log lines. Short
	// lowercase identifier — e.g. "slack", "discord".
	AppName string

	// FindMessageGroups returns each per-message wrapper inside the
	// active window in document order. The driver iterates these
	// linearly. Returning an empty slice triggers AX-gated detection.
	FindMessageGroups func(window *AXNode) []*AXNode

	// ParseMessage extracts sender/timestamp/content from a single
	// group. `lastSender` carries the sender of the most recently
	// emitted message so consecutive same-sender messages (which most
	// chat apps render with no repeated header) can inherit a sender.
	// Return nil to skip a row (dividers, attachment-only continuations,
	// unparseable junk).
	ParseMessage func(group *AXNode, lastSender string) *ConversationMessage

	// ResolveChannel returns (name, isChannel). When isChannel is true
	// the driver prefixes the rendered Channel with "#". Empty name is
	// allowed — the snapshot will simply ship without a channel label.
	ResolveChannel func(window *AXNode, windowTitle string) (string, bool)

	// PostProcess optionally reorders / filters the deduped message
	// list. Slack uses this to lift thread replies under their parents.
	// Most apps leave it nil.
	PostProcess func(messages []ConversationMessage, window *AXNode) []ConversationMessage
}

// ElectronChatParseTree drives the common Chromium chat parsing flow:
// locate window, run spec.FindMessageGroups, fall back to a typed
// AX-gated sentinel when the WebView tree is empty, parse + dedup each
// message, resolve the channel name, and emit a ParsedContent.
//
// Returns nil when the tree contains no parseable chat content AND the
// tree is not chrome-only — callers can then fall back to raw AX text.
func ElectronChatParseTree(spec *ElectronChatSpec, tree *AXNode, bundleID, windowTitle string) *ParsedContent {
	if spec == nil || tree == nil {
		return nil
	}
	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	var groups []*AXNode
	if spec.FindMessageGroups != nil {
		groups = spec.FindMessageGroups(window)
	}
	if len(groups) == 0 {
		if chromiumTreeIsAXGated(window) {
			chromiumLogAXGatedOnce(spec.AppName, bundleID)
			return electronChatGatedSentinel(spec.AppName, bundleID, windowTitle)
		}
		return nil
	}

	var msgs []ConversationMessage
	seen := make(map[string]struct{})
	lastSender := ""
	for _, g := range groups {
		if spec.ParseMessage == nil {
			break
		}
		m := spec.ParseMessage(g, lastSender)
		if m == nil {
			continue
		}
		key := electronChatDedupKey(*m)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		msgs = append(msgs, *m)
		if m.Sender != "" {
			lastSender = m.Sender
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	if spec.PostProcess != nil {
		msgs = spec.PostProcess(msgs, window)
	}

	channelName, isChannel := "", false
	if spec.ResolveChannel != nil {
		channelName, isChannel = spec.ResolveChannel(window, windowTitle)
	}
	if isChannel && channelName != "" && !strings.HasPrefix(channelName, "#") {
		channelName = "#" + channelName
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       windowTitle,
		Channel:     channelName,
		Messages:    msgs,
		Body:        renderConversationBody(msgs),
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      spec.AppName,
		},
	}
}

// electronChatDedupKey collapses messages that appear in multiple panes
// (e.g. Slack's main channel + open thread sidebar). Sender + timestamp
// is unique per channel for human-authored messages; bots that rapid-fire
// can share timestamps to the second, so we mix in a short content prefix
// as a tie-breaker.
func electronChatDedupKey(m ConversationMessage) string {
	c := m.Content
	if len(c) > 32 {
		c = c[:32]
	}
	return m.Sender + "|" + m.Timestamp + "|" + c
}

// chromiumChromeClasses lists the DOM classes that appear in an
// Electron/Chromium NSWindow's native chrome subtree — the only nodes
// exposed when the WebView accessibility tree is suppressed.
//
// Empirically verified by ax-tree-dump against Slack and Chrome on
// macOS 14 / 15. New chrome variants may appear in future Chromium
// releases; the gated-detection is conservative — encountering any
// other class is treated as proof the WebView is awake.
var chromiumChromeClasses = map[string]bool{
	"":                   true, // unset class — bare AXGroup wrapper
	"RootView":           true,
	"NonClientView":      true,
	"NativeFrameViewMac": true,
	"ClientView":         true,
	"View":               true,
}

// chromiumTreeIsAXGated reports whether the window subtree only contains
// the native chrome DOM classes — the signature of a Chromium-based app
// whose WebView accessibility tree has not been primed.
//
// Detection walks the tree once and short-circuits on the first sign of
// WebView content: a role that only appears inside the rendered DOM
// (AXScrollArea/AXList/AXSplitGroup/AXWebArea/AXStaticText/AXButton/
// AXLink) or any DOM class outside chromiumChromeClasses.
func chromiumTreeIsAXGated(window *AXNode) bool {
	if window == nil {
		return false
	}
	webViewSignal := false
	Traverse(window, func(n *AXNode) bool {
		switch n.Role {
		case "AXScrollArea", "AXList", "AXSplitGroup", "AXWebArea",
			"AXStaticText", "AXButton", "AXLink":
			webViewSignal = true
			return false
		}
		for _, cls := range n.DOMClassList {
			if !chromiumChromeClasses[cls] {
				webViewSignal = true
				return false
			}
		}
		return true
	})
	return !webViewSignal
}

// chromiumAXGatedLogged tracks per-app one-shot logging so the "tree is
// gated" warning surfaces exactly once per process per Electron app even
// though ParseTree runs ~1 Hz while the app is foreground.
var chromiumAXGatedLogged sync.Map // appName -> *uint32

// chromiumLogAXGatedOnce emits a one-shot warning for an Electron app
// whose WebView tree is unreachable. Greppable in production logs
// ("[appparser/<app>] WebView AX tree is gated").
func chromiumLogAXGatedOnce(app, bundleID string) {
	flagAny, _ := chromiumAXGatedLogged.LoadOrStore(app, new(uint32))
	flag := flagAny.(*uint32)
	if !atomic.CompareAndSwapUint32(flag, 0, 1) {
		return
	}
	log.Printf("[appparser/%s] WebView AX tree is gated by Electron "+
		"(only native chrome — RootView/NonClientView/NativeFrameViewMac "+
		"— is exposed). %s content will not be parsed unless VoiceOver "+
		"runs or tree_walk_app_to_buf primes AXEnhancedUserInterface "+
		"successfully. Run cmd/ax-tree-dump --bundle %s to confirm. "+
		"This message logs once per app per process.", app, app, bundleID)
}

// electronChatGatedSentinel returns the typed ParsedContent used when an
// Electron chat app's WebView tree is gated. Lets the activity pipeline
// record "user is in <app> but content is unreadable" rather than
// silently treating the snapshot as a parser-miss indistinguishable from
// a generic web page.
func electronChatGatedSentinel(app, bundleID, windowTitle string) *ParsedContent {
	return &ParsedContent{
		ContentType: "conversation",
		Title:       windowTitle,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      app,
			"axGated":  "true",
		},
	}
}
