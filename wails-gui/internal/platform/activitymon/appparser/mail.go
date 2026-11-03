package appparser

import (
	"strings"
)

// mailBundleIDs lists the macOS Mail app.
var mailBundleIDs = map[string]bool{
	"com.apple.mail": true,
}

// mailHeaderContentIdentifier names the AXTextArea that holds the
// reading-pane subject line. Mail packs the subject plus auxiliary text
// (recipient label, object-replacement glyphs) into a single Value field
// — we split it back out when extracting the header.
const mailHeaderContentIdentifier = "message.header.content"

// mailMessageHeaderIdentifier names the AXGroup that wraps the entire
// reading-pane header (from / to / mailbox / timestamp / subject textarea).
// Used to skip the header subtree when collecting body text so the
// sender/timestamp don't repeat as the first lines of the message body.
const mailMessageHeaderIdentifier = "message_header"

// mailListCellIdentifier names the AXUnknown wrapper Mail puts around
// each row in the inbox/message list. The cell's children are AXStaticText
// nodes whose identifiers (addressLabel / subjectLabel / dateLabel /
// summaryLabel) decompose into the message preview fields. Used as the
// fallback when no reading pane is open.
const mailListCellIdentifier = "Mail.messageList.cell.view"

// mailListCellLabel* hold the AXStaticText identifiers Mail uses inside
// each message-list cell.
const (
	mailListCellAddress = "Mail.messageList.cell.view.addressLabel"
	mailListCellSubject = "Mail.messageList.cell.view.subjectLabel"
	mailListCellDate    = "Mail.messageList.cell.view.dateLabel"
	mailListCellSummary = "Mail.messageList.cell.view.summaryLabel"
)

// mailInboxListMax caps the inbox-list fallback at a reasonable number
// of preview entries. 175 unread messages render in the AX tree of a
// typical inbox snapshot — emitting all of them in the body would flood
// downstream prompts. Newest-first ordering means truncation drops the
// oldest, least-relevant rows.
const mailInboxListMax = 30

type mailParser struct{}

func init() {
	Register(&mailParser{})
}

func (p *mailParser) CanParse(bundleID string) bool {
	return mailBundleIDs[bundleID]
}

func (p *mailParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "mail",
		Label:       "Apple Mail",
		ContentType: "conversation",
		Bundles:     []string{"com.apple.mail"},
	}
}

// ParseTree extracts content from Mail.app. Two dispatch paths cover the
// reality of how users interact with Mail:
//
//   - Reading-pane path: when a specific message is open, the AX tree
//     exposes a message_header AXGroup with labeled AXStaticText nodes
//     (identifier="message.from.0" / "message.timestamp" / etc.) plus
//     an AXTextArea[id="message.header.content"] that carries the subject.
//     Body still lives in an AXWebArea. We output a single
//     ConversationMessage with full headers + body — the high-signal case.
//   - Inbox-list fallback: when the user is only browsing the message
//     list (no reading pane focused), each visible row is an AXUnknown
//     [id="Mail.messageList.cell.view"] with AXStaticText children whose
//     identifiers decompose into sender/subject/date/summary. We output
//     one ConversationMessage per row, capped at mailInboxListMax to keep
//     prompt size bounded.
//
// The reading-pane path runs first so a split-view window (inbox list on
// the left + open message on the right, which is the default macOS Mail
// layout) emits the open message, not the inbox preview.
//
// Returns nil when neither path finds usable content (e.g. user has Mail
// open but it's showing a settings sheet or a compose window we don't
// support yet).
func (p *mailParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	if pc := mailReadingPaneContent(window, bundleID, windowTitle); pc != nil {
		return pc
	}
	if pc := mailInboxListContent(window, bundleID, windowTitle); pc != nil {
		return pc
	}
	return nil
}

// mailReadingPaneContent handles the high-signal case: a specific message
// is open in the reading pane. Returns nil when the message_header group
// isn't present so the caller can fall back to the inbox-list path.
func mailReadingPaneContent(window *AXNode, bundleID, windowTitle string) *ParsedContent {
	headerGroup := FindFirst(window, func(n *AXNode) bool {
		return n.Identifier == mailMessageHeaderIdentifier
	})
	if headerGroup == nil {
		return nil
	}

	from, to, headerMailbox, timestamp := mailReadHeaderLabels(headerGroup)
	subject := mailExtractSubjectFromHeader(headerGroup)

	body := mailExtractBody(window, headerGroup)
	if body == "" && from == "" && subject == "" {
		// Nothing useful — let the caller try the inbox-list path.
		return nil
	}

	mailbox := mailPickMailbox(headerMailbox, windowTitle, window)

	sender := strings.TrimSpace(from)
	if sender == "" {
		sender = "Email"
	}

	msg := ConversationMessage{
		Sender:    sender,
		Content:   body,
		Timestamp: strings.TrimSpace(timestamp),
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "mail",
		"view":     "reading",
	}
	if mailbox != "" {
		meta["mailbox"] = mailbox
	}
	if subject != "" {
		meta["subject"] = subject
	}
	if to != "" {
		meta["recipient"] = strings.TrimSpace(to)
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       subject,
		Channel:     mailbox,
		Messages:    []ConversationMessage{msg},
		Body:        renderConversationBody([]ConversationMessage{msg}),
		Metadata:    meta,
	}
}

