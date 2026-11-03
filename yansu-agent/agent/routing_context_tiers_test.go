package agent

import (
	"context"
	"errors"
	"testing"

	"yansu-agent/agent/tools"
)

func TestRoutingContextTiersComposeOnlySelectedBackend(t *testing.T) {
	for _, scenario := range []string{"external", "native choice", "native fallback", "missing tool support", "source bound"} {
		t.Run(scenario, func(t *testing.T) {
			native := &fakeAgentRunner{ready: true, runID: "native"}
			external := &extraToolsFakeRunner{fakeAgentRunner: &fakeAgentRunner{ready: true, runID: "external"}, supported: true}
			input := &TaskInput{BackendHint: BackendHintChat}
			nativeComposes, externalComposes := 0, 0
			input.PromptFunc = func() (string, string) { nativeComposes++; return "full native context", "native history" }
			input.ExternalPromptFunc = func() (string, string) { externalComposes++; return "external tool hint", "external history" }
			input.ExternalExtraTools = []tools.Tool{extraTestTool{name: "MemorySearch"}}
			switch scenario {
			case "native choice":
				input.ForceBackend = "native"
			case "native fallback":
				external.executeErr = routingLedgerFailure{cause: errors.New("retry later")}
			case "missing tool support":
				external.supported = false
			case "source bound":
				input.RequireNativeDispatch = true
			}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			_, err := router.ExecuteTask(context.Background(), input, func(*StreamEvent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			wantNative, wantExternal := 0, 1
			switch scenario {
			case "native choice", "missing tool support", "source bound":
				wantNative, wantExternal = 1, 0
			case "native fallback":
				wantNative, wantExternal = 1, 1
			}
			if nativeComposes != wantNative || externalComposes != wantExternal {
				t.Fatalf("composition native=%d external=%d", nativeComposes, externalComposes)
			}
			for _, got := range external.executed {
				if got.Prompt != "external tool hint" || got.History != "external history" || len(got.ExtraTools) != 1 || got.ExtraTools[0].Name() != "MemorySearch" {
					t.Fatalf("external task = %+v", got)
				}
			}
			for _, got := range native.executed {
				if got.Prompt != "full native context" || got.History != "native history" || len(got.ExtraTools) != 0 {
					t.Fatalf("native task = %+v", got)
				}
			}
			if len(input.ExtraTools) != 0 || input.Prompt != "" {
				t.Fatal("router mutated original task")
			}
		})
	}
}

func TestRoutingExternalToolsWithoutDeferredComposer(t *testing.T) {
	native := &fakeAgentRunner{ready: true}
	external := &extraToolsFakeRunner{fakeAgentRunner: &fakeAgentRunner{ready: true}, supported: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	input := &TaskInput{Prompt: "query", ExternalExtraTools: []tools.Tool{extraTestTool{name: "MemorySearch"}}}
	if _, err := router.ExecuteTask(context.Background(), input, func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(external.executed) != 1 || len(external.executed[0].ExtraTools) != 1 || external.executed[0].ExtraTools[0].Name() != "MemorySearch" {
		t.Fatal("external-only tool was dropped")
	}
	if len(input.ExtraTools) != 0 {
		t.Fatal("native registry changed")
	}
}
