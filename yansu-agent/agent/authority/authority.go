// Package authority defines the capability boundary shared by agent tools.
// It intentionally has no dependency on the agent loop or concrete tools so
// every execution surface can describe actions using the same contract.
package authority

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	PolicyVersion       = "capability-authority-v1"
	ShadowPolicyVersion = "shadow-runtime-v1"
)

type ExecutionMode string

const (
	ExecutionModeNormal ExecutionMode = "normal"
	ExecutionModeShadow ExecutionMode = "shadow"
)

type Effect string

const (
	EffectRead          Effect = "read"
	EffectLocalWrite    Effect = "local_write"
	EffectExternalWrite Effect = "external_write"
	EffectPrivileged    Effect = "privileged"
	EffectUnknown       Effect = "unknown"
)

type Decision string

const (
	DecisionAllow           Decision = "allow"
	DecisionRequireApproval Decision = "require_approval"
	DecisionDeny            Decision = "deny"
)

// Action is the normalized description of one attempted tool operation.
// Capability is stable policy input; ToolName and Operation are audit context.
type Action struct {
	Capability      string            `json:"capability"`
	ToolName        string            `json:"tool_name"`
	Operation       string            `json:"operation,omitempty"`
	Effect          Effect            `json:"effect"`
	Resource        string            `json:"resource,omitempty"`
	Recipient       string            `json:"recipient,omitempty"`
	Reversible      bool              `json:"reversible"`
	ApprovalPrefix  string            `json:"approval_prefix,omitempty"`
	ApprovalReason  string            `json:"approval_reason,omitempty"`
	AttendedGrantID string            `json:"attended_grant_id,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`

	// NoGrant marks an action no scoped grant may cover: a shell command, or a
	// command that runs several different effects at once. Only per-run
	// approval allows it.
	NoGrant bool `json:"no_grant,omitempty"`
}

type Context struct {
	RunID      string        `json:"run_id,omitempty"`
	SessionID  string        `json:"session_id,omitempty"`
	Initiative string        `json:"initiative_id,omitempty"`
	ShadowRun  string        `json:"shadow_run_id,omitempty"`
	Mode       ExecutionMode `json:"execution_mode"`
	Unattended bool          `json:"unattended"`
}

