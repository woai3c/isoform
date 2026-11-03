package appparser

import (
	"strings"
	"testing"
)

// discordHeader builds an AXHeading mimicking the cluster-header node
// real Discord captures publish — its title is the sole source of
// sender + timestamp. Pass an empty title to simulate a cluster
// follow-up (no header).
func discordHeader(title string) *AXNode {
	return &AXNode{
		Role:         "AXHeading",
		Title:        title,
		DOMClassList: []string{"header_c19a55"},
		Children: []*AXNode{
			{Role: "AXGroup", DOMClassList: []string{"headerText_c19a55"}},
			{Role: "AXGroup", DOMClassList: []string{"timestamp_c19a55", "timestampInline_c19a55"}},
		},
	}
}

// discordMessageContent builds the AXGroup[messageContent_…] subtree
// that holds visible body text + inline emoji buttons. The variadic
// items are added as children verbatim — pass AXStaticText for text
// runs, AXButton[emojiContainer__…] for emoji.
func discordMessageContent(children ...*AXNode) *AXNode {
	return &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"markup__75297", "messageContent_c19a55"},
		Children:     children,
	}
}

// discordListItem assembles the messageListItem wrapper around a header
// + body, matching the real AX shape:
//
//	AXGroup[messageListItem__5126c]
//	  AXGroup[AXDocumentArticle, message__5126c, …]
//	    AXHeading[header_c19a55]   (or omitted for follow-ups)
//	    AXGroup[messageContent_c19a55]
//
// Pass headerTitle="" to simulate a cluster follow-up (no AXHeading).
func discordListItem(headerTitle string, body *AXNode, extraArticleChildren ...*AXNode) *AXNode {
	var articleChildren []*AXNode
	articleChildren = append(articleChildren, extraArticleChildren...)
	if headerTitle != "" {
		articleChildren = append(articleChildren, discordHeader(headerTitle))
	}
	articleChildren = append(articleChildren, body)
	return &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"messageListItem__5126c"},
		Children: []*AXNode{
			{
				Role:    "AXGroup",
				Subrole: "AXDocumentArticle",
				DOMClassList: []string{
					"message__5126c", "cozyMessage__5126c", "groupStart__5126c",
					"wrapper_c19a55", "cozy_c19a55", "zalgo_c19a55",
				},
				Children: articleChildren,
			},
		},
	}
}

func discordTextRun(s string) *AXNode {
	return &AXNode{Role: "AXStaticText", Value: s}
}

func discordWindow(title string, listitems ...*AXNode) *AXNode {
	return &AXNode{
		Role:     "AXWindow",
		Title:    title,
		Children: listitems,
	}
}

func TestDiscordParser_CanParse(t *testing.T) {
	p := &discordParser{}
	for _, b := range []string{"com.hnc.Discord", "com.discordapp.Discord"} {
		if !p.CanParse(b) {
			t.Errorf("CanParse(%q) = false, want true", b)
		}
	}
	if p.CanParse("com.example.NotDiscord") {
		t.Errorf("CanParse accepted unrelated bundle id")
	}
}

func TestDiscordParser_HeadingDrivesSenderAndTimestamp(t *testing.T) {
	tree := discordWindow(
		"@alex_halcyon - Discord",
		discordListItem("alex_halcyon 2031/7/1 08:15",
			discordMessageContent(discordTextRun("sample greeting"))),
		discordListItem("Priya 2031/7/2 09:20",
			discordMessageContent(discordTextRun("sample response"))),
	)

	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Channel != "@alex_halcyon" {
		t.Errorf("channel: got %q want @alex_halcyon", got.Channel)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages: got %d want 2 (%+v)", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Sender != "alex_halcyon" || got.Messages[0].Content != "sample greeting" {
		t.Errorf("message[0]: got %+v want alex_halcyon/sample greeting", got.Messages[0])
	}
	if got.Messages[0].Timestamp != "2031/7/1 08:15" {
		t.Errorf("message[0].Timestamp: got %q want %q",
			got.Messages[0].Timestamp, "2031/7/1 08:15")
	}
	if got.Messages[1].Sender != "Priya" || got.Messages[1].Content != "sample response" {
		t.Errorf("message[1]: got %+v want Priya/sample response", got.Messages[1])
	}
	if got.Messages[1].Timestamp != "2031/7/2 09:20" {
		t.Errorf("message[1].Timestamp: got %q want %q",
			got.Messages[1].Timestamp, "2031/7/2 09:20")
	}
}

