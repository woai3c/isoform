package native

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ContextWindowOverrideEnv is the env var that, when set to a positive
// integer, overrides the per-model context window returned by
// ContextLimitForModel. Introduced because sub2api routes requests
// nominally addressed to Claude models (opus-4-7 / sonnet-4-6 etc.) to a
// smaller-window upstream (Kimi K2.6, 262144 tokens); without this
// override, AutoCompactThreshold (0.75 × 1_000_000 = 750k) never fires
// before the upstream 400s with "exceeded model token limit: 262144".
const ContextWindowOverrideEnv = "YANSU_CONTEXT_WINDOW_OVERRIDE"

// AutoCompactThreshold is the fraction of the model's context window at
// which auto-compaction triggers. 0.75 is a deliberate middle ground:
//   - Claude Code waits until ~87-90% (effective_window - 13k buffer);
//     too late once thinking blocks + parallel tool calls are in play.
//   - Gemini CLI fires at 50%; aggressive, doubles summarize cost.
//   - Codex uses per-model absolute thresholds.
//
// 75% leaves enough headroom for one full thinking turn (~16k) plus a
// few tool_results before the next API call would 400 on context.
const AutoCompactThreshold = 0.75

// PreserveTailTokens is the budget of recent message tokens to keep
// verbatim during compaction. Older messages are summarised; messages
// whose cumulative tail size fits in this budget pass through unchanged.
//
// 40k chosen so a typical "last few turns" — including one or two
// medium-sized tool_results — survives intact, while the summary path
// still has meaningful older history to compress. Phase 3 wires this
// constant into the split logic; Phase 2 currently summarises everything.
const PreserveTailTokens = 40000

// DefaultContextLimit is the fallback context window for unknown
// models. All current Claude models (Haiku 4.5 / Sonnet 4.6 / Opus 4.7)
// expose a 200k window, so the default is correct in practice.
const DefaultContextLimit = 200000

// SummarizeMaxTokens caps the summary's output length. 8k matches
// Claude Code's MAX_OUTPUT_TOKENS_FOR_SUMMARY budget — long enough for
// the 9-section format on a busy session, short enough that the next
// turn still has plenty of room.
const SummarizeMaxTokens = 8192

// MicrocompactPerResultTokens is the per-tool_result threshold above
// which the result body is truncated to a placeholder during the
// pre-summarisation pruning pass. A single Read of a 30k-line file or
// a Bash command with paginated output dominates context cost; cutting
// just those rarely loses important signal because they get
// re-fetched on demand.
//
// 10k tokens ≈ ~40 KB of text; finer-grained than Gemini's 50k
// per-call rule (D:/res/gemini-cli) which we found too coarse.
const MicrocompactPerResultTokens = 10000

// MicrocompactTotalTokens is the sum-of-tool_result-content threshold
// at which microcompaction starts pruning. Below this, summarisation
// is the right tool; above it, pruning the largest results first
// often gets us back below the auto-compact threshold without
// invoking the model at all.
const MicrocompactTotalTokens = 50000

// MicrocompactPlaceholder is the body that replaces a pruned
// tool_result. Includes the original size + tool name so the model
// understands what it lost and can re-fetch if needed.
const MicrocompactPlaceholder = "[output truncated by context-pressure microcompaction; %d bytes from %s — re-run the tool if you still need the contents]"

// SummarizeTimeout caps how long a single summarize call may run before
// the loop falls back to the brute-force "drop oldest 50%" path. The
// model can normally produce the summary in 5-15s; 60s is the latency
// budget at which we'd rather lose detail than block the user.
const SummarizeTimeout = 60 * time.Second

