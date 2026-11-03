package native

import (
	"strings"
	"testing"
)

func TestContextLimitForModel(t *testing.T) {
	cases := []struct {
		model    string
		expected int
	}{
		// 1M-context models per Anthropic docs.
		{"claude-opus-4-7", 1000000},
		{"claude-opus-4-6", 1000000},
		{"claude-sonnet-4-6", 1000000},
		{"claude-sonnet-4-6-20251015", 1000000},
		// 200k-context Claude models (Haiku 4.5 + earlier 4.x).
		{"claude-haiku-4-5", 200000},
		{"claude-haiku-4-5-20251001", 200000},
		{"claude-sonnet-4-5", 200000},
		{"claude-opus-4-5", 200000},
		{"claude-opus-4-1", 200000},
		// Other providers.
		{"gemini-2.5-pro", 1048576},
		{"gpt-5", 128000},
		// Fallbacks.
		{"unknown-model", DefaultContextLimit},
		{"", DefaultContextLimit},
	}
	for _, c := range cases {
		got := ContextLimitForModel(c.model)
		if got != c.expected {
			t.Errorf("ContextLimitForModel(%q) = %d, want %d", c.model, got, c.expected)
		}
	}
}

func TestMaxOutputTokensForModel(t *testing.T) {
	cases := []struct {
		model    string
		expected int
	}{
		// Opus 4.6/4.7 — 128k output.
		{"claude-opus-4-7", 128000},
		{"claude-opus-4-6", 128000},
		// Opus 4.5 — 64k output (legacy).
		{"claude-opus-4-5", 64000},
		{"claude-opus-4-5-20251101", 64000},
		// Opus 4.1 / 4 — 32k output (legacy / deprecated).
		{"claude-opus-4-1", 32000},
		{"claude-opus-4-1-20250805", 32000},
		{"claude-opus-4-0", 32000},
		{"claude-opus-4-20250514", 32000},
		// Sonnet — all 4.x are 64k output.
		{"claude-sonnet-4-6", 64000},
		{"claude-sonnet-4-6-20251015", 64000},
		{"claude-sonnet-4-5", 64000},
		{"claude-sonnet-4-5-20250929", 64000},
		{"claude-sonnet-4-0", 64000},
		{"claude-sonnet-4-20250514", 64000},
		// Haiku 4.5 — 64k output.
		{"claude-haiku-4-5", 64000},
		{"claude-haiku-4-5-20251001", 64000},
		// Claude 3.x must fall back to the safe 8192 default — these
		// models cap below 64k and the Anthropic API returns HTTP 400
		// when max_tokens exceeds the per-model cap. Locking these
		// down catches a regression if anyone broadens the substring
		// match to bare "sonnet" / "haiku".
		{"claude-3-5-sonnet-20241022", MaxOutputTokens},
		{"claude-3-5-sonnet-20240620", MaxOutputTokens},
		{"claude-3-sonnet-20240229", MaxOutputTokens},
		{"claude-3-7-sonnet-20250219", MaxOutputTokens},
		{"claude-3-5-haiku-20241022", MaxOutputTokens},
		{"claude-3-haiku-20240307", MaxOutputTokens},
		// Fallbacks.
		{"unknown-model", MaxOutputTokens},
		{"", MaxOutputTokens},
	}
	for _, c := range cases {
		got := MaxOutputTokensForModel(c.model)
		if got != c.expected {
			t.Errorf("MaxOutputTokensForModel(%q) = %d, want %d", c.model, got, c.expected)
		}
	}
}

func TestAutoCompactLimitForModel_Threshold(t *testing.T) {
	// Sanity: limit must be exactly 75% of the context window for any
	// model the threshold logic recognises. Off-by-one drift here
	// would cause warnings to fire at the wrong point.
	got := AutoCompactLimitForModel("claude-sonnet-4-6")
	want := int(1000000 * AutoCompactThreshold)
	if got != want {
		t.Errorf("AutoCompactLimitForModel(sonnet-4-6) = %d, want %d (75%% of 1M)", got, want)
	}
}