// mailReadHeaderLabels walks the message_header subtree pulling out the
// four labeled AXStaticText fields. Identifier values follow a stable
// pattern Mail emits across builds (message.from.0 / message.to.0 /
// message.mailbox / message.timestamp); when a message has multiple
// senders or recipients we keep the first one (".0") since that's what
// users typically care about for triage.
func mailReadHeaderLabels(headerGroup *AXNode) (from, to, mailbox, timestamp string) {
	Traverse(headerGroup, func(n *AXNode) bool {
		if n.Role != "AXStaticText" {
			return true
		}
		v := strings.TrimSpace(n.Value)
		if v == "" {
			v = strings.TrimSpace(n.Description)
		}
		if v == "" {
			return true
		}
		switch {
		case from == "" && strings.HasPrefix(n.Identifier, "message.from"):
			from = v
		case to == "" && strings.HasPrefix(n.Identifier, "message.to"):
			to = v
		case mailbox == "" && n.Identifier == "message.mailbox":
			mailbox = mailCleanMailbox(v)
		case timestamp == "" && n.Identifier == "message.timestamp":
			timestamp = v
		}
		return true
	})
	return from, to, mailbox, timestamp
}

// mailCleanMailbox strips the U+FFFC object-replacement glyphs and leading
// whitespace Mail prepends to the mailbox label (the glyph reserves space
// for the inline mailbox-icon image, which we don't care about).
func mailCleanMailbox(s string) string {
	s = strings.ReplaceAll(s, "￼", "")
	return strings.TrimSpace(s)
}

// mailExtractSubjectFromHeader pulls the subject out of the
// message.header.content AXTextArea. Mail packs that field as
// "<U+FFFC>\n<subject>\n收件人: <U+FFFC><U+FFFC>" — one or more object-
// replacement glyphs, the actual subject, then a "to" label echo. The
// subject is the first non-empty line that isn't the recipient echo;
// detection is locale-aware in the sense that we strip the glyphs first
// and then any line that begins with a known recipient label prefix.
func mailExtractSubjectFromHeader(headerGroup *AXNode) string {
	textArea := FindFirst(headerGroup, func(n *AXNode) bool {
		return n.Role == "AXTextArea" && n.Identifier == mailHeaderContentIdentifier
	})
	if textArea == nil {
		return ""
	}
	raw := strings.ReplaceAll(textArea.Value, "￼", "")
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip the "收件人:" / "to:" / "cc:" header echoes that share the
		// textarea — they're not the subject.
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "收件人") ||
			strings.HasPrefix(line, "发件人") ||
			strings.HasPrefix(line, "抄送") ||
			strings.HasPrefix(lower, "to:") ||
			strings.HasPrefix(lower, "from:") ||
			strings.HasPrefix(lower, "cc:") ||
			strings.HasPrefix(lower, "bcc:") {
			continue
		}
		return line
	}
	return ""
}

