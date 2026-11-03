package native

import (
	"context"
	"encoding/json"
	"fmt"

	"yansu-agent/agent/tools"
)

// DispatchTool exposes the exact native call gate to the per-run MCP server.
// Keeping this adapter at the execution boundary avoids copying or changing
// the protected-path, Bash, authority, approval and computer-use policies.
func DispatchTool(ctx context.Context, cfg *LoopConfig, name, id string, input map[string]any) (tools.ToolResult, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return tools.ToolResult{}, err
	}
	block, _, err := executeToolUse(ctx, cfg, toolUseReq{Name: name, ID: id, Input: string(raw)})
	result := tools.ToolResult{IsError: block.IsError}
	switch content := block.Content.(type) {
	case string:
		result.Content = content
	case []tools.ContentBlock:
		result.Blocks = content
	case nil:
	default:
		return tools.ToolResult{}, fmt.Errorf("unsupported native tool result: %T", content)
	}
	return result, err
}