func TestDiscordParser_HeadingRelativeTimestamp(t *testing.T) {
	cases := []struct {
		title  string
		sender string
		ts     string
	}{
		{"Priya 昨天10:34", "Priya", "昨天10:34"},
		{"alex_halcyon 今天 11:51", "alex_halcyon", "今天 11:51"},
		{"Bot 前天 08:12", "Bot", "前天 08:12"},
		{"Alice Today at 11:51 PM", "Alice", "Today at 11:51 PM"},
		{"Bob Yesterday at 8:01 AM", "Bob", "Yesterday at 8:01 AM"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			tree := discordWindow("Discord",
				discordListItem(tc.title,
					discordMessageContent(discordTextRun("sample body"))))
			p := &discordParser{}
			got := p.ParseTree(tree, "com.hnc.Discord", "Discord", "")
			if got == nil || len(got.Messages) != 1 {
				t.Fatalf("expected 1 message, got %+v", got)
			}
			if got.Messages[0].Sender != tc.sender {
				t.Errorf("sender: got %q want %q", got.Messages[0].Sender, tc.sender)
			}
			if got.Messages[0].Timestamp != tc.ts {
				t.Errorf("timestamp: got %q want %q", got.Messages[0].Timestamp, tc.ts)
			}
		})
	}
}

func TestDiscordParser_ReplyPreviewDoesNotHijackSender(t *testing.T) {
	// This is the bug the rewrite fixes: a "reply to Priya" message
	// publishes an AXButton[username_c19a55] whose title is "@Priya"
	// inside the repliedMessage subtree. The naïve "find AXButton
	// with username class" strategy attributed every such message to
	// Priya. The new parser ignores the reply block and reads sender
	// from AXHeading instead.
	replyBlock := &AXNode{
		Role:         "AXGroup",
		Description:  "alex_halcyon 正在回复 Priya",
		DOMClassList: []string{"repliedMessage_c19a55"},
		Children: []*AXNode{
			{
				Role:         "AXButton",
				Title:        "@Priya",
				DOMClassList: []string{"username_c19a55", "clickable_c19a55"},
			},
			{
				Role:         "AXButton",
				Title:        "REPLY_PREVIEW_BODY_MUST_NOT_LEAK",
				DOMClassList: []string{"repliedTextPreview_c19a55", "clickable_c19a55"},
			},
		},
	}
	tree := discordWindow("@alex_halcyon - Discord",
		discordListItem("alex_halcyon 2031/7/1 08:16",
			discordMessageContent(discordTextRun("示例回复用于验证边界情况")),
			replyBlock,
		),
	)
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil || len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", got)
	}
	if got.Messages[0].Sender != "alex_halcyon" {
		t.Errorf("sender: got %q want alex_halcyon (reply preview must not hijack)",
			got.Messages[0].Sender)
	}
	if strings.Contains(got.Messages[0].Content, "REPLY_PREVIEW_BODY_MUST_NOT_LEAK") {
		t.Errorf("body leaked replied-text preview: %q", got.Messages[0].Content)
	}
}

func TestDiscordParser_ClusterFollowupCarryover(t *testing.T) {
	tree := discordWindow("@alex_halcyon - Discord",
		discordListItem("Priya 2031/7/11 10:23",
			discordMessageContent(discordTextRun("sample first message"))),
		// follow-up: no header (cluster member)
		discordListItem("",
			discordMessageContent(discordTextRun("sample follow-up"))),
	)
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil || len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %+v", got)
	}
	if got.Messages[1].Sender != "Priya" {
		t.Errorf("follow-up sender: got %q want Priya (carryover)", got.Messages[1].Sender)
	}
	if got.Messages[1].Timestamp != "2031/7/11 10:23" {
		t.Errorf("follow-up timestamp: got %q want carryover from header",
			got.Messages[1].Timestamp)
	}
}