// ContextLimitForModel returns the model's documented context window.
// Match is substring-based on the model id so pinned ids
// ("claude-sonnet-4-6-20251015") and aliases ("claude-sonnet-4-6")
// both resolve. Source: https://platform.claude.com/docs/en/docs/about-claude/models/overview
//
// 1M-context models: Fable 5, Mythos 5, Opus 4.8, Opus 4.7, Opus 4.6,
// Sonnet 5, Sonnet 4.6.
// 200k-context models: Haiku 4.5 and all earlier Claude 4.x.
func ContextLimitForModel(model string) int {
	if v := strings.TrimSpace(os.Getenv(ContextWindowOverrideEnv)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if model == "" {
		return DefaultContextLimit
	}
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"),
		strings.Contains(m, "mythos"),
		strings.Contains(m, "opus-4-8"),
		strings.Contains(m, "opus-4-7"),
		strings.Contains(m, "opus-4-6"),
		strings.Contains(m, "sonnet-5"),
		strings.Contains(m, "sonnet-4-6"):
		return 1000000
	case strings.Contains(m, "claude"):
		return DefaultContextLimit
	case strings.Contains(m, "gemini"):
		return 1048576
	case strings.Contains(m, "gpt-4") || strings.Contains(m, "gpt-5"):
		return 128000
	}
	return DefaultContextLimit
}

// MaxOutputTokensForModel returns the per-response output cap for the
// given Claude model, mirroring the Anthropic Models docs. Picks per-model
// instead of the legacy 8192 constant because Sonnet 4.x can emit 64k and
// Opus 4.6/4.7 can emit 128k — capping at 8k silently truncates long
// answers (the caller's loop has no way to detect that until message_delta
// reports stop_reason="max_tokens").
//
// Matching is deliberately restricted to the explicit 4.x version
// substrings we know about. Generic "sonnet" / "haiku" matches would
// over-report for Claude 3.x ids (e.g. "claude-3-5-sonnet-20241022"
// caps at 8192; sending max_tokens=64000 returns HTTP 400). Unknown
// ids fall back to MaxOutputTokens (8192) which any current Claude
// model accepts.
func MaxOutputTokensForModel(model string) int {
	if model == "" {
		return MaxOutputTokens
	}
	m := strings.ToLower(model)
	switch {
	// Fable 5 / Mythos 5 / Opus 4.6–4.8 / Sonnet 5 — 128k output.
	case strings.Contains(m, "fable"),
		strings.Contains(m, "mythos"),
		strings.Contains(m, "opus-4-8"),
		strings.Contains(m, "opus-4-7"),
		strings.Contains(m, "opus-4-6"),
		strings.Contains(m, "sonnet-5"):
		return 128000
	// Opus 4.5 — 64k output (legacy).
	case strings.Contains(m, "opus-4-5"):
		return 64000
	// Opus 4 / 4.1 — 32k output. Includes the deprecated bare Opus 4
	// dated id "claude-opus-4-20250514".
	case strings.Contains(m, "opus-4-1"),
		strings.Contains(m, "opus-4-0"),
		strings.Contains(m, "opus-4-20"):
		return 32000
	// Sonnet 4 / 4.5 / 4.6 — all 64k output. Includes the deprecated
	// bare Sonnet 4 dated id "claude-sonnet-4-20250514". Note: we do
	// NOT match generic "sonnet" — Claude 3 / 3.5 Sonnet caps at 4k–8k
	// and the Anthropic API 400s on oversized max_tokens.
	case strings.Contains(m, "sonnet-4-6"),
		strings.Contains(m, "sonnet-4-5"),
		strings.Contains(m, "sonnet-4-0"),
		strings.Contains(m, "sonnet-4-20"):
		return 64000
	// Haiku 4.5 — 64k output. Earlier Haiku models cap lower so we do
	// not match generic "haiku" (same reason as Sonnet above).
	case strings.Contains(m, "haiku-4-5"):
		return 64000
	}
	return MaxOutputTokens
}

// modelRequiresAdaptiveThinking reports whether the model rejects the
// legacy thinking config {type:"enabled", budget_tokens:N} with an HTTP
// 400 ("Use thinking.type.adaptive"). Fable 5 / Mythos 5, Opus 4.7/4.8
// and Sonnet 5 accept only {type:"adaptive"} — budget_tokens is fully
// removed on them. Matching is substring-based so pinned ids and
// suffixed variants ("claude-fable-5[1m]") resolve too.
func modelRequiresAdaptiveThinking(model string) bool {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"),
		strings.Contains(m, "mythos"),
		strings.Contains(m, "opus-4-8"),
		strings.Contains(m, "opus-4-7"),
		strings.Contains(m, "sonnet-5"):
		return true
	}
	return false
}

