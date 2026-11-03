package toolserver

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// cuaImageContent promotes image blocks in the native daemon envelope to MCP
// images after the native page gate has completed. Unattended page snapshots
// have already had screenshots stripped; this never reads a screenshot file
// or recovers data that the gate removed. Other envelope metadata is retained.
func cuaImageContent(text string) (string, []mcp.Content) {
	var envelope map[string]any
	if json.Unmarshal([]byte(text), &envelope) != nil || envelope["kind"] != "call" {
		return text, nil
	}
	payload, ok := envelope["payload"].(map[string]any)
	if !ok {
		return text, nil
	}
	blocks, ok := payload["content"].([]any)
	if !ok {
		return text, nil
	}
	var images []mcp.Content
	kept := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "image" {
			kept = append(kept, raw)
			continue
		}
		data, _ := block["data"].(string)
		mime, _ := block["mimeType"].(string)
		if data == "" || mime == "" {
			kept = append(kept, raw)
			continue
		}
		images = append(images, mcp.NewImageContent(data, mime))
		// Preserve annotations and other image metadata without duplicating
		// the base64 payload in the text the assistant also receives.
		delete(block, "data")
		kept = append(kept, block)
	}
	if len(images) == 0 {
		return text, nil
	}
	payload["content"] = kept
	raw, err := json.Marshal(envelope)
	if err != nil {
		return text, nil
	}
	return string(raw), images
}
