package appparser

import (
	"regexp"
	"strings"
)

var slackBundleIDs = map[string]bool{
	"com.tinyspeck.slackmacgap": true,
}

const slackAppName = "slack"

type slackParser struct{}

func init() {
	Register(&slackParser{})
}

func (p *slackParser) CanParse(bundleID string) bool {
	return slackBundleIDs[bundleID]
}

func (p *slackParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        slackAppName,
		Label:       "Slack",
		ContentType: "conversation",
		Bundles:     []string{"com.tinyspeck.slackmacgap"},
	}
}

// ParseTree extracts the active Slack channel/DM and its message list.
// Mirrors LittleBird's slack_parser.js approach:
//   - find AXWindow, then AXList with AXContentList subrole (message list)
//   - derive channel/DM name from header or list description
//   - each message is an AXGroup with AXDocument subrole
//   - sender is in a button with class "c-message__sender_button"
//   - timestamp is in a link with class "c-timestamp"
func (p *slackParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree // fall through for popouts
	}

	// Slack publishes TWO `AXList[AXContentList]` nodes per visible message
	// pane: a 1×1 `sr-only` marker (channel name in Description, no
	// children) and the real virtual-scrolled message column. When the
	// user opens a thread, a SECOND pane appears alongside the main
	// channel — the sidebar — with its own pair of marker + real list.
	// We collect every "real" AXContentList (non-trivial frame or
	// non-empty children) so messages from both panes are captured.
	messageLists := FindAll(window, func(n *AXNode) bool {
		if n.Role != "AXList" || n.Subrole != "AXContentList" {
			return false
		}
		if n.Description == "" {
			return false
		}
		// Skip the sr-only marker by requiring a non-trivial frame OR
		// at least one child — the real message-column AXContentList
		// has children, the sr-only marker does not.
		if len(n.Children) > 0 {
			return true
		}
		if n.Frame != nil && (n.Frame.W > 4 || n.Frame.H > 4) {
			return true
		}
		return false
	})
	// Fallback: scan the whole window for AXGroup[AXDocument] message
	// wrappers. We use the window itself as the implicit list.
	if len(messageLists) == 0 {
		if FindFirst(window, func(n *AXNode) bool {
			return n.Role == "AXGroup" && n.Subrole == "AXDocument"
		}) != nil {
			messageLists = []*AXNode{window}
		}
	}
	if len(messageLists) == 0 {
		// Distinguish two failure modes:
		//   (a) Slack's WebView AX is gated (Electron suppresses the
		//       message-list subtree until VoiceOver enables it). The
		//       AX tree we get only contains native chrome — RootView /
		//       NonClientView / NativeFrameViewMac. ax-tree-dump
		//       confirms this for current Slack builds.
		//   (b) Wrong window / not on a channel view. Tree has WebView
		//       content but no AXContentList.
		// In case (a), return a typed sentinel via the shared electron
		// chat helper so the activity pipeline can record "user is in
		// Slack but content is unreadable" rather than silently
		// treating Slack snapshots as parser-misses indistinguishable
		// from generic web pages.
		if chromiumTreeIsAXGated(window) {
			chromiumLogAXGatedOnce(slackAppName, bundleID)
			return electronChatGatedSentinel(slackAppName, bundleID, windowTitle)
		}
		return nil
	}

	// For channel-name resolution prefer a list whose description
	// looks like a real channel/DM (no "Thread in …" prefix) — when a
	// thread sidebar is open the main pane often loses its
	// AXContentList wrapper, leaving only the thread's list visible.
	// Falling back to that list's description would mislabel the
	// snapshot as "Thread in pd-test"; the slackConversationInfo
	// header / window-title fallbacks recover the real channel.
	mainList := slackPickMainList(messageLists)
	channel, isChannel := slackConversationInfo(window, mainList, windowTitle)

	// Always scan the entire window for messages, regardless of which
	// AXContentList we matched. With a thread sidebar open Slack
	// restructures the main channel pane to drop its AXContentList
	// wrapper — messages then live directly under
	// `c-scrollbar__hider`, invisible to a per-list walk. Walking the
	// window covers main channel + sidebar + popouts uniformly. Thread
	// sidebars repeat the parent message in the main pane, so dedup
	// by (sender, timestamp, content-prefix) — the timestamp link
	// Description is second-precision and stable across panes.
	rawMessages := slackParseMessages(window)

	// Walk the thread sidebar (if present) separately so we can tag
	// each reply with its parent. The sidebar's first message is
	// always the thread parent (also present in the main channel —
	// dedup will collapse it), and every subsequent message in the
	// sidebar is a reply. Without this pass the sidebar's contents
	// land in the merged list as if they were standalone top-level
	// messages, which is what the user is seeing on the activity
	// detail panel today.
	threadParentRef := ""
	threadReplyKeys := make(map[string]struct{})
	for _, l := range messageLists {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(l.Description)), "thread") {
			continue
		}
		threadMsgs := slackParseMessages(l)
		if len(threadMsgs) < 2 {
			continue
		}
		parent := threadMsgs[0]
		threadParentRef = parent.Sender + "@" + parent.Timestamp
		for _, r := range threadMsgs[1:] {
			threadReplyKeys[slackMessageKey(r)] = struct{}{}
		}
		break
	}

	var messages []ConversationMessage
	seen := make(map[string]struct{})
	for _, m := range rawMessages {
		key := slackMessageKey(m)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, isReply := threadReplyKeys[key]; isReply && threadParentRef != "" {
			m.ThreadParent = threadParentRef
		}
		messages = append(messages, m)
	}
	if len(messages) == 0 {
		return nil
	}

	// Move each thread reply directly under its parent so the sequence
	// reads top-to-bottom as "<parent>, <reply>, <reply>, ..." instead
	// of leaking the replies to the bottom of the message list. The
	// merged window scan emits replies in whatever DOM order Slack used
	// (parent in main pane, replies in sidebar listed afterwards), so
	// this reorder is what actually surfaces the parent-reply
	// relationship in the rendered body.
	messages = slackReorderRepliesUnderParents(messages)

	channelLabel := channel
	if isChannel && !strings.HasPrefix(channelLabel, "#") {
		channelLabel = "#" + channelLabel
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       windowTitle,
		Channel:     channelLabel,
		Messages:    messages,
		Body:        renderConversationBody(messages),
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "slack",
		},
	}
}

