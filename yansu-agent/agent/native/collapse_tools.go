package native

import (
	"fmt"
	"log"
)

// ReIDToolBlocks re-generates all tool_use IDs and updates matching
// tool_result references so they are consistently paired. This avoids
// the sub2api proxy mangling historical IDs during Anthropic-to-OpenAI
// conversion (which causes "tool_call_id not found" 400s) while
// keeping the full structured tool context intact — the model never
// sees tool I/O as quotable text and cannot imitate it.
func ReIDToolBlocks(messages *[]Message) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}

	idMap := make(map[string]string) // old ID -> new ID
	counter := 0

	for i, m := range *messages {
		if m.Role != "assistant" {
			continue
		}
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		changed := false
		for j, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" {
				counter++
				newID := fmt.Sprintf("toolu_hist_%04d", counter)
				idMap[b.ID] = newID
				blocks[j].ID = newID
				changed = true
			}
		}
		if changed {
			(*messages)[i].Content = blocks
		}
	}

	for i, m := range *messages {
		if m.Role != "user" {
			continue
		}
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		changed := false
		for j, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				if newID, ok := idMap[b.ToolUseID]; ok {
					blocks[j].ToolUseID = newID
					changed = true
				}
			}
		}
		if changed {
			(*messages)[i].Content = blocks
		}
	}

	if counter > 0 {
		log.Printf("[re-id-tools] re-generated %d tool_use IDs in historical messages", counter)
	}
	return counter > 0
}

// CollapseToolBlocks converts all tool_use and tool_result content blocks
// into plain text blocks. Used as a last-resort fallback when ReIDToolBlocks
// + RepairOrphans cannot fix tool pairing errors from the proxy.
func CollapseToolBlocks(messages *[]Message) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}

	collapsed := false
	for i, m := range *messages {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}

		hasToolBlocks := false
		for _, b := range blocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				hasToolBlocks = true
				break
			}
		}
		if !hasToolBlocks {
			continue
		}

		var newBlocks []ContentBlock
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				collapsed = true
			case "tool_result":
				collapsed = true
			default:
				newBlocks = append(newBlocks, b)
			}
		}

		if len(newBlocks) == 0 {
			newBlocks = []ContentBlock{{Type: "text", Text: "[tool context removed]"}}
		}
		(*messages)[i].Content = newBlocks
	}

	if collapsed {
		log.Printf("[collapse-tools] stripped tool_use/tool_result blocks from %d messages", len(*messages))
	}
	return collapsed
}

// HasToolBlocks checks whether any message contains tool_use or
// tool_result content blocks.
func HasToolBlocks(messages []Message) bool {
	for _, m := range messages {
		blocks, ok := m.Content.([]ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}
