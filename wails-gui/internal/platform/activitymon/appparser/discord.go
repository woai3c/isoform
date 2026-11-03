package appparser

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Discord macOS desktop bundle ID. Historic builds shipped as
// "com.discordapp.Discord"; the modern App Store / DMG distribution is
// "com.hnc.Discord". We accept both — the listed set is intentionally
// small so an unrelated Hammer & Chisel app doesn't accidentally match.
var discordBundleIDs = map[string]bool{
	"com.hnc.Discord":        true,
	"com.discordapp.Discord": true,
}

const discordAppName = "discord"

type discordParser struct{}

func init() { Register(&discordParser{}) }

func (p *discordParser) CanParse(bundleID string) bool {
	return discordBundleIDs[bundleID]
}

func (p *discordParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        discordAppName,
		Label:       "Discord",
		ContentType: "conversation",
		Bundles:     []string{"com.discordapp.Discord", "com.hnc.Discord"},
	}
}

// ParseTree drives Discord parsing through the shared Electron chat
// driver. Real AX shape (verified against Discord 0.0.342 on macOS
// 25.2 — see cmd/ax-tree-dump output for the channel that motivated
// this implementation):
//
//	AXGroup[messageListItem__5126c]                       ← each message
//	  AXGroup[AXDocumentArticle, message__5126c, …]
//	    AXGroup[repliedMessage_c19a55]                    ← OPTIONAL reply
//	      AXButton[username_c19a55, clickable_c19a55]     ← @<replied-to>
//	      AXButton[repliedTextPreview_c19a55, …]          ← quoted body
//	    AXHeading[header_c19a55]                          ← cluster header
//	       title="<sender> <date> <time>"                  ← e.g. "Priya 2031/7/11 10:23"
//	       OR     "<sender> 昨天HH:MM"                       (zh-CN relative)
//	    AXGroup[markup__75297, messageContent_c19a55]      ← body
//	      AXStaticText … AXButton[emojiContainer__…] …
//
// Critical gotcha: the obvious-looking AXButton[username_c19a55] only
// ever appears inside the OPTIONAL repliedMessage block — its title is
// the replied-to user, NOT the sender of this message. Targeting that
// node by class causes every message to be attributed to whoever the
// first reply quotes. The truth lives in AXHeading.title; the spans
// that look like username/timestamp slots elsewhere
// (AXGroup[username_c19a55], AXGroup[timestamp_c19a55]) are empty
// layout shells without children.
//
// Cluster follow-ups (same-author bursts within ~7 min) omit the
// AXHeading entirely, mirroring Discord's UI. Sender is recovered via
// lastSender carryover; timestamp via discordTimestampCarryover post-
// process.
func (p *discordParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	spec := &ElectronChatSpec{
		AppName:           discordAppName,
		FindMessageGroups: discordFindMessageGroups,
		ParseMessage:      discordParseMessage,
		ResolveChannel:    discordResolveChannel,
		PostProcess:       discordTimestampCarryover,
	}
	return ElectronChatParseTree(spec, tree, bundleID, windowTitle)
}

// discordTimestampCarryover forward-fills empty timestamps from the
// most recent populated one. Cluster follow-ups (no AXHeading) leave
// the field empty in discordParseMessage; carryover stamps them with
// the cluster header time so every shipped message carries usable
// timing. Forward-fill only — back-filling could stamp a yesterday
// message with today's time.
func discordTimestampCarryover(msgs []ConversationMessage, _ *AXNode) []ConversationMessage {
	last := ""
	for i := range msgs {
		if msgs[i].Timestamp != "" {
			last = msgs[i].Timestamp
			continue
		}
		if last == "" {
			continue
		}
		msgs[i].Timestamp = last
	}
	return msgs
}

// discordFindMessageGroups returns every messageListItem wrapper under
// the window in traversal order. Matching by partial class name lets
// us survive the CSS-module hash suffix Discord appends
// (`messageListItem__5126c`, `messageListItem__abcdef`, …) without
// committing to a fragile exact-class match.
func discordFindMessageGroups(window *AXNode) []*AXNode {
	return FindAll(window, func(n *AXNode) bool {
		return HasClassContaining(n, "messageListItem")
	})
}

