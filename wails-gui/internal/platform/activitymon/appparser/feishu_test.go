package appparser

import (
	"strings"
	"testing"
)

// staticTextNode is a one-line helper for building AXStaticText leaves
// in the fixtures below. Frame is omitted because feishuParseChatRow
// reads frame only off the card container, not the static texts.
func staticTextNode(value string) *AXNode {
	return &AXNode{Role: "AXStaticText", Value: value}
}

// chatCardNode wraps a sequence of AXStaticText leaves in an AXGroup
// carrying the stable a11y_feed_card_item class anchor. The frame's Y
// drives the sort order in the rendered body.
func chatCardNode(y float64, statics ...*AXNode) *AXNode {
	return &AXNode{
		Role:         "AXGroup",
		DOMClassList: []string{"a11y_feed_card_item"},
		Frame:        &AXFrame{Y: y},
		Children:     statics,
	}
}

func TestFeishuCanParse(t *testing.T) {
	p := &feishuParser{}
	if !p.CanParse("com.electron.lark") {
		t.Fatal("expected com.electron.lark to match")
	}
	if p.CanParse("com.tencent.xinWeChat") {
		t.Fatal("WeChat bundle must not match")
	}
}

func TestFeishuParseTreeFullSnapshot(t *testing.T) {
	// Mirrors the real dump from the WeChat / Feishu investigation:
	// a compose textarea announcing the open channel + four chat-list
	// rows including an external badge and a fragmented @-mention
	// preview.
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:  "AXTextArea",
				Value: "发送给 Yansu-Team\n​\n​",
			},
			chatCardNode(-20,
				staticTextNode("Yansu-Team"),
				staticTextNode("4月27日"),
				staticTextNode("pd"),
				staticTextNode(": "),
				staticTextNode("@"),
				staticTextNode("YanSu"),
				staticTextNode(" hello"),
			),
			chatCardNode(42,
				staticTextNode("pd, pd"),
				staticTextNode("外部"),
				staticTextNode("4月8日"),
				staticTextNode("欢迎加入外部群"),
			),
			chatCardNode(104,
				staticTextNode("test"),
				staticTextNode("4月8日"),
				staticTextNode("yansu local test"),
				staticTextNode(": "),
				staticTextNode("**[Yansu Automation]** done"),
			),
			chatCardNode(166,
				staticTextNode("yansu local"),
				staticTextNode("3月19日"),
				staticTextNode("pd 创建了群组"),
			),
		},
	}

	got := (&feishuParser{}).ParseTree(tree, "com.electron.lark", "飞书", "")
	if got == nil {
		t.Fatal("expected non-nil ParsedContent")
	}
	if got.ContentType != "conversation" {
		t.Errorf("ContentType = %q, want %q", got.ContentType, "conversation")
	}
	if got.Channel != "Yansu-Team" {
		t.Errorf("Channel = %q, want %q", got.Channel, "Yansu-Team")
	}
	if got.Metadata["axCoverage"] != AXCoverageChatListOnly {
		t.Errorf("axCoverage = %q, want %q",
			got.Metadata["axCoverage"], AXCoverageChatListOnly)
	}
	if got.Metadata["app"] != feishuAppName {
		t.Errorf("app metadata = %q, want %q", got.Metadata["app"], feishuAppName)
	}

	wantSubstrings := []string{
		"Currently in: Yansu-Team",
		"Recent chats:",
		"- Yansu-Team (4月27日): pd: @YanSu hello",
		"- pd, pd [外部] (4月8日): 欢迎加入外部群",
		"- test (4月8日): yansu local test: **[Yansu Automation]** done",
		"- yansu local (3月19日): pd 创建了群组",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got.Body, want) {
			t.Errorf("Body missing %q\n--- body ---\n%s", want, got.Body)
		}
	}

	// Top-to-bottom ordering: Yansu-Team (y=-20) must come before
	// yansu local (y=166).
	yansuIdx := strings.Index(got.Body, "- Yansu-Team")
	localIdx := strings.Index(got.Body, "- yansu local")
	if yansuIdx < 0 || localIdx < 0 || yansuIdx > localIdx {
		t.Errorf("rows out of y-order: yansuTeam=%d, yansuLocal=%d", yansuIdx, localIdx)
	}
}