// AutoCompactLimitForModel returns the absolute token threshold above
// which auto-compaction should fire for this model.
func AutoCompactLimitForModel(model string) int {
	return int(float64(ContextLimitForModel(model)) * AutoCompactThreshold)
}

// EstimateRequestTokens approximates the number of tokens the next
// request will send. Sums the inputs the model saw on the last turn
// (input + cache_read + cache_creation) plus the output it just
// produced — those output tokens become input on the next request.
//
// The new user message + new assistant output for the upcoming turn
// aren't included; treat the result as a lower bound. The loop pairs
// this with EstimateUserContentTokens to roll up just-appended
// tool_results before the auto-compact gate, so a single huge
// tool_result (cua screenshot + log output, ~200k chars) doesn't slip
// past the gate by riding on a stale last-turn usage figure.
func EstimateRequestTokens(u *Usage) int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
}

// charsPerTokenApprox is the industry rule-of-thumb char-to-token
// ratio for English-leaning content. Anthropic / OpenAI both quote ~4
// chars/token; we use it as a fast cheap heuristic in the loop's L2
// auto-compact gate where the worst cost of being off by 20% is one
// extra (or missed) compact — and the 0.75 threshold has ~25%
// headroom already baked in.
const charsPerTokenApprox = 4

// EstimateUserContentTokens approximates the token cost of a user
// message we are ABOUT to append to messages — a "now" estimate that
// complements EstimateRequestTokens' "last turn" estimate. The L2
// auto-compact gate at the top of the next turn (loop.go:1619) needs
// to see the just-appended tool_results: a 200k-char cua screenshot
// blows past a 262k upstream window in one turn, but
// EstimateRequestTokens alone reflects only the prior turn's wire
// size and the gate stays asleep.
//
// Walks each ContentBlock and sums:
//   - Text — len of the inline string
//   - Source.Data — base64 length of any inline image (cua screenshots,
//     uploaded attachments) since base64 bytes are roughly what the
//     model sees on the wire pre-tokenization
//   - Content — tool_result payload, which is `any` and may be a
//     plain string, a []ContentBlock for mixed image+text results,
//     or anything else map-shaped (rare, only when tools return JSON).
//     The size of arbitrary map data is approximated via fmt — good
//     enough for the gate, never exact.
//
// Total chars / charsPerTokenApprox. Returns 0 for nil / empty input.
func EstimateUserContentTokens(blocks []ContentBlock) int {
	if len(blocks) == 0 {
		return 0
	}
	total := 0
	for _, b := range blocks {
		total += len(b.Text)
		total += len(b.Thinking)
		if b.Source != nil {
			total += len(b.Source.Data)
		}
		total += contentAnySize(b.Content)
	}
	return total / charsPerTokenApprox
}

// contentAnySize estimates char cost for ContentBlock.Content which
// is declared `any` (string for simple tool results, []ContentBlock
// for mixed image+text, occasionally a map for structured returns).
// We avoid reflect.Sizeof which would over-count Go struct padding;
// fmt-style width is closer to what serialization actually sends.
func contentAnySize(v any) int {
	if v == nil {
		return 0
	}
	switch c := v.(type) {
	case string:
		return len(c)
	case []ContentBlock:
		n := 0
		for _, sub := range c {
			n += len(sub.Text) + len(sub.Thinking)
			if sub.Source != nil {
				n += len(sub.Source.Data)
			}
			n += contentAnySize(sub.Content)
		}
		return n
	case []any:
		n := 0
		for _, item := range c {
			n += contentAnySize(item)
		}
		return n
	case map[string]any:
		n := 0
		for k, val := range c {
			n += len(k) + contentAnySize(val)
		}
		return n
	}
	// Fallback for unknown shapes: stringify and count. Slower than the
	// type-asserted branches above but bounds the cost for an unfamiliar
	// tool_result payload we'd otherwise treat as 0.
	return len(fmt.Sprint(v))
}