// slackConversationInfo returns (name, isChannel). Prefers the structural
// header label if present; falls back to the list description, then to the
// window title.
func slackConversationInfo(window, messageList *AXNode, windowTitle string) (string, bool) {
	// Header-based (most reliable).
	header := FindFirst(window, func(n *AXNode) bool {
		if n.Role != "AXStaticText" && n.Role != "AXButton" {
			return false
		}
		if n.Value == "" {
			return false
		}
		if HasClassContaining(n, "p-view_header") || HasClassContaining(n, "c-channel_name") {
			return true
		}
		if n.Identifier != "" && (strings.Contains(n.Identifier, "title") || strings.Contains(n.Identifier, "channel")) {
			return true
		}
		return false
	})
	if header != nil {
		name := strings.TrimSpace(header.Value)
		name = stripTrailingParenthetical(name)
		if name != "" {
			isChannel := strings.HasPrefix(name, "#")
			name = strings.TrimLeft(name, "# ")
			return name, isChannel
		}
	}

	// List-description-based. Skip thread sidebar descriptions like
	// "Thread in pd-test (channel, 1 reply)" — when a thread sidebar
	// is open, the main channel pane often loses its AXContentList
	// wrapper, so the only candidate `messageList` we can find is the
	// thread sidebar's. Treating its description as the channel name
	// would mislabel the snapshot. Falling through here lets the
	// window-title fallback below recover the real channel name from
	// "pd-test (Channel) - Isoform - 4 new items - Slack".
	if messageList != nil && messageList.Description != "" &&
		!strings.HasPrefix(strings.ToLower(strings.TrimSpace(messageList.Description)), "thread") {
		desc := messageList.Description
		lower := strings.ToLower(desc)
		isChannel := strings.Contains(lower, "channel")
		name := desc
		if i := strings.Index(lower, "(direct message"); i >= 0 {
			name = desc[:i]
		} else if i := strings.Index(lower, "(channel"); i >= 0 {
			name = desc[:i]
		}
		name = strings.TrimSpace(name)
		name = strings.TrimLeft(name, "# ")
		if name != "" {
			return name, isChannel
		}
	}

	// Window title fallback: "engineering (Channel) - Workspace - Slack".
	// Try the caller-supplied windowTitle first (production capture path
	// has it from FrontmostAppInfo); fall back to the AXWindow node's
	// own Title (carried in the AX tree itself) so the parser stays
	// useful when invoked without an external windowTitle — e.g. from
	// cmd/ax-tree-dump in --bundle mode.
	candidate := windowTitle
	if candidate == "" && window != nil {
		candidate = window.Title
	}
	if candidate != "" {
		left := strings.Split(candidate, " - ")[0]
		left = stripTrailingParenthetical(left)
		lower := strings.ToLower(candidate)
		isChannel := strings.Contains(lower, "(channel)")
		name := strings.TrimLeft(strings.TrimSpace(left), "# ")
		if name != "" {
			return name, isChannel
		}
	}
	return "", false
}