func TestContextLimitForModel_EnvOverride(t *testing.T) {
	// Valid positive override wins over the per-model default. This is
	// the sub2api path: nominally-Claude requests routed to Kimi K2.6
	// (262144) must compact at 0.75 × 262144 instead of 0.75 × 1M.
	t.Setenv(ContextWindowOverrideEnv, "262144")
	if got := ContextLimitForModel("claude-sonnet-4-6"); got != 262144 {
		t.Errorf("with override=262144, sonnet-4-6 got %d, want 262144", got)
	}
	if got := ContextLimitForModel(""); got != 262144 {
		t.Errorf("with override=262144, empty model got %d, want 262144 (override applies even when model unknown)", got)
	}
	if got := ContextLimitForModel("gemini-2.5-pro"); got != 262144 {
		t.Errorf("with override=262144, gemini got %d, want 262144 (override is unconditional)", got)
	}

	// Invalid / non-positive values must fall through to the per-model
	// default so a typo in the env doesn't silently shrink the window
	// to zero (which would compact every turn).
	for _, bad := range []string{"0", "-1", "not-a-number", "  "} {
		t.Setenv(ContextWindowOverrideEnv, bad)
		if got := ContextLimitForModel("claude-sonnet-4-6"); got != 1000000 {
			t.Errorf("with override=%q (invalid), sonnet-4-6 got %d, want 1000000 (fall through to default)", bad, got)
		}
	}
}

func TestEstimateRequestTokens_NilSafe(t *testing.T) {
	if got := EstimateRequestTokens(nil); got != 0 {
		t.Errorf("EstimateRequestTokens(nil) = %d, want 0", got)
	}
	got := EstimateRequestTokens(&Usage{
		InputTokens:              10,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 30,
		OutputTokens:             40,
	})
	if got != 100 {
		t.Errorf("EstimateRequestTokens = %d, want 100", got)
	}
}

func TestEstimateUserContentTokens(t *testing.T) {
	cases := []struct {
		name   string
		blocks []ContentBlock
		want   int
	}{
		{
			name:   "nil-safe",
			blocks: nil,
			want:   0,
		},
		{
			name: "plain text block",
			blocks: []ContentBlock{
				// 16 chars / 4 = 4 tokens
				{Type: "text", Text: "Hello, world!!!1"},
			},
			want: 4,
		},
		{
			name: "tool_result with string content",
			blocks: []ContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "abc",
					// 200 chars / 4 = 50 tokens
					Content: strings.Repeat("x", 200),
				},
			},
			want: 50,
		},
		{
			name: "tool_result with nested image+text blocks",
			blocks: []ContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "abc",
					Content: []ContentBlock{
						{Type: "text", Text: strings.Repeat("a", 100)},
						{Type: "image", Source: &ImageSource{Data: strings.Repeat("b", 400)}},
					},
				},
			},
			// 100 + 400 = 500 / 4 = 125
			want: 125,
		},
		{
			name: "inline image block",
			blocks: []ContentBlock{
				{Type: "image", Source: &ImageSource{Data: strings.Repeat("z", 800)}},
			},
			want: 200,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateUserContentTokens(tc.blocks); got != tc.want {
				t.Errorf("EstimateUserContentTokens = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestEstimateUserContentTokens_BigToolResultExceedsGate is the
// regression case for the runningTotalTokens estimation bug: a single
// large tool_result (typical cua_driver screenshot + log output) is
// big enough on its own to push the next request past a 262144-token
// upstream cap, but EstimateRequestTokens alone reflects only the
// PRIOR turn's wire size and wouldn't trip the auto-compact gate.
// EstimateUserContentTokens must catch this so the L2 gate fires
// before the request leaves the loop.
func TestEstimateUserContentTokens_BigToolResultExceedsGate(t *testing.T) {
	// 800k chars of base64-ish data → 200k tokens via the /4 estimator.
	// On its own, larger than the 0.75 × 262144 = 196608 token gate.
	huge := strings.Repeat("A", 800_000)
	blocks := []ContentBlock{
		{Type: "tool_result", ToolUseID: "cua_run_42", Content: huge},
	}
	got := EstimateUserContentTokens(blocks)
	gate := AutoCompactLimitForModel("claude-sonnet-4-6") // 1M × 0.75 fallback when no env
	t.Setenv(ContextWindowOverrideEnv, "262144")
	gateForSub2api := AutoCompactLimitForModel("claude-sonnet-4-6")
	if got <= gateForSub2api {
		t.Errorf("estimate (%d) should exceed sub2api gate (%d) so L2 auto-compact fires before the 262144 upstream cap is hit", got, gateForSub2api)
	}
	if got <= 0 || gate <= 0 {
		t.Fatalf("sanity: estimate=%d, default gate=%d", got, gate)
	}
}

func TestSplitForPreservedTail_FitsInBudget(t *testing.T) {
	// Build messages whose total estimated cost is around 12 tokens
	// per message (5-char body + per-message overhead). 80-token budget
	// keeps roughly the last 6 messages.
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "world"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "how"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "are"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "you"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "good"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "bye"}}},
	}
	older, tail := splitForPreservedTail(msgs, 80)
	if len(older)+len(tail) != len(msgs) {
		t.Fatalf("partition lost messages: %d+%d != %d", len(older), len(tail), len(msgs))
	}
	if len(tail) == 0 {
		t.Fatalf("tail empty under generous budget; older=%d tail=%d", len(older), len(tail))
	}
	if len(tail) > len(msgs) {
		t.Fatalf("tail oversize: %d > %d", len(tail), len(msgs))
	}
}