// discordHeadingRe pulls sender + timestamp out of an AXHeading title.
// The capture group covers every format observed in the dump:
//
//	"<sender> 2031/7/11 10:23"     absolute slash date with time
//	"<sender> 2031/6/30"           absolute slash date alone (older
//	                               messages where Discord drops the
//	                               time to save space; may also carry
//	                               a trailing comma like "2031/6/30,")
//	"<sender> 昨天10:34"            relative (no space)
//	"<sender> 昨天 10:34"           relative (with space)
//	"<sender> 今天 11:51"
//	"<sender> 前天 08:12"
//	"<sender> 10:25"               TODAY shorthand — bare HH:MM with no
//	                               date prefix (Discord's default for
//	                               messages sent within the current day).
//	                               Added 2026-05-21 after People-page
//	                               unidentified-senders bug surfaced
//	                               every name suffixed with its time.
//
// English-locale Discord adds " at " between date and time
// ("Today at 11:51 PM"); the alternation handles that too.
//
// The timestamp group is either:
//   - a date (absolute slash or Chinese relative or Today/Yesterday)
//     optionally followed by a time, optionally followed by a comma
//   - just a time (bare HH:MM with optional AM/PM)
var discordHeadingRe = regexp.MustCompile(
	`^(?P<sender>.+?)\s+(?P<ts>(?:(?:\d{4}/\d{1,2}/\d{1,2}|今天|昨天|前天|Today|Yesterday)(?:\s*(?:at\s+)?\d{1,2}:\d{2}(?:\s*[AP]M)?)?|\d{1,2}:\d{2}(?:\s*[AP]M)?),?)\s*$`)

// discordRoleBadges lists trailing inline labels Discord renders inside
// the cluster-header AX node next to the username (server-owner crown,
// the bot tag, the OP "Original Poster" thread badge, etc). When the AX
// title concatenates them after the username the regex above keeps them
// in the sender group; strip them here so the resulting sender is the
// bare username only. Order matters: longer phrases first so
// "OP Original Poster" is tried before "Original Poster".
var discordRoleBadges = []string{
	"OP Original Poster",
	"Original Poster",
	"Server Owner",
	"BOT",
	"Bot",
	"App",
	"系统",
}

// discordSplitHeading parses "<sender> <timestamp>" into its parts.
// Returns the full title as sender and an empty timestamp when the
// regex doesn't match — that way unexpected formats still surface the
// sender label rather than swallowing the message.
func discordSplitHeading(title string) (sender, timestamp string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", ""
	}
	if m := discordHeadingRe.FindStringSubmatch(title); m != nil {
		return stripDiscordRoleBadges(strings.TrimSpace(m[1])), strings.TrimSpace(m[2])
	}
	// No "<sender> <timestamp>" shape. The fallback surfaces the whole
	// title as the sender so genuinely unusual-but-valid headers still
	// yield a name — but a long / multi-line / "Server Tag:"-bearing title
	// is message body or a system card (the new-member welcome card
	// "<x> Server Tag: A\ I'm new here, say hi!" is the known offender)
	// that bled into the heading node. Surfacing those as senders pollutes
	// the Who you know page and poisons lastSender carryover, so drop the row.
	if !discordHeadingLooksLikeSender(title) {
		return "", ""
	}
	return stripDiscordRoleBadges(title), ""
}

// discordHeadingLooksLikeSender is a conservative sanity check for the
// no-timestamp fallback in discordSplitHeading: it accepts a short,
// single-line title and rejects the long / multi-line / badge-bearing
// titles that only occur when message body or a system card leaks into
// the heading. Mirrors identity.PlausibleAutoContactName's intent but is
// kept local so the AX parser stays free of identity-layer dependencies.
func discordHeadingLooksLikeSender(title string) bool {
	if strings.ContainsAny(title, "\n\r") {
		return false
	}
	if utf8.RuneCountInString(title) > 48 {
		return false
	}
	if strings.Contains(title, "Server Tag:") {
		return false
	}
	return true
}

func stripDiscordRoleBadges(s string) string {
	s = strings.TrimSpace(s)
	// Only strip a badge when it follows whitespace inside a longer
	// string — never when the badge equals the entire input. The latter
	// would erase the username for a legitimate user named "Bot" / "App"
	// / "Server Owner" and the cluster-header path would lose them
	// entirely.
	for changed := true; changed; {
		changed = false
		for _, badge := range discordRoleBadges {
			lowered := strings.ToLower(s)
			suffix := " " + strings.ToLower(badge)
			if strings.HasSuffix(lowered, suffix) {
				s = strings.TrimSpace(s[:len(s)-len(badge)])
				changed = true
				break
			}
		}
	}
	return s
}

