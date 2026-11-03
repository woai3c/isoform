package native

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

// bashStubTool stands in for the real Bash tool: it carries the name "Bash"
// (so the attended risky-command downgrade applies) and a chosen authority
// action, and records executions without running a shell.
type bashStubTool struct {
	action   authority.Action
	executes int
}

func (t *bashStubTool) Name() string        { return "Bash" }
func (t *bashStubTool) Description() string { return "bash stub" }
func (t *bashStubTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *bashStubTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (t *bashStubTool) DescribeAuthority(map[string]any) authority.Action {
	a := t.action
	a.ToolName = "Bash"
	return a
}
func (t *bashStubTool) Execute(context.Context, tools.ToolCall) (tools.ToolResult, error) {
	t.executes++
	return tools.ToolResult{Content: "ran"}, nil
}

func runBashStub(t *testing.T, ask, unattended bool) (string, int) {
	t.Helper()
	tool := &bashStubTool{action: authority.Action{
		Capability: "network.request.write",
		Effect:     authority.EffectExternalWrite,
		NoGrant:    true,
	}}
	reg := tools.NewRegistry()
	reg.Register(tool)
	cfg := &LoopConfig{
		Registry:               reg,
		Emit:                   func(LoopEvent) {},
		AutoApproveTools:       unattended,
		AskBeforeRiskyCommands: ask,
		AuthorityEvaluator:     authority.NewPolicy(nil),
	}
	_, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{
		ID:    "toolu_bash",
		Name:  "Bash",
		Input: `{"command":"curl https://example.invalid/x"}`,
	})
	if err != nil {
		t.Fatalf("executeToolUse error: %v", err)
	}
	return outcome, tool.executes
}

// TestAskBeforeRiskyCommandsOffRunsAttendedBash: with the setting off, an
// attended run executes a risky (external) Bash command without a prompt.
func TestAskBeforeRiskyCommandsOffRunsAttendedBash(t *testing.T) {
	outcome, executes := runBashStub(t, false, false)
	if outcome != toolOutcomeSuccess || executes != 1 {
		t.Fatalf("off/attended: outcome=%q executes=%d, want success and one run", outcome, executes)
	}
}

// TestAskBeforeRiskyCommandsOnAsksAttendedBash: with the setting on, the same
// command needs approval, so with no approval channel it is blocked, not run.
func TestAskBeforeRiskyCommandsOnAsksAttendedBash(t *testing.T) {
	outcome, executes := runBashStub(t, true, false)
	if outcome != toolOutcomePermissionDenied || executes != 0 {
		t.Fatalf("on/attended: outcome=%q executes=%d, want approval block and no run", outcome, executes)
	}
}

// TestAskBeforeRiskyCommandsOffKeepsUnattendedClosed: the setting never
// loosens an unattended run, which still fails closed on an external write.
func TestAskBeforeRiskyCommandsOffKeepsUnattendedClosed(t *testing.T) {
	outcome, executes := runBashStub(t, false, true)
	if outcome != toolOutcomePermissionDenied || executes != 0 {
		t.Fatalf("off/unattended: outcome=%q executes=%d, want denial and no run", outcome, executes)
	}
}

// Use the real shell, registry and Programmatic helper. The only destination
// is a local HTTP server, whose request count proves whether Bash executed.
func TestProgrammaticBashUsesAttendedRiskyCommandSetting(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	home, err := os.MkdirTemp(base, "iso-nb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	workDir := filepath.Join(home, "project")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", req.Method)
		}
		requests.Add(1)
		_, _ = w.Write([]byte("nested-bash-ran"))
	}))
	t.Cleanup(server.Close)
	command := "curl --noproxy 127.0.0.1 -sS --max-time 5 -X POST " + server.URL
	policy := authority.NewPolicy([]authority.Grant{{ID: "programmatic-test", Capability: "tool.programmatic"}})
	for _, tc := range []struct {
		name       string
		ask        bool
		unattended bool
		mode       authority.ExecutionMode
		wantRun    bool
	}{
		{name: "attended_off", wantRun: true},
		{name: "attended_on", ask: true},
		{name: "unattended_off", unattended: true},
		{name: "shadow_off", mode: authority.ExecutionModeShadow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			reg := tools.NewRegistry()
			reg.Register(tools.NewBashTool())
			reg.Register(tools.NewProgrammaticTool(reg, []string{"Bash"}))
			input, err := json.Marshal(map[string]any{"code": "result = bash(" + strconv.Quote(command) + ").content"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := &LoopConfig{
				Registry: reg, WorkDir: workDir, Emit: func(LoopEvent) {},
				AutoApproveTools: tc.unattended, AskBeforeRiskyCommands: tc.ask,
				AuthorityEvaluator: policy, AuthorityContext: authority.Context{Mode: tc.mode},
			}
			result, _, err := executeToolUse(context.Background(), cfg, toolUseReq{
				ID: "nested-bash", Name: "Programmatic", Input: string(input),
			})
			if err != nil {
				t.Fatal(err)
			}
			gotRun := requests.Load() == before+1
			if gotRun != tc.wantRun {
				t.Fatalf("ran=%v, want %v; result=%+v", gotRun, tc.wantRun, result)
			}
			if tc.mode != authority.ExecutionModeShadow && !strings.Contains(result.Content.(string), `"tool": "Bash"`) {
				t.Fatalf("test did not reach the nested Bash dispatch: %+v", result)
			}
			if tc.wantRun && !strings.Contains(result.Content.(string), "nested-bash-ran") {
				t.Fatalf("missing subprocess output: %+v", result)
			}
		})
	}
}
