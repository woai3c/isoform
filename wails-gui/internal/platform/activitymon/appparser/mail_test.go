package appparser

import (
	"strings"
	"testing"
)

// mailReadingPaneTree builds a tree that mirrors the modern macOS Mail
// reading-pane shape: an AXWindow containing a message_header AXGroup
// with labeled-identifier AXStaticText nodes plus the subject AXTextArea,
// followed by an AXWebArea for the body. Each identifier on the
// AXStaticText matches what live Mail emits.
func mailReadingPaneTree(windowTitle string) *AXNode {
	header := &AXNode{
		Role:       "AXGroup",
		Identifier: "message_header",
		Children: []*AXNode{
			{
				Role:       "AXTextArea",
				Identifier: "message.header.content",
				// Mail packs subject + recipient label into one value,
				// separated by U+FFFC glyphs and newlines.
				Value: "￼\n渡轮确认：Halcyon 至 Vesper Isle 的行程\n收件人: ￼￼",
			},
			{Role: "AXStaticText", Identifier: "message.from.0", Value: "Halcyon Rail"},
			{Role: "AXStaticText", Identifier: "message.to.0", Value: "maya.chen@example.com"},
			{Role: "AXStaticText", Identifier: "message.mailbox", Value: " ￼收件箱 - 旅行邮箱"},
			{Role: "AXStaticText", Identifier: "message.timestamp", Value: "2027年8月2日 14:26"},
		},
	}
	body := &AXNode{
		Role: "AXWebArea",
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "陈玛雅，你好，"},
			{Role: "AXStaticText", Value: "请于 2027 年 8 月 1 日前确认 Halcyon 至 Vesper Isle 的渡轮座位。"},
			{Role: "AXStaticText", Value: "Halcyon Rail"},
		},
	}
	return &AXNode{
		Role:  "AXWindow",
		Title: windowTitle,
		Children: []*AXNode{
			{
				Role: "AXScrollArea",
				Children: []*AXNode{
					{
						Role:       "AXGroup",
						Identifier: "message_view",
						Children:   []*AXNode{header, body},
					},
				},
			},
		},
	}
}

func TestMailParser_ReadingPaneExtractsAllHeaderFields(t *testing.T) {
	tree := mailReadingPaneTree("渡轮确认：Halcyon 至 Vesper Isle 的行程 – 收件箱 – 旅行 · 过滤条件：未读（42封邮件）")

	parsed := (&mailParser{}).ParseTree(tree, "com.apple.mail",
		"渡轮确认：Halcyon 至 Vesper Isle 的行程 – 收件箱 – 旅行 · 过滤条件：未读（42封邮件）", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for reading-pane mode")
	}
	if parsed.Metadata["view"] != "reading" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "reading")
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(parsed.Messages))
	}
	msg := parsed.Messages[0]
	if msg.Sender != "Halcyon Rail" {
		t.Errorf("sender = %q, want %q", msg.Sender, "Halcyon Rail")
	}
	if msg.Timestamp != "2027年8月2日 14:26" {
		t.Errorf("timestamp = %q, want %q", msg.Timestamp, "2027年8月2日 14:26")
	}
	if parsed.Title != "渡轮确认：Halcyon 至 Vesper Isle 的行程" {
		t.Errorf("title = %q, want subject", parsed.Title)
	}
	if parsed.Metadata["subject"] != "渡轮确认：Halcyon 至 Vesper Isle 的行程" {
		t.Errorf("metadata.subject = %q", parsed.Metadata["subject"])
	}
	if parsed.Metadata["recipient"] != "maya.chen@example.com" {
		t.Errorf("metadata.recipient = %q", parsed.Metadata["recipient"])
	}
	// Mailbox must come from message.mailbox (cleaner) rather than the
	// windowTitle suffix or the sidebar — the U+FFFC glyph must be stripped.
	if parsed.Channel != "收件箱 - 旅行邮箱" {
		t.Errorf("channel = %q, want %q (from message.mailbox)", parsed.Channel, "收件箱 - 旅行邮箱")
	}
	if strings.Contains(parsed.Channel, "￼") {
		t.Errorf("U+FFFC leaked into mailbox: %q", parsed.Channel)
	}
	for _, want := range []string{"你好", "Halcyon 至 Vesper Isle"} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, msg.Content)
		}
	}
}

