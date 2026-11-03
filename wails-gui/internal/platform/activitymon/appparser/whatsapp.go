package appparser

import (
	"regexp"
	"sort"
	"strings"
)

// whatsappBundleIDs covers the macOS WhatsApp Desktop builds. The
// historic Electron build uses `net.whatsapp.WhatsApp`; the Mac Catalyst
// rebuild distributed via the Mac App Store ships under the same ID in
// most channels but some builds expose `desktop.WhatsApp` or just
// `WhatsApp`. All three accept the same heuristics because the message
// schema in the AX tree is the same — Catalyst flattens each message
// into a single AXStaticText with a screen-reader description.
var whatsappBundleIDs = map[string]bool{
	"net.whatsapp.WhatsApp": true,
	"desktop.WhatsApp":      true,
	"WhatsApp":              true,
}

const whatsappAppName = "whatsapp"

// whatsappLRM is the LEFT-TO-RIGHT MARK (U+200E) WhatsApp inserts into
// every screen-reader description to keep BIDI rendering predictable
// for mixed-language chats. It's invisible on screen but breaks any
// prefix / regex matching unless stripped first.
const whatsappLRM = "‎"

// whatsappMaxMessages caps the conversation slice. WhatsApp virtualises
// the message list, but a long scrollback can still pump 100+ rows into
// the AX tree at once; downstream prompts only ever need the recent
// window.
const whatsappMaxMessages = 80

// whatsappSidebarHeadings lists the AXHeading titles that appear in
// the left-hand chat list — we filter them out of channel resolution
// so the conversation pane's heading wins.
var whatsappSidebarHeadings = map[string]bool{
	"Chats":       true,
	"Calls":       true,
	"Status":      true,
	"Archived":    true,
	"Starred":     true,
	"Communities": true,
	"Channels":    true,
}

// whatsappDateDividerRE matches the in-pane date dividers ("Fri, Feb 13"
// or "Today" / "Yesterday"). We exclude these from channel resolution
// so they don't masquerade as the conversation title.
var whatsappDateDividerRE = regexp.MustCompile(
	`^([A-Z][a-z]{2}, [A-Z][a-z]+ ?\d+|Today|Yesterday)$`,
)

// whatsappMessageRE parses one WhatsApp message description after the
// LRM marks have been stripped. Format observed against WhatsApp
// Desktop (Mac Catalyst, 2026 builds):
//
//	"<sender phrase>, <body multi-line>, <Month><Day>,at<HH:MM>, <suffix>"
//
// Where:
//   - sender phrase is "Your message" for sent messages and
//     "<contact>'s message" for received ones.
//   - body preserves embedded newlines from the original message.
//   - the date+time pair is glued together as `<Month><Day>,at<HH:MM>`
//     (no space between the comma and "at"), which keeps it as a
//     single comma-separated token and lets us anchor on it.
//   - suffix is "Sent to <recipient>, <send-status colour>" etc.
//
// The `(?s)` flag lets `.+` cross newlines so multi-paragraph messages
// stay intact. The lazy quantifier `.+?` stops at the FIRST date marker
// so a body containing a literal "March 5,at..." substring couldn't
// accidentally swallow into the metadata (unlikely but cheap).
//
// Inter-segment whitespace uses `[\s\x{00A0}]+` rather than a literal
// space — accessibility descriptions are vendor-controlled and stable
// WITHIN a build, but spacing has drifted between macOS versions
// (Catalyst 17 vs 26 in particular emit subtly different separators).
// U+00A0 (no-break space) is the load-bearing case: Go's RE2 `\s`
// matches only ASCII `[\t\n\f\r ]`, so NBSP would silently zero out
// the Messages list if a future build switched to it.
var whatsappMessageRE = regexp.MustCompile(
	`(?s)^([^,]+),[\s\x{00A0}]+(.+?),[\s\x{00A0}]+([A-Z][a-z]+ ?\d+,at\d+:\d+)(?:,|$)`,
)

type whatsappParser struct{}

func init() {
	Register(&whatsappParser{})
}

func (p *whatsappParser) CanParse(bundleID string) bool {
	return whatsappBundleIDs[bundleID]
}

func (p *whatsappParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        whatsappAppName,
		Label:       "WhatsApp",
		ContentType: "conversation",
		Bundles: []string{
			"WhatsApp",
			"desktop.WhatsApp",
			"net.whatsapp.WhatsApp",
		},
	}
}

// ParseTree extracts the active WhatsApp chat as a ConversationMessage
// list.
//
// WhatsApp Desktop (Mac Catalyst) does NOT expose the message list as a
// proper AXScrollArea / AXList. Instead, each message is a flat
// AXStaticText sitting alongside dozens of other UI nodes, with the
// screen-reader hint packed into the node's .description field (the
// .value field is reserved for the sidebar preview that mirrors the
// latest message per chat). The conversation pane lives at a higher
// x-coordinate than the sidebar, but the cleanest signal is just "is
// the description string-shaped like a message?" — we match against
// whatsappMessageRE and sort by frame.y to recover chronological order.
//
// Sidebar preview rows are skipped naturally because their description
// is the contact name (e.g. "alex example"), not the WhatsApp message
// format — the regex returns nil for those.
func (p *whatsappParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	channel := whatsappResolveChannel(tree, windowTitle)
	messages := collectWhatsappMessages(tree)

	if len(messages) > whatsappMaxMessages {
		messages = messages[len(messages)-whatsappMaxMessages:]
	}

	// Always produce a body. When extraction worked the body is the
	// chronological rendering; when it didn't (parser miss, AX-gated
	// chrome only) we still hand back the collected static texts so
	// the DevTools probe panel can show "what was reachable" and the
	// operator can tune the heuristic.
	body := whatsappBuildBody(messages, tree)
	if body == "" && len(messages) == 0 {
		return nil
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       channel,
		Channel:     channel,
		Body:        body,
		Messages:    messages,
		Metadata: map[string]string{
			"app":      whatsappAppName,
			"bundleId": bundleID,
		},
	}
}

