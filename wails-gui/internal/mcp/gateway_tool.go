package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/authority"
	yansutools "yansu-agent/agent/tools"
)

const (
	gatewaySearchToolsSuffix  = "search_tools"
	gatewayDescribeSuffix     = "describe_tool"
	gatewayCallSuffix         = "call_tool"
	defaultGatewaySearchLimit = 10
	maxGatewaySearchLimit     = 50
)

// NewGatewayTools returns the fixed facade registered for a server in
// gateway exposure mode. The upstream MCP tools remain hidden from the
// model until it searches/describes a specific tool and asks call_tool
// to dispatch the original MCP tool name.
// trusted seeds the per-server "skip approval" flag on the call tool so
// trusted servers don't prompt even on the very first gateway call after
// registration; runtime toggles route through gatewayCallTool.SetTrusted
// via the bridge.
func NewGatewayTools(serverID string, manager BridgeManager, trusted bool) []yansutools.Tool {
	call := &gatewayCallTool{serverID: serverID, manager: manager}
	call.trusted.Store(trusted)
	return []yansutools.Tool{
		&gatewaySearchTool{serverID: serverID, manager: manager},
		&gatewayDescribeTool{serverID: serverID, manager: manager},
		call,
	}
}

func gatewayToolName(serverID, suffix string) string {
	return buildExposedName(serverID, suffix)
}

type gatewaySearchTool struct {
	serverID string
	manager  BridgeManager
}

func (t *gatewaySearchTool) Name() string {
	return gatewayToolName(t.serverID, gatewaySearchToolsSuffix)
}

func (t *gatewaySearchTool) Description() string {
	return fmt.Sprintf("Search available MCP tools on server %q and return lightweight metadata without schemas.", t.serverID)
}

func (t *gatewaySearchTool) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search text matched against upstream tool names and descriptions.",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of tools to return.",
			},
		},
		"required": []any{"query"},
	}
}

func (t *gatewaySearchTool) NeedsPermission(input map[string]any) (bool, yansutools.PermissionPrompt) {
	return false, yansutools.PermissionPrompt{}
}

func (t *gatewaySearchTool) DescribeAuthority(input map[string]any) authority.Action {
	return authority.Action{
		Capability: "mcp." + strings.ToLower(sanitizeToolNameSegment(t.serverID)) + ".tools.search",
		ToolName:   t.Name(),
		Operation:  gatewaySearchToolsSuffix,
		Effect:     authority.EffectRead,
		Reversible: true,
	}
}

func (t *gatewaySearchTool) Execute(ctx context.Context, call yansutools.ToolCall) (yansutools.ToolResult, error) {
	_ = ctx
	tools, err := t.manager.Tools(t.serverID)
	if err != nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway search %q: %w", t.serverID, err)
	}
	query, _ := call.Input["query"].(string)
	limit := gatewaySearchLimit(call.Input["limit"])
	matches := searchGatewayTools(tools, query, limit)
	data, err := json.Marshal(matches)
	if err != nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway search %q: marshal results: %w", t.serverID, err)
	}
	return yansutools.ToolResult{Content: string(data)}, nil
}

type gatewayToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type scoredGatewayTool struct {
	tool  mcp.Tool
	score int
	index int
}

func searchGatewayTools(tools []mcp.Tool, query string, limit int) []gatewayToolSearchResult {
	query = strings.TrimSpace(strings.ToLower(query))
	scored := make([]scoredGatewayTool, 0, len(tools))
	for i, tool := range tools {
		score := gatewayToolScore(tool, query)
		if query != "" && score == 0 {
			continue
		}
		scored = append(scored, scoredGatewayTool{tool: tool, score: score, index: i})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index < scored[j].index
	})
	if limit > len(scored) {
		limit = len(scored)
	}
	out := make([]gatewayToolSearchResult, 0, limit)
	for _, item := range scored[:limit] {
		out = append(out, gatewayToolSearchResult{
			Name:        item.tool.Name,
			Description: item.tool.Description,
		})
	}
	return out
}

func gatewayToolScore(tool mcp.Tool, query string) int {
	if query == "" {
		return 1
	}
	name := strings.ToLower(tool.Name)
	description := strings.ToLower(tool.Description)
	score := 0
	if strings.Contains(name, query) {
		score += 100
	}
	if strings.Contains(description, query) {
		score += 50
	}
	for _, term := range strings.Fields(query) {
		if strings.Contains(name, term) {
			score += 10
		}
		if strings.Contains(description, term) {
			score += 5
		}
	}
	return score
}

func gatewaySearchLimit(raw any) int {
	switch v := raw.(type) {
	case int:
		return clampGatewaySearchLimit(v)
	case int64:
		return clampGatewaySearchLimit(int(v))
	case float64:
		return clampGatewaySearchLimit(int(v))
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return defaultGatewaySearchLimit
		}
		return clampGatewaySearchLimit(int(i))
	default:
		return defaultGatewaySearchLimit
	}
}

func clampGatewaySearchLimit(limit int) int {
	if limit <= 0 {
		return defaultGatewaySearchLimit
	}
	if limit > maxGatewaySearchLimit {
		return maxGatewaySearchLimit
	}
	return limit
}

type gatewayDescribeTool struct {
	serverID string
	manager  BridgeManager
}

func (t *gatewayDescribeTool) Name() string {
	return gatewayToolName(t.serverID, gatewayDescribeSuffix)
}

func (t *gatewayDescribeTool) Description() string {
	return fmt.Sprintf("Describe one upstream MCP tool on server %q, including its input schema.", t.serverID)
}

func (t *gatewayDescribeTool) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name": map[string]any{
				"type":        "string",
				"description": "Original upstream MCP tool name.",
			},
		},
		"required": []any{"tool_name"},
	}
}

