package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/gate"
	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
	"yansu-agent/agent/toolserver"
)

// ToolServerSupports checks the actual registry before routing. A server run
// must not silently lose a requested tool, including per-task desktop tools.
func (r *NativeAgentRunner) ToolServerSupports(input *TaskInput) bool {
	if input == nil {
		return false
	}
	if input.NoTools {
		return true
	}
	registry := r.registryForSession(tools.NewPlanModeState(input.PlanMode), tools.NewTodoList(), input.AllowedTools)
	addExtraTools(registry, input.ExtraTools)
	for _, name := range input.AllowedTools {
		if registry.Get(name) == nil {
			return false
		}
	}
	for _, tool := range registry.List(input.AllowedTools) {
		if !toolserver.ValidToolName(tool.Name()) {
			return false
		}
		if _, err := json.Marshal(tool.JSONSchema()); err != nil {
			return false
		}
	}
	return true
}

// NewToolServer keeps the run evaluator, grants, tool implementations and
// permission manager in process. The CLI receives only a relay ticket.
func (r *NativeAgentRunner) NewToolServer(ctx context.Context, input *TaskInput, runID string, callback StreamCallback) (*toolserver.Server, error) {
	if input == nil {
		return nil, fmt.Errorf("tool server requires a task")
	}
	if !r.ToolServerSupports(input) {
		return nil, fmt.Errorf("task needs a tool unavailable on the assistant server")
	}
	plan := tools.NewPlanModeState(input.PlanMode)
	registry := r.registryForSession(plan, tools.NewTodoList(), input.AllowedTools)
	addExtraTools(registry, input.ExtraTools)
	// Restrict the registry itself, not only the advertised MCP tool list.
	// Programmatic's nested helpers are constructed from this same scoped
	// registry below so a helper cannot call a disabled tool.
	scoped := tools.NewRegistry()
	disabled := map[string]bool{}
	for _, name := range input.DisabledTools {
		disabled[name] = true
	}
	for _, tool := range registry.List(input.AllowedTools) {
		if !disabled[tool.Name()] && tool.Name() != "Programmatic" {
			scoped.Register(tool)
		}
	}
	if !disabled["Programmatic"] && shouldRegisterProgrammaticTool(input.AllowedTools) {
		scoped.Register(tools.NewProgrammaticTool(scoped, allowedProgrammaticHelpers(input.AllowedTools)))
	}
	evaluator := input.AuthorityEvaluator
	if evaluator == nil {
		evaluator = authority.NewPolicy(input.AuthorityGrants)
	}
	r.mu.Lock()
	onPersistentAllow := r.onPersistentAllow
	r.mu.Unlock()
	sessionID := input.SessionID
	if sessionID == "" {
		sessionID = runID
	}
	cfg := &native.LoopConfig{
		Registry: scoped, Permission: r.perm, SessionTrust: r.sessionTrust, OnPersistentAllow: onPersistentAllow,
		AutoApproveTools: input.AutoApproveTools, AskBeforeRiskyCommands: input.AskBeforeRiskyCommands,
		AuthorityEvaluator: evaluator, AuthorityContext: authority.Context{Initiative: input.AuthorityInitiativeID, ShadowRun: input.AuthorityShadowRunID, Mode: input.ExecutionMode, Unattended: input.AutoApproveTools}, AuthorityRecorder: r.authorityRecorder,
		RunID: runID, SessionID: sessionID, WorkDir: input.WorkDir, PlanMode: plan,
		AllowedTools: input.AllowedTools, DisabledTools: input.DisabledTools, NoTools: input.NoTools, DeniedBashPrefixes: deniedBashPrefixes(input),
	}
	cfg.Emit = func(event native.LoopEvent) {
		if callback == nil {
			return
		}
		if converted := r.loopEventToStream(event, runID, sessionID); converted != nil {
			_ = callback(converted)
		}
	}
	return toolserver.New(ctx, toolserver.Config{Gate: &gate.Gate{Config: cfg}, OnActionStarted: func(count int) error {
		if callback == nil {
			return nil
		}
		return callback(&StreamEvent{Type: "action_started", RunID: runID, SessionID: sessionID, ActionsStarted: count})
	}})
}
