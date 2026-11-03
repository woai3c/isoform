// Package gate adapts the native execution boundary for assistant tool calls.
// Native and external callers share DispatchTool; this package deliberately
// contains no copy of the native safety policy.
package gate

import (
	"context"
	"fmt"
	"strings"

	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
)

const ToolPrefix = "mcp__isoform__"

// Gate holds one run's registry, grants, approval channel and execution policy.
type Gate struct {
	Config *native.LoopConfig
}

// NativeName strips only our transport namespace. User MCP tools keep their
// own namespace so they continue using their registered authority descriptor.
func NativeName(name string) string { return strings.TrimPrefix(name, ToolPrefix) }

func (g *Gate) Tools() []tools.Tool {
	if g == nil || g.Config == nil || g.Config.NoTools {
		return nil
	}
	disabled := map[string]bool{}
	for _, name := range g.Config.DisabledTools {
		disabled[name] = true
	}
	var result []tools.Tool
	for _, tool := range g.Config.Registry.List(g.Config.AllowedTools) {
		planAllowed := true
		if g.Config.PlanMode != nil && g.Config.PlanMode.Active() {
			planAllowed = false
			switch tool.Name() {
			case "Read", "Grep", "Glob", "TodoWrite", "ExitPlanMode", "WebFetch", "WebSearch":
				planAllowed = true
			}
		}
		if !disabled[tool.Name()] && planAllowed {
			result = append(result, tool)
		}
	}
	return result
}

func (g *Gate) Execute(ctx context.Context, name, id string, input map[string]any, before func(string, tools.ToolCall) error) (tools.ToolResult, error) {
	name = NativeName(name)
	allowed := false
	for _, tool := range g.Tools() {
		if tool.Name() == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ToolResult{IsError: true, Content: fmt.Sprintf("tool %q is not allowed for this run", name)}, nil
	}
	cfg := *g.Config
	cfg.BeforeToolExecution = before
	emit := cfg.Emit
	var resultEvent *native.LoopEvent
	cfg.Emit = func(event native.LoopEvent) {
		if event.Type == "tool_result" {
			resultEvent = &event
			return
		}
		if emit != nil {
			emit(event)
		}
	}
	result, err := native.DispatchTool(ctx, &cfg, name, id, input)
	if resultEvent != nil && resultEvent.Failed {
		result.IsError = true
	}
	// Source readers publish their event only after the per-run transport has
	// rechecked and constructed its response under the source fence.
	_, sourceRead := cfg.Registry.Get(name).(tools.SourceReadTool)
	if emit != nil && !sourceRead {
		if resultEvent == nil {
			resultEvent = &native.LoopEvent{Type: "tool_result", ToolName: name, ToolUseID: id, Content: result.Content}
		}
		resultEvent.Failed = resultEvent.Failed || result.IsError || err != nil
		if resultEvent.Failed {
			resultEvent.ToolSucceeded = false
		}
		emit(*resultEvent)
	}
	return result, err
}
