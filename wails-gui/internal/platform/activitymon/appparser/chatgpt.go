package appparser

import (
	"sort"
	"strings"
)

// chatgptBundleIDs covers ChatGPT's native macOS app and its web client
// running under common browsers. Web variants rely on DOM class attributes;
// the native app has a rich AX tree with AXSplitGroup layout.
var chatgptBundleIDs = map[string]bool{
	"com.openai.chat": true,
}

type chatgptParser struct{}

func init() {
	Register(&chatgptParser{})
}

func (p *chatgptParser) CanParse(bundleID string) bool {
	return chatgptBundleIDs[bundleID]
}

func (p *chatgptParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "chatgpt",
		Label:       "ChatGPT (native)",
		ContentType: "conversation",
		Bundles:     []string{"com.openai.chat"},
	}
}

// ParseTree extracts the active ChatGPT conversation from the AX tree.
// Mirrors LittleBird's chatgpt_parser.js approach:
//   - find AXSplitGroup > last AXGroup > AXScrollArea > AXCollectionList > AXSectionList
//   - each AXGroup child of the section list is one message
//   - distinguish user (right-aligned) vs ChatGPT (left-aligned) by frame.x
//     via largest-gap clustering on unique x values.
func (p *chatgptParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	sectionList := findMessageSectionList(tree)
	if sectionList == nil {
		return nil
	}

	groups := ChildrenByRole(sectionList, "AXGroup")
	if len(groups) == 0 {
		return nil
	}

	// First pass: collect text parts + leading-text X position for each group.
	type msgData struct {
		textParts []string
		textX     float64
		hasX      bool
	}
	var data []msgData
	for _, g := range groups {
		parts := collectMessageTexts(g)
		if len(parts) == 0 {
			continue
		}
		x, hasX := firstTextX(g)
		data = append(data, msgData{textParts: parts, textX: x, hasX: hasX})
	}
	if len(data) == 0 {
		return nil
	}

	// Cluster unique X positions to find the user/assistant split.
	seen := map[float64]bool{}
	var uniq []float64
	for _, d := range data {
		if d.hasX && !seen[d.textX] {
			seen[d.textX] = true
			uniq = append(uniq, d.textX)
		}
	}
	sort.Float64s(uniq)
	splitX := 0.0
	haveSplit := false
	if len(uniq) >= 2 {
		maxGap := 0.0
		gapIdx := 0
		for i := 1; i < len(uniq); i++ {
			gap := uniq[i] - uniq[i-1]
			if gap > maxGap {
				maxGap = gap
				gapIdx = i
			}
		}
		splitX = (uniq[gapIdx-1] + uniq[gapIdx]) / 2
		haveSplit = true
	}

	messages := make([]ConversationMessage, 0, len(data))
	for _, d := range data {
		content := strings.TrimSpace(strings.Join(d.textParts, "\n"))
		if content == "" {
			continue
		}
		sender := "ChatGPT"
		if haveSplit && d.hasX && d.textX > splitX {
			sender = "user"
		}
		messages = append(messages, ConversationMessage{Sender: sender, Content: content})
	}
	if len(messages) == 0 {
		return nil
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       windowTitle,
		Channel:     "ChatGPT",
		Messages:    messages,
		Body:        renderConversationBody(messages),
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "chatgpt",
		},
	}
}

// findMessageSectionList walks the ChatGPT layout to find the AXSectionList
// that holds message groups. Two layouts are supported:
//  1. Main window:  AXSplitGroup > AXGroup (last) > AXScrollArea > AXCollectionList > AXSectionList
//  2. Popout:       AXGroup (AXHostingView) > AXScrollArea > AXCollectionList > AXSectionList
func findMessageSectionList(root *AXNode) *AXNode {
	var searchRoot *AXNode
	splitGroup := FindFirst(root, func(n *AXNode) bool { return n.Role == "AXSplitGroup" })
	if splitGroup != nil {
		// Main window layout: use last AXGroup child.
		groups := ChildrenByRole(splitGroup, "AXGroup")
		if len(groups) > 0 {
			searchRoot = groups[len(groups)-1]
		}
	} else {
		searchRoot = root
	}
	if searchRoot == nil {
		return nil
	}

	collectionList := FindFirst(searchRoot, func(n *AXNode) bool {
		return n.Role == "AXList" && n.Subrole == "AXCollectionList"
	})
	if collectionList == nil {
		return nil
	}

	for _, c := range collectionList.Children {
		if c != nil && c.Role == "AXList" && c.Subrole == "AXSectionList" {
			return c
		}
	}
	return nil
}

// collectMessageTexts gathers AXStaticText values from a message subtree,
// skipping scrollbars. Matches LittleBird's extractMessageContent walker.
func collectMessageTexts(msg *AXNode) []string {
	var parts []string
	Traverse(msg, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Description
			if t == "" {
				t = n.Value
			}
			if t != "" {
				parts = append(parts, t)
			}
		}
		return true
	})
	return parts
}

// firstTextX returns the X coordinate of the first leading text in the
// message group. Matches LittleBird's getFirstTextX helper: looks at the
// AXHostingView child and finds the first AXStaticText frame, or descends
// into AXScrollArea for code-block messages.
func firstTextX(group *AXNode) (float64, bool) {
	var hosting *AXNode
	for _, c := range group.Children {
		if c != nil && c.Subrole == "AXHostingView" {
			hosting = c
			break
		}
	}
	if hosting == nil {
		return 0, false
	}
	for _, child := range hosting.Children {
		if child == nil {
			continue
		}
		if child.Role == "AXStaticText" {
			t := child.Description
			if t == "" {
				t = child.Value
			}
			if t != "" && child.Frame != nil {
				return child.Frame.X, true
			}
		}
		if child.Role == "AXScrollArea" {
			textNode := FindFirst(child, func(n *AXNode) bool {
				if n.Role != "AXStaticText" {
					return false
				}
				t := n.Description
				if t == "" {
					t = n.Value
				}
				return t != ""
			})
			if textNode != nil && textNode.Frame != nil {
				return textNode.Frame.X, true
			}
		}
	}
	return 0, false
}

// renderConversationBody serializes structured messages into a flat text
// body suitable for prompt inclusion. Shared across chat parsers.
//
// Thread replies (messages with a non-empty ThreadParent) are rendered
// with a leading "    ↳ " marker so the threading is visible even in
// the flat text representation. Top-level messages keep the original
// "Sender [timestamp]: content" shape.
func renderConversationBody(messages []ConversationMessage) string {
	var sb strings.Builder
	for i, m := range messages {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		if m.ThreadParent != "" {
			sb.WriteString("    ↳ ")
		}
		if m.Sender != "" {
			sb.WriteString(m.Sender)
			if m.Timestamp != "" {
				sb.WriteString(" [")
				sb.WriteString(m.Timestamp)
				sb.WriteString("]")
			}
			sb.WriteString(": ")
		}
		sb.WriteString(m.Content)
	}
	return sb.String()
}