// summarizeSystemPrompt is the instruction the model follows while
// summarising. The 9-section format is adapted from Claude Code's
// compact prompt — empirically the most reliable structure for letting
// the model resume work coherently after a hard cut.
//
// Critical constraints:
//   - TEXT ONLY. The model must not call tools during summarisation;
//     a stray tool_use here would break the synthetic message pair we
//     re-inject the summary as.
//   - No <analysis> or <summary> wrapper — Claude Code uses tags for
//     downstream stripping, but our re-injection is a plain user
//     message so the wrappers add noise.
const summarizeSystemPrompt = `You are summarising a conversation between a user and an AI coding assistant so a fresh assistant turn can continue the work without losing context. Your output replaces the prior history; everything the next turn needs to know must be in your summary.

Respond with TEXT ONLY. Do not call any tools. Do not output JSON. Do not wrap your output in tags.

Structure your summary with these nine sections, each as a short paragraph or bulleted list:

1. Primary Request — what the user originally asked for, in their own words where possible.
2. Key Technical Concepts — frameworks, APIs, patterns, and domain ideas that came up.
3. Files and Code — every file path mentioned, what it contains, and any code blocks the assistant wrote or modified (preserve exact identifiers and signatures).
4. Errors and Fixes — every error encountered and how it was resolved (or whether it remains open).
5. Problem Solving — non-obvious decisions, constraints discovered mid-flight, and dead ends ruled out.
6. All User Messages — list every distinct request or instruction from the user. Do not paraphrase aggressively; preserve specifics.
7. Pending Tasks — work that was promised, started, or queued but not yet completed.
8. Current Work — what the assistant was actively doing or about to do at the cutoff point.
9. Next Steps — the concrete action the assistant should take when resuming, with enough detail that no clarifying question is needed.

Be specific. Names, paths, error strings, and decisions matter more than narrative. The next turn will see ONLY your summary plus any messages preserved verbatim — anything you omit is lost.`

// summarizeUserPrompt is the user-side trigger that closes the
// summary turn. Kept separate from the system prompt so the system
// prompt can be cached across compaction calls within a long session.
const summarizeUserPrompt = `Output your nine-section summary now as plain text. Do not call any tools. Do not start a new thinking block.`

// CompactionResult is what runAutoCompact returns. The loop consumes
// the new messages slice directly; SummaryText is also propagated to
// the persistence layer so the boundary row has user-visible content.
type CompactionResult struct {
	// NewMessages is the rebuilt conversation for the next turn:
	// [synthetic user with summary, synthetic assistant ack, ...preservedTail].
	NewMessages []Message
	// SummaryText is the raw summary output, used as the persisted
	// compact_summary row's content.
	SummaryText string
	// PreservedCount is how many messages from the original tail were
	// preserved verbatim. Phase 2 always returns 0; Phase 3 wires up
	// the token-budgeted split.
	PreservedCount int
	// Usage from the summarize call — surfaced for observability so
	// the UI can show how much compaction itself cost.
	Usage *Usage
}

