package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/isoformskill"
)

// Transport fixtures assert what reaches each CLI's stdin, without invoking a
// model or changing the user's assistant configuration.
func TestControlledHandoffCarriesOnlyIsoformResultContract(t *testing.T) {
	const plainTask = "Use only this context.\nFinish with the requested result contract."
	for _, tt := range []struct{ name, task string }{
		{"plain", plainTask},
		{"native contract", "Task brief\n\n" + isoformskill.Instructions + "\n\n" + plainTask},
		{"contract after history", "User: prior turn\n\n" + isoformskill.Instructions + "\n\n" + plainTask},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const format = "claude-code"
			state := externalFixtureState(t)
			t.Setenv("HOME", state)
			path := filepath.Join(state, "prompt")
			result := `{"type":"result","subtype":"success","result":"done","is_error":false}`
			init := validControlledInit()
			init["tools"] = []string{}
			initJSON, _ := json.Marshal(init)
			binary := writeFakeCLI(t, "assistant", `case "$1" in
--version) echo '2.1.296 (Claude Code)'; exit;;
--help) echo '`+strings.Join(controlledClaudeFlags, " ")+`'; exit;;
esac
cat > '`+path+`'
echo '`+string(initJSON)+`'
echo '`+result+`'`)
			runner := NewExternalAgentRunner(ExternalAgentConfig{Format: format, Command: binary, ToolShimCommand: "/unused/yansu", ToolServerFactory: NewNativeAgentRunner(NativeRunnerConfig{}).NewToolServer})
			if err := runner.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			task := tt.task
			calls := 0
			input := &TaskInput{NoTools: true, Prompt: "stale task", PromptFunc: func() (string, string) {
				calls++
				return task, ""
			}}
			_, err := runner.ExecuteTask(context.Background(), input, func(*StreamEvent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			prompt := string(data)
			if format == "claude-code" {
				var message struct {
					Message struct {
						Content string
					}
				}
				if err := json.Unmarshal(data, &message); err != nil {
					t.Fatalf("invalid Claude prompt: %v", err)
				}
				prompt = message.Message.Content
			}
			if strings.Count(prompt, isoformskill.Instructions) != 1 || !strings.HasSuffix(prompt, task) {
				t.Fatal("assistant handoff lost skill instructions or changed the task")
			}
			if strings.Count(prompt, isoformskill.ResultContract) != 1 || strings.Count(prompt, "## Report back") != 1 {
				t.Fatal("controlled handoff must carry the result contract exactly once")
			}
			for _, manual := range []string{"# Isoform\n", "## Start with the handoff", "## Read context through MCP", "## The CLI", "--- Bundled isoform skill ---", "Use the isoform skill", "$isoform"} {
				if strings.Contains(prompt, manual) {
					t.Errorf("controlled handoff contains manual guidance %q", manual)
				}
			}
			if calls != 1 || input.Prompt != "stale task" {
				t.Fatalf("handoff resolution changed caller input: calls=%d prompt=%q", calls, input.Prompt)
			}
		})
	}
}

func TestExternalIsoformSkillDoesNotMakeEmptyTaskRunnable(t *testing.T) {
	for _, format := range []string{"claude-code"} {
		runner := NewExternalAgentRunner(ExternalAgentConfig{Format: format, ToolShimCommand: "/unused/yansu", ToolServerFactory: NewNativeAgentRunner(NativeRunnerConfig{}).NewToolServer})
		runner.profileCheckedAt = time.Now()
		if _, err := runner.ExecuteTask(context.Background(), &TaskInput{Prompt: " \n "}, nil); err == nil || err.Error() != "prompt is required" {
			t.Fatalf("%s empty task accepted: %v", format, err)
		}
	}
}
