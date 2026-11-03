package appparser

import (
	"strings"
)

var gmailWebHosts = []string{
	"mail.google.com",
}

type gmailParser struct{}

func init() {
	Register(&gmailParser{})
}

// CanParse always returns false — Gmail is web-only for our purposes.
func (p *gmailParser) CanParse(string) bool { return false }

func (p *gmailParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, gmailWebHosts...)
}

func (p *gmailParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "gmail",
		Label:       "Gmail",
		ContentType: "conversation",
		URLHosts:    append([]string(nil), gmailWebHosts...),
	}
}

// ParseTree extracts the focused Gmail surface. Two shapes matter:
//
//	Open thread (#inbox/<id>, #sent/<id>, #label/.../<id>)
//	  → conversation with one ConversationMessage per visible message
//
//	List view (#inbox, #sent, #starred, #label/...)
//	  → document body of subject lines + sender previews
//
// Gmail's DOM uses opaque generated class names but its ARIA roles are
// stable: the open-thread message stack is `role="list"` of
// `role="listitem"` blocks, each carrying a heading-ish sender row plus
// a body subtree. We anchor on the AXHeading subject + AXList items to
// extract the thread.
func (p *gmailParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
	if tree == nil {
		return nil
	}
	root := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if root == nil {
		return nil
	}

	if msgs, subject := gmailExtractThread(root, windowTitle); len(msgs) > 0 {
		return &ParsedContent{
			ContentType: "conversation",
			Title:       subject,
			Channel:     gmailMailbox(url, windowTitle),
			Messages:    msgs,
			Body:        renderConversationBody(msgs),
			URL:         url,
			Metadata: map[string]string{
				"bundleId": bundleID,
				"app":      "gmail",
				"page":     "thread",
			},
		}
	}

	if body := gmailExtractList(root); body != "" {
		return &ParsedContent{
			ContentType: "document",
			Title:       strings.TrimSpace(windowTitle),
			Body:        body,
			URL:         url,
			Metadata: map[string]string{
				"bundleId": bundleID,
				"app":      "gmail",
				"page":     "list",
			},
		}
	}
	return nil
}

// gmailExtractThread looks for the subject AXHeading at the top of the
// reading pane and the AXList of message AXGroups beneath it. Each
// message contributes one ConversationMessage. Returns (nil, "") when
// no thread is open.
func gmailExtractThread(root *AXNode, windowTitle string) ([]ConversationMessage, string) {
	heading := FindFirst(root, func(n *AXNode) bool { return n.Role == "AXHeading" })
	if heading == nil {
		return nil, ""
	}
	subject := strings.TrimSpace(Text(heading))
	if subject == "" {
		subject = strings.TrimSpace(strings.Split(windowTitle, " - ")[0])
	}

	list := FindFirst(root, func(n *AXNode) bool {
		return n.Role == "AXList" && len(n.Children) > 0
	})
	if list == nil {
		return nil, ""
	}

	var msgs []ConversationMessage
	for _, item := range list.Children {
		if item == nil {
			continue
		}
		// Only AXGroup-shaped items are messages; Gmail uses AXSeparator
		// between collapsed days etc.
		if item.Role != "AXGroup" && item.Role != "AXListItem" {
			continue
		}
		msg := gmailExtractMessage(item)
		if msg == nil {
			continue
		}
		msgs = append(msgs, *msg)
	}
	if len(msgs) == 0 {
		return nil, ""
	}
	return msgs, subject
}

// gmailExtractMessage pulls sender + body out of a single message group.
// Gmail puts the sender's display name in the first AXLink (mailto: link
// on the avatar) or in the first non-trivial AXStaticText with a `gD`
// class. Body content is the remaining AXStaticTexts in the subtree.
func gmailExtractMessage(group *AXNode) *ConversationMessage {
	sender := gmailMessageSender(group)
	timestamp := gmailMessageTimestamp(group)

	var bodyParts []string
	Traverse(group, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		// Skip the message-action toolbar and quick-reply UI.
		if HasClassContaining(n, "amn") || HasClassContaining(n, "amk") {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Value
			if t == "" {
				t = n.Description
			}
			t = strings.TrimSpace(t)
			if t == "" || t == sender || t == timestamp {
				return true
			}
			bodyParts = append(bodyParts, t)
		}
		return true
	})
	body := strings.TrimSpace(strings.Join(bodyParts, "\n"))
	if body == "" {
		return nil
	}
	if sender == "" {
		sender = "Email"
	}
	return &ConversationMessage{
		Sender:    sender,
		Content:   body,
		Timestamp: timestamp,
	}
}

func gmailMessageSender(group *AXNode) string {
	// Class `gD` is Gmail's wrapper around the sender display name in the
	// header row — stable across UI redesigns.
	tagged := FindFirst(group, func(n *AXNode) bool {
		return HasClassContaining(n, "gD") && (n.Value != "" || n.Title != "")
	})
	if tagged != nil {
		v := tagged.Value
		if v == "" {
			v = tagged.Title
		}
		return strings.TrimSpace(v)
	}
	// Fallback: first AXLink with a display label.
	link := FindFirst(group, func(n *AXNode) bool {
		return n.Role == "AXLink" && (n.Title != "" || n.Description != "")
	})
	if link != nil {
		v := link.Title
		if v == "" {
			v = link.Description
		}
		return strings.TrimSpace(v)
	}
	return ""
}

func gmailMessageTimestamp(group *AXNode) string {
	// Class `g3` wraps the relative date span in the message header.
	tagged := FindFirst(group, func(n *AXNode) bool {
		return HasClassContaining(n, "g3") && n.Value != ""
	})
	if tagged != nil {
		return strings.TrimSpace(tagged.Value)
	}
	return ""
}

// gmailExtractList renders the inbox / labeled-list view as a flat body
// of subject + sender + snippet lines. We anchor on AXRow elements,
// which Gmail uses for each conversation row.
func gmailExtractList(root *AXNode) string {
	rows := FindAll(root, func(n *AXNode) bool { return n.Role == "AXRow" })
	if len(rows) == 0 {
		return ""
	}
	var lines []string
	for _, row := range rows {
		texts := CollectStaticTexts(row)
		if len(texts) == 0 {
			continue
		}
		seen := map[string]bool{}
		var cells []string
		for _, t := range texts {
			t = strings.TrimSpace(t)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			cells = append(cells, t)
		}
		if len(cells) == 0 {
			continue
		}
		lines = append(lines, "- "+strings.Join(cells, " · "))
	}
	return strings.Join(lines, "\n")
}

// gmailMailbox derives the active mailbox / label from the URL hash
// fragment (Gmail SPA routes are #-based: #inbox, #sent, #label/...,
// #search/...). Falls back to the trailing window-title segment.
func gmailMailbox(rawURL, windowTitle string) string {
	if i := strings.Index(rawURL, "#"); i >= 0 {
		frag := rawURL[i+1:]
		if j := strings.Index(frag, "/"); j >= 0 {
			frag = frag[:j]
		}
		frag = strings.TrimSpace(frag)
		if frag != "" {
			return frag
		}
	}
	if windowTitle != "" {
		// "Subject - Inbox - user@example.com - Gmail"
		segs := strings.Split(windowTitle, " - ")
		if len(segs) >= 2 {
			return strings.TrimSpace(segs[len(segs)-2])
		}
	}
	return ""
}