func (t *gatewayDescribeTool) NeedsPermission(input map[string]any) (bool, yansutools.PermissionPrompt) {
	return false, yansutools.PermissionPrompt{}
}

func (t *gatewayDescribeTool) DescribeAuthority(input map[string]any) authority.Action {
	return authority.Action{
		Capability: "mcp." + strings.ToLower(sanitizeToolNameSegment(t.serverID)) + ".tools.describe",
		ToolName:   t.Name(),
		Operation:  gatewayDescribeSuffix,
		Effect:     authority.EffectRead,
		Reversible: true,
	}
}

func (t *gatewayDescribeTool) Execute(ctx context.Context, call yansutools.ToolCall) (yansutools.ToolResult, error) {
	_ = ctx
	toolName, _ := call.Input["tool_name"].(string)
	tool, ok, err := findGatewayTool(t.manager, t.serverID, toolName)
	if err != nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway describe %q: %w", t.serverID, err)
	}
	if !ok {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway describe %q: unknown tool %q", t.serverID, toolName)
	}
	out := map[string]any{
		"name":         tool.Name,
		"description":  tool.Description,
		"input_schema": NewToolAdapter(t.serverID, "", tool, t.manager, false).JSONSchema(),
	}
	data, err := json.Marshal(out)
	if err != nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway describe %q: marshal result: %w", t.serverID, err)
	}
	return yansutools.ToolResult{Content: string(data)}, nil
}

type gatewayCallTool struct {
	serverID string
	manager  BridgeManager
	// trusted mirrors McpToolAdapter.trusted: when true, the gateway
	// dispatches without asking. Toggled by the bridge alongside the
	// per-tool adapters whenever the user's trust for serverID changes.
	trusted atomic.Bool
}

// SetTrusted flips the gateway's persistent-trust flag at runtime.
func (t *gatewayCallTool) SetTrusted(trusted bool) {
	t.trusted.Store(trusted)
}

func (t *gatewayCallTool) Name() string {
	return gatewayToolName(t.serverID, gatewayCallSuffix)
}

func (t *gatewayCallTool) Description() string {
	return fmt.Sprintf("Call one upstream MCP tool on server %q by original tool name.", t.serverID)
}

func (t *gatewayCallTool) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name": map[string]any{
				"type":        "string",
				"description": "Original upstream MCP tool name.",
			},
			"args": map[string]any{
				"type":        "object",
				"description": "Arguments passed to the upstream MCP tool.",
			},
		},
		"required": []any{"tool_name", "args"},
	}
}

func (t *gatewayCallTool) NeedsPermission(input map[string]any) (bool, yansutools.PermissionPrompt) {
	toolName, _ := input["tool_name"].(string)
	if t.trusted.Load() {
		return false, yansutools.PermissionPrompt{}
	}
	return true, yansutools.PermissionPrompt{
		Reason: fmt.Sprintf("Run MCP tool %s on server %s?", toolName, t.serverID),
		Prefix: "mcp__" + normalizeForExposure(t.serverID) + "__",
		Options: []yansutools.PermissionOption{
			{ID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{ID: "allow_session", Name: "Allow for this chat", Kind: "allow_session"},
			{ID: "allow_always", Name: "Always allow this server", Kind: "allow_always"},
			{ID: "reject_once", Name: "Reject", Kind: "reject_once"},
		},
	}
}

func (t *gatewayCallTool) DescribeAuthority(input map[string]any) authority.Action {
	toolName, _ := input["tool_name"].(string)
	action := authority.Action{
		Capability:     mcpCapability(t.serverID, toolName),
		ToolName:       t.Name(),
		Operation:      toolName,
		Effect:         authority.EffectUnknown,
		ApprovalPrefix: "mcp__" + normalizeForExposure(t.serverID) + "__",
		ApprovalReason: fmt.Sprintf("Run MCP tool %s on server %s?", toolName, t.serverID),
		Metadata: map[string]string{
			"server_id": t.serverID,
			"tool_name": toolName,
		},
	}
	if t.manager != nil {
		if tool, ok, err := findGatewayTool(t.manager, t.serverID, toolName); err == nil && ok {
			applyMCPAnnotations(&action, tool)
		}
	}
	if t.trusted.Load() {
		action.AttendedGrantID = "mcp-trusted:" + t.serverID
	}
	return action
}

func (t *gatewayCallTool) Execute(ctx context.Context, call yansutools.ToolCall) (yansutools.ToolResult, error) {
	toolName, _ := call.Input["tool_name"].(string)
	args, ok := call.Input["args"].(map[string]any)
	if !ok || args == nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp gateway call %q: args must be an object", t.serverID)
	}
	res, err := t.manager.CallTool(ctx, t.serverID, toolName, args)
	if err != nil {
		return yansutools.ToolResult{}, fmt.Errorf("mcp server %q: tool %q failed: %w", t.serverID, toolName, err)
	}
	if res == nil {
		return yansutools.ToolResult{Content: ""}, nil
	}
	content := FormatGatewayToolResult(toolName, res)
	if res.IsError {
		return yansutools.ToolResult{Content: content}, fmt.Errorf("mcp tool %q reported error", toolName)
	}
	return yansutools.ToolResult{Content: content}, nil
}

func findGatewayTool(manager BridgeManager, serverID, toolName string) (mcp.Tool, bool, error) {
	tools, err := manager.Tools(serverID)
	if err != nil {
		return mcp.Tool{}, false, err
	}
	for _, tool := range tools {
		if tool.Name == toolName {
			return tool, true, nil
		}
	}
	return mcp.Tool{}, false, nil
}
