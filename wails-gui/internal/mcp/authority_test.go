package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/authority"
)

func TestMCPAuthorityDescriptionKeepsTrustAttendedOnly(t *testing.T) {
	adapter := NewToolAdapter("slack", "", mcp.Tool{Name: "send_message"}, nil, true)
	action := adapter.DescribeAuthority(nil)
	if action.Effect != authority.EffectUnknown {
		t.Fatalf("effect=%q, want unknown", action.Effect)
	}
	if action.Capability != "mcp.slack.send_message" {
		t.Fatalf("capability=%q", action.Capability)
	}
	if action.AttendedGrantID != "mcp-trusted:slack" {
		t.Fatalf("attended grant=%q", action.AttendedGrantID)
	}
}

func TestShadowPolicyAllowsAnnotatedMCPReadAndBlocksTrustedUnknownCall(t *testing.T) {
	policy := authority.NewPolicy(nil)
	run := authority.Context{Mode: authority.ExecutionModeShadow}

	trusted := NewToolAdapter("slack", "", mcp.Tool{Name: "send_message"}, nil, true)
	blocked := policy.Evaluate(context.Background(), run, trusted.DescribeAuthority(nil))
	if blocked.Decision != authority.DecisionDeny || blocked.GrantID != "" {
		t.Fatalf("trusted mutation=%+v, want ungranted shadow denial", blocked)
	}

	readOnly := true
	reader := NewToolAdapter("search", "", mcp.Tool{
		Name: "find_documents",
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint: &readOnly,
		},
	}, nil, false)
	allowed := policy.Evaluate(context.Background(), run, reader.DescribeAuthority(nil))
	if allowed.Decision != authority.DecisionAllow {
		t.Fatalf("annotated read=%+v, want shadow allow", allowed)
	}
}

func TestMCPGatewayDiscoveryIsReadOnlyButCallIsNot(t *testing.T) {
	search := &gatewaySearchTool{serverID: "slack"}
	if got := search.DescribeAuthority(nil); got.Effect != authority.EffectRead {
		t.Fatalf("search effect=%q, want read", got.Effect)
	}
	call := &gatewayCallTool{serverID: "slack"}
	got := call.DescribeAuthority(map[string]any{"tool_name": "send_message"})
	if got.Effect != authority.EffectUnknown ||
		got.Capability != "mcp.slack.send_message" {
		t.Fatalf("call action=%+v", got)
	}
}

func TestMCPReadOnlyAnnotationAllowsReadClassification(t *testing.T) {
	readOnly := true
	adapter := NewToolAdapter("search", "", mcp.Tool{
		Name: "find_documents",
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint: &readOnly,
		},
	}, nil, false)
	got := adapter.DescribeAuthority(nil)
	if got.Effect != authority.EffectRead || !got.Reversible {
		t.Fatalf("action=%+v, want reversible read", got)
	}
}
