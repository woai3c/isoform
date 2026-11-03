package appparser

import (
	"regexp"
	"sort"
	"strings"
)

// feishuBundleIDs covers the macOS Feishu / Lark Electron clients. The
// mainland-China Feishu client is `com.electron.lark`. The international
// Lark client and the App Store Lark build use different IDs and emit a
// different UI language (English placeholders); when those are needed
// the bundle map AND feishuComposePlaceholderRE below must be widened.
var feishuBundleIDs = map[string]bool{
	"com.electron.lark": true,
}

const feishuAppName = "feishu"

// feishuMaxChatRows caps how many chat-list rows we render into the
// inbox summary. The visible list usually shows 8-15 rows on a normal
// display; capping at 30 leaves headroom for users with extra-tall
// windows without flooding the prompt.
const feishuMaxChatRows = 30

// feishuComposePlaceholderRE matches the compose textarea placeholder
// Feishu paints on the bottom of the open conversation pane. The
// trailing zero-width-space + newline sequence is part of the
// AXTextArea.value Feishu emits (`"发送给 X\n​\n​..."`); the
// (?s)/non-greedy capture stops at the first newline so we don't drag
// the placeholder padding into the channel name.
var feishuComposePlaceholderRE = regexp.MustCompile(`^发送给\s+(.+?)\s*(?:\n|$)`)

// feishuChatListItemClass is Lark's stable accessibility-helper class
// on each chat-list row. The hash-suffix siblings (`_1e0a457`,
// `_2a5a61c`, ...) change between releases; this prefix-less name is
// the only one designed to survive bundler churn.
const feishuChatListItemClass = "a11y_feed_card_item"

// feishuReplyMarker is the static text Lark renders under each thread-
// enabled message ("回复话题" — "reply to topic"). Useful as a
// supplementary signal when scoping the conversation pane in tests, but
// not required for production parsing (groups without thread mode never
// emit this label).
const feishuReplyMarker = "回复话题"

// feishuConversationBoilerplate lists exact-match static texts that
// appear inside the open conversation pane as UI chrome rather than
// actual chat content. We filter these out before rendering so the
// `Conversation:` section doesn't carry "welcome to the group" or the
// compose-area keyboard hint as if they were messages.
//
// Each entry is matched after TrimSpace. Keep the list short and
// deterministic — heuristic filtering (e.g. "anything under N chars
// that ends in a verb") would drop real one-word user messages.
var feishuConversationBoilerplate = map[string]bool{
	"欢迎加入群组": true, // Empty-group welcome banner
	"你可以尝试以下操作，快速开始协作": true,
	"置顶重要链接":           true, // Three "Get started" CTAs Lark renders under the banner
	"完善群信息":            true,
	"完善群公告":            true,
	"Shift + Enter 换行": true, // Compose-area keyboard hint at the bottom of the pane
	feishuReplyMarker:  true, // Thread reply marker — surfaced via Message.IsReply instead
}

// feishuDateRE matches the date/time label Lark renders to the right
// of each chat title. We need to discriminate it from the preview
// text so the per-row split puts the preview tail in the right
// bucket. Patterns observed against zh-CN Feishu (2026 builds):
//
//	"4月27日" / "12月3日"          — explicit month/day in this year
//	"昨天"                          — yesterday
//	"星期一" / "星期日"             — weekday within the last week
//	"14:23"                         — same-day clock time
//	"2024年12月3日"                — older threads, full date
//	"2024-12-03" / "2024/12/03"     — alternate locales / Lark Intl
//
// We intentionally keep this Chinese-only for now; switching the bundle
// map to include Lark International requires both adding the English
// equivalents here and widening feishuComposePlaceholderRE.
var feishuDateRE = regexp.MustCompile(
	`^(\d{4}年)?\d{1,2}月\d{1,2}日$|^昨天$|^今天$|^星期[一二三四五六日天]$|^\d{1,2}:\d{2}$|^\d{4}[-/]\d{1,2}[-/]\d{1,2}$`,
)