// discordParseMessage extracts sender / timestamp / content from one
// messageListItem. Cluster header messages have an AXHeading; cluster
// follow-ups don't, and rely on the driver's lastSender carryover
// plus the PostProcess timestamp carryover.
func discordParseMessage(group *AXNode, lastSender string) *ConversationMessage {
	heading := FindFirst(group, func(n *AXNode) bool {
		return n.Role == "AXHeading" && HasClassContaining(n, "header_")
	})

	sender, timestamp := "", ""
	if heading != nil {
		sender, timestamp = discordSplitHeading(heading.Title)
	}
	if sender == "" && lastSender != "" {
		sender = lastSender
	}
	if sender == "" {
		return nil
	}

	body := discordExtractBody(group)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return &ConversationMessage{
		Sender:    sender,
		Content:   body,
		Timestamp: timestamp,
	}
}

// discordExtractBody collects the visible message text. Strategy:
//
//  1. Walk inside the `messageContent_c19a55` subtree only — this
//     intentionally excludes the `repliedMessage_c19a55` block (the
//     quoted preview when this message is a reply), reactions,
//     accessories, and embed previews, all of which sit alongside
//     messageContent under the AXDocumentArticle.
//  2. Concatenate AXStaticText.Value in document order.
//  3. Inline emoji are AXButton[emojiContainer__…] whose Title carries
//     the actual glyph (e.g. "😮‍💨") — include those titles so the
//     rendered body matches what the user sees.
//
// When no messageContent_ subtree exists (system messages, blocked
// messages, ephemeral placeholders), the body is empty and the caller
// drops the row.
func discordExtractBody(group *AXNode) string {
	content := FindFirst(group, func(n *AXNode) bool {
		return HasClassContaining(n, "messageContent_") ||
			HasClassContaining(n, "markup__")
	})
	if content == nil {
		return ""
	}

	var texts []string
	Traverse(content, func(n *AXNode) bool {
		switch n.Role {
		case "AXStaticText":
			if n.Value != "" {
				texts = append(texts, n.Value)
			}
		case "AXButton":
			if HasClassContaining(n, "emojiContainer") && n.Title != "" {
				texts = append(texts, n.Title)
			}
		}
		return true
	})
	return strings.TrimRight(strings.Join(texts, ""), " \t\n\r")
}

// discordResolveChannel derives the channel / DM name from whatever
// title source carries usable text. Observed shapes:
//
//	windowTitle == ""                            (Electron occasionally drops it)
//	AXWindow.Title == "@alex_halcyon - Discord"          (DM with @<user>)
//	AXWebArea.Title == "@alex_halcyon"                   (web view embedded title)
//	"#general - Server - Discord"                (text channel)
//
// Priority: caller-supplied windowTitle → AXWindow.Title → AXWebArea.Title.
// The " - Discord" suffix and the "|"/" - " server separators are
// stripped uniformly.
func discordResolveChannel(window *AXNode, windowTitle string) (string, bool) {
	candidate := strings.TrimSpace(windowTitle)
	if candidate == "" && window != nil {
		candidate = strings.TrimSpace(window.Title)
	}
	if candidate == "" && window != nil {
		if wa := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXWebArea" }); wa != nil {
			candidate = strings.TrimSpace(wa.Title)
		}
	}
	if candidate == "" || strings.EqualFold(candidate, "Discord") {
		return "", false
	}
	if i := strings.LastIndex(candidate, " - Discord"); i > 0 {
		candidate = candidate[:i]
	}
	for _, sep := range []string{"|", " - "} {
		if i := strings.Index(candidate, sep); i > 0 {
			candidate = candidate[:i]
			break
		}
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", false
	}
	if strings.HasPrefix(candidate, "#") {
		return strings.TrimSpace(strings.TrimLeft(candidate, "#")), true
	}
	// DMs render as "@username" — keep the @ to disambiguate from
	// channel names so the rendered Channel field stays informative.
	return candidate, false
}
