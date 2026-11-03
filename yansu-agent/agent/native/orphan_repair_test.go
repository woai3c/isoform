package native

import "testing"

// TestRepairOrphans_NoOpWhenClean leaves clean messages untouched.
// Idempotency is the whole point of running this on every turn — clean
// state should incur zero structural change.
func TestRepairOrphans_NoOpWhenClean(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "running tool"},
			{Type: "tool_use", ID: "toolu_1", Name: "Read", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_1", Content: "ok"},
		}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "done"}}},
	}
	before := len(msgs)
	repaired := RepairOrphans(&msgs)
	if repaired {
		t.Errorf("expected no repair on clean messages")
	}
	if len(msgs) != before {
		t.Errorf("messages length changed on clean input: %d → %d", before, len(msgs))
	}
}

// TestRepairOrphans_ReverseOrphanReplacedWithPlaceholder: a tool_result
// whose ToolUseID has no matching tool_use must be removed, but the
// containing user message must NOT be dropped — that would break
// Anthropic's strict user/assistant alternation when the message sits
// between two assistant messages. Replace the now-empty content with
// a placeholder text block instead.
func TestRepairOrphans_ReverseOrphanReplacedWithPlaceholder(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "ok"}}},
		// Orphan tool_result — no matching tool_use anywhere.
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "call_00_orphan", Content: "stale"},
		}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "carrying on"}}},
	}
	repaired := RepairOrphans(&msgs)
	if !repaired {
		t.Errorf("expected repair to fire on reverse-orphan tool_result")
	}
	// All 4 messages must survive — alternation depends on it.
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages preserved (placeholder substitution), got %d", len(msgs))
	}
	// Index 2: the user message must now be a single text placeholder,
	// not a tool_result.
	blocks, ok := msgs[2].Content.([]ContentBlock)
	if !ok || len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("expected single text placeholder at msgs[2], got %+v", msgs[2].Content)
	}
	if !contains(blocks[0].Text, "Stale tool_result") {
		t.Errorf("placeholder text shape changed: %q", blocks[0].Text)
	}
	// No tool_result anywhere in output.
	for i, m := range msgs {
		bs, _ := m.Content.([]ContentBlock)
		for _, b := range bs {
			if b.Type == "tool_result" {
				t.Errorf("orphan tool_result survived at msgs[%d]: %+v", i, b)
			}
		}
	}
	// Alternation check.
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Errorf("role alternation broken at msgs[%d]: %s→%s", i, msgs[i-1].Role, msgs[i].Role)
		}
	}
}

// TestRepairOrphans_AppendsSyntheticForForwardOrphan: a tool_use without
// a matching tool_result gets a synthetic [interrupted] tool_result so
// the conversation can move forward.
func TestRepairOrphans_AppendsSyntheticForForwardOrphan(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do thing"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "toolu_orphan", Name: "Bash", Input: map[string]any{}},
		}},
	}
	repaired := RepairOrphans(&msgs)
	if !repaired {
		t.Errorf("expected repair to fire on forward-orphan tool_use")
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after synthetic append, got %d: %+v", len(msgs), msgs)
	}
	last := msgs[2]
	if last.Role != "user" {
		t.Fatalf("last message should be synthetic user message, got role=%s", last.Role)
	}
	blocks := last.Content.([]ContentBlock)
	if len(blocks) != 1 || blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "toolu_orphan" || !blocks[0].IsError {
		t.Errorf("synthetic tool_result wrong shape: %+v", blocks)
	}
}

// TestRepairOrphans_PartialMessageStripping: when a user message has BOTH
// a valid tool_result and an orphan tool_result, only the orphan should
// be filtered — the valid one survives in place.
func TestRepairOrphans_PartialMessageStripping(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "toolu_good", Name: "Read", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_good", Content: "ok"},
			{Type: "tool_result", ToolUseID: "call_orphan", Content: "stale"},
		}},
	}
	if !RepairOrphans(&msgs) {
		t.Errorf("expected repair to fire")
	}
	// The user message survives with only the good tool_result.
	blocks := msgs[2].Content.([]ContentBlock)
	if len(blocks) != 1 || blocks[0].ToolUseID != "toolu_good" {
		t.Errorf("expected only good tool_result to survive; got %+v", blocks)
	}
}

