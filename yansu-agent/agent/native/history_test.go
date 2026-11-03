package native

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildMessages_UserAssistant(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "hello"},
		{Kind: "assistant", Content: "hi there"},
		{Kind: "user", Content: "bye"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len = %d, want 3", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Errorf("roles = %v / %v / %v", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
}

func TestBuildMessages_ToolUseToolResultPaired(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "read file /tmp/x"},
		{Kind: "assistant", Content: "ok"},
		{Kind: "tool_use", ToolName: "Read", ToolUseID: "tu_1", ToolInput: `{"path":"/tmp/x"}`},
		{Kind: "tool_result", ToolUseID: "tu_1", Content: "contents"},
		{Kind: "assistant", Content: "here you go"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("len = %d, want 4", len(msgs))
	}
	// msgs[1] should be assistant with text + tool_use blocks.
	b, ok := msgs[1].Content.([]ContentBlock)
	if !ok {
		t.Fatalf("msgs[1] content not blocks: %#v", msgs[1].Content)
	}
	if len(b) != 2 || b[0].Type != "text" || b[1].Type != "tool_use" {
		t.Errorf("assistant blocks = %+v", b)
	}
	// msgs[2] should be user with tool_result block.
	b, ok = msgs[2].Content.([]ContentBlock)
	if !ok || b[0].Type != "tool_result" || b[0].ToolUseID != "tu_1" {
		t.Errorf("tool_result block wrong: %+v", b)
	}
}