// slackReorderRepliesUnderParents takes the merged message list and
// produces a sequence where every thread reply immediately follows its
// parent. Order between top-level messages is preserved; within a
// thread, replies keep their original relative order. Replies whose
// parent is missing from the list (e.g. parent scrolled off-screen)
// fall back to the end of the sequence so the data isn't lost.
func slackReorderRepliesUnderParents(msgs []ConversationMessage) []ConversationMessage {
	repliesByParent := make(map[string][]ConversationMessage)
	var topLevel []ConversationMessage
	for _, m := range msgs {
		if m.ThreadParent != "" {
			repliesByParent[m.ThreadParent] = append(repliesByParent[m.ThreadParent], m)
			continue
		}
		topLevel = append(topLevel, m)
	}
	out := make([]ConversationMessage, 0, len(msgs))
	used := make(map[string]bool)
	for _, p := range topLevel {
		out = append(out, p)
		ref := p.Sender + "@" + p.Timestamp
		if replies, ok := repliesByParent[ref]; ok {
			out = append(out, replies...)
			used[ref] = true
		}
	}
	for ref, replies := range repliesByParent {
		if !used[ref] {
			out = append(out, replies...)
		}
	}
	return out
}

// slackMessageKey is a thin alias over the shared electron chat dedup
// key — kept for call-site readability inside slack.go.
func slackMessageKey(m ConversationMessage) string {
	return electronChatDedupKey(m)
}

// slackPickMainList chooses the AXContentList whose description best
// represents the active channel / DM context. Thread sidebars publish a
// description like "Thread in pd-test (channel, 1 reply)" — using that
// as the channel label would falsely tag every snapshot as a thread
// view. We prefer a list whose description does NOT start with
// "Thread", falling back to the largest-frame list when nothing else
// fits (e.g. a thread popout where there's no main pane in the tree).
func slackPickMainList(lists []*AXNode) *AXNode {
	if len(lists) == 0 {
		return nil
	}
	for _, l := range lists {
		if l == nil {
			continue
		}
		desc := strings.TrimSpace(l.Description)
		if !strings.HasPrefix(strings.ToLower(desc), "thread") {
			return l
		}
	}
	main := lists[0]
	for _, l := range lists[1:] {
		if listFrameArea(l) > listFrameArea(main) {
			main = l
		}
	}
	return main
}