func TestDiscordParser_TimestampCarryoverLeadingStaysEmpty(t *testing.T) {
	tree := discordWindow("@alex_halcyon - Discord",
		discordListItem("", discordMessageContent(discordTextRun("sample leading orphan"))),
		discordListItem("Priya 2031/7/11 10:23",
			discordMessageContent(discordTextRun("sample later message"))),
	)
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	// The leading follow-up has no sender carryover available either,
	// so it should be dropped entirely.
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message (leading orphan dropped), got %+v", got.Messages)
	}
	if got.Messages[0].Sender != "Priya" || got.Messages[0].Timestamp != "2031/7/11 10:23" {
		t.Errorf("kept message: got %+v want Priya / 2031/7/11 10:23", got.Messages[0])
	}
}

func TestDiscordParser_EmojiInBody(t *testing.T) {
	emoji := &AXNode{
		Role:         "AXButton",
		Title:        "😮‍💨",
		DOMClassList: []string{"emojiContainer__75abc", "emojiContainerClickable__75abc"},
	}
	tree := discordWindow("@alex_halcyon - Discord",
		discordListItem("Priya 2031/7/1 07:08",
			discordMessageContent(
				discordTextRun("Sample "),
				emoji,
				discordTextRun(" body"),
			)),
	)
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil || len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", got)
	}
	if !strings.Contains(got.Messages[0].Content, "😮‍💨") {
		t.Errorf("emoji missing from body: %q", got.Messages[0].Content)
	}
}

func TestDiscordParser_DMChannelFromWindowTitle(t *testing.T) {
	tree := discordWindow("@maya_chen - Discord",
		discordListItem("maya_chen 2031/7/3 09:01",
			discordMessageContent(discordTextRun("sample morning message"))))
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Channel != "@maya_chen" {
		t.Errorf("channel: got %q want @maya_chen", got.Channel)
	}
}

func TestDiscordParser_ChannelFromWebAreaWhenWindowTitleBlank(t *testing.T) {
	// In real captures windowTitle can be empty and the AXWindow's
	// own Title is the only signal. Cover the case where even that
	// is empty and an inner AXWebArea carries the label.
	wa := &AXNode{Role: "AXWebArea", Title: "@alex_halcyon"}
	li := discordListItem("alex_halcyon 2031/7/1 08:15",
		discordMessageContent(discordTextRun("sample ping")))
	tree := &AXNode{Role: "AXWindow", Children: []*AXNode{wa, li}}
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Channel != "@alex_halcyon" {
		t.Errorf("channel: got %q want @alex_halcyon (AXWebArea fallback)", got.Channel)
	}
}

func TestDiscordParser_TextChannelGetsHashPrefix(t *testing.T) {
	tree := discordWindow("#general - Halcyon - Discord",
		discordListItem("Alex 2031/7/4 09:01",
			discordMessageContent(discordTextRun("sample channel message"))))
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "#general - Halcyon - Discord", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Channel != "#general" {
		t.Errorf("channel: got %q want #general", got.Channel)
	}
}

func TestDiscordParser_AXGated(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXGroup", DOMClassList: []string{"RootView"}, Children: []*AXNode{
				{Role: "AXGroup", DOMClassList: []string{"NonClientView"}},
				{Role: "AXGroup", DOMClassList: []string{"NativeFrameViewMac"}},
			}},
		},
	}
	p := &discordParser{}
	got := p.ParseTree(tree, "com.hnc.Discord", "Discord", "")
	if got == nil {
		t.Fatalf("expected sentinel, got nil")
	}
	if got.Metadata["axGated"] != "true" {
		t.Errorf("expected metadata.axGated=true, got %q", got.Metadata["axGated"])
	}
}

// ── discordSplitHeading direct coverage ──

