package appparser

import (
	"sort"
	"strings"
)

// webChatSpec parameterizes the shared driver that turns an AX web tree
// for an AI chat site (ChatGPT, Claude.ai, …) into a structured
// ConversationMessage list. Each supported site fills in a spec at
// init() time; the driver itself is locale-agnostic and assumes only
// that messages are identifiable by a stable DOM class.
type webChatSpec struct {
	// AppName tags the parser's output in metadata.app so consumers can
	// distinguish "chatgpt-web" snapshots from "claude-web" ones.
	AppName string

	// Hosts are the URL hosts (passed to hostMatches) where this spec
	// should be applied. Supports the same glob shape as other URL
	// matchers (e.g. "claude.ai", "*.example.com").
	Hosts []string

	// UserMessageClass / AgentMessageClass identify the DOM containers
	// for user input and assistant output respectively. Substring match
	// is used (HasClassContaining), so specs can ignore Tailwind's "!"
	// importance prefix and other decorations on the class string —
	// Claude tags user messages as "!font-user-message" with the
	// importance bang, ChatGPT writes "agent-turn" plain. Both work as
	// substring matches against the stripped target.
	UserMessageClass  string
	AgentMessageClass string

	// AgentSender is the sender label used in ConversationMessage.Sender
	// for assistant replies ("ChatGPT", "Claude", "Gemini", …). The user
	// side is always "User" for consistency across web chat parsers —
	// the spec doesn't need to vary it.
	AgentSender string

	// TitleSuffix is an optional suffix to strip from the AXWebArea
	// title before exposing it as ParsedContent.Title. ChatGPT writes
	// the bare conversation name; Claude appends " - Claude" so we
	// strip it to recover the conversation name in clean form.
	TitleSuffix string
}

// webChatDriver runs spec against a parsed AX tree. It locates message
// containers by class, sorts them top-to-bottom by frame.y (the only
// reliable ordering signal — DOM order can be perturbed by virtualized
// scroll containers in some chat UIs), and renders each into a
// ConversationMessage. Returns nil when no messages are present so the
// dispatcher can fall back to the generic browser parser.
func webChatDriver(tree *AXNode, spec webChatSpec, bundleID, url string) *ParsedContent {
	if tree == nil {
		return nil
	}

	webArea := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if webArea == nil {
		return nil
	}

	type taggedTurn struct {
		node   *AXNode
		sender string
		y      float64
	}
	var turns []taggedTurn
	Traverse(webArea, func(n *AXNode) bool {
		if n == nil {
			return false
		}
		// Important: when a message container matches, prune the
		// traversal at that node. Both ChatGPT and Claude nest the
		// agent turn's class around inner user-bubble fragments
		// (citations and tool-call cards inherit the parent class
		// space), so a non-pruning walk would double-count those as
		// extra user turns.
		switch {
		case HasClassContaining(n, spec.UserMessageClass):
			turns = append(turns, taggedTurn{node: n, sender: "User", y: webChatNodeY(n)})
			return false
		case HasClassContaining(n, spec.AgentMessageClass):
			turns = append(turns, taggedTurn{node: n, sender: spec.AgentSender, y: webChatNodeY(n)})
			return false
		}
		return true
	})

	sort.SliceStable(turns, func(i, j int) bool { return turns[i].y < turns[j].y })

	var msgs []ConversationMessage
	for _, t := range turns {
		content := webChatCollectText(t.node)
		if content == "" {
			continue
		}
		msgs = append(msgs, ConversationMessage{
			Sender:  t.sender,
			Content: content,
		})
	}
	if len(msgs) == 0 {
		return nil
	}

	title := strings.TrimSpace(webArea.Title)
	if spec.TitleSuffix != "" && strings.HasSuffix(title, spec.TitleSuffix) {
		title = strings.TrimSpace(strings.TrimSuffix(title, spec.TitleSuffix))
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      spec.AppName,
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       title,
		URL:         url,
		Messages:    msgs,
		Body:        renderConversationBody(msgs),
		Metadata:    meta,
	}
}

// webChatNodeY returns a node's screen-y position for chronological
// sorting. Falls back to 0 when the node has no Frame (off-screen or
// missing in older AX builds), which puts unframed turns first — these
// are usually system-injected placeholders that aren't real messages
// and get filtered later by the empty-content check.
func webChatNodeY(n *AXNode) float64 {
	if n == nil || n.Frame == nil {
		return 0
	}
	return n.Frame.Y
}

// webChatCollectText concatenates all visible AXStaticText under a
// message turn. Skips scrollbars and action toolbars (which use the
// opacity-0 + pointer-events-none Tailwind classes both sites use for
// hover-revealed reply/copy buttons) — those are chrome inside the
// message bubble, not content.
func webChatCollectText(root *AXNode) string {
	var parts []string
	Traverse(root, func(n *AXNode) bool {
		if n == nil {
			return false
		}
		if n.Role == "AXScrollBar" {
			return false
		}
		// Hover-revealed action toolbars carry an opacity-0 +
		// pointer-events-none class pair; skip those subtrees so
		// "Copy" / "Edit" / "回复操作" don't leak into the body.
		if HasClassContaining(n, "pointer-events-none") &&
			HasClassContaining(n, "opacity-0") {
			return false
		}
		if n.Role != "AXStaticText" {
			return true
		}
		v := strings.TrimSpace(n.Value)
		if v == "" {
			v = strings.TrimSpace(n.Description)
		}
		if v != "" {
			parts = append(parts, v)
		}
		return true
	})
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