func TestFeishuParseTreeNoChannelButRowsPresent(t *testing.T) {
	// Feishu splash with no compose box but a populated inbox.
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			chatCardNode(0,
				staticTextNode("yansu local"),
				staticTextNode("3月19日"),
				staticTextNode("pd 创建了群组"),
			),
		},
	}
	got := (&feishuParser{}).ParseTree(tree, "com.electron.lark", "飞书", "")
	if got == nil {
		t.Fatal("expected non-nil ParsedContent with rows alone")
	}
	if got.Channel != "" {
		t.Errorf("Channel = %q, want empty", got.Channel)
	}
	if !strings.HasPrefix(got.Body, "Recent chats:") {
		t.Errorf("Body should lead with Recent chats when no channel; got %q", got.Body)
	}
}

func TestFeishuParseTreeChannelOnlyNoRows(t *testing.T) {
	// User opened a chat but the list panel scrolled off / collapsed.
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "发送给 SoloChat\n"},
		},
	}
	got := (&feishuParser{}).ParseTree(tree, "com.electron.lark", "飞书", "")
	if got == nil {
		t.Fatal("expected non-nil ParsedContent with channel alone")
	}
	if got.Channel != "SoloChat" {
		t.Errorf("Channel = %q, want %q", got.Channel, "SoloChat")
	}
	if got.Body != "Currently in: SoloChat" {
		t.Errorf("Body = %q, want %q", got.Body, "Currently in: SoloChat")
	}
}