// whatsappResolveChannel pulls the conversation name out of the AX
// tree. Strategy:
//
//  1. Prefer an AXHeading whose text is not a sidebar tab header
//     ("Chats", "Calls", …) and not an in-pane date divider
//     ("Fri, Feb 13", "Today", …) — that leaves the conversation
//     header in the right pane (e.g. "alex example").
//  2. Fall back to the window title, stripping any " — WhatsApp"
//     suffix some builds attach.
//  3. Return "" when neither succeeds; consumers can decide whether to
//     render the parser hit anyway.
func whatsappResolveChannel(tree *AXNode, windowTitle string) string {
	var best string
	Traverse(tree, func(n *AXNode) bool {
		if best != "" {
			return false
		}
		if n.Role != "AXHeading" {
			return true
		}
		t := strings.TrimSpace(strings.ReplaceAll(Text(n), whatsappLRM, ""))
		if t == "" || t == "WhatsApp" || whatsappSidebarHeadings[t] {
			return true
		}
		if whatsappDateDividerRE.MatchString(t) {
			return true
		}
		best = t
		return false
	})
	if best != "" {
		return best
	}
	t := strings.TrimSpace(windowTitle)
	if t != "" && t != "WhatsApp" {
		if idx := strings.Index(t, " — "); idx > 0 {
			return strings.TrimSpace(t[:idx])
		}
		return t
	}
	return ""
}

// collectWhatsappMessages walks the AX tree for AXStaticText nodes
// whose description parses as a WhatsApp message, then sorts them by
// vertical frame position so the output is chronological (top to
// bottom on screen, oldest to newest).
//
// Sidebar preview rows are filtered out by the regex itself — their
// description is just a contact name and doesn't match the
// "<phrase>, <body>, <date>,at<time>" shape.
func collectWhatsappMessages(tree *AXNode) []ConversationMessage {
	type entry struct {
		y   float64
		msg ConversationMessage
	}
	var entries []entry
	Traverse(tree, func(n *AXNode) bool {
		if n.Role != "AXStaticText" {
			return true
		}
		desc := strings.TrimSpace(n.Description)
		if desc == "" {
			return true
		}
		msg := whatsappParseDescription(desc)
		if msg == nil {
			return true
		}
		y := 0.0
		if n.Frame != nil {
			y = n.Frame.Y
		}
		entries = append(entries, entry{y: y, msg: *msg})
		return true
	})
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].y < entries[j].y
	})
	out := make([]ConversationMessage, len(entries))
	for i, e := range entries {
		out[i] = e.msg
	}
	return out
}

// whatsappParseDescription pulls sender / body / timestamp out of one
// WhatsApp screen-reader description string. Returns nil when the
// shape doesn't match (sidebar previews, headers, decorations).
func whatsappParseDescription(desc string) *ConversationMessage {
	s := strings.ReplaceAll(desc, whatsappLRM, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := whatsappMessageRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	senderPhrase := strings.TrimSpace(m[1])
	body := strings.TrimSpace(m[2])
	timestamp := strings.TrimSpace(m[3])
	if body == "" {
		return nil
	}

	sender := ""
	switch {
	case strings.HasPrefix(senderPhrase, "Your"):
		sender = "You"
	case strings.HasSuffix(senderPhrase, "'s message"):
		sender = strings.TrimSuffix(senderPhrase, "'s message")
	case strings.HasSuffix(senderPhrase, " message"):
		sender = strings.TrimSuffix(senderPhrase, " message")
	default:
		sender = senderPhrase
	}

	return &ConversationMessage{
		Sender:    sender,
		Content:   body,
		Timestamp: timestamp,
	}
}

// whatsappBuildBody flattens a message list into a readable string. If
// extraction failed (no messages) we still emit a flat body of the
// collected static texts so the AX Parser probe panel has something to
// display — "no Messages but a body" is the signal to tune the regex.
func whatsappBuildBody(msgs []ConversationMessage, fallback *AXNode) string {
	if len(msgs) > 0 {
		lines := make([]string, 0, len(msgs))
		for _, m := range msgs {
			line := m.Content
			if m.Sender != "" {
				line = m.Sender + ": " + line
			}
			if m.Timestamp != "" {
				line += "  (" + m.Timestamp + ")"
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}
	texts := CollectStaticTexts(fallback)
	cleaned := make([]string, 0, len(texts))
	for _, t := range texts {
		t = strings.TrimSpace(strings.ReplaceAll(t, whatsappLRM, ""))
		if t != "" {
			cleaned = append(cleaned, t)
		}
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