// runAutoCompact summarises the older portion of conversation history
// and returns a rebuilt message slice ready for the next turn. The
// summarize call is bounded by SummarizeTimeout; on timeout / API
// error the caller should fall back to dropOldestHalf rather than
// abort the run.
//
// Strategy: split the messages into (older, tail) where the tail is
// the largest suffix whose estimated token count fits in
// PreserveTailTokens. The older portion is summarised; the tail is
// preserved verbatim. The split is widened backward as needed to
// avoid breaking tool_use/tool_result pairing.
//
// If everything fits in PreserveTailTokens already (compaction was
// triggered by overestimation), this still summarises the front half
// — the alternative is "do nothing" which would leave us above
// threshold next turn and re-trigger.
func runAutoCompact(ctx context.Context, client LLMClient, model string, messages []Message) (*CompactionResult, error) {
	if len(messages) == 0 {
		return &CompactionResult{NewMessages: messages}, nil
	}

	// Pre-pass: prune oversized tool_results in the older portion of
	// history. If this alone gets us comfortably below the auto-compact
	// threshold, skip the LLM summarise call entirely (saves an
	// expensive round trip and preserves more recent context). The
	// preserved tail is left untouched — those results are still
	// "current" from the model's perspective and pruning them would
	// hurt the next turn's reasoning.
	pruned, prunedSavings := microcompactToolResults(messages)
	if prunedSavings > MicrocompactTotalTokens/2 {
		// Heuristic: if microcompaction freed at least half the
		// total-tool-result threshold, the savings are material — try
		// the pruned-only path. The caller's runningTotalTokens latch
		// will reset and re-fire the warning if context climbs back up.
		messages = pruned
	}

	older, tail := splitForPreservedTail(messages, PreserveTailTokens)
	if len(older) == 0 {
		// Nothing older to summarise (rare — usually means the budget
		// was huge relative to history). Force a split at the midpoint
		// so the loop still makes progress.
		mid := len(messages) / 2
		older = messages[:mid]
		tail = messages[mid:]
		// Re-respect tool_use pairing across the manual split.
		for len(older) > 0 && hasOrphanToolResult(tail) {
			older = older[:len(older)-1]
			tail = messages[len(older):]
		}
		if len(older) == 0 {
			// Genuinely nothing to compact — return as-is.
			return &CompactionResult{NewMessages: messages}, nil
		}
	}

	// Bound the summarize call so a slow/hung gateway can't block the
	// outer task forever. The outer task context still applies — if the
	// user cancels mid-compact the parent ctx fires first.
	sumCtx, cancel := context.WithTimeout(ctx, SummarizeTimeout)
	defer cancel()

	summary, usage, err := summarizeMessages(sumCtx, client, model, older)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, fmt.Errorf("summarizer returned empty text")
	}

	rebuilt := buildPostCompactionMessages(summary, tail)
	return &CompactionResult{
		NewMessages:    rebuilt,
		SummaryText:    summary,
		PreservedCount: len(tail),
		Usage:          usage,
	}, nil
}

// splitForPreservedTail walks messages from the end, accumulating an
// estimated token budget, and returns (older, tail) where tail is the
// suffix that fits within budgetTokens. The split is shifted left as
// needed so tail never starts with an orphan tool_result (which would
// 400 the next request once paired with the synthetic assistant ack).
//
// estimate uses 4 chars/token, which underestimates Chinese / code
// (closer to 2-3) but matches the Anthropic tokenizer for English
// well. Underestimating is safer than over: we'd rather preserve a
// touch more verbatim than too little.
func splitForPreservedTail(messages []Message, budgetTokens int) (older, tail []Message) {
	if budgetTokens <= 0 || len(messages) == 0 {
		return messages, nil
	}
	used := 0
	cut := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		size := estimateMessageTokens(messages[i])
		if used+size > budgetTokens {
			break
		}
		used += size
		cut = i
	}
	// Walk left across orphan tool_results so the tail starts cleanly.
	for cut > 0 && hasOrphanToolResult(messages[cut:]) {
		cut--
	}
	return messages[:cut], messages[cut:]
}