func TestMailParser_BodyDoesNotEchoHeaderLabels(t *testing.T) {
	// Header AXStaticTexts under message_header must not get re-collected
	// into the body — otherwise the body starts with the sender and
	// timestamp duplicated from the message header.
	tree := mailReadingPaneTree("subject – mailbox")
	parsed := (&mailParser{}).ParseTree(tree, "com.apple.mail", "subject – mailbox", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	body := parsed.Messages[0].Content
	// "2027年8月2日 14:26" is the header timestamp — must not appear in body.
	if strings.Contains(body, "2027年8月2日 14:26") {
		t.Errorf("header timestamp leaked into body:\n%s", body)
	}
	// "maya.chen@example.com" is the recipient — also must not be in body.
	if strings.Contains(body, "maya.chen@example.com") {
		t.Errorf("recipient address leaked into body:\n%s", body)
	}
}

func TestMailParser_DashCompatibilityInWindowTitle(t *testing.T) {
	// Mail uses EN dash on modern macOS, EM dash on legacy builds, plain
	// hyphen in some intermediate builds. The mailbox fallback chain must
	// survive all three so dispatch never silently falls into the sidebar
	// path that returns the wrong group title.
	cases := []struct{ title, wantMailbox string }{
		{"Subject – Inbox", "Inbox"}, // EN dash
		{"Subject — Inbox", "Inbox"}, // EM dash
		{"Subject - Inbox", "Inbox"}, // hyphen
		{"Subject – Inbox · 过滤条件：未读", "Inbox"},
	}
	for _, c := range cases {
		got := mailPickMailbox("", c.title, nil)
		if got != c.wantMailbox {
			t.Errorf("mailPickMailbox(%q) = %q, want %q",
				c.title, got, c.wantMailbox)
		}
	}
}

func TestMailParser_SubjectStripsObjectReplacementAndRecipient(t *testing.T) {
	// The message.header.content textarea is one of the trickier signals
	// because Mail packs the subject between U+FFFC placeholders and a
	// "收件人:" echo. Validate each cleaning step in isolation.
	cases := []struct {
		name, raw, want string
	}{
		{
			name: "standard zh layout",
			raw:  "￼\n关键主题\n收件人: ￼￼",
			want: "关键主题",
		},
		{
			name: "english to-label",
			raw:  "￼\nImportant Update\nTo: someone@example.com",
			want: "Important Update",
		},
		{
			name: "empty leading lines",
			raw:  "\n\n\n实际主题\n收件人: x",
			want: "实际主题",
		},
		{
			name: "only placeholders",
			raw:  "￼\n￼\n￼",
			want: "",
		},
	}
	for _, c := range cases {
		hg := &AXNode{
			Role: "AXGroup",
			Children: []*AXNode{
				{
					Role:       "AXTextArea",
					Identifier: "message.header.content",
					Value:      c.raw,
				},
			},
		}
		got := mailExtractSubjectFromHeader(hg)
		if got != c.want {
			t.Errorf("%s: subject = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMailParser_InboxListFallbackEmitsPreviewMessages(t *testing.T) {
	// When no reading pane is open (no message_header AXGroup), the
	// parser falls back to enumerating Mail.messageList.cell.view rows.
	makeCell := func(sender, subject, date, summary string) *AXNode {
		return &AXNode{
			Role:       "AXUnknown",
			Identifier: "Mail.messageList.cell.view",
			Children: []*AXNode{
				{Role: "AXStaticText", Identifier: mailListCellAddress, Value: sender},
				{Role: "AXStaticText", Identifier: mailListCellSubject, Value: subject},
				{Role: "AXStaticText", Identifier: mailListCellDate, Value: date},
				{Role: "AXStaticText", Identifier: mailListCellSummary, Value: summary},
			},
		}
	}
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "收件箱 – 旅行",
		Children: []*AXNode{
			makeCell("Halcyon Rail", "渡轮确认", "2027/8/2", "请于 2027 年 8 月 1 日前..."),
			makeCell("Vesper Isle Print Studio", "驻留作品已归档", "2027/9/12", "陈玛雅，你的版画..."),
			makeCell("Halcyon Library", "秋季讲座预约成功", "2027/9/3", "天文讲座..."),
		},
	}
	parsed := (&mailParser{}).ParseTree(tree, "com.apple.mail", "收件箱 – 旅行", "")
	if parsed == nil {
		t.Fatal("expected inbox-list fallback to produce content")
	}
	if parsed.Metadata["view"] != "inbox" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "inbox")
	}
	if len(parsed.Messages) != 3 {
		t.Fatalf("expected 3 preview messages, got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Sender != "Halcyon Rail" {
		t.Errorf("first preview sender = %q, want %q", parsed.Messages[0].Sender, "Halcyon Rail")
	}
	if !strings.Contains(parsed.Messages[0].Content, "渡轮确认") {
		t.Errorf("subject missing from preview content: %q", parsed.Messages[0].Content)
	}
	if !strings.Contains(parsed.Messages[0].Content, "请于 2027") {
		t.Errorf("summary missing from preview content: %q", parsed.Messages[0].Content)
	}
	if parsed.Messages[0].Timestamp != "2027/8/2" {
		t.Errorf("preview timestamp = %q", parsed.Messages[0].Timestamp)
	}
}

func TestMailParser_ReadingPaneWinsOverInboxList(t *testing.T) {
	// Split-view windows contain both an inbox list and an open message.
	// The reading-pane path must run first so the parsed output is the
	// open message — list preview would dilute the signal.
	header := &AXNode{
		Role:       "AXGroup",
		Identifier: "message_header",
		Children: []*AXNode{
			{
				Role:       "AXTextArea",
				Identifier: "message.header.content",
				Value:      "￼\nOpen Message\n收件人: x",
			},
			{Role: "AXStaticText", Identifier: "message.from.0", Value: "Alice"},
			{Role: "AXStaticText", Identifier: "message.timestamp", Value: "Today 10:00"},
		},
	}
	body := &AXNode{
		Role: "AXWebArea",
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "Reading pane body."},
		},
	}
	listRow := &AXNode{
		Role:       "AXUnknown",
		Identifier: "Mail.messageList.cell.view",
		Children: []*AXNode{
			{Role: "AXStaticText", Identifier: mailListCellAddress, Value: "Bob"},
			{Role: "AXStaticText", Identifier: mailListCellSubject, Value: "Other thread"},
		},
	}
	tree := &AXNode{
		Role:     "AXWindow",
		Title:    "Open Message – Inbox",
		Children: []*AXNode{header, body, listRow},
	}
	parsed := (&mailParser{}).ParseTree(tree, "com.apple.mail", "Open Message – Inbox", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if parsed.Metadata["view"] != "reading" {
		t.Errorf("split-view should prefer reading pane, got view=%q",
			parsed.Metadata["view"])
	}
	if parsed.Messages[0].Sender != "Alice" {
		t.Errorf("expected Alice from reading pane, got %q", parsed.Messages[0].Sender)
	}
	if strings.Contains(parsed.Body, "Bob") || strings.Contains(parsed.Body, "Other thread") {
		t.Errorf("inbox list bled into reading-pane output:\n%s", parsed.Body)
	}
}