// TestRepairOrphans_ForwardOrphanMergesIntoTrailingUser: the synthetic
// [interrupted] result must be MERGED into the trailing user message
// when one already exists. Otherwise Pass 4's blind append would
// produce two consecutive user messages — Anthropic 400s on that.
//
// Construct the scenario where it could happen: an assistant tool_use
// with no result, followed by a stray user text message (could come
// from BuildMessages's pre-existing flushPending edge case, or from a
// loaded history that was already irregular).
func TestRepairOrphans_ForwardOrphanMergesIntoTrailingUser(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do work"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "tu_orphan", Name: "Bash", Input: map[string]any{}},
		}},
		// Stray trailing user message (no tool_result for tu_orphan).
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "follow-up"}}},
	}
	if !RepairOrphans(&msgs) {
		t.Errorf("expected repair to fire on forward-orphan tool_use")
	}
	// Length must stay at 3 — the synthetic merges into msgs[2], it does
	// not become a new msgs[3].
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (synthetic merged into trailing user), got %d: %+v", len(msgs), msgs)
	}
	// msgs[2] must be a user message holding BOTH the original text
	// block AND the synthetic tool_result.
	last := msgs[2]
	if last.Role != "user" {
		t.Fatalf("trailing message role = %s, want user", last.Role)
	}
	blocks := last.Content.([]ContentBlock)
	var sawText, sawSynthetic bool
	for _, b := range blocks {
		if b.Type == "text" && b.Text == "follow-up" {
			sawText = true
		}
		if b.Type == "tool_result" && b.ToolUseID == "tu_orphan" && b.IsError {
			sawSynthetic = true
		}
	}
	if !sawText || !sawSynthetic {
		t.Errorf("merged user message missing text=%v or synthetic=%v: %+v", sawText, sawSynthetic, blocks)
	}
	// Alternation invariant.
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Errorf("role alternation broken at msgs[%d]: %s→%s", i, msgs[i-1].Role, msgs[i].Role)
		}
	}
}

// TestRepairOrphans_OpenAIStyleIdAccepted: when the tool_use ID uses an
// OpenAI-style "call_..." prefix (left over from the deprecated
// anthropicproxy path), pairing still works because we treat IDs as
// opaque strings. Verifies the bug class we're shipping this PR for
// stays fixed even on legacy data.
func TestRepairOrphans_OpenAIStyleIdAccepted(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "call_00_LegacyOpenAIStyle", Name: "Read", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "call_00_LegacyOpenAIStyle", Content: "ok"},
		}},
	}
	if RepairOrphans(&msgs) {
		t.Errorf("legacy openai-style id pair must not trigger any repair: %+v", msgs)
	}
}

// TestRepairOrphans_EmptyIDToolUseDropped covers the sub2api/Kimi
// proxy-bug case where a tool_use block reaches messages with an
// empty id. Pass 0 must remove it; otherwise sending the request
// triggers `tool_call_id  is not found` (double-space template
// signature) and the run.error event we shipped this defense for.
func TestRepairOrphans_EmptyIDToolUseDropped(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "calling tool"},
			{Type: "tool_use", ID: "", Name: "Read", Input: map[string]any{}},
		}},
	}
	if !RepairOrphans(&msgs) {
		t.Fatalf("expected repair to fire on empty-id tool_use")
	}
	blocks := msgs[1].Content.([]ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("empty-id tool_use survived: %+v", b)
		}
	}
	// The text block stays.
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Errorf("expected text block to remain, got %+v", blocks)
	}
}

// TestRepairOrphans_EmptyIDToolUseAlonePlaceholderInserted: when the
// only block in an assistant message is an empty-id tool_use, removing
// it leaves zero blocks — which Anthropic rejects. The placeholder
// text substitution preserves role alternation.
func TestRepairOrphans_EmptyIDToolUseAlonePlaceholderInserted(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "", Name: "Bash", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "next"}}},
	}
	if !RepairOrphans(&msgs) {
		t.Fatalf("expected repair to fire")
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages preserved, got %d", len(msgs))
	}
	blocks := msgs[1].Content.([]ContentBlock)
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("expected single placeholder text at msgs[1], got %+v", blocks)
	}
	if !contains(blocks[0].Text, "Tool call dropped") {
		t.Errorf("placeholder text shape unexpected: %q", blocks[0].Text)
	}
}

// TestRepairOrphans_EmptyIDToolResultDropped: an empty-id tool_result
// is unpairable by definition. Pass 2 must drop it — the previous
// `if b.ToolUseID != ""` guard let these slip through untouched.
func TestRepairOrphans_EmptyIDToolResultDropped(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "ok"}}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "", Content: "stale"},
		}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "carrying on"}}},
	}
	if !RepairOrphans(&msgs) {
		t.Fatalf("expected repair to fire on empty-id tool_result")
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages preserved (placeholder substitution), got %d", len(msgs))
	}
	blocks := msgs[2].Content.([]ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_result" {
			t.Errorf("empty-id tool_result survived: %+v", b)
		}
	}
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Errorf("expected single text placeholder, got %+v", blocks)
	}
}