func TestFeishuParseTreeEmptyReturnsNil(t *testing.T) {
	// Neither channel nor rows reachable — must produce nil so the
	// activity pipeline falls back to raw AX text / OCR.
	tree := &AXNode{Role: "AXWindow"}
	got := (&feishuParser{}).ParseTree(tree, "com.electron.lark", "飞书", "")
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestFeishuDateRegex(t *testing.T) {
	cases := []struct {
		in    string
		match bool
	}{
		{"4月27日", true},
		{"12月3日", true},
		{"2024年12月3日", true},
		{"昨天", true},
		{"今天", true},
		{"星期一", true},
		{"星期日", true},
		{"星期天", true},
		{"14:23", true},
		{"2024-12-03", true},
		{"2024/12/03", true},
		// Not dates:
		{"Yansu-Team", false},
		{"外部", false},
		{"pd 创建了群组", false},
		{":", false},
	}
	for _, c := range cases {
		got := feishuDateRE.MatchString(c.in)
		if got != c.match {
			t.Errorf("feishuDateRE.MatchString(%q) = %v, want %v", c.in, got, c.match)
		}
	}
}

// feishuFixtureWindow mirrors the real two-WebView tree CUA dumps from
// modern Lark builds: a window root with two sibling WebView subtrees
// — the inbox sidebar (anchored on a11y_feed_card_item rows) and the
// open-chat pane (containing the channel-name header + message text +
// compose textarea). Helper exists because every test below needs a
// tree that matches the parser's dual-signal scoping path.
func feishuFixtureWindow(channel string, chatList []*AXNode, transcript []*AXNode, composeValue string) *AXNode {
	chatWebView := &AXNode{
		Role:     "AXGroup",
		Title:    "MultiWebView - messenger",
		Children: chatList,
	}
	transcriptChildren := []*AXNode{
		{Role: "AXStaticText", Value: channel}, // transcript header (channel name)
	}
	transcriptChildren = append(transcriptChildren, transcript...)
	transcriptChildren = append(transcriptChildren, &AXNode{
		Role:  "AXTextArea",
		Value: composeValue, // compose textarea (anchor for "this is the active chat")
	})
	openChatWebView := &AXNode{
		Role:     "AXGroup",
		Title:    "MultiWebView - messenger:messenger-chat:default",
		Children: transcriptChildren,
	}
	return &AXNode{
		Role:     "AXWindow",
		Children: []*AXNode{chatWebView, openChatWebView},
	}
}

func TestFeishuParseTreeWithConversationPane(t *testing.T) {
	// Realistic open-chat snapshot: 4 chat-list rows on the left, an
	// open Yansu-Team conversation on the right with two date-
	// separated bot messages and a user @-mention. Verifies that we:
	//   - extract Conversation: lines tagged by their preceding date,
	//   - drop the axCoverage=chat_list_only flag (full coverage),
	//   - skip welcome-banner boilerplate and the compose placeholder,
	//   - preserve top-to-bottom chronological order.
	chatList := []*AXNode{
		chatCardNode(-20,
			staticTextNode("Yansu-Team"),
			staticTextNode("4月27日"),
			staticTextNode("pd"),
			staticTextNode(": "),
			staticTextNode("@"),
			staticTextNode("YanSu"),
			staticTextNode(" hello"),
		),
	}
	transcript := []*AXNode{
		// Empty-group welcome banner — must be filtered.
		{Role: "AXStaticText", Value: "欢迎加入群组"},
		{Role: "AXStaticText", Value: "置顶重要链接"},
		// First bot message under 4月22日.
		{Role: "AXStaticText", Value: "4月22日"},
		{Role: "AXStaticText", Value: "YanSu 机器人 yansu bot"},
		{Role: "AXStaticText", Value: "**[Yansu Automation] test-feishu-channel-prod — Completed**"},
		// Second bot message under 4月25日.
		{Role: "AXStaticText", Value: "4月25日"},
		{Role: "AXStaticText", Value: "YanSu 机器人 yansu bot"},
		{Role: "AXStaticText", Value: "测试任务已完成!"},
		// User @YanSu hello + bot reply under 4月27日.
		{Role: "AXStaticText", Value: "4月27日"},
		{Role: "AXStaticText", Value: "@YanSu hello"},
		{Role: "AXStaticText", Value: "YanSu 汝好呀！吾乃**奔波儿霸**"},
		{Role: "AXStaticText", Value: feishuReplyMarker}, // must be filtered
		// Compose-area keyboard hint.
		{Role: "AXStaticText", Value: "Shift + Enter 换行"},
	}
	tree := feishuFixtureWindow("Yansu-Team", chatList, transcript, "发送给 Yansu-Team\n​\n")

	got := (&feishuParser{}).ParseTree(tree, "com.electron.lark", "飞书", "")
	if got == nil {
		t.Fatal("expected non-nil ParsedContent")
	}
	if got.Channel != "Yansu-Team" {
		t.Errorf("Channel = %q, want %q", got.Channel, "Yansu-Team")
	}
	if got.Metadata["axCoverage"] != "" {
		t.Errorf("axCoverage = %q, want \"\" (conversation pane captured = full)",
			got.Metadata["axCoverage"])
	}

	// Conversation section must exist, sectioned by date with each
	// date emitted once as a [date] header.
	if !strings.Contains(got.Body, "\nConversation:") {
		t.Errorf("Body missing Conversation: section\n--- body ---\n%s", got.Body)
	}
	wantConversationSubstrings := []string{
		"[4月22日]",
		"YanSu 机器人 yansu bot",
		"**[Yansu Automation] test-feishu-channel-prod — Completed**",
		"[4月25日]",
		"测试任务已完成!",
		"[4月27日]",
		"@YanSu hello",
		"汝好呀！吾乃**奔波儿霸**",
	}
	for _, want := range wantConversationSubstrings {
		if !strings.Contains(got.Body, want) {
			t.Errorf("Body missing %q\n--- body ---\n%s", want, got.Body)
		}
	}

	// Boilerplate must NOT leak into the transcript.
	for _, blocked := range []string{"欢迎加入群组", "置顶重要链接", feishuReplyMarker, "Shift + Enter 换行"} {
		if strings.Contains(got.Body, blocked) {
			t.Errorf("Body should drop boilerplate %q\n--- body ---\n%s", blocked, got.Body)
		}
	}

	// The compose placeholder ("发送给 Yansu-Team") must NOT show up as a
	// transcript line — it's the anchor, not content.
	if strings.Contains(got.Body, "发送给 Yansu-Team") {
		t.Errorf("Body must not contain compose placeholder\n--- body ---\n%s", got.Body)
	}

	// Order check: 4月22日 must come before 4月27日 in the rendered body.
	idx22 := strings.Index(got.Body, "[4月22日]")
	idx27 := strings.Index(got.Body, "[4月27日]")
	if idx22 < 0 || idx27 < 0 || idx22 > idx27 {
		t.Errorf("date order wrong: idx(4月22日)=%d, idx(4月27日)=%d", idx22, idx27)
	}
}

func TestFeishuParseTreeFallsBackWhenNoConversationPane(t *testing.T) {
	// Chat list visible but no chat open (no compose textarea) —
	// behavior must remain chat-list-only with the partial-AX flag
	// set so OCR fusion still triggers downstream.
	tree := feishuFixtureWindow("Yansu-Team",
		[]*AXNode{
			chatCardNode(0,
				staticTextNode("Yansu-Team"),
				staticTextNode("4月27日"),
				staticTextNode("pd: hello"),
			),
		},
		nil, // no transcript content
		"",  // no compose textarea
	)
	// Strip the compose textarea + channel-name header the helper
	// inserts so this test exercises the no-pane fallback path.
	stripped := &AXNode{Role: tree.Role, Children: tree.Children[:1]}

	got := (&feishuParser{}).ParseTree(stripped, "com.electron.lark", "飞书", "")
	if got == nil {
		t.Fatal("expected non-nil result with chat list only")
	}
	if got.Metadata["axCoverage"] != AXCoverageChatListOnly {
		t.Errorf("axCoverage = %q, want %q (no conversation pane found)",
			got.Metadata["axCoverage"], AXCoverageChatListOnly)
	}
	if strings.Contains(got.Body, "Conversation:") {
		t.Errorf("Body should NOT have Conversation: section without a pane\n--- body ---\n%s", got.Body)
	}
}

func TestFeishuFindConversationPaneRejectsChatListChannelName(t *testing.T) {
	// Regression: a real Lark tree always has the channel name in
	// TWO places — as a chat-list-row title AND as the transcript
	// header. The pane lookup must anchor on the HEADER copy
	// (outside a11y_feed_card_item) and NOT on the row copy, else
	// the chat-list WebView gets misclassified as the conversation
	// pane and its row text bleeds into the transcript.
	//
	// We construct a tree where the chat-list card carries the
	// channel name BUT the compose textarea sits at window root
	// with no separate transcript header. The dual-signal walk
	// must return nil so axCoverage stays chat_list_only.
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXTextArea", Value: "发送给 Yansu-Team"},
			chatCardNode(0,
				staticTextNode("Yansu-Team"),
				staticTextNode("4月27日"),
				staticTextNode("hi"),
			),
		},
	}
	pane := feishuFindConversationPane(tree, "Yansu-Team")
	if pane != nil {
		t.Fatalf("expected nil pane when channel name only appears in chat-list card")
	}
}

func TestFeishuComposeRegex(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"发送给 Yansu-Team\n​\n", "Yansu-Team"},
		{"发送给 pd, pd\n", "pd, pd"},
		{"发送给 SoloChat", "SoloChat"},
		// Negative — empty when no match.
		{"Shift + Enter 换行", ""},
		{"", ""},
	}
	for _, c := range cases {
		m := feishuComposePlaceholderRE.FindStringSubmatch(c.in)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("placeholder %q -> %q, want %q", c.in, got, c.want)
		}
	}
}
