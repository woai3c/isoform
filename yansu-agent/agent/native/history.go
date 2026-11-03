package native

import (
	"encoding/json"
	"fmt"
)

// HistoryItem is the persistence-layer shape used by the loop to read
// prior stream items. It's deliberately decoupled from wails-gui's
// storage.StreamItemRow so the loop can be unit-tested without dragging
// in SQLite.
type HistoryItem struct {
	Kind              string // user | assistant | thinking | tool_use | tool_result | image
	Content           string
	ToolName          string
	ToolInput         string // JSON-encoded; may be empty
	ToolUseID         string // required for tool_result entries
	ThinkingSignature string // required when replaying Anthropic thinking blocks
	Images            string // JSON-encoded list of {mediaType, dataBase64}; only on user entries
	IsError           bool   // only meaningful for tool_result
}

// HistoryLoader is what the NativeAgentRunner uses to read prior
// stream items from storage.
type HistoryLoader interface {
	LoadHistory(sessionID string) ([]HistoryItem, error)
}

// BuildMessages converts a flat history into Anthropic-format messages.
// It enforces the tool_use/tool_result pairing invariant in BOTH
// directions:
//   - every assistant tool_use must be followed by a user message
//     containing a matching tool_result; orphans get a synthetic
//     "[interrupted]" tool_result appended (forward-orphan).
//   - every tool_result whose ToolUseID has no matching tool_use earlier
//     in history is silently dropped (reverse-orphan). This handles
//     persistence inconsistencies where a tool_use row failed to commit
//     but its tool_result row did, or where rows for a previous
//     OpenAI-format tool_use leaked into history without the matching
//     tool_use rebuilding correctly.
//
// Without the reverse-orphan filter, a single missing tool_use row in
// the SQLite stream_items table dooms every subsequent send with a 400
// from the Anthropic API, locking the session forever.
func BuildMessages(history []HistoryItem) ([]Message, error) {
	// Pre-pass: collect every tool_use_id that exists in history. Used
	// below to filter out reverse-orphan tool_results that reference an
	// id never declared by any tool_use row.
	toolUseIDs := make(map[string]bool)
	for _, it := range history {
		if it.Kind == "tool_use" && it.ToolUseID != "" {
			toolUseIDs[it.ToolUseID] = true
		}
	}

	var out []Message
	var pending map[string]bool // tool_use ids awaiting a result

	// drainPending returns all pending forward-orphan tool_uses as
	// synthetic [interrupted] tool_result blocks AND clears the pending
	// map. Returning blocks (rather than appending a message) lets the
	// caller decide where they go: when followed by a user message we
	// prepend them to its content (single user message preserves
	// alternation); when followed by an assistant or end-of-history we
	// emit a fresh user message via flushPending.
	//
	// Grouping all ids into one block list per call also fixes a
	// pre-existing bug where the previous flushPending appended ONE
	// user message PER pending id — multiple unresolved tool_uses
	// produced multiple consecutive user messages, which Anthropic
	// rejects with "messages: roles must alternate between 'user' and
	// 'assistant'".
	drainPending := func() []ContentBlock {
		if len(pending) == 0 {
			return nil
		}
		blocks := make([]ContentBlock, 0, len(pending))
		for id := range pending {
			blocks = append(blocks, ContentBlock{
				Type:      "tool_result",
				ToolUseID: id,
				Content:   "[interrupted]",
				IsError:   true,
			})
		}
		pending = nil
		return blocks
	}

	flushPending := func() {
		blks := drainPending()
		if len(blks) == 0 {
			return
		}
		// Defense in depth: if the trailing message is already user,
		// merge into it rather than producing a consecutive user.
		// Normally won't fire because the call sites above already
		// route through drainPending in the user case, but guards
		// against future call-site additions.
		if n := len(out); n > 0 && out[n-1].Role == "user" {
			last := &out[n-1]
			existing, _ := last.Content.([]ContentBlock)
			last.Content = append(existing, blks...)
			return
		}
		out = append(out, Message{Role: "user", Content: blks})
	}

	for _, it := range history {
		switch it.Kind {
		case "user":
			// A user message may also include images.
			blocks, err := buildUserBlocks(it)
			if err != nil {
				return nil, err
			}
			// If any tool_uses are pending, prepend their synthetic
			// [interrupted] tool_results into THIS user message rather
			// than emitting a separate one. The synthetic results pair
			// with the immediately-preceding assistant tool_use, and
			// folding them into the upcoming user message keeps the
			// conversation strictly alternating.
			if synth := drainPending(); len(synth) > 0 {
				blocks = append(synth, blocks...)
			}
			out = append(out, Message{Role: "user", Content: blocks})
		case "assistant":
			if pending != nil {
				flushPending()
			}
			if it.Content == "" {
				continue
			}
			out = append(out, Message{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: it.Content}}})
		case "thinking":
			// Thinking blocks are expected only when the prior
			// assistant message had extended thinking enabled. We keep
			// them only when the persisted row includes Anthropic's
			// signature. Unsigned thinking blocks are rejected by the
			// Messages API when replayed, so legacy rows are skipped.
			if it.ThinkingSignature == "" {
				continue
			}
			if len(out) == 0 {
				continue
			}
			last := &out[len(out)-1]
			if last.Role != "assistant" {
				continue
			}
			blocks, _ := last.Content.([]ContentBlock)
			blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: it.Content, Signature: it.ThinkingSignature})
			last.Content = blocks
		case "tool_use":
			// Mirror the tool_result branch's empty-id guard. A tool_use
			// with no id can never be paired with a tool_result, and
			// shipping it triggers the sub2api/Kimi `tool_call_id  is
			// not found` 400. history_loader.go already drops these at
			// the storage layer, but callers that pass PriorHistory
			// directly bypass that filter — guard here too.
			if it.ToolUseID == "" {
				continue
			}
			// Append to (or create) a trailing assistant message.
			// Anthropic API requires tool_use.input to be present and to
			// be a JSON object — null/missing/non-object all yield 400.
			// Default to {} so legacy rows (or rows persisted before the
			// loop emitted the parsed input) don't break history reload.
			var input any = map[string]any{}
			if it.ToolInput != "" {
				var parsed any
				if err := json.Unmarshal([]byte(it.ToolInput), &parsed); err == nil {
					if m, ok := parsed.(map[string]any); ok {
						input = m
					}
				}
			}
			block := ContentBlock{
				Type:  "tool_use",
				ID:    it.ToolUseID,
				Name:  it.ToolName,
				Input: input,
			}
			if len(out) > 0 && out[len(out)-1].Role == "assistant" {
				last := &out[len(out)-1]
				blocks, _ := last.Content.([]ContentBlock)
				last.Content = append(blocks, block)
			} else {
				out = append(out, Message{Role: "assistant", Content: []ContentBlock{block}})
			}
			if pending == nil {
				pending = make(map[string]bool)
			}
			pending[it.ToolUseID] = true
		case "tool_result":
			// Reverse-orphan guard: a tool_result whose ToolUseID was
			// never declared by any tool_use row in this history is
			// guaranteed to 400 the request. Drop it entirely; downstream
			// RepairOrphans pass on the assembled message slice will
			// catch anything we miss here, but skipping at this layer
			// keeps the message slice cleaner from the start.
			if it.ToolUseID == "" || !toolUseIDs[it.ToolUseID] {
				continue
			}
			if pending != nil {
				delete(pending, it.ToolUseID)
				if len(pending) == 0 {
					pending = nil
				}
			}
			block := ContentBlock{
				Type:      "tool_result",
				ToolUseID: it.ToolUseID,
				Content:   it.Content,
				IsError:   it.IsError,
			}
			if len(out) > 0 && out[len(out)-1].Role == "user" {
				last := &out[len(out)-1]
				blocks, _ := last.Content.([]ContentBlock)
				last.Content = append(blocks, block)
			} else {
				out = append(out, Message{Role: "user", Content: []ContentBlock{block}})
			}
		default:
			// Unknown kinds (system, info, error) are skipped; they are
			// for UX, not model context.
			continue
		}
	}
	flushPending()
	return out, nil
}