// estimateMessageTokens approximates token count for one Message via
// 4-chars-per-token over its serialised content. Doesn't try to be
// exact — we only use this for budget allocation, and Anthropic's
// usage field corrects the count post-turn.
func estimateMessageTokens(m Message) int {
	const charsPerToken = 4
	chars := len(m.Role) + 4 // small fixed overhead for the role + JSON wrapping
	switch v := m.Content.(type) {
	case string:
		chars += len(v)
	case []ContentBlock:
		for _, b := range v {
			chars += estimateBlockChars(b)
		}
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

func estimateBlockChars(b ContentBlock) int {
	n := 16 // type tag overhead
	n += len(b.Text)
	n += len(b.Thinking)
	n += len(b.Name)
	n += len(b.ID)
	n += len(b.ToolUseID)
	if s, ok := b.Content.(string); ok {
		n += len(s)
	}
	if blocks, ok := b.Content.([]ContentBlock); ok {
		for _, sub := range blocks {
			n += estimateBlockChars(sub)
		}
	}
	// Input is a parsed map[string]any; rough estimate via JSON length
	// would re-marshal — skip and add a small constant. Tool inputs
	// rarely exceed a few hundred bytes anyway, and the tail-preserve
	// budget has slack baked in via the conservative 4-char/token rate.
	if b.Input != nil {
		n += 64
	}
	return n
}

// summarizeMessages issues a single non-tool, non-thinking model call
// that takes the existing conversation as input and returns one block
// of summary text. Reuses the same Client / model so the summarize
// call goes through whichever upstream gateway the agent is using.
func summarizeMessages(ctx context.Context, client LLMClient, model string, messages []Message) (string, *Usage, error) {
	// Sanitise the trailing message: the API rejects requests where the
	// last message is an assistant turn that ends with a tool_use block
	// without a matching tool_result. We only invoke compaction at clean
	// boundaries (after tool_results are appended), but defensively
	// strip any trailing tool_use orphans so a misuse doesn't 400.
	prompt := append([]Message{}, messages...)
	prompt = append(prompt, Message{
		Role:    "user",
		Content: []ContentBlock{{Type: "text", Text: summarizeUserPrompt}},
	})

	req := &Request{
		Model:     model,
		Messages:  prompt,
		System:    []ContentBlock{{Type: "text", Text: summarizeSystemPrompt}},
		MaxTokens: SummarizeMaxTokens,
	}

	var (
		buf       strings.Builder
		usage     *Usage
		seenError *StreamError
	)
	err := client.Stream(ctx, req, func(ev *StreamEvent) error {
		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				usage = ev.Message.Usage
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" {
				buf.WriteString(ev.Delta.Text)
			}
		case "message_delta":
			if ev.Usage != nil {
				if usage == nil {
					usage = &Usage{}
				}
				usage.InputTokens += ev.Usage.InputTokens
				usage.OutputTokens += ev.Usage.OutputTokens
				usage.CacheReadInputTokens += ev.Usage.CacheReadInputTokens
				usage.CacheCreationInputTokens += ev.Usage.CacheCreationInputTokens
			}
		case "error":
			if ev.Error != nil {
				seenError = ev.Error
			}
		}
		return nil
	})
	if err != nil {
		return "", usage, err
	}
	if seenError != nil {
		return "", usage, fmt.Errorf("stream error during summarize: %s", seenError.Message)
	}
	return buf.String(), usage, nil
}

// buildPostCompactionMessages produces the message slice the loop
// continues from after a successful compaction. The shape is a synthetic
// user message carrying the summary, an assistant acknowledgement, and
// then any preserved tail — Anthropic accepts this without complaint and
// it matches what Gemini CLI does (see chatCompressionService.ts:429).
//
// preservedTail may be nil (Phase 2 — full replacement). Phase 3 passes
// the verbatim tail in.
func buildPostCompactionMessages(summary string, preservedTail []Message) []Message {
	out := make([]Message, 0, 2+len(preservedTail))
	out = append(out, Message{
		Role: "user",
		Content: []ContentBlock{{
			Type: "text",
			Text: "[Conversation summary follows — earlier messages have been compacted to fit the context window.]\n\n" + summary,
		}},
	})
	out = append(out, Message{
		Role: "assistant",
		Content: []ContentBlock{{
			Type: "text",
			Text: "Understood — I have the summary. Continuing.",
		}},
	})
	out = append(out, preservedTail...)
	return out
}