func TestSplitForPreservedTail_RespectsToolUsePairing(t *testing.T) {
	// Construct: ... [assistant with tool_use] [user with tool_result].
	// The tail must not start with the tool_result alone — that
	// orphans the result and the next API request 400s.
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "old1"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "old2"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "tu_1", Name: "Read", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_1", Content: "data"},
		}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "ack"}}},
	}
	// Budget tight enough to want only the very last message; pairing
	// rule should expand the tail backwards through the tool_use turn.
	_, tail := splitForPreservedTail(msgs, 30)
	if len(tail) == 0 {
		t.Fatalf("expected non-empty tail")
	}
	first := tail[0]
	if blocks, ok := first.Content.([]ContentBlock); ok {
		for _, b := range blocks {
			if b.Type == "tool_result" {
				t.Errorf("tail starts with tool_result (orphan): %+v", first)
			}
		}
	}
}

func TestHasOrphanToolResult(t *testing.T) {
	orphan := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_x", Content: "data"},
		}},
	}
	if !hasOrphanToolResult(orphan) {
		t.Errorf("did not detect orphan tool_result")
	}
	clean := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	if hasOrphanToolResult(clean) {
		t.Errorf("false-positive on plain user message")
	}
	empty := []Message{}
	if hasOrphanToolResult(empty) {
		t.Errorf("false-positive on empty slice")
	}
}

func TestDropOldestHalf_PreservesPairing(t *testing.T) {
	// Build a thread where the natural midpoint cut would split a
	// tool_use/tool_result pair. dropOldestHalf must walk left until
	// the kept slice starts cleanly.
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "1"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "2"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "3"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "tu_a", Name: "X", Input: map[string]any{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_a", Content: "ok"},
		}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "6"}}},
	}
	cut := dropOldestHalf(msgs)
	if len(cut) == 0 {
		t.Fatalf("dropOldestHalf returned empty slice")
	}
	// First message is the truncation notice; second onwards is real.
	for i, m := range cut[1:] {
		if blocks, ok := m.Content.([]ContentBlock); ok && i == 0 {
			for _, b := range blocks {
				if b.Type == "tool_result" {
					t.Errorf("kept slice starts with orphan tool_result: %+v", m)
				}
			}
		}
	}
}

