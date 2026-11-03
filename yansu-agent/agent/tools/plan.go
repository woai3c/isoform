package tools

import (
	"context"
	"fmt"
)

// PlanModeState is an atomic flag tracked per session. The tool uses
// it to reject invocations when the session isn't in plan mode.
type PlanModeState struct {
	active bool
}

// NewPlanModeState constructs a plan-mode tracker. Caller mutates Set.
func NewPlanModeState(active bool) *PlanModeState { return &PlanModeState{active: active} }

// Active reports whether plan mode is currently on.
func (p *PlanModeState) Active() bool { return p.active }

// Set updates the flag.
func (p *PlanModeState) Set(v bool) { p.active = v }

// ExitPlanModeTool is the tool the model calls to signal "I'm done
// planning". The outer loop treats the returned Content as the plan
// text to show the user for approval.
type ExitPlanModeTool struct {
	state *PlanModeState
}

// NewExitPlanModeTool binds the tool to the session's plan state.
func NewExitPlanModeTool(state *PlanModeState) *ExitPlanModeTool {
	return &ExitPlanModeTool{state: state}
}

func (*ExitPlanModeTool) Name() string { return "ExitPlanMode" }

func (*ExitPlanModeTool) Description() string {
	return "Call this when you have finished planning and are ready to present the plan for user approval. " +
		"The `plan` field is a markdown description of what you intend to do."
}

func (*ExitPlanModeTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"plan"},
		"properties": map[string]any{
			"plan": map[string]any{"type": "string", "description": "markdown plan"},
		},
	}
}

func (t *ExitPlanModeTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *ExitPlanModeTool) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	if !t.state.Active() {
		return ToolResult{}, fmt.Errorf("not in plan mode")
	}
	plan, _ := call.Input["plan"].(string)
	if plan == "" {
		return ToolResult{}, fmt.Errorf("plan is required")
	}
	// The outer loop inspects the permission granted to decide whether
	// to flip plan mode off. Return the plan so the UI can render it.
	return ToolResult{
		Content:  plan,
		Metadata: map[string]any{"plan": plan},
	}, nil
}
