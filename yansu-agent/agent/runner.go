package agent

import "context"

// AgentRunner abstracts task execution. The GUI programs against this
// interface so the backing implementation can be swapped without touching
// call sites.
type AgentRunner interface {
	// Init initializes the runner. Called once at startup.
	Init(ctx context.Context) error

	// ExecuteTask starts a streaming task execution.
	// Emits run_created, streaming events, done, and close events via callback.
	// Returns the run ID assigned to this execution.
	ExecuteTask(ctx context.Context, input *TaskInput, callback StreamCallback) (runID string, err error)

	// CancelRun cancels an in-progress run by its ID.
	CancelRun(runID string) error

	// RespondToPermission forwards the user's choice for an interactive
	// permission request (ExitPlanMode / AskUserQuestion).
	RespondToPermission(sessionID, optionID string) error

	// CancelPermission cancels a pending permission request without
	// cancelling the entire run.
	CancelPermission(sessionID string)

	// Shutdown releases resources. Called on application exit.
	Shutdown() error

	// IsReady reports whether the runner can accept tasks.
	IsReady() bool
}

// ExtraToolsRunner is the opt-in capability for runners that carry a task's
// tools to their model and execute them through Isoform's native authority gate.
// The router keeps a tool-bearing turn native when this capability is absent.
type ExtraToolsRunner interface {
	SupportsExtraTools(*TaskInput) bool
}

func taskHasExtraTools(input *TaskInput) bool {
	if input == nil || input.NoTools {
		return false
	}
	for _, tool := range input.ExtraTools {
		if tool == nil || allowsTool(input.DisabledTools, tool.Name()) {
			continue
		}
		if len(input.AllowedTools) == 0 || allowsTool(input.AllowedTools, tool.Name()) {
			return true
		}
	}
	return false
}

// Chat tool injection does not extend the external route to screen-level or
// authority-scoped turns. Their caller-owned tool instances stay native.
func extraToolsRequireNativeScope(input *TaskInput) bool {
	return taskHasExtraTools(input) && (input.BackendHint == BackendHintComputerUse ||
		allowsTool(input.AllowedTools, "cua_driver") || input.AuthorityInitiativeID != "" ||
		input.AuthorityShadowRunID != "" || len(input.AuthorityGrants) > 0 || input.AuthorityEvaluator != nil)
}
