package tools

import (
	"context"
	"fmt"
	"strings"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools/guard"
)

// AuthorityDescriber lets a tool map its concrete input onto a stable
// capability and effect before execution.
type AuthorityDescriber interface {
	DescribeAuthority(input map[string]any) authority.Action
}

// workDirAuthorityDescriber is an AuthorityDescriber whose classification
// depends on the session work folder (Bash: writes outside it need approval).
type workDirAuthorityDescriber interface {
	DescribeAuthorityIn(input map[string]any, workDir string) authority.Action
}

// AuthorityRuntime is attached to ToolCall so direct and nested dispatches use
// the same evaluator. Approved applies only to this call; the attended Bash
// preference is carried separately so it can apply to nested Bash calls.
type AuthorityRuntime struct {
	Context    authority.Context
	Evaluator  authority.Evaluator
	Evaluation *authority.Evaluation
	Approved   bool
	Recorded   bool
	Record     func(authority.Evaluation)
	// AllowBashWithoutApproval is the attended "Ask before running risky
	// commands" OFF preference. It never overrides denial or unattended mode.
	AllowBashWithoutApproval bool
	// DeniedBashPrefixes hard-blocks Bash commands whose resolved command keys
	// match a denied prefix (unattended runs). Enforced here, in the shared
	// registry dispatch path, so nested Programmatic calls cannot skip the gate
	// the native loop applies to direct calls.
	DeniedBashPrefixes []string
}

type AuthorityError struct {
	Evaluation authority.Evaluation
}

func (e *AuthorityError) Error() string {
	return fmt.Sprintf(
		"authority %s: %s (%s)",
		e.Evaluation.Decision,
		e.Evaluation.Reason,
		e.Evaluation.Action.Capability,
	)
}

func DescribeAuthority(t Tool, input map[string]any, workDirs ...string) authority.Action {
	workDir := ""
	if len(workDirs) > 0 {
		workDir = strings.TrimSpace(workDirs[0])
	}
	if describer, ok := t.(workDirAuthorityDescriber); ok {
		action := describer.DescribeAuthorityIn(input, workDir)
		if action.ToolName == "" {
			action.ToolName = t.Name()
		}
		applyFilesystemAuthority(&action, t.Name(), input, workDir)
		return action
	}
	if describer, ok := t.(AuthorityDescriber); ok {
		action := describer.DescribeAuthority(input)
		if action.ToolName == "" {
			action.ToolName = t.Name()
		}
		applyFilesystemAuthority(&action, t.Name(), input, workDir)
		return action
	}

	name := t.Name()
	action := authority.Action{
		Capability: "tool." + strings.ToLower(name),
		ToolName:   name,
		Operation:  name,
		Effect:     authority.EffectUnknown,
	}
	switch name {
	case "Read", "Glob", "Grep", "WebFetch", "WebSearch", "BashOutput":
		action.Effect = authority.EffectRead
		action.Reversible = true
	case "Write", "Edit", "NotebookEdit", "TodoWrite", "KillShell", "ExitPlanMode", "Programmatic":
		action.Effect = authority.EffectLocalWrite
		action.Reversible = true
	default:
		if strings.HasPrefix(name, "mcp__") {
			action.Capability = "mcp.unknown.call"
			action.Effect = authority.EffectUnknown
			action.ApprovalPrefix = mcpApprovalPrefix(name)
			action.ApprovalReason = fmt.Sprintf("Run external MCP operation %s?", name)
		}
	}
	applyFilesystemAuthority(&action, name, input, workDir)
	return action
}

func applyFilesystemAuthority(
	action *authority.Action,
	name string,
	input map[string]any,
	workDir string,
) {
	if action == nil {
		return
	}
	capabilities := map[string]string{
		"Read":         "tool.read",
		"Glob":         "tool.glob",
		"Grep":         "tool.grep",
		"Write":        "tool.write",
		"Edit":         "tool.edit",
		"NotebookEdit": "tool.notebookedit",
	}
	capability, ok := capabilities[name]
	if !ok {
		return
	}
	action.Capability = capability
	action.Operation = strings.ToLower(name)
	action.Reversible = true
	if name == "Write" || name == "Edit" || name == "NotebookEdit" {
		action.Effect = authority.EffectLocalWrite
	} else {
		action.Effect = authority.EffectRead
	}
	rawPath, _ := input["path"].(string)
	if strings.TrimSpace(rawPath) == "" && (name == "Glob" || name == "Grep") {
		rawPath = workDir
	}
	canonical, err := guard.CanonicalPath(rawPath)
	if err != nil {
		action.Resource = ""
		if action.Metadata == nil {
			action.Metadata = map[string]string{}
		}
		action.Metadata["resource_error"] = err.Error()
		return
	}
	action.Resource = canonical
	if persistence := persistenceFileAction(name, input, workDir); persistence != nil {
		*action = *persistence
	}
}

// DeniedBashError is returned when a Bash command matches the unattended deny
// list. It is distinct from AuthorityError so callers can tell a hard
// externally-visible-write block from an ordinary policy denial.
type DeniedBashError struct {
	Command string
	Prefix  string
}