// mailExtractBody collects the visible body text under the AXWebArea,
// skipping the header subtree so labels and timestamps don't double-print
// as the first lines of the body.
func mailExtractBody(window, headerGroup *AXNode) string {
	webArea := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if webArea == nil {
		return ""
	}
	var parts []string
	Traverse(webArea, func(n *AXNode) bool {
		if n == nil || n.Role == "AXScrollBar" {
			return false
		}
		// Don't re-descend into the header — its texts are exposed
		// separately via identifier-labeled fields.
		if n == headerGroup {
			return false
		}
		if strings.HasPrefix(n.Identifier, "message.") {
			// Defensive: any nested header label that leaks past the
			// headerGroup boundary still gets pruned.
			return false
		}
		if n.Role != "AXStaticText" {
			return true
		}
		t := strings.TrimSpace(n.Value)
		if t == "" {
			t = strings.TrimSpace(n.Description)
		}
		if t == "" {
			return true
		}
		parts = append(parts, t)
		return true
	})
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// mailPickMailbox picks the most authoritative mailbox name available.
// Priority:
//  1. headerMailbox from message.mailbox (e.g. "收件箱 - 谷歌") — only
//     present when a message is open; this is the message's actual
//     mailbox, not the user's currently-focused sidebar entry.
//  2. windowTitle split on dash. macOS Mail formats the window title as
//     "<subject> — <mailbox> · <filter>" but uses an EN dash (–, U+2013)
//     not the EM dash (—, U+2014) the old parser expected. We split on
//     both so the heuristic survives either build.
//  3. Sidebar first row (fallback for unusual windows like Compose).
func mailPickMailbox(headerMailbox, windowTitle string, window *AXNode) string {
	if headerMailbox != "" {
		return headerMailbox
	}
	if windowTitle != "" {
		// macOS Mail uses different separators across builds and locales —
		// EN dash (–) is current, EM dash (—) is legacy. Try both.
		for _, sep := range []string{" – ", " — ", " - "} {
			if idx := strings.LastIndex(windowTitle, sep); idx >= 0 {
				return mailTrimMailboxSuffix(windowTitle[idx+len(sep):])
			}
		}
	}
	return mailSidebarMailbox(window)
}

// mailTrimMailboxSuffix drops the "· <filter>" portion macOS Mail appends
// to the mailbox segment when a filter is active (e.g.
// "收件箱 – 主要 · 过滤条件：未读（175封邮件）" → "收件箱"). The "·"
// glyph (U+00B7) is the consistent separator across locales.
func mailTrimMailboxSuffix(s string) string {
	if idx := strings.Index(s, " · "); idx >= 0 {
		s = s[:idx]
	}
	// Same with the EN-space variant used in some builds.
	if idx := strings.Index(s, "·"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// mailSidebarMailbox returns the first AXStaticText under the sidebar
// AXOutline as a best-effort fallback. This is not great — Mail's sidebar
// renders nested group headers (e.g. "邮箱", "智能邮箱", "我的 Mac 上",
// "谷歌") before the actual mailbox rows, so the first row often is the
// "邮箱" group title rather than a real mailbox. Callers should prefer
// mailbox from message header or window title.
func mailSidebarMailbox(window *AXNode) string {
	outline := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXOutline" })
	if outline == nil {
		return ""
	}
	row := FindFirst(outline, func(n *AXNode) bool { return n.Role == "AXRow" })
	if row == nil {
		return ""
	}
	text := FindFirst(row, func(n *AXNode) bool { return n.Role == "AXStaticText" })
	if text == nil {
		return ""
	}
	name := strings.TrimSpace(text.Value)
	if name == "" {
		name = strings.TrimSpace(text.Title)
	}
	return name
}

// mailInboxListContent handles the fallback case: no reading pane focused,
// so we surface the inbox-list preview entries instead. Each row's
// AXStaticText children carry pre-decomposed sender / subject / date /
// summary via identifier values, making this the cleanest list extraction
// across our parsers.
func mailInboxListContent(window *AXNode, bundleID, windowTitle string) *ParsedContent {
	cells := FindAll(window, func(n *AXNode) bool {
		return n.Identifier == mailListCellIdentifier
	})
	if len(cells) == 0 {
		return nil
	}

	var msgs []ConversationMessage
	for _, cell := range cells {
		if len(msgs) >= mailInboxListMax {
			break
		}
		msg := mailListRowToMessage(cell)
		if msg == nil {
			continue
		}
		msgs = append(msgs, *msg)
	}
	if len(msgs) == 0 {
		return nil
	}

	mailbox := mailPickMailbox("", windowTitle, window)

	meta := map[string]string{
		"bundleId":     bundleID,
		"app":          "mail",
		"view":         "inbox",
		"messageCount": itoaMail(len(msgs)),
	}
	if mailbox != "" {
		meta["mailbox"] = mailbox
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       mailbox,
		Channel:     mailbox,
		Messages:    msgs,
		Body:        renderConversationBody(msgs),
		Metadata:    meta,
	}
}

// mailListRowToMessage decomposes a single Mail.messageList.cell.view
// AXUnknown into a ConversationMessage. Subject and summary are folded
// into Content as "<subject>\n<summary>" since ConversationMessage doesn't
// have separate slots — the structured shape matches how renderers
// already format conversation entries downstream.
func mailListRowToMessage(cell *AXNode) *ConversationMessage {
	var sender, subject, date, summary string
	Traverse(cell, func(n *AXNode) bool {
		if n.Role != "AXStaticText" {
			return true
		}
		v := strings.TrimSpace(n.Value)
		if v == "" {
			v = strings.TrimSpace(n.Description)
		}
		if v == "" {
			return true
		}
		switch n.Identifier {
		case mailListCellAddress:
			sender = v
		case mailListCellSubject:
			subject = v
		case mailListCellDate:
			date = v
		case mailListCellSummary:
			summary = v
		}
		return true
	})
	// A row must have at least a sender or a subject to be worth
	// surfacing — otherwise it's an empty cell (the AX tree occasionally
	// pre-allocates blank placeholder rows during scroll).
	if sender == "" && subject == "" {
		return nil
	}
	content := subject
	if summary != "" {
		if content != "" {
			content += "\n"
		}
		content += summary
	}
	if sender == "" {
		sender = "Email"
	}
	return &ConversationMessage{
		Sender:    sender,
		Content:   content,
		Timestamp: date,
	}
}

// itoaMail is a tiny non-imported int-to-string converter. Avoiding the
// strconv import keeps this file self-contained when read alongside the
// other parsers, none of which currently pull strconv in.
func itoaMail(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