// TestDropOldestHalf_RepairsForwardOrphanInTail: when the kept slice's
// last assistant message contains a tool_use whose tool_result lived in
// the dropped half (rare but possible if the message list was already
// mid-pair), the post-cut RepairOrphans pass appends a synthetic
// [interrupted] tool_result so the next API request stays valid. This
// is the defense-in-depth half of the dropOldestHalf fix.
func TestDropOldestHalf_RepairsForwardOrphanInTail(t *testing.T) {
	// Construct a slice where the cut at mid lands such that the
	// kept-half ends with a tool_use whose result was in the dropped
	// half. Easiest construction: dropOldestHalf splits at len/2; we
	// place a tool_use at index len/2-1 (in the dropped half) and its
	// result at index len/2 (in the kept half), then place a NEW
	// tool_use at the very end with no following result.
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "1"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "2"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "3"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "4"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "5"}}},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "tu_tail", Name: "Bash", Input: map[string]any{}},
		}},
	}
	cut := dropOldestHalf(msgs)
	if len(cut) == 0 {
		t.Fatalf("dropOldestHalf returned empty slice")
	}
	// The synthetic [interrupted] tool_result must exist somewhere in
	// the output for the trailing tool_use.
	var found bool
	for _, m := range cut {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID == "tu_tail" && b.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("dropOldestHalf did not append synthetic [interrupted] for trailing tool_use; got: %+v", cut)
	}
}

func TestBuildPostCompactionMessages_ShapeAndAck(t *testing.T) {
	tail := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "recent"}}},
	}
	out := buildPostCompactionMessages("THE-SUMMARY", tail)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (summary user + ack + tail), got %d", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("first message role = %q, want user", out[0].Role)
	}
	if out[1].Role != "assistant" {
		t.Errorf("second message role = %q, want assistant", out[1].Role)
	}
	if blocks, ok := out[0].Content.([]ContentBlock); ok {
		if len(blocks) == 0 || !strings.Contains(blocks[0].Text, "THE-SUMMARY") {
			t.Errorf("summary text not embedded in first user message: %+v", blocks)
		}
	} else {
		t.Errorf("first message content not blocks")
	}
}

func TestMicrocompactToolResults_PrunesLargeOutputs(t *testing.T) {
	// Total tool_result bytes must exceed MicrocompactTotalTokens *
	// charsPerToken so the pre-pass actually fires. Using two oversized
	// results pushes the cumulative budget past the threshold.
	bigBody := strings.Repeat("x", 80_000)  // 20k tokens at 4 cps
	medBody := strings.Repeat("y", 160_000) // 40k tokens at 4 cps
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "noop"}}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_big", Content: bigBody},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_small", Content: "tiny"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_med", Content: medBody},
		}},
	}
	out, saved := microcompactToolResults(msgs)
	if saved <= 0 {
		t.Fatalf("expected savings > 0, got %d", saved)
	}
	// big and med should be pruned to a placeholder; small kept verbatim.
	bigBlock := out[1].Content.([]ContentBlock)[0]
	smallBlock := out[2].Content.([]ContentBlock)[0]
	medBlock := out[3].Content.([]ContentBlock)[0]
	if s, _ := bigBlock.Content.(string); !strings.Contains(s, "truncated") {
		t.Errorf("big result not truncated: %q", s)
	}
	if s, _ := medBlock.Content.(string); !strings.Contains(s, "truncated") {
		t.Errorf("med result not truncated: %q", s)
	}
	if s, _ := smallBlock.Content.(string); s != "tiny" {
		t.Errorf("small result mutated: %q", s)
	}
}

func TestMicrocompactToolResults_BelowThresholdNoop(t *testing.T) {
	// Total tool_result bytes well under MicrocompactTotalTokens — no pruning.
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_1", Content: "hello"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tu_2", Content: "world"},
		}},
	}
	out, saved := microcompactToolResults(msgs)
	if saved != 0 {
		t.Errorf("expected no savings under threshold, got %d", saved)
	}
	// Slice should be returned as-is (same backing arrays).
	if len(out) != len(msgs) {
		t.Errorf("length changed: %d vs %d", len(out), len(msgs))
	}
}