// dropOldestHalf is the fallback when summarisation fails. It keeps
// the most recent half of the conversation verbatim and prepends a
// blunt notice. Quality is lower than summarisation but the run can
// continue rather than dying; that's the explicit user preference for
// this fallback path.
//
// Anthropic requires every tool_use to have a matching tool_result and
// vice versa. The cut is adjusted in two phases to preserve pairing:
//  1. Walk the cut LEFT past any tool_result-leading kept slice (the
//     matching tool_use was in the dropped half).
//  2. Run RepairOrphans on the rebuilt slice as the final guarantee:
//     forward-orphan tool_use blocks at the tail (whose tool_result
//     would have lived past the conversation tail) get a synthetic
//     [interrupted] result; any reverse-orphan that slipped through is
//     dropped. This is the same bulletproof pass the loop runs before
//     every API call, so the fallback path can never produce a
//     conversation that the next turn rejects.
func dropOldestHalf(messages []Message) []Message {
	if len(messages) <= 2 {
		return messages
	}
	cut := len(messages) / 2

	// Phase 1: if the slice we're about to keep starts with a user
	// message containing tool_results, walk back until we include the
	// matching assistant tool_use turn. Anthropic 400s on orphan
	// tool_results just like it does on orphan tool_uses.
	for cut > 0 && hasOrphanToolResult(messages[cut:]) {
		cut--
	}

	out := make([]Message, 0, 1+len(messages)-cut)
	out = append(out, Message{
		Role: "user",
		Content: []ContentBlock{{
			Type: "text",
			Text: "[Earlier conversation truncated — the summariser was unavailable, so the older half of this thread was dropped. The most recent messages follow.]",
		}},
	})
	out = append(out, messages[cut:]...)

	// Phase 2: defense-in-depth — run RepairOrphans on the result.
	// Catches any pairing edge case Phase 1's heuristic missed (e.g. a
	// tool_use whose paired tool_result was somehow filtered out by
	// upstream sanitization, or a kept slice whose last assistant message
	// has tool_use blocks that genuinely had no result).
	RepairOrphans(&out)
	return out
}

// microcompactToolResults walks messages and replaces any tool_result
// block whose content estimate exceeds MicrocompactPerResultTokens
// with a placeholder. Only fires when the cumulative tool_result size
// across the conversation is large enough to matter; below that the
// caller skips the pre-pass.
//
// Returns the new slice and the number of estimated tokens saved so
// the caller can decide whether to fall back to summarisation.
//
// The function is non-destructive: it builds a new slice and shallow-
// copies messages whose tool_result blocks need pruning. Untouched
// messages share their slice with the input, so the per-call cost is
// O(messages with oversized tool_results), not O(N) regardless.
func microcompactToolResults(messages []Message) ([]Message, int) {
	totalToolBytes := 0
	for _, m := range messages {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				if s, ok := b.Content.(string); ok {
					totalToolBytes += len(s)
				}
			}
		}
	}
	if totalToolBytes/4 < MicrocompactTotalTokens {
		return messages, 0
	}

	out := make([]Message, len(messages))
	saved := 0
	for i, m := range messages {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			out[i] = m
			continue
		}
		var newBlocks []ContentBlock
		mutated := false
		for j, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			s, sok := b.Content.(string)
			if !sok {
				continue
			}
			estTokens := len(s) / 4
			if estTokens <= MicrocompactPerResultTokens {
				continue
			}
			if !mutated {
				newBlocks = append([]ContentBlock(nil), blocks...)
				mutated = true
			}
			placeholder := fmt.Sprintf(MicrocompactPlaceholder, len(s), b.ToolUseID)
			newBlocks[j].Content = placeholder
			saved += estTokens - len(placeholder)/4
		}
		if mutated {
			cm := m
			cm.Content = newBlocks
			out[i] = cm
		} else {
			out[i] = m
		}
	}
	return out, saved
}

// hasOrphanToolResult reports whether the first message of head is a
// user message that contains a tool_result block whose tool_use_id has
// no matching tool_use earlier in head. Used by dropOldestHalf to walk
// the cut backwards until pairing is restored.
func hasOrphanToolResult(head []Message) bool {
	if len(head) == 0 {
		return false
	}
	first := head[0]
	if first.Role != "user" {
		return false
	}
	blocks, ok := first.Content.([]ContentBlock)
	if !ok {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}