func TestDiscordSplitHeading(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantSender    string
		wantTimestamp string
	}{
		{"absolute_date", "alex_halcyon 2031/7/1 08:15", "alex_halcyon", "2031/7/1 08:15"},
		{"bare_absolute_date", "halcyon 2031/6/29", "halcyon", "2031/6/29"},
		{"bare_absolute_date_with_comma", "halcyon 2031/6/30,", "halcyon", "2031/6/30,"},
		{"chinese_relative_no_space", "alex_halcyon 昨天10:34", "alex_halcyon", "昨天10:34"},
		{"chinese_relative_with_space", "alex_halcyon 今天 11:51", "alex_halcyon", "今天 11:51"},
		{"english_today_at_with_ampm", "Priya Today at 11:51 PM", "Priya", "Today at 11:51 PM"},
		// New: bare HH:MM with no date prefix (Discord's default for messages
		// sent the same day). Previously the regex required a date prefix and
		// fell through to "treat the whole string as sender", which made the
		// Who you know page accumulate one Anonymous bucket per (name, timestamp).
		{"bare_hhmm_today_shorthand", "Maya Chen 10:25", "Maya Chen", "10:25"},
		{"bare_hhmm_ampm_today_shorthand", "Maya Chen 10:25 PM", "Maya Chen", "10:25 PM"},
		// Role-badge stripping: when Discord renders a server-owner crown,
		// the OP "Original Poster" thread tag, or the BOT label inside the
		// AX header, the AX title concatenates them with the username — the
		// regex keeps them in the sender capture and we strip them here.
		{"strips_op_original_poster", "maya_chen OP Original Poster 10:12", "maya_chen", "10:12"},
		{"strips_original_poster_only", "maya_chen Original Poster 10:12", "maya_chen", "10:12"},
		{"strips_bot_tag", "Yansu BOT 08:01", "Yansu", "08:01"},
		{"strips_server_owner", "Boss Server Owner Today at 10:00 AM", "Boss", "Today at 10:00 AM"},
		// No timestamp at all — sender is the whole title, role badge still
		// gets stripped if present at the tail.
		{"empty_input", "", "", ""},
		{"no_timestamp_strips_badge", "Yansu BOT", "Yansu", ""},
		// No-timestamp titles that are clearly NOT a sender header (message
		// body / system card bled into the heading) are dropped rather than
		// surfaced as a garbage sender. The welcome card with a clan badge
		// is the canonical case that polluted the Who you know page.
		{"drops_welcome_card_with_server_tag", "welcome Server Tag: X\\ This is a sample welcome!", "", ""},
		{"drops_overlong_body_bleed", "this is a synthetic long line of message body that bled into the heading node", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSender, gotTimestamp := discordSplitHeading(c.input)
			if gotSender != c.wantSender {
				t.Errorf("sender = %q, want %q", gotSender, c.wantSender)
			}
			if gotTimestamp != c.wantTimestamp {
				t.Errorf("timestamp = %q, want %q", gotTimestamp, c.wantTimestamp)
			}
		})
	}
}

func TestStripDiscordRoleBadges(t *testing.T) {
	cases := []struct{ in, want string }{
		{"maya_chen OP Original Poster", "maya_chen"},
		{"maya_chen Original Poster", "maya_chen"},
		{"Yansu BOT", "Yansu"},
		{"Yansu bot", "Yansu"}, // case-insensitive suffix match
		{"Boss Server Owner", "Boss"},
		{"Alex", "Alex"}, // no badge — untouched
		{"", ""},
		// Multiple badges back-to-back get stripped one at a time.
		{"Boss Server Owner Original Poster", "Boss"},
		// A user literally named after a badge must NOT be stripped to
		// the empty string — Discord allows usernames like "Bot" or
		// "App", and the stripper would erase them if it treated the
		// whole input as a badge match. The "always require a leading
		// space" rule defends this.
		{"Bot", "Bot"},
		{"App", "App"},
		{"Server Owner", "Server Owner"},
	}
	for _, c := range cases {
		got := stripDiscordRoleBadges(c.in)
		if got != c.want {
			t.Errorf("stripDiscordRoleBadges(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
