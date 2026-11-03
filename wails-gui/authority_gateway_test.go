package main

import (
	"context"
	"testing"

	"yansu-agent/agent/authority"
)

func TestAuthorityDecisionLedgerPreservesEvaluation(t *testing.T) {
	store := newTestStore(t)
	evaluation := authority.NewPolicy([]authority.Grant{{
		ID:             "grant-1",
		Capability:     "connector.slack.message.reply",
		ResourcePrefix: "slack:thread:123",
	}}).Evaluate(context.Background(), authority.Context{
		RunID:      "run-authority",
		SessionID:  "session-authority",
		Initiative: "initiative-authority",
		Unattended: true,
	}, authority.Action{
		Capability: "connector.slack.message.reply",
		ToolName:   "slack_connector",
		Operation:  "reply",
		Effect:     authority.EffectExternalWrite,
		Resource:   "slack:thread:123",
	})
	evaluation.ToolUseID = "toolu-authority"

	if err := store.SaveAuthorityDecision(evaluation); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAuthorityDecisions("initiative-authority", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("decisions=%d, want 1", len(items))
	}
	got := items[0].Evaluation
	if got.Decision != authority.DecisionAllow ||
		got.GrantID != "grant-1" ||
		got.ToolUseID != "toolu-authority" ||
		got.Context.RunID != "run-authority" {
		t.Fatalf("evaluation = %+v", got)
	}
}

func TestInboundConnectorReplyUsesExactScopedGrant(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	if err := app.authorizeInboundConnectorReply(
		context.Background(),
		"slack",
		"slack:bot:channel:user:thread",
		"slack:bot:channel:user:thread",
	); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAuthorityDecisions("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("decisions=%d, want 1", len(items))
	}
	got := items[0].Evaluation
	if got.Decision != authority.DecisionAllow ||
		got.Action.Capability != "connector.slack.message.reply" ||
		got.Action.Resource != "slack:bot:channel:user:thread" ||
		got.GrantID == "" {
		t.Fatalf("evaluation = %+v", got)
	}
}

// The reply gate is a real check: a reply aimed at any other thread is refused,
// including a longer thread name that the grant's prefix would otherwise cover.
func TestInboundConnectorReplyRefusesAnotherDestination(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	inbound := "slack:bot:channel:user:thread"
	for _, destination := range []string{
		"slack:bot:channel:user:thread-other",
		"slack:bot:channel:user",
		"slack:bot:other-channel:user:thread",
		"",
	} {
		if err := app.authorizeInboundConnectorReply(context.Background(), "slack", inbound, destination); err == nil {
			t.Errorf("reply to %q was allowed for the thread %q", destination, inbound)
		}
	}
	if err := app.authorizeInboundConnectorReply(context.Background(), "slack", "", ""); err == nil {
		t.Error("a reply with no inbound thread was allowed")
	}
	items, err := store.ListAuthorityDecisions("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("decisions=%d, want one recorded refusal per refused reply", len(items))
	}
	for _, item := range items {
		if item.Evaluation.Decision != authority.DecisionDeny {
			t.Fatalf("recorded decision = %s, want deny", item.Evaluation.Decision)
		}
	}
}

func TestAuthorityDecisionLedgerLinksShadowRuntime(t *testing.T) {
	store := newTestStore(t)
	evaluation := authority.NewPolicy(nil).Evaluate(
		context.Background(),
		authority.Context{
			RunID:      "agent-run-shadow",
			ShadowRun:  "shadow-run-authority",
			Mode:       authority.ExecutionModeShadow,
			Unattended: true,
		},
		authority.Action{
			Capability: "filesystem.write",
			ToolName:   "Write",
			Effect:     authority.EffectLocalWrite,
		},
	)
	if err := store.SaveAuthorityDecision(evaluation); err != nil {
		t.Fatal(err)
	}
	var shadowRunID, executionMode string
	if err := store.db.QueryRow(`
		SELECT shadow_run_id, execution_mode
		FROM authority_decisions
		WHERE run_id = ?`,
		evaluation.Context.RunID,
	).Scan(&shadowRunID, &executionMode); err != nil {
		t.Fatal(err)
	}
	if shadowRunID != "shadow-run-authority" ||
		executionMode != string(authority.ExecutionModeShadow) {
		t.Fatalf("shadow_run_id=%q execution_mode=%q", shadowRunID, executionMode)
	}
}