// feishuChatRow is the structured shape we lift out of each
// a11y_feed_card_item node before rendering. Kept package-private —
// outside callers see only the joined Body string.
type feishuChatRow struct {
	y       float64
	title   string
	badges  []string // e.g. "外部" external-group tag
	date    string
	preview string
}

// feishuConversationLine is one rendered line of the open conversation
// pane. We don't try to reconstruct exact (sender, body) tuples — the
// AX nodes Lark emits don't carry a stable "this is the sender row"
// flag, and the threaded-reply view nests bodies an arbitrary number
// of containers deep. Instead we tag each non-boilerplate static text
// with the date separator that immediately preceded it, then concat
// in tree order. This preserves chronological + per-message reading
// order without depending on the parent layout staying stable across
// Lark releases.
type feishuConversationLine struct {
	date string // date separator that was last seen (cumulative; "" before any)
	text string // the line content (already TrimSpace'd, non-empty)
}

type feishuParser struct{}

func init() {
	Register(&feishuParser{})
}

func (p *feishuParser) CanParse(bundleID string) bool {
	return feishuBundleIDs[bundleID]
}

func (p *feishuParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        feishuAppName,
		Label:       "Feishu",
		ContentType: "conversation",
		Bundles:     []string{"com.electron.lark"},
		Notes: "Lark renders the open chat in a SEPARATE WebView from the " +
			"left chat-list (messenger-chat:default vs messenger). Both " +
			"are DOM-backed and reachable via AX. The parser pulls chat-" +
			"list previews via the a11y_feed_card_item anchor and the " +
			"open-conversation messages via a (compose-textarea + channel-" +
			"heading) dual-signal scope. When both are captured the " +
			"axCoverage flag is dropped (full); when only the chat list is " +
			"reachable (no chat open) it stays chat_list_only and OCR " +
			"fusion splices the missing pane.",
	}
}

// ParseTree extracts a Feishu inbox snapshot.
//
// Strategy:
//  1. Recover the currently open conversation name from the compose
//     textarea placeholder ("发送给 <channel>"). This is the only
//     unambiguous "what is the user looking at" signal — the chat-list
//     row that's highlighted carries an obfuscated CSS-hash class that
//     rotates between Lark releases, so we don't anchor on it.
//  2. Collect every chat-list row via the stable a11y_feed_card_item
//     class anchor. Each row is a small bag of AXStaticText nodes in
//     left-to-right reading order: title, optional badges ("外部"),
//     date, then the preview tail (often fragmented across nodes for
//     `<sender>: <body>` and "@<mention>" rendering).
//  3. Sort rows by frame.y so the output is top-to-bottom on screen.
//  4. Locate the open conversation pane (the second WebView Lark
//     opens as a sibling of the chat list one) and extract its
//     message text. The pane is the smallest subtree that contains
//     BOTH the compose textarea (anchor for "this is the active
//     chat") AND a static-text exactly equal to the channel name
//     (Lark's transcript header). When the user has no chat open the
//     dual signal fails and this step is skipped.
//  5. axCoverage is dropped (full coverage) when the conversation
//     pane was captured. It stays AXCoverageChatListOnly when only
//     the inbox sidebar was reachable — in which case ax_ocr_fusion
//     will splice OCR for the missing pane.
func (p *feishuParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	channel := feishuResolveChannel(tree, windowTitle)
	rows := feishuCollectChatRows(tree)
	if len(rows) > feishuMaxChatRows {
		rows = rows[:feishuMaxChatRows]
	}

	var conversation []feishuConversationLine
	if channel != "" {
		pane := feishuFindConversationPane(tree, channel)
		if pane != nil {
			conversation = feishuExtractConversation(pane, channel)
		}
	}

	if channel == "" && len(rows) == 0 && len(conversation) == 0 {
		return nil
	}

	body := feishuRenderBody(channel, rows, conversation)

	meta := map[string]string{
		"app":      feishuAppName,
		"bundleId": bundleID,
	}
	// Only set the partial-AX flag when we genuinely missed the open
	// conversation. When the pane was captured we ARE full and
	// downstream OCR fusion should skip; otherwise IsPartialAX(p)
	// flips on and the activity pipeline splices OCR in.
	if len(conversation) == 0 {
		meta["axCoverage"] = AXCoverageChatListOnly
	}
	if channel != "" {
		meta["channel"] = channel
	}

	title := channel
	if title == "" {
		title = strings.TrimSpace(windowTitle)
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       title,
		Channel:     channel,
		Body:        body,
		Metadata:    meta,
	}
}

