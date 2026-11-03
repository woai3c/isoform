package authority

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicySeparatesEffectFromAuthority(t *testing.T) {
	policy := NewPolicy(nil)

	tests := []struct {
		name       string
		unattended bool
		effect     Effect
		want       Decision
	}{
		{name: "read attended", effect: EffectRead, want: DecisionAllow},
		{name: "read unattended", unattended: true, effect: EffectRead, want: DecisionDeny},
		{name: "local write attended", effect: EffectLocalWrite, want: DecisionAllow},
		{name: "local write unattended", unattended: true, effect: EffectLocalWrite, want: DecisionDeny},
		{name: "external attended", effect: EffectExternalWrite, want: DecisionRequireApproval},
		{name: "external unattended", unattended: true, effect: EffectExternalWrite, want: DecisionDeny},
		{name: "privileged unattended", unattended: true, effect: EffectPrivileged, want: DecisionDeny},
		{name: "unknown attended", effect: EffectUnknown, want: DecisionRequireApproval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.Evaluate(context.Background(), Context{Unattended: tt.unattended}, Action{
				Capability: "example.action",
				Effect:     tt.effect,
			})
			if got.Decision != tt.want {
				t.Fatalf("decision = %q, want %q (%s)", got.Decision, tt.want, got.Reason)
			}
		})
	}
}

func TestPolicyRequiresResourceScopedGrantForUnattendedRead(t *testing.T) {
	root := t.TempDir()
	policy := NewPolicy([]Grant{{
		ID:             "workspace-read",
		Capability:     "tool.read",
		ResourcePrefix: root,
	}})

	inside := policy.Evaluate(context.Background(), Context{Unattended: true}, Action{
		Capability: "tool.read",
		Effect:     EffectRead,
		Resource:   filepath.Join(root, "notes.txt"),
	})
	if inside.Decision != DecisionAllow || inside.GrantID != "workspace-read" {
		t.Fatalf("inside evaluation = %+v, want scoped allow", inside)
	}

	sibling := policy.Evaluate(context.Background(), Context{Unattended: true}, Action{
		Capability: "tool.read",
		Effect:     EffectRead,
		Resource:   root + "-sibling/notes.txt",
	})
	if sibling.Decision != DecisionDeny {
		t.Fatalf("sibling evaluation = %+v, want component-aware denial", sibling)
	}
}

func TestPolicyRequiresMatchingUnexpiredScopedGrant(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	policy := &Policy{
		Now: func() time.Time { return now },
		Grants: []Grant{
			{ID: "expired", Capability: "github.*", ExpiresAt: now},
			{ID: "wrong-resource", Capability: "github.*", ResourcePrefix: "Isoform/other"},
			{ID: "matching", Capability: "github.*", ResourcePrefix: "Isoform/something", ExpiresAt: now.Add(time.Hour)},
		},
	}
	got := policy.Evaluate(context.Background(), Context{Unattended: true}, Action{
		Capability: "github.pull_request.create",
		Effect:     EffectExternalWrite,
		Resource:   "Isoform/something#42",
	})
	if got.Decision != DecisionAllow || got.GrantID != "matching" {
		t.Fatalf("evaluation = %+v, want allow through matching grant", got)
	}
}

func TestNoGrantActionIgnoresMatchingGrants(t *testing.T) {
	policy := NewPolicy([]Grant{
		{ID: "exact", Capability: "local.shell.execute"},
		{ID: "wild", Capability: "local.*"},
	})
	action := Action{Capability: "local.shell.execute", Effect: EffectLocalWrite, NoGrant: true}
	unattended := policy.Evaluate(context.Background(), Context{Unattended: true}, action)
	if unattended.Decision != DecisionDeny || unattended.GrantID != "" {
		t.Fatalf("unattended evaluation = %+v, want deny without a grant", unattended)
	}
	unknown := policy.Evaluate(context.Background(), Context{}, Action{
		Capability: "local.shell.unknown",
		Effect:     EffectUnknown,
		NoGrant:    true,
	})
	if unknown.Decision != DecisionRequireApproval {
		t.Fatalf("attended unknown evaluation = %+v, want require approval", unknown)
	}
}

func TestAttendedGrantNeverAuthorizesUnattendedRun(t *testing.T) {
	policy := NewPolicy(nil)
	action := Action{
		Capability:      "mcp.slack.message.send",
		Effect:          EffectExternalWrite,
		AttendedGrantID: "mcp-trusted:slack",
	}
	attended := policy.Evaluate(context.Background(), Context{}, action)
	if attended.Decision != DecisionAllow {
		t.Fatalf("attended decision = %q, want allow", attended.Decision)
	}
	unattended := policy.Evaluate(context.Background(), Context{Unattended: true}, action)
	if unattended.Decision != DecisionDeny {
		t.Fatalf("unattended decision = %q, want deny", unattended.Decision)
	}
}

func TestShadowRuntimeAllowsOnlyReadsAndIgnoresGrants(t *testing.T) {
	policy := NewPolicy([]Grant{{
		ID:         "grant-everything",
		Capability: "example.*",
	}})
	tests := []struct {
		effect Effect
		want   Decision
	}{
		{effect: EffectRead, want: DecisionAllow},
		{effect: EffectLocalWrite, want: DecisionDeny},
		{effect: EffectExternalWrite, want: DecisionDeny},
		{effect: EffectPrivileged, want: DecisionDeny},
		{effect: EffectUnknown, want: DecisionDeny},
	}
	for _, tt := range tests {
		t.Run(string(tt.effect), func(t *testing.T) {
			got := policy.Evaluate(context.Background(), Context{
				Mode:       ExecutionModeShadow,
				ShadowRun:  "shadow-1",
				Unattended: true,
			}, Action{
				Capability:      "example.action",
				Effect:          tt.effect,
				AttendedGrantID: "attended-grant",
			})
			if got.Decision != tt.want {
				t.Fatalf("decision = %q, want %q (%s)", got.Decision, tt.want, got.Reason)
			}
			if got.PolicyVersion != ShadowPolicyVersion {
				t.Fatalf("policy version = %q, want %q", got.PolicyVersion, ShadowPolicyVersion)
			}
			if got.Context.ShadowRun != "shadow-1" || got.Context.Mode != ExecutionModeShadow {
				t.Fatalf("context = %+v, want linked shadow mode", got.Context)
			}
			if got.Decision == DecisionAllow && got.GrantID != "" {
				t.Fatalf("shadow read unexpectedly used grant %q", got.GrantID)
			}
		})
	}
}

func TestPolicyRejectsUnknownExecutionMode(t *testing.T) {
	got := NewPolicy(nil).Evaluate(context.Background(), Context{
		Mode: ExecutionMode("future-mode"),
	}, Action{
		Capability: "example.read",
		Effect:     EffectRead,
	})
	if got.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want deny", got.Decision)
	}
}
