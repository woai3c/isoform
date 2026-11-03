package native

import (
	"context"
	"testing"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

type changingAuthorityEvaluator struct {
	calls int
}

func (e *changingAuthorityEvaluator) Evaluate(
	_ context.Context,
	run authority.Context,
	action authority.Action,
) authority.Evaluation {
	e.calls++
	decision := authority.DecisionAllow
	reason := "active envelope"
	grantID := "envelope-1"
	if e.calls > 1 {
		decision = authority.DecisionDeny
		reason = "operating envelope was revoked"
		grantID = ""
	}
	return authority.Evaluation{
		PolicyVersion: "dynamic-test-v1",
		Decision:      decision,
		Reason:        reason,
		GrantID:       grantID,
		Context:       run,
		Action:        action,
	}
}

type authorityTestTool struct {
	action   authority.Action
	executes int
}

func (t *authorityTestTool) Name() string        { return "authority_test" }
func (t *authorityTestTool) Description() string { return "authority test tool" }
func (t *authorityTestTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *authorityTestTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (t *authorityTestTool) DescribeAuthority(map[string]any) authority.Action {
	action := t.action
	action.ToolName = t.Name()
	return action
}
func (t *authorityTestTool) Execute(context.Context, tools.ToolCall) (tools.ToolResult, error) {
	t.executes++
	return tools.ToolResult{Content: "executed"}, nil
}

func TestAuthorityAutoApproveCannotAuthorizeExternalWrite(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "connector.message.send",
		Effect:     authority.EffectExternalWrite,
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	var events []LoopEvent
	cfg := &LoopConfig{
		Registry:           registry,
		Emit:               func(event LoopEvent) { events = append(events, event) },
		AutoApproveTools:   true,
		AuthorityEvaluator: authority.NewPolicy(nil),
	}

	result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_external",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil {
		t.Fatalf("executeToolUse error: %v", err)
	}
	if outcome != toolOutcomePermissionDenied || !result.IsError {
		t.Fatalf("outcome=%q result=%+v, want permission denial", outcome, result)
	}
	if tool.executes != 0 {
		t.Fatalf("tool executed %d times, want 0", tool.executes)
	}
	if len(events) < 2 || events[0].Type != "authority_decision" || events[1].Type != "tool_result" {
		t.Fatalf("events = %+v, want authority decision before blocked result", events)
	}
}

func TestAuthorityScopedGrantAllowsUnattendedExternalWrite(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "connector.message.send",
		Effect:     authority.EffectExternalWrite,
		Resource:   "slack:channel:C123",
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	cfg := &LoopConfig{
		Registry:         registry,
		Emit:             func(LoopEvent) {},
		AutoApproveTools: true,
		AuthorityEvaluator: authority.NewPolicy([]authority.Grant{{
			ID:             "grant-slack-c123",
			Capability:     "connector.message.*",
			ResourcePrefix: "slack:channel:C123",
		}}),
	}

	result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_granted",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil {
		t.Fatalf("executeToolUse error: %v", err)
	}
	if outcome != toolOutcomeSuccess || result.IsError {
		t.Fatalf("outcome=%q result=%+v, want success", outcome, result)
	}
	if tool.executes != 1 {
		t.Fatalf("tool executed %d times, want 1", tool.executes)
	}
}

func TestAuthorityReadRequiresScopedGrantWhenUnattended(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "computer.window.read",
		Effect:     authority.EffectRead,
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	cfg := &LoopConfig{
		Registry:           registry,
		Emit:               func(LoopEvent) {},
		AutoApproveTools:   true,
		AuthorityEvaluator: authority.NewPolicy(nil),
	}

	result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_read",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil || outcome != toolOutcomePermissionDenied ||
		!result.IsError || tool.executes != 0 {
		t.Fatalf("err=%v outcome=%q result=%+v executes=%d, want denial", err, outcome, result, tool.executes)
	}
}

func TestAuthorityRechecksDynamicEvaluatorImmediatelyBeforeDispatch(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "tool.write",
		Effect:     authority.EffectLocalWrite,
		Resource:   "/tmp/workspace/draft.txt",
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	evaluator := &changingAuthorityEvaluator{}
	evaluations := make([]authority.Evaluation, 0, 2)
	cfg := &LoopConfig{
		Registry:           registry,
		Emit:               func(LoopEvent) {},
		AutoApproveTools:   true,
		AuthorityEvaluator: evaluator,
		AuthorityRecorder: func(evaluation authority.Evaluation) {
			evaluations = append(evaluations, evaluation)
		},
	}

	result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_revoked",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil {
		t.Fatalf("executeToolUse error: %v", err)
	}
	if outcome != toolOutcomeRegistryError || !result.IsError || tool.executes != 0 {
		t.Fatalf("outcome=%q result=%+v executes=%d, want final-dispatch denial", outcome, result, tool.executes)
	}
	if evaluator.calls != 2 {
		t.Fatalf("evaluator calls = %d, want initial and final checks", evaluator.calls)
	}
	if len(evaluations) != 2 ||
		evaluations[0].Decision != authority.DecisionAllow ||
		evaluations[1].Decision != authority.DecisionDeny {
		t.Fatalf("evaluations = %+v, want allow then revoked denial", evaluations)
	}
}