// feishuFindConversationPane returns the smallest subtree that
// represents the open messenger-chat WebView. We identify it via two
// co-occurrent signals only present in this pane:
//
//   - The compose textarea (placeholder "发送给 <channel>"), confirming
//     this subtree IS the active chat surface.
//   - A static-text exactly equal to the channel name, which Lark
//     renders as the transcript header at the top of the pane. The
//     chat-list WebView also contains the channel name but only as
//     part of a longer compound string ("Yansu-Team 4月27日 pd : @ X
//     hello") inside a chat-row card, so the exact-equality check
//     reliably differentiates the two WebViews.
//
// Returns nil when no chat is open (no compose textarea) OR when the
// channel name can't be found verbatim (Lark splash, settings view,
// or an early-render state where the transcript header hasn't
// painted yet).
//
// The smallest-subtree pick matters because the WHOLE window also
// contains both signals — picking the deepest qualifying ancestor
// strips out the chat-list WebView's content from the scope so
// feishuExtractConversation doesn't double-emit chat-row text.
//
// Implementation: single bottom-up pass (O(N)) that bubbles up
// (hasCompose, hasHeading, smallestPane). The naive top-down version
// re-scanned the subtree at every qualifying ancestor, which on Lark's
// deep DOM (~28+ container levels per the TREE_MAX_DEPTH bump in
// axtree_tree_darwin.go) degenerated to roughly O(D × N) and ate into
// the per-snapshot capture budget.
func feishuFindConversationPane(root *AXNode, channel string) *AXNode {
	if root == nil || channel == "" {
		return nil
	}
	_, _, best := feishuConversationPaneSearch(root, channel, false)
	return best
}

// feishuConversationPaneSearch is the bottom-up worker for
// feishuFindConversationPane. For the subtree rooted at n it returns
// whether the subtree contains a compose textarea, whether it contains
// a valid transcript-header text (channel name OUTSIDE any
// a11y_feed_card_item ancestor — sidebar row titles are deliberately
// excluded, see feishuFindConversationPane), and the smallest
// descendant subtree that already contains both signals if any. The
// inChatCard flag carries lineage state (AXNode has no parent links)
// so the heading check can be context-aware on a single pass.
func feishuConversationPaneSearch(n *AXNode, channel string, inChatCard bool) (hasCompose, hasHeading bool, best *AXNode) {
	if n == nil {
		return false, false, nil
	}
	nowInChatCard := inChatCard || HasClass(n, feishuChatListItemClass)
	if n.Role == "AXTextArea" && feishuComposePlaceholderRE.MatchString(n.Value) {
		hasCompose = true
	}
	if !nowInChatCard && n.Role == "AXStaticText" &&
		strings.TrimSpace(Text(n)) == channel {
		hasHeading = true
	}
	for _, c := range n.Children {
		cCompose, cHeading, cBest := feishuConversationPaneSearch(c, channel, nowInChatCard)
		if cCompose {
			hasCompose = true
		}
		if cHeading {
			hasHeading = true
		}
		// First child that already isolated a complete pane wins —
		// Lark only ever renders one messenger-chat:default WebView at
		// a time, so multiple qualifying children would be a fixture
		// pathology rather than real-world state.
		if best == nil && cBest != nil {
			best = cBest
		}
	}
	if best == nil && hasCompose && hasHeading {
		best = n
	}
	return hasCompose, hasHeading, best
}