func buildUserBlocks(it HistoryItem) ([]ContentBlock, error) {
	var blocks []ContentBlock

	// Parse images when present.
	if it.Images != "" {
		var imgs []struct {
			MediaType  string `json:"media_type"`
			DataBase64 string `json:"data_base64"`
		}
		if err := json.Unmarshal([]byte(it.Images), &imgs); err != nil {
			return nil, fmt.Errorf("unmarshal images: %w", err)
		}
		for _, img := range imgs {
			if img.MediaType == "" || img.DataBase64 == "" {
				continue
			}
			blocks = append(blocks, ContentBlock{
				Type:   "image",
				Source: &ImageSource{Type: "base64", MediaType: img.MediaType, Data: img.DataBase64},
			})
		}
	}
	if it.Content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: it.Content})
	}
	if len(blocks) == 0 {
		// Empty text with no images shouldn't happen, but guard anyway.
		blocks = append(blocks, ContentBlock{Type: "text", Text: ""})
	}
	return blocks, nil
}

// ApplyCachePrefix marks the last N-1 items of messages with ephemeral
// cache_control so subsequent turns reuse the prefix. Anthropic's
// caching requires the suffix we want cached to be exactly the same
// across requests; tool results need to be contiguous.
//
// Policy (§4.2): mark the penultimate message's last block. Messages
// with 0 or 1 entries are left unmarked.
func ApplyCachePrefix(messages []Message) {
	// Content is an `any` holding a []ContentBlock; the slice header we
	// read back shares the same backing array, so mutating blocks[j] in
	// place is visible through messages[i].Content without a writeback.
	for i := range messages {
		blocks, ok := messages[i].Content.([]ContentBlock)
		if !ok || len(blocks) == 0 {
			continue
		}
		for j := range blocks {
			blocks[j].CacheControl = nil
		}
	}

	if len(messages) < 2 {
		return
	}
	blocks, ok := messages[len(messages)-2].Content.([]ContentBlock)
	if !ok || len(blocks) == 0 {
		return
	}
	blocks[len(blocks)-1].CacheControl = &CacheControl{Type: "ephemeral"}
}
