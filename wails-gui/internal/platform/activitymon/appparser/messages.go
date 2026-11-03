package appparser

import (
	"regexp"
	"strings"
)

var messagesBundleIDs = map[string]bool{
	"com.apple.MobileSMS": true, // macOS Messages
}

type messagesParser struct{}

func init() {
	Register(&messagesParser{})
}

func (p *messagesParser) CanParse(bundleID string) bool {
	return messagesBundleIDs[bundleID]
}

func (p *messagesParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "messages",
		Label:       "Apple Messages",
		ContentType: "conversation",
		Bundles:     []string{"com.apple.MobileSMS"},
	}
}

// messageDescTimeAtEnd matches ", HH:MM" at end of an AXGroup description.
var messageDescTimeAtEnd = regexp.MustCompile(`,\s*\d{1,2}:\d{2}\s*$`)

// messageDescTimeWithMeta matches ", HH:MM, metadata" for rich messages.
var messageDescTimeWithMeta = regexp.MustCompile(`,\s*\d{1,2}:\d{2}\s*,\s*[^,]+\s*$`)

// messagesDateHeaderRe extracts the date portion from a Messages date divider.
// Bare HH:MM bubbles lose year/month/day context — readers seeing only "07:16"
// can't tell which day a transcript belongs to. macOS Messages emits the date
// as a sibling node before each day's first bubble, so we promote it onto the
// timestamp of every subsequent bubble until the next divider.
//
// Matches (in priority order):
//   - Chinese full date:  "2024年11月6日"
//   - Chinese month-day:  "11月6日"  (Messages drops the year for current year)
//   - English absolute:   "November 6, 2024" / "Nov 6"
//   - English relative:   "Yesterday" / "Today" / "Monday".."Sunday"
var messagesDateHeaderRe = regexp.MustCompile(
	`\d{4}年\d{1,2}月\d{1,2}日` +
		`|\d{1,2}月\d{1,2}日` +
		`|(?i)(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+\d{1,2}(?:,\s*\d{4})?` +
		`|(?i)\b(?:yesterday|today|mon|tue|wed|thu|fri|sat|sun)(?:day|sday|nesday|rsday|urday)?\b`,
)

// ParseTree extracts the active iMessage conversation.
// Mirrors LittleBird's messages_parser.js approach:
//   - AXWindow title is the channel/participant name
//   - a container AXGroup holds many child AXGroups where each child has a
//     description like "Sender, Content, HH:MM" (or with trailing metadata)
//   - use window frame midpoint to detect user vs other via bubble x-center
func (p *messagesParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}
	window := tree
	if window.Role != "AXWindow" {
		w := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
		if w != nil {
			window = w
		}
	}

	channel := window.Title
	if channel == "" {
		channel = windowTitle
	}
	if channel == "" {
		return nil
	}

	// Find the container group that holds the message children.
	var container *AXNode
	Traverse(window, func(n *AXNode) bool {
		if n.Role != "AXGroup" || len(n.Children) == 0 {
			return true
		}
		hits := 0
		for _, c := range n.Children {
			if isMessageGroup(c) {
				hits++
				if hits > 2 {
					container = n
					return false
				}
			}
		}
		return true
	})
	if container == nil {
		return nil
	}

	// Window midpoint for left/right classification.
	var midX float64
	haveMid := false
	if window.Frame != nil && window.Frame.W > 0 {
		midX = window.Frame.X + window.Frame.W/2
		haveMid = true
	}

	var msgs []ConversationMessage
	var currentDate string
	for _, child := range container.Children {
		if !isMessageGroup(child) {
			if d := extractMessagesDate(child); d != "" {
				currentDate = d
			}
			continue
		}
		desc := child.Description
		if desc == "" {
			continue
		}
		sender, content, ts := parseMessageDescription(desc)
		if content == "" {
			continue
		}
		// Geometry override: if the bubble is on the right half of the window,
		// treat as self-sent.
		if haveMid && child.Frame != nil && child.Frame.W > 0 {
			bubbleMid := child.Frame.X + child.Frame.W/2
			if bubbleMid > midX {
				sender = "user"
			}
		}
		if currentDate != "" && ts != "" {
			ts = currentDate + " " + ts
		} else if currentDate != "" {
			ts = currentDate
		}
		msgs = append(msgs, ConversationMessage{
			Sender:    sender,
			Content:   content,
			Timestamp: ts,
		})
	}
	if len(msgs) == 0 {
		return nil
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       windowTitle,
		Channel:     channel,
		Messages:    msgs,
		Body:        renderConversationBody(msgs),
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "messages",
		},
	}
}

// extractMessagesDate looks for a date divider in a non-message-group node.
// macOS Messages renders day dividers as plain AXStaticText (or thin AXGroup
// wrappers around one), so we probe description/value/title at the node and
// its immediate children. Returns the matched date substring (e.g.
// "2024年11月6日") or "" when nothing date-like is present.
func extractMessagesDate(n *AXNode) string {
	if n == nil {
		return ""
	}
	for _, s := range []string{n.Description, n.Value, n.Title} {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if m := messagesDateHeaderRe.FindString(s); m != "" {
			return m
		}
	}
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		for _, s := range []string{c.Description, c.Value, c.Title} {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if m := messagesDateHeaderRe.FindString(s); m != "" {
				return m
			}
		}
	}
	return ""
}

// isMessageGroup returns true when the node looks like a single message
// bubble: an AXGroup with a description ending in a time pattern.
func isMessageGroup(n *AXNode) bool {
	if n == nil || n.Role != "AXGroup" || n.Description == "" {
		return false
	}
	if !strings.Contains(n.Description, ",") {
		return false
	}
	return messageDescTimeAtEnd.MatchString(n.Description) ||
		messageDescTimeWithMeta.MatchString(n.Description)
}

// parseMessageDescription splits "Sender, Content, HH:MM[, metadata]" into
// its parts. Returns sender="" when the description couldn't be parsed.
func parseMessageDescription(desc string) (sender, content, timestamp string) {
	parts := splitCSVPreservingQuotes(desc)
	if len(parts) < 3 {
		return "", "", ""
	}
	// The LAST comma-separated field that matches HH:MM is the timestamp;
	// everything before is sender+content, everything after is metadata.
	tsIdx := -1
	timePattern := regexp.MustCompile(`^\s*\d{1,2}:\d{2}\s*$`)
	for i := len(parts) - 1; i >= 0; i-- {
		if timePattern.MatchString(parts[i]) {
			tsIdx = i
			break
		}
	}
	if tsIdx < 1 {
		return "", "", ""
	}
	timestamp = strings.TrimSpace(parts[tsIdx])
	sender = strings.TrimSpace(parts[0])
	content = strings.TrimSpace(strings.Join(parts[1:tsIdx], ", "))
	return sender, content, timestamp
}

// splitCSVPreservingQuotes splits a string on commas. For AXDescription
// output this is a plain split (Apple doesn't emit quoted fields).
func splitCSVPreservingQuotes(s string) []string {
	return strings.Split(s, ",")
}
