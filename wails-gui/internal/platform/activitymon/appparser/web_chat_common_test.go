package appparser

import (
	"strings"
	"testing"
)

// webChatTurnNode builds a message-turn AXGroup with the given class
// list, frame.y, and text bodies. Mirrors the real ChatGPT/Claude AX
// shape where each turn is an AXGroup containing AXStaticText nodes.
func webChatTurnNode(classes []string, y float64, texts ...string) *AXNode {
	var children []*AXNode
	for _, t := range texts {
		children = append(children, &AXNode{Role: "AXStaticText", Value: t})
	}
	return &AXNode{
		Role:         "AXGroup",
		DOMClassList: classes,
		Frame:        &AXFrame{Y: y},
		Children:     children,
	}
}

// webChatTree wraps a list of turn nodes inside an AXWebArea with the
// given title — the structure the live driver walks.
func webChatTree(title string, turns ...*AXNode) *AXNode {
	return &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:     "AXWebArea",
				Title:    title,
				Children: turns,
			},
		},
	}
}

// TestChatGPTWeb_ConversationExtraction locks the ChatGPT-web spec
// against the real class structure observed in /tmp/chatgpt-web.json.
func TestChatGPTWeb_ConversationExtraction(t *testing.T) {
	tree := webChatTree(
		"Universal Control 设置指南",
		webChatTurnNode([]string{"user-message-bubble-color"}, 100,
			"我有个macbook pro，我还有个mac mini"),
		webChatTurnNode([]string{"agent-turn", "group/turn-messages"}, 200,
			"我会按你这个场景来讲：",
			"最适合你的方案叫 Universal Control"),
	)

	parsed := (&chatgptWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://chatgpt.com/c/123")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if parsed.Title != "Universal Control 设置指南" {
		t.Errorf("title = %q, want %q", parsed.Title, "Universal Control 设置指南")
	}
	if parsed.Metadata["app"] != "chatgpt-web" {
		t.Errorf("metadata.app = %q, want %q", parsed.Metadata["app"], "chatgpt-web")
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Sender != "User" {
		t.Errorf("first sender = %q, want User", parsed.Messages[0].Sender)
	}
	if parsed.Messages[1].Sender != "ChatGPT" {
		t.Errorf("second sender = %q, want ChatGPT", parsed.Messages[1].Sender)
	}
	if !strings.Contains(parsed.Messages[1].Content, "Universal Control") {
		t.Errorf("agent body missing expected text:\n%s", parsed.Messages[1].Content)
	}
}

// TestChatGPTWebParser_CanParseURL guards the host whitelist for
// chatgpt.com + chat.openai.com — both have to be recognized because
// some users still land on the legacy chat.openai.com host.
func TestChatGPTWebParser_CanParseURL(t *testing.T) {
	p := &chatgptWebParser{}
	cases := map[string]bool{
		"https://chatgpt.com/c/abc":       true,
		"https://chatgpt.com":             true,
		"https://chat.openai.com/c/abc":   true,
		"https://chat.openai.com/g/g-foo": true,
		"https://www.openai.com/blog":     false,
		"https://notion.so/abc":           false,
		"":                                false,
	}
	for url, want := range cases {
		if got := p.CanParseURL(url); got != want {
			t.Errorf("CanParseURL(%q) = %v, want %v", url, got, want)
		}
	}
}

// TestClaudeWeb_ConversationExtraction covers the Claude.ai spec with
// the "!font-user-message" importance-prefixed class — substring match
// must find it even with the leading "!".
func TestClaudeWeb_ConversationExtraction(t *testing.T) {
	tree := webChatTree(
		"网球拍平衡点数据分析 - Claude",
		webChatTurnNode([]string{"!font-user-message", "font-large"}, 100,
			"请问网球拍的平衡数据影响什么。",
			"287/320/301"),
		webChatTurnNode([]string{"font-claude-response", "font-claude-response-body"}, 200,
			"网球拍参数解析与对比",
			"挥重越高越稳定"),
	)

	parsed := (&claudeWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://claude.ai/chat/abc")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	// "- Claude" suffix must be stripped from the title.
	if parsed.Title != "网球拍平衡点数据分析" {
		t.Errorf("title = %q, want suffix-stripped form", parsed.Title)
	}
	if parsed.Metadata["app"] != "claude-web" {
		t.Errorf("metadata.app = %q, want %q", parsed.Metadata["app"], "claude-web")
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Sender != "User" {
		t.Errorf("user sender = %q", parsed.Messages[0].Sender)
	}
	if parsed.Messages[1].Sender != "Claude" {
		t.Errorf("agent sender = %q, want Claude", parsed.Messages[1].Sender)
	}
	if !strings.Contains(parsed.Messages[1].Content, "挥重越高越稳定") {
		t.Errorf("agent content missing: %q", parsed.Messages[1].Content)
	}
}

// TestClaudeWebParser_CanParseURL guards the claude.ai whitelist.
func TestClaudeWebParser_CanParseURL(t *testing.T) {
	p := &claudeWebParser{}
	cases := map[string]bool{
		"https://claude.ai/chat/abc":   true,
		"https://claude.ai":            true,
		"https://chatgpt.com/c/abc":    false,
		"https://anthropic.com/claude": false,
		"":                             false,
	}
	for url, want := range cases {
		if got := p.CanParseURL(url); got != want {
			t.Errorf("CanParseURL(%q) = %v, want %v", url, got, want)
		}
	}
}

// TestWebChatDriver_OrdersByFrameY pins the chronological-by-y sort.
// DOM order alone is unsafe because some chat UIs use virtualized
// scrollers that re-mount messages out of order on long threads.
func TestWebChatDriver_OrdersByFrameY(t *testing.T) {
	// Insert turns in reverse y order; driver must sort them back.
	tree := webChatTree(
		"unordered",
		webChatTurnNode([]string{"agent-turn"}, 500, "third (agent)"),
		webChatTurnNode([]string{"user-message-bubble-color"}, 100, "first (user)"),
		webChatTurnNode([]string{"agent-turn"}, 300, "second (agent)"),
	)
	parsed := (&chatgptWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://chatgpt.com/c/x")
	if parsed == nil {
		t.Fatal("expected non-nil")
	}
	if len(parsed.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(parsed.Messages))
	}
	wantOrder := []string{"first (user)", "second (agent)", "third (agent)"}
	for i, w := range wantOrder {
		if !strings.Contains(parsed.Messages[i].Content, w) {
			t.Errorf("message[%d] content = %q, want to contain %q",
				i, parsed.Messages[i].Content, w)
		}
	}
}

// TestWebChatDriver_PrunesAtMatchedTurn guards against double-counting:
// when an agent-turn class is on an outer container, the driver must
// not also pick up nested user-bubble fragments inside that subtree
// (some plugin renderings inside Claude responses inherit class space
// from the outer container).
func TestWebChatDriver_PrunesAtMatchedTurn(t *testing.T) {
	agentTurn := webChatTurnNode([]string{"agent-turn"}, 200, "agent says hi")
	// Inject a nested user-bubble inside the agent turn — driver must
	// NOT emit it as a second user turn.
	agentTurn.Children = append(agentTurn.Children, &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"user-message-bubble-color"},
		Frame:        &AXFrame{Y: 220},
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "this is a nested fragment inside agent"},
		},
	})
	tree := webChatTree("nested",
		webChatTurnNode([]string{"user-message-bubble-color"}, 100, "real user"),
		agentTurn,
	)
	parsed := (&chatgptWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://chatgpt.com/c/x")
	if parsed == nil {
		t.Fatal("expected non-nil")
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("expected 2 messages (1 user + 1 agent), got %d:\n%+v",
			len(parsed.Messages), parsed.Messages)
	}
}