// listFrameArea returns the on-screen area of a message-list node (0 when
// frame is missing). Used by slackPickMainList to break ties between
// candidate channel-name sources.
func listFrameArea(n *AXNode) float64 {
	if n == nil || n.Frame == nil {
		return 0
	}
	return n.Frame.W * n.Frame.H
}

func stripTrailingParenthetical(s string) string {
	// Remove a trailing "(...)" segment, e.g. "Alex (DM)" -> "Alex"
	if i := strings.LastIndex(s, "("); i >= 0 {
		if j := strings.Index(s[i:], ")"); j >= 0 && i+j == len(s)-1 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// slackParseMessages walks the message list and extracts each message group.
//
// Two passes for resilience:
//
//  1. Prefer `c-virtual_list__item` AXGroup containers — these are the
//     per-message wrappers Slack uses, and crucially each one carries a
//     screen-reader summary in its `Title` attribute that holds the
//     full message text (e.g. "Pd: @yansu hello. 12:51 PM. 1 reply.").
//     We need this because Slack/Chromium suppress AXStaticText
//     children of `p-rich_text_section` for visible messages that
//     have thread replies — the structured walk returns no body in
//     that case, but the wrapper title is always populated.
//
//  2. Fallback to direct `AXGroup[AXDocument]` walk for trees where
//     the virtual_list__item wrapper isn't present (popouts, search
//     results, threads sidebar — each has its own DOM scaffolding).
//
// The container's title is passed to slackParseMessageGroup as a
// content fallback; the structured rich-text extraction still wins
// when it succeeds, since it preserves inline formatting that the
// title strips.
func slackParseMessages(list *AXNode) []ConversationMessage {
	items := FindAll(list, func(n *AXNode) bool {
		if n.Role != "AXGroup" || !HasClassContaining(n, "c-virtual_list__item") {
			return false
		}
		// Filter out non-message virtual list items (day dividers, sticky
		// headers, the empty trailing item below the latest message). A
		// real message contains a c-message_kit__hover AXDocument.
		return FindFirst(n, func(c *AXNode) bool {
			return c.Role == "AXGroup" && c.Subrole == "AXDocument"
		}) != nil
	})

	type entry struct {
		group     *AXNode
		itemTitle string
	}
	var pairs []entry
	if len(items) > 0 {
		for _, it := range items {
			grp := FindFirst(it, func(c *AXNode) bool {
				return c.Role == "AXGroup" && c.Subrole == "AXDocument"
			})
			if grp != nil {
				pairs = append(pairs, entry{group: grp, itemTitle: it.Title})
			}
		}
	} else {
		// Fallback path: no wrapper containers visible (popouts /
		// search results / thread sidebar). Walk for AXDocument
		// directly and accept that titleFallback is unavailable.
		groups := FindAll(list, func(n *AXNode) bool {
			return n.Role == "AXGroup" && n.Subrole == "AXDocument"
		})
		for _, g := range groups {
			pairs = append(pairs, entry{group: g})
		}
	}

	var msgs []ConversationMessage
	var lastSender string
	for _, p := range pairs {
		m := slackParseMessageGroup(p.group, lastSender, p.itemTitle)
		if m == nil {
			continue
		}
		msgs = append(msgs, *m)
		if m.Sender != "" {
			lastSender = m.Sender
		}
	}
	return msgs
}

func slackParseMessageGroup(group *AXNode, lastSender, titleFallback string) *ConversationMessage {
	// Sender button
	senderBtn := FindFirst(group, func(n *AXNode) bool {
		return n.Role == "AXButton" && HasClassContaining(n, "sender_button")
	})
	// Timestamp link
	tsLink := FindFirst(group, func(n *AXNode) bool {
		return n.Role == "AXLink" && HasClassContaining(n, "timestamp")
	})

	sender := ""
	if senderBtn != nil && senderBtn.Title != "" {
		sender = senderBtn.Title
	} else if lastSender != "" && tsLink != nil {
		// Carry over the last sender ONLY when this group has its own
		// timestamp link — otherwise Slack's per-attachment
		// virtual_list__item children (no sender, no timestamp, but
		// nestled below a real message) get falsely attributed to the
		// previous sender and surface as "<lastSender>: . 12:00 PM. 1
		// attachment.". Real consecutive-same-sender messages always
		// keep their own timestamp link.
		sender = lastSender
	} else {
		return nil
	}

	timestamp := ""
	if tsLink != nil {
		if tsLink.Description != "" {
			timestamp = tsLink.Description
		} else {
			tsText := FindFirst(tsLink, func(n *AXNode) bool {
				return n.Role == "AXStaticText" && n.Value != ""
			})
			if tsText != nil {
				timestamp = tsText.Value
			}
		}
	}

	content := slackExtractMessageContent(group, senderBtn, tsLink)
	if slackContentIsBlank(content) {
		// Slack/Chromium suppresses AXStaticText children of
		// `p-rich_text_section` for visible messages with thread
		// activity (verified empirically: same Pd messages parsed
		// fine before yansu auto-replied, then went blank once each
		// gained a "1 reply" indicator). The wrapper title carries
		// the same text as a screen-reader summary, so use it as a
		// last-resort content source.
		if titleFallback != "" {
			fb := slackExtractContentFromTitle(titleFallback, sender)
			if fb != "" {
				content = fb
			}
		}
		if slackContentIsBlank(content) {
			return nil
		}
	}
	return &ConversationMessage{Sender: sender, Content: content, Timestamp: timestamp}
}

// slackContentIsBlank reports whether the extracted content is effectively
// empty — pure whitespace (regular or non-breaking) leaks through when
// Slack's AX tree exposes only the inter-token separators between sender,
// timestamp, and a stripped-out body.
func slackContentIsBlank(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', ' ':
			continue
		}
		return false
	}
	return true
}

