package appparser

import (
	"strings"
	"testing"
)

// slackTestBuilders construct minimal AXNode trees mimicking real Slack
// captures. The shapes mirror what cmd/ax-tree-dump actually produces
// against current Slack desktop builds (see slack.go for prose on the
// virtual_list__item / c-message_kit__hover / sender_button /
// c-timestamp DOM scaffolding).

func slackMsg(sender, ts, body string) *AXNode {
	wrapperTitle := sender + ": " + body + ". " + ts + "."
	return &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"c-virtual_list__item"},
		Title:        wrapperTitle,
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				Subrole:      "AXDocument",
				DOMClassList: []string{"c-message_kit__hover"},
				Children: []*AXNode{
					{
						Role:         "AXButton",
						DOMClassList: []string{"c-message__sender_button"},
						Title:        sender,
					},
					{
						Role:         "AXLink",
						DOMClassList: []string{"c-timestamp"},
						Description:  ts,
					},
					{Role: "AXStaticText", Value: body},
				},
			},
		},
	}
}

func slackList(description string, msgs []*AXNode) *AXNode {
	return &AXNode{
		Role:        "AXList",
		Subrole:     "AXContentList",
		Description: description,
		Frame:       &AXFrame{W: 800, H: 600},
		Children:    msgs,
	}
}

func slackWindow(lists ...*AXNode) *AXNode {
	children := make([]*AXNode, 0, len(lists))
	children = append(children, lists...)
	return &AXNode{Role: "AXWindow", Title: "engineering - Isoform - Slack", Children: children}
}

func TestSlackParser_AXGated(t *testing.T) {
	// A gated tree only carries the native NSWindow chrome — no WebView
	// roles, no DOM classes outside chromiumChromeClasses. The parser
	// must surface a typed sentinel so downstream consumers can tell
	// this apart from a generic web-page miss.
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"RootView"},
				Children: []*AXNode{
					{Role: "AXGroup", DOMClassList: []string{"NonClientView"}},
					{Role: "AXGroup", DOMClassList: []string{"NativeFrameViewMac"}},
				},
			},
		},
	}

	p := &slackParser{}
	got := p.ParseTree(tree, "com.tinyspeck.slackmacgap", "Slack", "")
	if got == nil {
		t.Fatalf("expected sentinel, got nil")
	}
	if got.Metadata["axGated"] != "true" {
		t.Errorf("expected metadata.axGated=true, got %q", got.Metadata["axGated"])
	}
	if got.Metadata["app"] != "slack" {
		t.Errorf("expected metadata.app=slack, got %q", got.Metadata["app"])
	}
	if len(got.Messages) != 0 {
		t.Errorf("expected no messages in sentinel, got %d", len(got.Messages))
	}
}

func TestSlackParser_SingleChannelTwoMessages(t *testing.T) {
	tree := slackWindow(
		slackList("engineering (channel)", []*AXNode{
			slackMsg("Alice", "12:51 PM", "hello"),
			slackMsg("Bob", "12:52 PM", "hi"),
		}),
	)

	p := &slackParser{}
	got := p.ParseTree(tree, "com.tinyspeck.slackmacgap", "engineering - Isoform - Slack", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.ContentType != "conversation" {
		t.Errorf("contentType: got %q want conversation", got.ContentType)
	}
	if got.Channel != "#engineering" {
		t.Errorf("channel: got %q want #engineering", got.Channel)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages: got %d want 2 (%+v)", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Sender != "Alice" || got.Messages[0].Content != "hello" {
		t.Errorf("message[0]: got %+v want Alice/hello", got.Messages[0])
	}
	if got.Messages[1].Sender != "Bob" || got.Messages[1].Content != "hi" {
		t.Errorf("message[1]: got %+v want Bob/hi", got.Messages[1])
	}
	if !strings.Contains(got.Body, "Alice [12:51 PM]: hello") {
		t.Errorf("body missing Alice line: %q", got.Body)
	}
}

func TestSlackParser_ThreadReplyReorder(t *testing.T) {
	// Slack publishes the same parent message in both the main channel
	// list and the thread sidebar. Dedup must collapse the parent;
	// reorder must place the reply directly under the parent, even
	// though it lived in a different list in the source tree.
	tree := slackWindow(
		slackList("engineering (channel)", []*AXNode{
			slackMsg("Alice", "12:51 PM", "question?"),
			slackMsg("Carol", "12:55 PM", "unrelated"),
		}),
		slackList("Thread in engineering (channel, 1 reply)", []*AXNode{
			slackMsg("Alice", "12:51 PM", "question?"),
			slackMsg("Bob", "12:53 PM", "answer"),
		}),
	)

	p := &slackParser{}
	got := p.ParseTree(tree, "com.tinyspeck.slackmacgap", "engineering - Isoform - Slack", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Channel != "#engineering" {
		t.Errorf("channel: got %q want #engineering (must skip thread sidebar description)", got.Channel)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages: got %d want 3 (alice/bob/carol after dedup): %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Sender != "Alice" {
		t.Errorf("message[0]: got %q want Alice (parent first)", got.Messages[0].Sender)
	}
	if got.Messages[1].Sender != "Bob" {
		t.Errorf("message[1]: got %q want Bob (reply directly under parent)", got.Messages[1].Sender)
	}
	if got.Messages[1].ThreadParent != "Alice@12:51 PM" {
		t.Errorf("message[1].ThreadParent: got %q want Alice@12:51 PM", got.Messages[1].ThreadParent)
	}
	if got.Messages[2].Sender != "Carol" {
		t.Errorf("message[2]: got %q want Carol (unrelated message preserves top-level order)", got.Messages[2].Sender)
	}
}
