package native

import (
	"log"
)

// RepairOrphans reconciles tool_use ↔ tool_result pairing across the
// entire messages slice, in BOTH directions. Anthropic strictly requires:
//   - every assistant tool_use block to be followed by a user message
//     containing a tool_result block with the matching ToolUseID
//   - every user tool_result block to be preceded somewhere upstream by
//     an assistant tool_use block with the matching ID
//
// Either kind of orphan poisons the next API request with a 400:
//
//	"unexpected `messages.N.content.M: tool_use_id` found in `tool_result`
//	 blocks: <id>. Each `tool_result` block must have a corresponding
//	 `tool_use` block in the previous message."
//
// or the converse "tool_use without tool_result". This function is
// idempotent: running it on every turn before sending the request is the
// universal bulletproof safety net. Returns true when any repair
// happened (used by the loop's retry path to log the recovery).
//
// Mutates messages in place where possible; for forward-orphan repair we
// append synthetic [interrupted] tool_results, which the model reads as
// "the tool didn't get to run, plan accordingly".
func RepairOrphans(messages *[]Message) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}
	repaired := false

	// Pass 0: strip tool_use blocks whose ID is empty. An empty id is
	// unrecoverable — neither side can pair it, and leaving it in
	// triggers the sub2api/Kimi proxy's `tool_call_id  is not found`
	// 400 with the double-space template signature. runOneTurn's
	// stream parser already synthesizes a fallback id at the source,
	// but legacy in-memory state (PriorHistory supplied by callers
	// directly, mid-run mutations) can still slip an empty-id block
	// through. Drop those blocks; if removal empties the assistant
	// message we replace its content with a placeholder text so role
	// alternation is preserved (Anthropic rejects assistant messages
	// with no content blocks).
	src0 := *messages
	for i := range src0 {
		if src0[i].Role != "assistant" {
			continue
		}
		blocks, ok := src0[i].Content.([]ContentBlock)
		if !ok {
			continue
		}
		kept := blocks[:0]
		dropped := 0
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID == "" {
				log.Printf("[orphan-repair] dropping empty-id tool_use block tool=%s (unpairable; sub2api/Kimi would 400 with `tool_call_id  is not found`)", b.Name)
				dropped++
				continue
			}
			kept = append(kept, b)
		}
		if dropped > 0 {
			repaired = true
			if len(kept) == 0 {
				src0[i].Content = []ContentBlock{{
					Type: "text",
					Text: "[Tool call dropped — upstream proxy returned empty tool_use id.]",
				}}
			} else {
				src0[i].Content = kept
			}
		}
	}

	// Pass 1: collect every tool_use ID emitted by an assistant message.
	expected := make(map[string]string) // id -> tool name (for diagnostic)
	for _, m := range *messages {
		if m.Role != "assistant" {
			continue
		}
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" {
				expected[b.ID] = b.Name
			}
		}
	}

	// Pass 2: drop reverse-orphan tool_results. Walk every user message
	// and filter its content blocks to remove tool_results whose
	// ToolUseID isn't in the expected set. If filtering empties the
	// content, replace it with a placeholder text block — we MUST NOT
	// drop the user message outright because Anthropic strictly enforces
	// user/assistant alternation. A user message between two assistant
	// messages, dropped, would yield assistant→assistant which the API
	// rejects with "messages: roles must alternate".
	src := *messages
	out := src[:0]
	for _, m := range src {
		if m.Role != "user" {
			out = append(out, m)
			continue
		}
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			out = append(out, m)
			continue
		}
		kept := blocks[:0]
		for _, b := range blocks {
			if b.Type == "tool_result" {
				// Empty ToolUseID is always a reverse-orphan: no
				// tool_use can ever match it, and sending it triggers
				// the same `tool_call_id  is not found` 400. Previously
				// this branch only checked non-empty ids, which let the
				// empty case slip past the entire RepairOrphans pass.
				if b.ToolUseID == "" {
					log.Printf("[orphan-repair] dropping empty-id tool_result (unpairable)")
					repaired = true
					continue
				}
				if _, ok := expected[b.ToolUseID]; !ok {
					log.Printf("[orphan-repair] dropping reverse-orphan tool_result tool_use_id=%s (no matching tool_use)", b.ToolUseID)
					repaired = true
					continue
				}
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			// All content was orphan tool_results. Replace with a
			// placeholder text block instead of dropping the message
			// — preserves role alternation regardless of what came
			// before/after this message. The text is informative
			// enough that the model treats it as harmless metadata
			// rather than a user instruction.
			m.Content = []ContentBlock{{
				Type: "text",
				Text: "[Stale tool_result discarded — no matching tool_use in history.]",
			}}
		} else {
			m.Content = kept
		}
		out = append(out, m)
	}
	*messages = out

	// Pass 3: collect tool_use IDs that are now covered by a tool_result
	// (after the reverse-orphan pass).
	fulfilled := make(map[string]bool)
	for _, m := range *messages {
		if m.Role != "user" {
			continue
		}
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				fulfilled[b.ToolUseID] = true
			}
		}
	}

	// Pass 4: append synthetic [interrupted] results for forward-orphans
	// (tool_use without a matching tool_result). Group all of them into a
	// single user-role payload — Anthropic accepts a user message that
	// contains multiple tool_result blocks, and a single payload avoids
	// inserting many short user messages.
	//
	// Whether we APPEND a fresh user message or MERGE into the existing
	// last message depends on the role of the current tail:
	//   - tail is assistant (the common case after a turn that ended
	//     mid-tool-execution): append a new user message. Result:
	//     assistant→user, valid alternation.
	//   - tail is already user (rare; can happen if BuildMessages's
	//     existing flushPending path emitted a user message just before,
	//     or if the loaded history was already irregular): merging the
	//     synthetic blocks into that user message preserves alternation.
	//     Anthropic rejects two consecutive user messages outright.
	var synthetic []ContentBlock
	for id := range expected {
		if fulfilled[id] {
			continue
		}
		log.Printf("[orphan-repair] appending synthetic [interrupted] tool_result for forward-orphan tool_use_id=%s tool=%s", id, expected[id])
		synthetic = append(synthetic, ContentBlock{
			Type:      "tool_result",
			ToolUseID: id,
			Content:   "[interrupted]",
			IsError:   true,
		})
		repaired = true
	}
	if len(synthetic) > 0 {
		if n := len(*messages); n > 0 && (*messages)[n-1].Role == "user" {
			last := &(*messages)[n-1]
			blocks, ok := last.Content.([]ContentBlock)
			if !ok {
				// Last user message had a string Content (legacy shape).
				// Preserve it as a text block so we don't lose its body
				// while still merging the synthetic results in.
				if s, isStr := last.Content.(string); isStr && s != "" {
					blocks = []ContentBlock{{Type: "text", Text: s}}
				}
			}
			last.Content = append(blocks, synthetic...)
		} else {
			*messages = append(*messages, Message{Role: "user", Content: synthetic})
		}
	}

	return repaired
}
