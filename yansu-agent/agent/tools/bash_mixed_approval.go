package tools

import (
	"context"

	"yansu-agent/agent/authority"
)

const blockedMixedBashActions = "blocked_mixed_bash_actions"

const mixedBashActionsReason = "A command cannot combine a process launcher with startup or persistence changes. Run these operations as separate tool calls."

// A single approval flag cannot represent independent launcher and persistence
// consent. Refuse the combination instead of silently choosing one prompt.
func (s *bashScan) mixedApprovalAction(command string) *authority.Action {
	persistence := s.persistenceApprovalAction(command)
	if persistence == nil {
		return nil
	}
	launcher := s.launcherApprovalAction(command)
	if launcher == nil {
		return nil
	}
	return &authority.Action{
		Capability: "process.mixed.persistence", ToolName: "Bash",
		Operation: launcher.Operation + "; " + persistence.Operation,
		Effect:    authority.EffectPrivileged, NoGrant: true,
		ApprovalReason: mixedBashActionsReason,
		Metadata:       map[string]string{blockedMixedBashActions: "true"},
	}
}

func enforceMixedBashActions(ctx context.Context, call ToolCall) error {
	command, _ := call.Input["command"].(string)
	action := analyzeBash(command, call.WorkDir).mixedApprovalAction(command)
	if action == nil {
		return nil
	}
	run := authority.Context{}
	if call.Authority != nil {
		run = call.Authority.Context
	}
	evaluation := authority.NewPolicy(nil).Evaluate(ctx, run, *action)
	ApplyBashApprovalPreference(&evaluation, false)
	return &AuthorityError{Evaluation: evaluation}
}