// Grant is an explicit caller-provided authority scope. Capability accepts an
// exact value or a trailing ".*" prefix pattern. ResourcePrefix optionally
// narrows the grant further. Grants are never synthesized from confidence,
// memory, or model output.
type Grant struct {
	ID             string    `json:"id"`
	Capability     string    `json:"capability"`
	ResourcePrefix string    `json:"resource_prefix,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

type Evaluation struct {
	PolicyVersion string   `json:"policy_version"`
	ToolUseID     string   `json:"tool_use_id,omitempty"`
	Decision      Decision `json:"decision"`
	Reason        string   `json:"reason"`
	GrantID       string   `json:"grant_id,omitempty"`
	Context       Context  `json:"context"`
	Action        Action   `json:"action"`
}

type Evaluator interface {
	Evaluate(ctx context.Context, run Context, action Action) Evaluation
}

// Policy is the deterministic default evaluator used by the native runtime.
// Confidence and memory are deliberately absent from both its inputs and state.
type Policy struct {
	Grants []Grant
	Now    func() time.Time
}

func NewPolicy(grants []Grant) *Policy {
	return &Policy{Grants: append([]Grant(nil), grants...)}
}

func (p *Policy) Evaluate(_ context.Context, run Context, action Action) Evaluation {
	run = normalizeContext(run)
	evaluation := Evaluation{
		PolicyVersion: PolicyVersion,
		Context:       run,
		Action:        normalizeAction(action),
	}
	action = evaluation.Action

	if run.Mode == ExecutionModeShadow {
		evaluation.PolicyVersion = ShadowPolicyVersion
		if action.Effect == EffectRead {
			evaluation.Decision = DecisionAllow
			evaluation.Reason = "shadow runtime permits read-only action"
		} else {
			evaluation.Decision = DecisionDeny
			evaluation.Reason = "shadow runtime blocks every non-read or unclassified side effect"
		}
		return evaluation
	}
	if run.Mode != ExecutionModeNormal {
		evaluation.Decision = DecisionDeny
		evaluation.Reason = fmt.Sprintf("unrecognized execution mode %q", run.Mode)
		return evaluation
	}

	if action.AttendedGrantID != "" && !run.Unattended {
		evaluation.Decision = DecisionAllow
		evaluation.Reason = "covered by an explicit attended grant"
		evaluation.GrantID = action.AttendedGrantID
		return evaluation
	}

	if grant, ok := p.matchingGrant(action); ok && !action.NoGrant {
		evaluation.Decision = DecisionAllow
		evaluation.Reason = "covered by an explicit scoped authority grant"
		evaluation.GrantID = grant.ID
		return evaluation
	}

	switch action.Effect {
	case EffectRead:
		if run.Unattended {
			evaluation.Decision = DecisionDeny
			evaluation.Reason = "unattended read has no matching scoped authority grant"
		} else {
			evaluation.Decision = DecisionAllow
			evaluation.Reason = "attended read-only action"
		}
	case EffectLocalWrite:
		if run.Unattended {
			evaluation.Decision = DecisionDeny
			evaluation.Reason = "unattended local write has no matching scoped authority grant"
		} else {
			evaluation.Decision = DecisionAllow
			evaluation.Reason = "attended workspace-local action"
		}
	case EffectExternalWrite, EffectPrivileged, EffectUnknown:
		if run.Unattended {
			evaluation.Decision = DecisionDeny
			evaluation.Reason = "unattended execution has no matching scoped authority grant"
		} else {
			evaluation.Decision = DecisionRequireApproval
			evaluation.Reason = "state-changing action requires explicit user approval"
		}
	default:
		evaluation.Decision = DecisionDeny
		evaluation.Reason = fmt.Sprintf("unrecognized action effect %q", action.Effect)
	}
	return evaluation
}

func normalizeContext(run Context) Context {
	run.RunID = strings.TrimSpace(run.RunID)
	run.SessionID = strings.TrimSpace(run.SessionID)
	run.Initiative = strings.TrimSpace(run.Initiative)
	run.ShadowRun = strings.TrimSpace(run.ShadowRun)
	mode := strings.ToLower(strings.TrimSpace(string(run.Mode)))
	if mode == "" {
		mode = string(ExecutionModeNormal)
	}
	run.Mode = ExecutionMode(mode)
	return run
}

func (p *Policy) matchingGrant(action Action) (Grant, bool) {
	now := time.Now()
	if p != nil && p.Now != nil {
		now = p.Now()
	}
	if p == nil {
		return Grant{}, false
	}
	for _, grant := range p.Grants {
		if strings.TrimSpace(grant.ID) == "" || !capabilityMatches(grant.Capability, action.Capability) {
			continue
		}
		if !grant.ExpiresAt.IsZero() && !now.Before(grant.ExpiresAt) {
			continue
		}
		if prefix := strings.TrimSpace(grant.ResourcePrefix); prefix != "" &&
			!resourceMatches(prefix, action.Resource) {
			continue
		}
		return grant, true
	}
	return Grant{}, false
}

func resourceMatches(prefix, resource string) bool {
	prefix = strings.TrimSpace(prefix)
	resource = strings.TrimSpace(resource)
	if prefix == "" {
		return true
	}
	if resource == "" {
		return false
	}
	if filepath.IsAbs(prefix) && filepath.IsAbs(resource) {
		relative, err := filepath.Rel(filepath.Clean(prefix), filepath.Clean(resource))
		if err != nil {
			return false
		}
		return relative == "." || relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	return strings.HasPrefix(resource, prefix)
}

func capabilityMatches(pattern, capability string) bool {
	pattern = strings.TrimSpace(pattern)
	capability = strings.TrimSpace(capability)
	if pattern == "" || capability == "" {
		return false
	}
	if pattern == capability {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(capability, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func normalizeAction(action Action) Action {
	action.Capability = strings.TrimSpace(action.Capability)
	action.ToolName = strings.TrimSpace(action.ToolName)
	action.Operation = strings.TrimSpace(action.Operation)
	action.Resource = strings.TrimSpace(action.Resource)
	action.Recipient = strings.TrimSpace(action.Recipient)
	action.ApprovalPrefix = strings.TrimSpace(action.ApprovalPrefix)
	action.ApprovalReason = strings.TrimSpace(action.ApprovalReason)
	action.AttendedGrantID = strings.TrimSpace(action.AttendedGrantID)
	if action.Capability == "" {
		action.Capability = "tool.unknown"
	}
	if action.Effect == "" {
		action.Effect = EffectUnknown
	}
	return action
}

// Describe says in plain words what an action does, for refusals the user
// reads: "run commands on this Mac", "change files".
func Describe(action Action) string {
	capability := strings.TrimSpace(action.Capability)
	switch {
	case capability == "local.shell.execute" || capability == "local.shell.unknown":
		return "run commands on this computer"
	case capability == "tool.read":
		return "read files"
	case capability == "tool.glob" || capability == "tool.grep":
		return "search files"
	case capability == "tool.write" || capability == "tool.edit" || capability == "tool.notebookedit":
		return "change files"
	case capability == "tool.webfetch":
		return "open web pages"
	case capability == "tool.websearch":
		return "search the web"
	case capability == "tool.todowrite":
		return "keep a task list"
	case capability == "tool.programmatic":
		return "run code that calls tools"
	case strings.HasPrefix(capability, "mcp."):
		return "use connected MCP tools"
	case strings.HasPrefix(capability, "computer."):
		return "use apps on the screen"
	case capability == "connector.command.execute":
		return "use opencli"
	case strings.HasPrefix(capability, "github."):
		return "change GitHub"
	case capability == "network.request.write" || capability == "network.socket.open":
		return "send data over the network"
	case capability == "remote.shell.execute" || capability == "remote.filesystem.write":
		return "reach other computers"
	case capability == "mail.send":
		return "send email"
	case capability == "schedule.write" || capability == "os.schedule.write":
		return "change schedules"
	case strings.HasPrefix(capability, "package."):
		return "install, run or publish packages"
	case capability == "process.interpreter.inline":
		return "run code it writes itself"
	case strings.HasPrefix(capability, "extension."):
		return "change skills or MCP servers"
	case capability == "infrastructure.mutate":
		return "change cloud services"
	default:
	}
	name := strings.TrimSpace(action.ToolName)
	if name == "" {
		name = capability
	}
	return "use " + name
}