// Regression: rows persisted before the loop emitted parsed input
// (or rows where the input failed to capture) have ToolInput="".
// BuildMessages must default the block's Input to {} so the request
// doesn't 400 with "tool_use.input: Field required".
func TestBuildMessages_ToolUseEmptyInput_DefaultsToObject(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "do it"},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_empty", ToolInput: ""},
		{Kind: "tool_result", ToolUseID: "tu_empty", Content: "done"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	b := msgs[1].Content.([]ContentBlock)
	tu := b[len(b)-1]
	if tu.Type != "tool_use" {
		t.Fatalf("expected tool_use, got %s", tu.Type)
	}
	m, ok := tu.Input.(map[string]any)
	if !ok {
		t.Fatalf("Input type = %T, want map[string]any", tu.Input)
	}
	if len(m) != 0 {
		t.Errorf("Input = %v, want empty map", m)
	}
	// Marshal-check: the JSON sent to the API must contain "input":{}.
	out, err := json.Marshal(tu)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"input":{}`)) {
		t.Errorf("serialized block missing input field: %s", out)
	}
}

// ToolInput that decodes to a non-object (null, number, string, array)
// must also fall back to {} — Anthropic rejects anything but an object.
func TestBuildMessages_ToolUseNonObjectInput_DefaultsToObject(t *testing.T) {
	cases := []string{`null`, `123`, `"abc"`, `[1,2,3]`, `{not json`}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			msgs, err := BuildMessages([]HistoryItem{
				{Kind: "user", Content: "x"},
				{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_x", ToolInput: raw},
				{Kind: "tool_result", ToolUseID: "tu_x", Content: "ok"},
			})
			if err != nil {
				t.Fatal(err)
			}
			b := msgs[1].Content.([]ContentBlock)
			tu := b[len(b)-1]
			m, ok := tu.Input.(map[string]any)
			if !ok || len(m) != 0 {
				t.Errorf("Input = %v (type %T), want empty map", tu.Input, tu.Input)
			}
		})
	}
}

func TestBuildMessages_OrphanToolUse_InjectsInterrupted(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "do thing"},
		{Kind: "assistant", Content: ""},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_orphan", ToolInput: `{"command":"true"}`},
		{Kind: "user", Content: "another message"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	// Expect a synthetic user tool_result right after the orphan tool_use.
	var found bool
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		b, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, blk := range b {
			if blk.Type == "tool_result" && blk.ToolUseID == "tu_orphan" && blk.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("orphan tool_use not repaired with synthetic tool_result: %+v", msgs)
	}
}

func TestBuildMessages_Images(t *testing.T) {
	items := []HistoryItem{
		{
			Kind:    "user",
			Content: "what's in this picture?",
			Images:  `[{"media_type":"image/png","data_base64":"abc=="}]`,
		},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := msgs[0].Content.([]ContentBlock)
	if !ok {
		t.Fatalf("content type = %T", msgs[0].Content)
	}
	if len(b) != 2 {
		t.Fatalf("blocks = %d, want 2 (image + text)", len(b))
	}
	if b[0].Type != "image" || b[0].Source.MediaType != "image/png" {
		t.Errorf("image block = %+v", b[0])
	}
	if b[1].Type != "text" || b[1].Text != "what's in this picture?" {
		t.Errorf("text block = %+v", b[1])
	}
}

func TestBuildMessages_UnsignedThinkingIsSkipped(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "think hard"},
		{Kind: "assistant", Content: "done"},
		{Kind: "thinking", Content: "internal monologue"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Fatalf("last role = %s", last.Role)
	}
	b := last.Content.([]ContentBlock)
	if len(b) != 1 || b[0].Type != "text" || b[0].Text != "done" {
		t.Errorf("unsigned thinking should not be replayed: %+v", b)
	}
}

func TestBuildMessages_SignedThinkingAttachesToAssistant(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "think hard"},
		{Kind: "assistant", Content: "done"},
		{Kind: "thinking", Content: "internal monologue", ThinkingSignature: "sig_history"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Fatalf("last role = %s", last.Role)
	}
	b := last.Content.([]ContentBlock)
	if len(b) != 2 || b[1].Type != "thinking" || b[1].Thinking != "internal monologue" || b[1].Signature != "sig_history" {
		t.Errorf("signed thinking not attached: %+v", b)
	}
}

func TestApplyCachePrefix(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "A"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "B"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "C"}}},
	}
	ApplyCachePrefix(msgs)

	b := msgs[1].Content.([]ContentBlock)
	if b[0].CacheControl == nil || b[0].CacheControl.Type != "ephemeral" {
		t.Errorf("penultimate block should have ephemeral cache_control: %+v", b[0])
	}
	// First and last should NOT be marked (we only mark the last block
	// of the penultimate message).
	first := msgs[0].Content.([]ContentBlock)
	if first[0].CacheControl != nil {
		t.Errorf("first message should not have cache_control: %+v", first[0])
	}
}

func TestApplyCachePrefix_ReplacesOlderMarker(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "A"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "B"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "C"}}},
	}

	ApplyCachePrefix(msgs)

	msgs = append(msgs, Message{
		Role:    "assistant",
		Content: []ContentBlock{{Type: "text", Text: "D"}},
	})
	msgs = append(msgs, Message{
		Role:    "user",
		Content: []ContentBlock{{Type: "text", Text: "E"}},
	})

	ApplyCachePrefix(msgs)

	firstAssistant := msgs[1].Content.([]ContentBlock)
	if firstAssistant[0].CacheControl != nil {
		t.Fatalf("older cache marker should be cleared, got %+v", firstAssistant[0].CacheControl)
	}

	latestAssistant := msgs[3].Content.([]ContentBlock)
	if latestAssistant[0].CacheControl == nil || latestAssistant[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("latest penultimate message should carry cache_control, got %+v", latestAssistant[0].CacheControl)
	}
}

func TestBuildMessages_NoMessages(t *testing.T) {
	msgs, err := BuildMessages(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d", len(msgs))
	}
}

// Reverse-orphan defense: a tool_result row whose tool_use_id was never
// declared by any tool_use row in this history must be dropped.
// Otherwise the assembled messages 400 the next API request and lock
// the session forever — the production bug this fixes.
func TestBuildMessages_DropsReverseOrphanToolResult(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "hi"},
		{Kind: "assistant", Content: "ok"},
		// orphan tool_result — no tool_use row anywhere with this id.
		{Kind: "tool_result", ToolUseID: "call_00_orphan", Content: "stale result"},
		{Kind: "user", Content: "next"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID == "call_00_orphan" {
				t.Errorf("orphan tool_result survived BuildMessages; should have been dropped")
			}
		}
	}
}

// TestBuildMessages_DropsEmptyIDToolUse mirrors the empty-ToolUseID
// guard on the tool_result branch. A tool_use HistoryItem whose
// ToolUseID is "" can never be paired with a tool_result and would 400
// the next request with `tool_call_id  is not found` (double space,
// sub2api/Kimi template signature). history_loader.go drops these at
// the storage layer; this test pins the same behaviour on the
// PriorHistory path, which bypasses the loader.
func TestBuildMessages_DropsEmptyIDToolUse(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "do work"},
		// Empty-id tool_use — must be skipped entirely.
		{Kind: "tool_use", ToolName: "Read", ToolUseID: "", ToolInput: `{"path":"/tmp/x"}`},
		// And the trailing user message must still appear cleanly.
		{Kind: "user", Content: "follow-up"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID == "" {
				t.Errorf("empty-id tool_use survived BuildMessages at msgs[%d]: %+v", i, b)
			}
		}
	}
}

// Mixed: real tool_use/tool_result pair plus a separate orphan
// tool_result. Real pair must survive untouched, orphan must be dropped.
func TestBuildMessages_KeepsValidPairDropsOrphan(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "do work"},
		{Kind: "tool_use", ToolName: "Read", ToolUseID: "toolu_real", ToolInput: `{"path":"/tmp/x"}`},
		{Kind: "tool_result", ToolUseID: "toolu_real", Content: "real content"},
		{Kind: "tool_result", ToolUseID: "call_orphan_xyz", Content: "stale"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	var sawReal, sawOrphan bool
	for _, m := range msgs {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				if b.ToolUseID == "toolu_real" {
					sawReal = true
				}
				if b.ToolUseID == "call_orphan_xyz" {
					sawOrphan = true
				}
			}
		}
	}
	if !sawReal {
		t.Errorf("real tool_result was dropped (should have been kept)")
	}
	if sawOrphan {
		t.Errorf("orphan tool_result survived (should have been dropped)")
	}
}

// Pre-existing bug: when multiple tool_use rows had no matching
// tool_result rows, the old flushPending appended ONE user message
// PER pending id — Anthropic 400s on consecutive user messages. The
// fix groups all synthetic [interrupted] results into a SINGLE user
// message and (when followed by a real user message) prepends them
// into its content rather than emitting a separate message.
func TestBuildMessages_MultiplePendingMergeIntoSingleUser(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "do two things"},
		{Kind: "assistant", Content: "ok"},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_a", ToolInput: `{}`},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_b", ToolInput: `{}`},
		{Kind: "user", Content: "follow up"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	// Strict alternation invariant.
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Errorf("role alternation broken at msgs[%d]: %s→%s (full: %+v)", i, msgs[i-1].Role, msgs[i].Role, msgs)
		}
	}
	// The trailing user must contain BOTH synthetic tool_results AND
	// the original "follow up" text.
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("trailing message role = %s, want user", last.Role)
	}
	blocks, ok := last.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("trailing user content not blocks: %T", last.Content)
	}
	var sawA, sawB, sawText bool
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == "tu_a" && b.IsError {
			sawA = true
		}
		if b.Type == "tool_result" && b.ToolUseID == "tu_b" && b.IsError {
			sawB = true
		}
		if b.Type == "text" && b.Text == "follow up" {
			sawText = true
		}
	}
	if !sawA || !sawB || !sawText {
		t.Errorf("merged user message missing parts: A=%v B=%v text=%v blocks=%+v", sawA, sawB, sawText, blocks)
	}
}

// End-of-history flushPending with multiple pending ids: same merging
// requirement, but no trailing user to merge into. Must emit exactly
// ONE user message containing all synthetic results — not one per id.
func TestBuildMessages_EndOfHistoryFlushPendingSingleUser(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "kick off"},
		{Kind: "assistant", Content: "running"},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_a", ToolInput: `{}`},
		{Kind: "tool_use", ToolName: "Bash", ToolUseID: "tu_b", ToolInput: `{}`},
		// History ends here — both tool_uses orphaned at tail.
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Errorf("role alternation broken at msgs[%d]: %s→%s (full: %+v)", i, msgs[i-1].Role, msgs[i].Role, msgs)
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("trailing message role = %s, want user", last.Role)
	}
	blocks, ok := last.Content.([]ContentBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("trailing user must hold both synthetic tool_results in one message; got %+v", last.Content)
	}
}

func TestBuildMessages_DeepEqualSnapshot(t *testing.T) {
	items := []HistoryItem{
		{Kind: "user", Content: "ping"},
		{Kind: "assistant", Content: "pong"},
	}
	msgs, err := BuildMessages(items)
	if err != nil {
		t.Fatal(err)
	}
	want := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "ping"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "pong"}}},
	}
	if !reflect.DeepEqual(msgs, want) {
		t.Errorf("snapshot mismatch: got %+v", msgs)
	}
}