// feishuExtractConversation walks the conversation pane in tree order
// and emits non-boilerplate static text lines, each tagged with the
// most recent date separator. The output preserves chronological
// reading order (top-to-bottom in the rendered DOM) — Lark's
// messenger-chat layout naturally orders messages from oldest to
// newest, so a depth-first traversal of the pane yields the same
// sequence the user sees on screen.
//
// Filtering rules:
//   - Skip empty / whitespace-only texts.
//   - Skip the channel-name heading (we re-emit it as "Currently in:"
//     elsewhere).
//   - Skip the compose-area placeholder ("发送给 X"), which is the
//     anchor we used to find the pane in the first place.
//   - Skip date-pattern texts but capture them as the current date.
//   - Skip the boilerplate UI labels in feishuConversationBoilerplate
//     ("欢迎加入群组", "回复话题", keyboard hints).
//
// We do NOT try to reconstruct (sender, body) tuples. The DOM nests
// each message in a chain of containers whose depth varies with
// thread/reply state, and the bot-suffix marker ("yansu bot") isn't
// present on every sender. Emitting a flat dated sequence is more
// robust to Lark's UI churn and downstream LLM consumers can still
// reason about turn boundaries from the text content.
func feishuExtractConversation(pane *AXNode, channel string) []feishuConversationLine {
	if pane == nil {
		return nil
	}
	var out []feishuConversationLine
	var currentDate string

	composePlaceholderText := ""
	Traverse(pane, func(n *AXNode) bool {
		// Defensive: if `pane` happens to include the chat-list
		// WebView as a sibling subtree (because the dual-signal walk
		// could not narrow further), the chat-list-card previews
		// would otherwise spill into the conversation transcript.
		// Bailing on this anchor keeps the two surfaces strictly
		// separated regardless of how loose the pane scope ended up.
		if HasClass(n, feishuChatListItemClass) {
			return false
		}
		if n.Role != "AXStaticText" {
			return true
		}
		t := strings.TrimSpace(Text(n))
		if t == "" {
			return true
		}
		if t == channel {
			return true // transcript header — already surfaced as Channel
		}
		if feishuConversationBoilerplate[t] {
			return true
		}
		if feishuDateRE.MatchString(t) {
			currentDate = t
			return true
		}
		// The compose placeholder sometimes leaks into AXStaticText
		// rather than AXTextArea Value in certain Lark builds; treat
		// it as boilerplate too.
		if feishuComposePlaceholderRE.MatchString(t) {
			composePlaceholderText = t
			return true
		}
		if t == composePlaceholderText {
			return true
		}
		out = append(out, feishuConversationLine{
			date: currentDate,
			text: t,
		})
		return true
	})
	return out
}

// feishuResolveChannel pulls the open conversation name out of the
// compose textarea placeholder. Returns "" when no placeholder is
// present (Feishu splash screen, settings, or any non-chat pane).
//
// The window title is intentionally NOT used as fallback — Feishu
// always sets it to a fixed string ("飞书" / "Lark") regardless of
// which conversation is open, so it's a misleading signal.
func feishuResolveChannel(tree *AXNode, _ string) string {
	var best string
	Traverse(tree, func(n *AXNode) bool {
		if best != "" {
			return false
		}
		if n.Role != "AXTextArea" {
			return true
		}
		v := n.Value
		if v == "" {
			return true
		}
		m := feishuComposePlaceholderRE.FindStringSubmatch(v)
		if m == nil {
			return true
		}
		best = strings.TrimSpace(m[1])
		return false
	})
	return best
}