func (e *DeniedBashError) Error() string {
	return fmt.Sprintf(
		"Blocked: %q matches the denied command prefix %q. This unattended "+
			"session cannot perform externally-visible write operations; the "+
			"user must run this themselves.", e.Command, e.Prefix)
}

func enforceAuthority(ctx context.Context, tool Tool, call *ToolCall) error {
	if call == nil {
		return nil
	}
	// The app data folder is off limits on every dispatch path, with or
	// without an authority runtime, and no grant or approval lifts it.
	if err := CheckProtectedPaths(tool.Name(), call.Input, call.WorkDir); err != nil {
		return err
	}
	if call.Authority == nil {
		if err := enforcePersistenceApproval(ctx, tool.Name(), *call); err != nil {
			return err
		}
		if tool.Name() == "Bash" {
			return enforceBashLauncherApproval(ctx, *call)
		}
		return nil
	}
	// Unattended-session deny gate. Checked before the authority evaluator and
	// on every dispatch path (direct and nested Programmatic), so neither a
	// scoped grant nor the programmatic helper can bypass it.
	if len(call.Authority.DeniedBashPrefixes) > 0 && tool.Name() == "Bash" {
		if cmd, ok := call.Input["command"].(string); ok {
			if denied := DeniedBashCommandMatch(cmd, call.Authority.DeniedBashPrefixes); denied != "" {
				return &DeniedBashError{Command: cmd, Prefix: denied}
			}
		}
	}
	if call.Authority.Evaluator == nil {
		if err := enforcePersistenceApproval(ctx, tool.Name(), *call); err != nil {
			return err
		}
		if tool.Name() == "Bash" {
			return enforceBashLauncherApproval(ctx, *call)
		}
		return nil
	}
	runtime := call.Authority
	previous := runtime.Evaluation
	value := runtime.Evaluator.Evaluate(
		ctx,
		runtime.Context,
		DescribeAuthority(tool, call.Input, call.WorkDir),
	)
	ApplyBashApprovalPreference(&value, runtime.AllowBashWithoutApproval)
	evaluation := &value
	runtime.Evaluation = evaluation
	if evaluation.ToolUseID == "" {
		evaluation.ToolUseID = call.ID
	}
	changed := previous == nil ||
		previous.Decision != evaluation.Decision ||
		previous.GrantID != evaluation.GrantID ||
		previous.Reason != evaluation.Reason ||
		previous.PolicyVersion != evaluation.PolicyVersion
	if (!runtime.Recorded || changed) && runtime.Record != nil {
		runtime.Record(*evaluation)
		runtime.Recorded = true
	}

	switch evaluation.Decision {
	case authority.DecisionAllow:
		return nil
	case authority.DecisionRequireApproval:
		if runtime.Approved {
			return nil
		}
		return &AuthorityError{Evaluation: *evaluation}
	case authority.DecisionDeny:
		return &AuthorityError{Evaluation: *evaluation}
	default:
		return &AuthorityError{Evaluation: *evaluation}
	}
}

// ApplyBashApprovalPreference applies the same attended setting in the native
// loop and registry dispatch, including Programmatic's nested Bash helper.
// Hard blocks run before this decision and denial is never downgraded.
func ApplyBashApprovalPreference(evaluation *authority.Evaluation, allowWithoutApproval bool) {
	if evaluation.Action.Metadata[blockedMixedBashActions] == "true" {
		evaluation.Decision = authority.DecisionDeny
		evaluation.Reason = mixedBashActionsReason
		return
	}
	if evaluation.Action.Metadata[mandatoryPersistenceApproval] == "true" {
		if evaluation.Context.Unattended || evaluation.Context.Mode == authority.ExecutionModeShadow {
			evaluation.Decision = authority.DecisionDeny
			evaluation.Reason = "unattended runs cannot change startup or persistence configuration"
		} else if evaluation.Decision != authority.DecisionDeny {
			evaluation.Decision = authority.DecisionRequireApproval
			evaluation.Reason = "changing startup or persistence configuration requires explicit approval"
		}
		return
	}
	if evaluation.Action.ToolName == "Bash" && evaluation.Action.Metadata[mandatoryLauncherApproval] == "true" {
		if evaluation.Context.Unattended || evaluation.Context.Mode == authority.ExecutionModeShadow {
			evaluation.Decision = authority.DecisionDeny
			evaluation.Reason = "unattended runs cannot start a process outside the shell sandbox"
		} else if evaluation.Decision != authority.DecisionDeny {
			evaluation.Decision = authority.DecisionRequireApproval
			evaluation.Reason = "launching outside the shell sandbox always requires explicit approval"
		}
		return
	}
	if allowWithoutApproval && !evaluation.Context.Unattended && evaluation.Context.Mode != authority.ExecutionModeShadow &&
		evaluation.Action.ToolName == "Bash" && evaluation.Decision == authority.DecisionRequireApproval {
		evaluation.Decision = authority.DecisionAllow
		evaluation.Reason = "attended Bash runs without asking (risky-command prompt is off)"
	}
}

func mcpApprovalPrefix(name string) string {
	parts := strings.Split(name, "__")
	if len(parts) < 3 {
		return name
	}
	return strings.Join(parts[:2], "__") + "__"
}