func TestShadowRuntimeBlocksLocalWriteEvenWithGrant(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "filesystem.write",
		Effect:     authority.EffectLocalWrite,
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	var evaluations []authority.Evaluation
	cfg := &LoopConfig{
		Registry:         registry,
		Emit:             func(LoopEvent) {},
		AutoApproveTools: true,
		AuthorityContext: authority.Context{
			Mode:      authority.ExecutionModeShadow,
			ShadowRun: "shadow-run-1",
		},
		AuthorityEvaluator: authority.NewPolicy([]authority.Grant{{
			ID:         "grant-filesystem",
			Capability: "filesystem.*",
		}}),
		AuthorityRecorder: func(evaluation authority.Evaluation) {
			evaluations = append(evaluations, evaluation)
		},
	}

	result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_shadow_write",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil {
		t.Fatalf("executeToolUse error: %v", err)
	}
	if outcome != toolOutcomePermissionDenied || !result.IsError || tool.executes != 0 {
		t.Fatalf("outcome=%q result=%+v executes=%d, want blocked", outcome, result, tool.executes)
	}
	if len(evaluations) != 1 {
		t.Fatalf("evaluations = %d, want 1", len(evaluations))
	}
	got := evaluations[0]
	if got.PolicyVersion != authority.ShadowPolicyVersion ||
		got.Context.ShadowRun != "shadow-run-1" ||
		got.Context.Mode != authority.ExecutionModeShadow ||
		got.GrantID != "" {
		t.Fatalf("evaluation = %+v, want ungranted shadow denial", got)
	}
}

func TestShadowRuntimeReadsStillExecute(t *testing.T) {
	tool := &authorityTestTool{action: authority.Action{
		Capability: "filesystem.read",
		Effect:     authority.EffectRead,
	}}
	registry := tools.NewRegistry()
	registry.Register(tool)
	cfg := &LoopConfig{
		Registry:           registry,
		Emit:               func(LoopEvent) {},
		AutoApproveTools:   true,
		AuthorityContext:   authority.Context{Mode: authority.ExecutionModeShadow},
		AuthorityEvaluator: authority.NewPolicy(nil),
	}

	_, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_shadow_read",
		Name:  tool.Name(),
		Input: `{}`,
	})
	if err != nil || outcome != toolOutcomeSuccess || tool.executes != 1 {
		t.Fatalf("err=%v outcome=%q executes=%d, want read success", err, outcome, tool.executes)
	}
}

// TestToolResultReportsSuccessOnlyWhenTheToolRan lets a caller tell a write
// that happened from one the policy denied: only the tool_result of a tool
// that ran and returned without an error carries ToolSucceeded.
func TestToolResultReportsSuccessOnlyWhenTheToolRan(t *testing.T) {
	run := func(grants []authority.Grant) (LoopEvent, *authorityTestTool) {
		t.Helper()
		tool := &authorityTestTool{action: authority.Action{
			Capability: "tool.write",
			Effect:     authority.EffectLocalWrite,
			Resource:   "/work/report.md",
		}}
		registry := tools.NewRegistry()
		registry.Register(tool)
		var results []LoopEvent
		cfg := &LoopConfig{
			Registry: registry,
			Emit: func(event LoopEvent) {
				if event.Type == "tool_result" {
					results = append(results, event)
				}
			},
			AutoApproveTools:   true,
			AuthorityEvaluator: authority.NewPolicy(grants),
		}
		if _, _, err := executeToolUse(context.Background(), cfg, toolUseReq{ID: "toolu_w", Name: tool.Name(), Input: `{}`}); err != nil {
			t.Fatalf("executeToolUse error: %v", err)
		}
		if len(results) != 1 || results[0].ToolUseID != "toolu_w" {
			t.Fatalf("tool_result events = %+v, want one for toolu_w", results)
		}
		return results[0], tool
	}
	denied, tool := run(nil)
	if denied.ToolSucceeded || tool.executes != 0 {
		t.Fatalf("denied write: event %+v, executes %d; want no success and no run", denied, tool.executes)
	}
	allowed, tool := run([]authority.Grant{{ID: "g", Capability: "tool.write", ResourcePrefix: "/work/"}})
	if !allowed.ToolSucceeded || tool.executes != 1 {
		t.Fatalf("granted write: event %+v, executes %d; want success", allowed, tool.executes)
	}
}