// feishuCollectChatRows walks every a11y_feed_card_item subtree and
// folds it into a feishuChatRow. Rows are returned in top-to-bottom
// screen order (sorted by frame.y).
func feishuCollectChatRows(tree *AXNode) []feishuChatRow {
	cards := FindAll(tree, func(n *AXNode) bool {
		return HasClass(n, feishuChatListItemClass)
	})
	rows := make([]feishuChatRow, 0, len(cards))
	for _, c := range cards {
		row := feishuParseChatRow(c)
		if row.title == "" {
			// A card with no title is structural noise (placeholder,
			// loading skeleton) — skip rather than emit a blank row.
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].y < rows[j].y
	})
	return rows
}

// feishuParseChatRow lifts a single chat-list row into the structured
// row shape. The static-text sequence inside a Lark card is:
//
//	[title, badge*, date, previewFragment+]
//
// where the title is always first, zero or more localized badges
// ("外部") sit between title and date, and the preview tail is
// fragmented across nodes (one per inline run). We classify date
// purely by regex so the badge slot stays free for any future tag
// Lark adds.
func feishuParseChatRow(card *AXNode) feishuChatRow {
	var row feishuChatRow
	if card == nil {
		return row
	}
	if card.Frame != nil {
		row.y = card.Frame.Y
	}

	statics := CollectStaticTexts(card)
	if len(statics) == 0 {
		return row
	}

	row.title = strings.TrimSpace(statics[0])

	var previewParts []string
	dateSeen := false
	for _, raw := range statics[1:] {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if !dateSeen && feishuDateRE.MatchString(t) {
			row.date = t
			dateSeen = true
			continue
		}
		if !dateSeen {
			row.badges = append(row.badges, t)
			continue
		}
		previewParts = append(previewParts, raw)
	}
	row.preview = strings.TrimSpace(joinPreviewParts(previewParts))
	return row
}

// joinPreviewParts concatenates the fragmented preview tail. Feishu
// splits "@user" mentions and "sender: body" colons into separate
// AXStaticText nodes; joining without a separator restores the
// rendered text faithfully (the original AXStaticText values already
// carry the inter-word spaces).
func joinPreviewParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "")
}

// feishuRenderBody formats the inbox snapshot for prompt consumption.
//
// Layout chosen so downstream LLMs can quote the open channel, the
// inbox sidebar, and the open conversation transcript separately
// without re-parsing:
//
//	Currently in: <channel>
//
//	Recent chats:
//	- <title> [<badges>] (<date>): <preview>
//	- ...
//
//	Conversation:
//	[<date>]
//	  <line>
//	  <line>
//	[<date>]
//	  <line>
//	  ...
//
// Each section is omitted when its source is missing. The Conversation
// block prints each unique date once as a [date] header and indents
// the lines under it; lines without any prior date separator are
// emitted under a single "[earlier]" header so they don't lose
// chronological context.
func feishuRenderBody(channel string, rows []feishuChatRow, conversation []feishuConversationLine) string {
	var b strings.Builder
	if channel != "" {
		b.WriteString("Currently in: ")
		b.WriteString(channel)
	}
	if len(rows) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Recent chats:")
		for _, r := range rows {
			b.WriteString("\n- ")
			b.WriteString(r.title)
			if len(r.badges) > 0 {
				b.WriteString(" [")
				b.WriteString(strings.Join(r.badges, ", "))
				b.WriteString("]")
			}
			if r.date != "" {
				b.WriteString(" (")
				b.WriteString(r.date)
				b.WriteString(")")
			}
			if r.preview != "" {
				b.WriteString(": ")
				b.WriteString(r.preview)
			}
		}
	}
	if len(conversation) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Conversation:")
		lastDate := ""
		for _, line := range conversation {
			date := line.date
			if date == "" {
				date = "earlier"
			}
			if date != lastDate {
				b.WriteString("\n[")
				b.WriteString(date)
				b.WriteString("]")
				lastDate = date
			}
			b.WriteString("\n  ")
			b.WriteString(line.text)
		}
	}
	return b.String()
}