// slackTitleReplyRe strips a trailing reply-count clause from a wrapper
// title. Matches both English shapes Slack uses ("1 reply" / "3 replies").
var slackTitleReplyRe = regexp.MustCompile(`\.\s+\d+\s+repl(?:y|ies)\.?\s*$`)

// slackTitleTimestampRe strips a trailing time clause. Slack writes the
// time in 12-hour form for the screen reader regardless of the user's
// display preference, so we don't need to handle 24-hour separately.
var slackTitleTimestampRe = regexp.MustCompile(`\.\s+\d{1,2}:\d{2}\s+(?:AM|PM|am|pm)\.?\s*$`)

// slackExtractContentFromTitle peels the sender prefix and the trailing
// timestamp / reply-count metadata off a wrapper title to recover the
// original message body. Title format observed across Slack 4.x builds:
//
//	"<sender>: <body>. <H:MM AM|PM>.[ <N> repl(y|ies).]"
//
// Returns "" if nothing meaningful remains after stripping (e.g. a
// system-generated title with no body).
func slackExtractContentFromTitle(title, sender string) string {
	t := strings.TrimSpace(title)
	if sender != "" {
		// Slack always writes "<sender>: " for individual messages and
		// "<sender> APP " for app-published messages. Strip whichever
		// matches.
		for _, prefix := range []string{sender + ": ", sender + " APP ", sender + " "} {
			if strings.HasPrefix(t, prefix) {
				t = t[len(prefix):]
				break
			}
		}
	}
	t = slackTitleReplyRe.ReplaceAllString(t, "")
	t = slackTitleTimestampRe.ReplaceAllString(t, "")
	return strings.TrimSpace(t)
}

