package appparser

import "testing"

func TestParseTree_NormalizesBlankLineRunsForEveryParser(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:  "AXTextArea",
				Value: "Trip Plan\n\n\n\nDay 1\n  \n\t\n\nDay 2",
			},
		},
	}

	parsed := ParseTree(tree, "", "com.apple.Notes", "Trip Plan", "")
	if parsed == nil {
		t.Fatal("expected Notes content from registry dispatch")
	}

	want := "Trip Plan\n\nDay 1\n\nDay 2"
	if parsed.Body != want {
		t.Errorf("Body = %q, want %q", parsed.Body, want)
	}
}

func TestNormalizeParsedContent_NormalizesConversationMessages(t *testing.T) {
	parsed := normalizeParsedContent(&ParsedContent{
		Body: "Alice: hello\n\n\nBob: reply",
		Messages: []ConversationMessage{
			{Sender: "Alice", Content: "hello\n\n\nworld"},
			{Sender: "Bob", Content: "reply\n  \n\t\n\nagain"},
		},
	})

	if parsed.Body != "Alice: hello\n\nBob: reply" {
		t.Errorf("Body = %q, want collapsed blank run", parsed.Body)
	}
	if parsed.Messages[0].Content != "hello\n\nworld" {
		t.Errorf("Messages[0].Content = %q, want collapsed blank run", parsed.Messages[0].Content)
	}
	if parsed.Messages[1].Content != "reply\n\nagain" {
		t.Errorf("Messages[1].Content = %q, want whitespace-padded blank run collapsed",
			parsed.Messages[1].Content)
	}
}
