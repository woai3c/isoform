package agent

import (
	"context"
	"testing"

	"yansu-agent/agent/tools"
)

type extraTestTool struct{ name string }

func (t extraTestTool) Name() string               { return t.name }
func (t extraTestTool) Description() string        { return "test tool" }
func (t extraTestTool) JSONSchema() map[string]any { return map[string]any{"type": "object"} }
func (t extraTestTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (t extraTestTool) Execute(context.Context, tools.ToolCall) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "extra:" + t.name}, nil
}

func TestAddExtraToolsScopesToOneRunAndNeverReplacesBuiltins(t *testing.T) {
	r := NewNativeAgentRunner(NativeRunnerConfig{})
	plan, todos := tools.NewPlanModeState(false), tools.NewTodoList()

	scoped := r.registryForSession(plan, todos, nil)
	addExtraTools(scoped, []tools.Tool{nil, extraTestTool{name: "UpcomingList"}, extraTestTool{name: "Bash"}})

	if scoped.Get("UpcomingList") == nil {
		t.Fatal("the extra tool is not registered on the run's registry")
	}
	if _, ok := scoped.Get("Bash").(extraTestTool); ok {
		t.Fatal("an extra tool replaced the built-in Bash tool")
	}
	if r.Registry().Get("UpcomingList") != nil {
		t.Fatal("the extra tool leaked into the runner's shared registry")
	}
	if other := r.registryForSession(plan, todos, nil); other.Get("UpcomingList") != nil {
		t.Fatal("the extra tool leaked into another run's registry")
	}
}