// TestWebChatDriver_SkipsActionToolbarChrome verifies the
// hover-revealed Copy/Edit/Regenerate toolbar (opacity-0 +
// pointer-events-none) is not collected as body text.
func TestWebChatDriver_SkipsActionToolbarChrome(t *testing.T) {
	agentTurn := &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"agent-turn"},
		Frame:        &AXFrame{Y: 100},
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "Main response text."},
			{
				Role: "AXGroup",
				DOMClassList: []string{
					"pointer-events-none",
					"opacity-0",
					"some-toolbar-class",
				},
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "Copy"},
					{Role: "AXStaticText", Value: "Edit"},
					{Role: "AXStaticText", Value: "Regenerate"},
				},
			},
		},
	}
	tree := webChatTree("toolbar test", agentTurn)
	parsed := (&chatgptWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://chatgpt.com/c/x")
	if parsed == nil {
		t.Fatal("expected non-nil")
	}
	body := parsed.Messages[0].Content
	if !strings.Contains(body, "Main response text.") {
		t.Errorf("main text missing: %q", body)
	}
	for _, leaked := range []string{"Copy", "Edit", "Regenerate"} {
		if strings.Contains(body, leaked) {
			t.Errorf("action toolbar leaked into body (%q):\n%s", leaked, body)
		}
	}
}

// TestWebChatDriver_NoMessagesReturnsNil covers the empty-page case —
// the parser must yield nil so the dispatcher can fall back to the
// generic browser parser instead of emitting an empty conversation.
func TestWebChatDriver_NoMessagesReturnsNil(t *testing.T) {
	tree := webChatTree("empty conversation")
	parsed := (&chatgptWebParser{}).ParseTree(tree, "com.google.Chrome", "", "https://chatgpt.com/")
	if parsed != nil {
		t.Errorf("expected nil for message-less page, got %+v", parsed)
	}
}
