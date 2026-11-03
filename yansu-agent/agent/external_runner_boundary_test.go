package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/isoformskill"
)

func TestRoutingRejectsExternalRunnerWithoutToolServer(t *testing.T) {
	for _, format := range []string{"claude-code", "codex"} {
		t.Run(format, func(t *testing.T) {
			external := NewExternalAgentRunner(ExternalAgentConfig{Format: format})
			external.ready = true
			native := &fakeAgentRunner{ready: true, runID: "native"}
			router := NewRoutingAgentRunner(native, external, RoutingPolicy{ExternalFormat: format, ExternalSignedIn: true})
			if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "hello"}, nil); err != nil {
				t.Fatal(err)
			}
			if len(native.executed) != 1 {
				t.Fatal("routing selected a runner without a tool server")
			}
			composed := false
			_, err := external.ExecuteTask(context.Background(), &TaskInput{PromptFunc: func() (string, string) {
				composed = true
				return "private prompt", "private history"
			}}, nil)
			if err == nil || !strings.Contains(err.Error(), "tool server") || composed {
				t.Fatalf("direct execution bypassed controlled transport: err=%v composed=%v", err, composed)
			}
		})
	}
}

// A protocol fixture exercises the real controlled spawn, stdin and init
// validation. It never invokes a signed-in assistant or a model.
func TestControlledAfterRewindStartsFreshWithIsoformHistory(t *testing.T) {
	for _, history := range []string{"User: kept question\n\nAssistant: kept answer", ""} {
		t.Run("history="+history, func(t *testing.T) {
			state := externalFixtureState(t)
			t.Setenv("HOME", state)
			init := validControlledInit()
			init["tools"] = []string{}
			init["session_id"] = "provider-thread-with-removed-turns"
			initJSON, _ := json.Marshal(init)
			binary := writeFakeCLI(t, "claude", `case "$1" in
--version) echo '2.1.296 (Claude Code)'; exit;;
--help) echo '`+strings.Join(controlledClaudeFlags, " ")+`'; exit;;
esac
printf '%s\n' "$@" > args
cat > prompt
echo '`+string(initJSON)+`'
echo '{"type":"result","subtype":"success","result":"done"}'`)
			native := NewNativeAgentRunner(NativeRunnerConfig{})
			runner := NewExternalAgentRunner(ExternalAgentConfig{Command: binary, ToolShimCommand: "/unused/yansu", ToolServerFactory: native.NewToolServer})
			if err := runner.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer runner.Shutdown()
			// First use teaches the legacy transport a provider session id. The
			// second use must still be fresh even with the same Isoform session.
			input := &TaskInput{Prompt: "old question", SessionID: "provider-thread-with-removed-turns", WorkDir: state, NoTools: true}
			if _, err := runner.ExecuteTask(context.Background(), input, nil); err != nil {
				t.Fatal(err)
			}
			input.History = "removed question and removed answer"
			input.PromptFunc = func() (string, string) { return "replacement question", history }
			if _, err := runner.ExecuteTask(context.Background(), input, nil); err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(filepath.Join(state, "args"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "resume") || strings.Contains(string(args), input.SessionID) || !strings.Contains(string(args), "--no-session-persistence") {
				t.Fatalf("controlled turn reused provider state: %s", args)
			}
			data, err := os.ReadFile(filepath.Join(state, "prompt"))
			if err != nil {
				t.Fatal(err)
			}
			var message struct{ Message struct{ Content string } }
			if err := json.Unmarshal(data, &message); err != nil {
				t.Fatal(err)
			}
			prompt := "replacement question"
			if history != "" {
				prompt = history + "\n\n" + prompt
			}
			if message.Message.Content != isoformskill.Prompt(prompt) {
				t.Fatalf("continued turn did not use only Isoform history: %q", message.Message.Content)
			}
		})
	}
}

func TestRoutingAfterRewindClearsStaleHistory(t *testing.T) {
	native := &fakeAgentRunner{ready: true, runID: "native"}
	router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
	input := &TaskInput{Prompt: "replacement", History: "removed turns", PromptFunc: func() (string, string) { return "replacement", "" }}
	if _, err := router.ExecuteTask(context.Background(), input, nil); err != nil {
		t.Fatal(err)
	}
	if len(native.executed) != 1 || native.executed[0].History != "" || input.History != "removed turns" {
		t.Fatal("routing retained stale history or mutated the caller's task")
	}
}