// slackExtractMessageContent collects all static text in the message subtree,
// skipping the sender button, timestamp link, reactions, and thread metadata.
func slackExtractMessageContent(group, senderBtn, tsLink *AXNode) string {
	skipSet := map[*AXNode]bool{}
	Traverse(group, func(n *AXNode) bool {
		// Reactions
		if HasClassContaining(n, "reaction") || (n.Identifier != "" && strings.Contains(n.Identifier, "reaction")) {
			skipSet[n] = true
		}
		// Reply buttons / thread links
		if n.Role == "AXButton" && (HasClassContaining(n, "reply") || n.Identifier == "reply_button") {
			skipSet[n] = true
		}
		if n.Role == "AXGroup" && (HasClassContaining(n, "thread") || (n.Identifier != "" && strings.Contains(n.Identifier, "thread"))) {
			skipSet[n] = true
		}
		if HasClassContaining(n, "reply_count") {
			skipSet[n] = true
		}
		// Reply-bar wrapper that Slack appends below messages with thread
		// activity (class `c-message__reply_bar_description`). It contains
		// two AXStaticText children — a relative timestamp like
		// "Today at 12:39 PM" and the literal "View thread" — both of
		// which leak into the message body otherwise. Match by partial
		// class name so future Slack rewrites that rename the variant
		// (`__reply_bar`, `__reply_bar_v2`, …) keep being filtered.
		if HasClassContaining(n, "reply_bar") {
			skipSet[n] = true
		}
		return true
	})

	// Helpers to test if a node is inside any skip subtree.
	var texts []string
	var walk func(n *AXNode, inSkip bool)
	walk = func(n *AXNode, inSkip bool) {
		if n == nil {
			return
		}
		if skipSet[n] {
			inSkip = true
		}
		if !inSkip && !nodeIsInsideOfSlackRefs(n, senderBtn, tsLink) {
			if n.Role == "AXStaticText" && n.Value != "" {
				v := n.Value
				if v != "(edited)" && v != "  " && v != " | " && v != "Added by " && v != "View thread" && !strings.HasPrefix(v, "Last reply") {
					texts = append(texts, v)
				}
			}
			// Slack renders @-mentions and #channel links as AXLink
			// nodes (DOM class c-member_slug / c-channel_slug) whose
			// visible text lives in Description, not in any
			// AXStaticText child. Without this branch, "@yansu hello"
			// gets collected as just " hello" — which throws away the
			// "who was pinged?" signal that's usually the most
			// important part of a Slack message. We also pick up
			// plain inline AXLinks (regular hyperlinks) by Description
			// when they have no AXStaticText child to fall back on,
			// which preserves link text in body content.
			//
			// We don't double-count: AXLink nodes that wrap an
			// AXStaticText (e.g. the timestamp link wrapping "12:51 PM")
			// are excluded by the senderBtn/tsLink ref check above. For
			// links that carry text in Description but also have an
			// AXStaticText child, prefer the child to avoid duplication.
			if n.Role == "AXLink" && n.Description != "" {
				hasStaticChild := false
				for _, c := range n.Children {
					if c != nil && c.Role == "AXStaticText" && c.Value != "" {
						hasStaticChild = true
						break
					}
				}
				if !hasStaticChild {
					texts = append(texts, n.Description)
				}
			}
		}
		for _, c := range n.Children {
			walk(c, inSkip)
		}
	}
	walk(group, false)

	content := strings.Join(texts, "")
	content = strings.TrimRight(content, " \t\n\r")
	return content
}

// nodeIsInsideOfSlackRefs walks upward via pointer identity. Since we can't
// walk up in our struct (no parent ref), we do a contains check by traversing
// down from each reference.
func nodeIsInsideOfSlackRefs(n, senderBtn, tsLink *AXNode) bool {
	if senderBtn != nil && subtreeContains(senderBtn, n) {
		return true
	}
	if tsLink != nil && subtreeContains(tsLink, n) {
		return true
	}
	return false
}

func subtreeContains(root, target *AXNode) bool {
	if root == nil || target == nil {
		return false
	}
	if root == target {
		return true
	}
	for _, c := range root.Children {
		if subtreeContains(c, target) {
			return true
		}
	}
	return false
}
