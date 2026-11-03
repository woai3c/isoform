package agent

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

type extraToolsFakeRunner struct {
	*fakeAgentRunner
	supported bool
}

func (r *extraToolsFakeRunner) SupportsExtraTools(*TaskInput) bool { return r.supported }

func TestRoutingExtraToolsCapability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capability bool
		supported  bool
		mutate     func(*TaskInput)
		wantNative bool
	}{
		{name: "capability absent", wantNative: true},
		{name: "tools unsupported", capability: true, wantNative: true},
		{name: "tools supported", capability: true, supported: true},
		{name: "native override", capability: true, supported: true, mutate: func(in *TaskInput) { in.ForceBackend = "native" }, wantNative: true},
		{name: "shadow stays native", capability: true, supported: true, mutate: func(in *TaskInput) { in.ExecutionMode = authority.ExecutionModeShadow }, wantNative: true},
		{name: "screen turn stays native", capability: true, supported: true, mutate: func(in *TaskInput) { in.BackendHint = BackendHintComputerUse }, wantNative: true},
		{name: "screen tool stays native", capability: true, supported: true, mutate: func(in *TaskInput) { in.AllowedTools = []string{"GoalCreate", "cua_driver"} }, wantNative: true},
		{name: "scoped grants stay native", capability: true, supported: true, mutate: func(in *TaskInput) { in.AuthorityGrants = []authority.Grant{{ID: "scope"}} }, wantNative: true},
		{name: "initiative stays native", capability: true, supported: true, mutate: func(in *TaskInput) { in.AuthorityInitiativeID = "initiative" }, wantNative: true},
		{name: "live evaluator stays native", capability: true, supported: true, mutate: func(in *TaskInput) { in.AuthorityEvaluator = authority.NewPolicy(nil) }, wantNative: true},
		{name: "no tools requested", mutate: func(in *TaskInput) { in.ExtraTools = nil }},
		{name: "nil extra ignored", mutate: func(in *TaskInput) { in.ExtraTools = []tools.Tool{nil} }},
		{name: "no tools mode", mutate: func(in *TaskInput) { in.NoTools = true }},
		{name: "extra disabled", mutate: func(in *TaskInput) { in.DisabledTools = []string{"GoalCreate"} }},
		{name: "extra filtered", mutate: func(in *TaskInput) { in.AllowedTools = []string{"Read"} }},
		{name: "extra explicitly allowed", mutate: func(in *TaskInput) { in.AllowedTools = []string{"GoalCreate"} }, wantNative: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := &fakeAgentRunner{runID: "native"}
			external := &fakeAgentRunner{runID: "external"}
			var runner AgentRunner = external
			if tc.capability {
				runner = &extraToolsFakeRunner{fakeAgentRunner: external, supported: tc.supported}
			}
			router := NewRoutingAgentRunner(native, runner, signedInPolicy)
			tool := &extraTestTool{name: "GoalCreate"}
			input := &TaskInput{Prompt: "Track this goal", BackendHint: BackendHintChat, ExtraTools: []tools.Tool{tool}}
			if tc.mutate != nil {
				tc.mutate(input)
			}
			id, err := router.ExecuteTask(context.Background(), input, func(event *StreamEvent) error {
				if event.Type == "assistant" || event.Type == "error" {
					t.Fatalf("capability fallback emitted chat content: %+v", event)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			selected, other := external, native
			if tc.wantNative {
				selected, other = native, external
			}
			if id != selected.runID || len(selected.executed) != 1 || len(other.executed) != 0 {
				t.Fatalf("run=%q native=%d external=%d", id, len(native.executed), len(external.executed))
			}
			got := selected.executed[0]
			if len(got.ExtraTools) != len(input.ExtraTools) || got.Prompt != input.Prompt {
				t.Fatal("routing lost turn tools or prompt")
			}
			if len(input.ExtraTools) > 0 && got.ExtraTools[0] != input.ExtraTools[0] {
				t.Fatal("routing replaced the in-process tool instance")
			}
		})
	}
}

func TestExternalRunnerRefusesToDropExtraTools(t *testing.T) {
	for _, format := range []string{"claude-code", "codex"} {
		t.Run(format, func(t *testing.T) {
			runner := NewExternalAgentRunner(ExternalAgentConfig{Format: format})
			_, err := runner.ExecuteTask(context.Background(), &TaskInput{
				Prompt: "Track this goal", ExtraTools: []tools.Tool{extraTestTool{name: "GoalCreate"}},
			}, func(*StreamEvent) error { t.Fatal("unsupported CLI was started"); return nil })
			if !errors.Is(err, ErrExtraToolsRequireNative) {
				t.Fatalf("got %v, want tools-required error before starting a CLI", err)
			}
		})
	}
}

func TestExternalRunnerExtraToolsSupportRequiresGatedCoverage(t *testing.T) {
	native := NewNativeAgentRunner(NativeRunnerConfig{DisableEnvFallback: true})
	input := &TaskInput{ExtraTools: []tools.Tool{extraTestTool{name: "GoalCreate"}}}
	for _, tc := range []struct {
		name   string
		mutate func(*ExternalAgentConfig)
		want   bool
	}{
		{name: "gated Claude", want: runtime.GOOS != "windows"},
		{name: "missing tool server", mutate: func(c *ExternalAgentConfig) { c.ToolServerFactory = nil }},
		{name: "unverified Codex profile", mutate: func(c *ExternalAgentConfig) { c.Format = "codex" }},
		{name: "tool coverage fails", mutate: func(c *ExternalAgentConfig) { c.ToolServerSupports = func(*TaskInput) bool { return false } }},
		{name: "shim missing", mutate: func(c *ExternalAgentConfig) { c.ToolShimCommand = "" }},
		{name: "sandbox bypass", mutate: func(c *ExternalAgentConfig) { c.DisableSandbox = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ExternalAgentConfig{Format: "claude-code", ToolShimCommand: "/bundled/yansu", ToolServerFactory: native.NewToolServer, ToolServerSupports: native.ToolServerSupports}
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}
			if got := NewExternalAgentRunner(cfg).SupportsExtraTools(input); got != tc.want {
				t.Fatalf("support=%t, want %t", got, tc.want)
			}
		})
	}
}
