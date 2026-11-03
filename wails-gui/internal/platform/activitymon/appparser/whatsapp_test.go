package appparser

import (
	"strings"
	"testing"
)

// TestWhatsappParser_CatalystMessages exercises the real shape WhatsApp
// Desktop (Mac Catalyst) emits: messages live in AXStaticText.description
// strings, prefixed with the U+200E LRM mark, formatted as
// "<phrase>, <body>, <Month><Day>,at<HH:MM>, <suffix>". The sidebar
// preview reuses the same format but lands in .value (not .description)
// and at a different x-coordinate.
func TestWhatsappParser_CatalystMessages(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "‎WhatsApp",
		Children: []*AXNode{
			// Sidebar header — must NOT win channel resolution.
			{Role: "AXHeading", Title: "‎Chats"},
			// Conversation header — wins channel.
			{Role: "AXHeading", Title: "alex example"},
			// In-pane date divider — must be excluded from channel.
			{Role: "AXHeading", Title: "Fri, Feb 13"},
			// Sidebar preview row: same message format but in .value,
			// not .description. Must be skipped.
			{
				Role:        "AXStaticText",
				Value:       "‎Your message, the latest body, February13,at14:06, ‎Sent to ‎You, ‎Red",
				Description: "alex example",
				Frame:       &AXFrame{X: -1372, Y: -1181},
			},
			// Actual conversation messages — description carries the
			// payload, sorted by y ascending (oldest to newest).
			{
				Role:        "AXStaticText",
				Description: "‎Your message, 你好, February13,at12:59, ‎Sent to ‎You, ‎Red",
				Frame:       &AXFrame{X: -996, Y: -1269},
			},
			{
				Role:        "AXStaticText",
				Description: "‎Your message, 我叫 pd，你叫奎因吧, February13,at13:07, ‎Sent to ‎You, ‎Red",
				Frame:       &AXFrame{X: -996, Y: -1134.5},
			},
			{
				Role:        "AXStaticText",
				Description: "‎Your message, the latest body, February13,at14:06, ‎Sent to ‎You, ‎Red",
				Frame:       &AXFrame{X: -996, Y: -624.5},
			},
		},
	}

	p := &whatsappParser{}
	got := p.ParseTree(tree, "net.whatsapp.WhatsApp", "‎WhatsApp", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "conversation" {
		t.Errorf("contentType: got %q want conversation", got.ContentType)
	}
	if got.Channel != "alex example" {
		t.Errorf("channel: got %q want alex example", got.Channel)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("expected 3 messages (sidebar preview skipped), got %d", len(got.Messages))
	}
	// Chronological order: oldest first.
	wantOrder := []struct {
		content string
		ts      string
	}{
		{"你好", "February13,at12:59"},
		{"我叫 pd，你叫奎因吧", "February13,at13:07"},
		{"the latest body", "February13,at14:06"},
	}
	for i, want := range wantOrder {
		got := got.Messages[i]
		if got.Content != want.content {
			t.Errorf("msg[%d] content: got %q want %q", i, got.Content, want.content)
		}
		if got.Timestamp != want.ts {
			t.Errorf("msg[%d] timestamp: got %q want %q", i, got.Timestamp, want.ts)
		}
		if got.Sender != "You" {
			t.Errorf("msg[%d] sender: got %q want You", i, got.Sender)
		}
	}
}

// TestWhatsappParser_MultilineBody guards the (?s) flag on the message
// regex — WhatsApp preserves embedded newlines from the original text,
// and lazy `.+?` must not stop at the first paragraph break.
func TestWhatsappParser_MultilineBody(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXHeading", Title: "Alice"},
			{
				Role:        "AXStaticText",
				Description: "‎Your message, line one\n\nline two\nline three, February13,at13:11, ‎Sent to ‎You, ‎Red",
				Frame:       &AXFrame{X: -996, Y: -1000},
			},
		},
	}
	p := &whatsappParser{}
	got := p.ParseTree(tree, "net.whatsapp.WhatsApp", "", "")
	if got == nil || len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", got)
	}
	m := got.Messages[0]
	if !strings.Contains(m.Content, "line one") ||
		!strings.Contains(m.Content, "line two") ||
		!strings.Contains(m.Content, "line three") {
		t.Errorf("multi-line body lost: %q", m.Content)
	}
}

// TestWhatsappParseDescription_SenderForms covers the three sender
// phrases WhatsApp emits: "Your message", "<name>'s message",
// and the bare "<name> message" (defensive — observed in some locales).
func TestWhatsappParseDescription_SenderForms(t *testing.T) {
	cases := []struct {
		name       string
		desc       string
		wantSender string
		wantBody   string
	}{
		{
			name:       "self",
			desc:       "‎Your message, hi, February13,at12:59, ‎Sent to ‎You, ‎Red",
			wantSender: "You",
			wantBody:   "hi",
		},
		{
			name:       "possessive other",
			desc:       "‎Alice's message, hey there, February13,at12:59, ‎From ‎Alice",
			wantSender: "Alice",
			wantBody:   "hey there",
		},
		{
			name:       "bare other (locale variant)",
			desc:       "‎Bob message, yo, February13,at12:59, ‎From ‎Bob",
			wantSender: "Bob",
			wantBody:   "yo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := whatsappParseDescription(tc.desc)
			if m == nil {
				t.Fatal("expected ConversationMessage, got nil")
			}
			if m.Sender != tc.wantSender {
				t.Errorf("sender: got %q want %q", m.Sender, tc.wantSender)
			}
			if m.Content != tc.wantBody {
				t.Errorf("body: got %q want %q", m.Content, tc.wantBody)
			}
		})
	}
}

// TestWhatsappParseDescription_NonMessage guards against false positives —
// sidebar headers, contact-name descriptions, and chrome strings must
// not parse as messages.
func TestWhatsappParseDescription_NonMessage(t *testing.T) {
	for _, desc := range []string{
		"alex example",
		"‎Search",
		"‎Your personal messages are end-to-end encrypted",
		"Fri, Feb 13",
		"",
	} {
		if m := whatsappParseDescription(desc); m != nil {
			t.Errorf("expected nil for %q, got %+v", desc, m)
		}
	}
}

// TestWhatsappParseDescription_WhitespaceVariants guards against the
// strict-single-space failure mode in earlier builds of whatsappMessageRE.
// macOS Catalyst has been observed emitting double spaces and U+00A0
// (no-break space) as inter-segment separators across version bumps; one
// missed space would otherwise zero out the Messages list silently.
func TestWhatsappParseDescription_WhitespaceVariants(t *testing.T) {
	tests := []struct {
		name string
		desc string
	}{
		{
			name: "double_space_after_comma",
			desc: "Your message,  Hello world,  Mar 5,at12:30,  Sent to Bob",
		},
		{
			name: "no_break_space_after_comma",
			desc: "Your message, Hello world, Mar 5,at12:30, Sent to Bob",
		},
		{
			name: "tab_after_comma",
			desc: "Your message,\tHello world,\tMar 5,at12:30,\tSent to Bob",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := whatsappParseDescription(tc.desc)
			if m == nil {
				t.Fatalf("expected message, got nil")
			}
			if m.Sender != "You" {
				t.Errorf("sender: got %q want You", m.Sender)
			}
			if m.Content != "Hello world" {
				t.Errorf("content: got %q want Hello world", m.Content)
			}
			if m.Timestamp != "Mar 5,at12:30" {
				t.Errorf("timestamp: got %q want Mar 5,at12:30", m.Timestamp)
			}
		})
	}
}
