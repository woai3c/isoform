package agent

import (
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

func TestChooseBackendCompatibilityUsesAssistantFirst(t *testing.T) {
	cases := []struct {
		name      string
		policy    RoutingPolicy
		available bool
		want      Backend
	}{
		{"signed in defaults to assistant", RoutingPolicy{ExternalFormat: "claude-code", ExternalSignedIn: true}, true, BackendExternal},
		{"legacy default flag cannot bypass sign in", RoutingPolicy{ExternalFormat: "claude-code", DefaultExternal: true}, true, BackendNative},
		{"binary missing", RoutingPolicy{ExternalFormat: "claude-code", ExternalSignedIn: true}, false, BackendNative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := chooseBackend(&TaskInput{BackendHint: BackendHintCron, DenyExternalWrites: true}, tc.policy, tc.available, authority.ExecutionModeNormal)
			if got != tc.want || reason == "" {
				t.Fatalf("backend=%s reason=%q, want %s", got, reason, tc.want)
			}
		})
	}
}

func TestClaudeCodeArgsHonourPlanMode(t *testing.T) {
	args := controlledClaudeArgs(&TaskInput{Model: "sonnet", PlanMode: true}, ExternalAgentConfig{}, "/run/mcp.json", nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--permission-mode plan") {
		t.Fatalf("plan mode must select Claude Code's plan permission mode: %s", joined)
	}
	if strings.Contains(joined, "bypassPermissions") {
		t.Fatalf("plan mode must not bypass permissions: %s", joined)
	}
}

func TestNativeRunnerOfLooksThroughTheRouter(t *testing.T) {
	native := &NativeAgentRunner{}
	if got, ok := NativeRunnerOf(native); !ok || got != native {
		t.Fatal("a bare native runner must be returned as is")
	}
	router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
	if got, ok := NativeRunnerOf(router); !ok || got != native {
		t.Fatal("the router must expose its native runner")
	}
	if _, ok := NativeRunnerOf(nil); ok {
		t.Fatal("nil is not a native runner")
	}
	if router.Native() != native {
		t.Fatal("Native() must return the wrapped runner")
	}
}
