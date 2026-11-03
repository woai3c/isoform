package native

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

// Exercise the native loop, default authority policy and Bash tool over real
// loopback HTTP. The scoped-grant case proves the same tool can reach the sink;
// an approval-requiring operation cannot run merely because cron is unattended.
func TestUnattendedLoopBlocksApprovalToolWithoutScopedGrant(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is required for the real loopback tool invocation")
	}
	home, err := os.MkdirTemp("/tmp", "pa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	for _, granted := range []bool{false, true} {
		name := "no_grant"
		if granted {
			name = "scoped_grant"
		}
		t.Run(name, func(t *testing.T) {
			var writes atomic.Int32
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("sink method = %s, want POST", r.Method)
				}
				writes.Add(1)
				_, _ = w.Write([]byte("written"))
			}))
			t.Cleanup(sink.Close)

			input := map[string]any{
				"command":     "curl --disable --noproxy '*' --max-time 5 --silent --show-error -X POST " + sink.URL,
				"description": "Send one request to the local test server",
			}
			tool := tools.NewBashTool()
			action := tools.DescribeAuthority(tool, input, home)
			policy := authority.NewPolicy(nil)
			attended := policy.Evaluate(context.Background(), authority.Context{}, action)
			if attended.Decision != authority.DecisionRequireApproval {
				t.Fatalf("attended decision = %s for %+v, want require_approval", attended.Decision, action)
			}
			if granted {
				policy = authority.NewPolicy([]authority.Grant{{
					ID:         "loopback-post",
					Capability: action.Capability,
				}})
			}
			encodedInput, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			toolTurn := newSSE().messageStart().toolUseBlockWithInput(tool.Name(), "local-post", string(encodedInput)).messageDeltaStop("tool_use").messageStop().build()
			finalTurn := newSSE().messageStart().textBlock("Finished the local test.").messageDeltaStop("end_turn").messageStop().build()
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request Request
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode native request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch requests.Add(1) {
				case 1:
					if len(request.Tools) != 1 || request.Tools[0].Name != tool.Name() {
						t.Errorf("advertised tools = %+v, want Bash", request.Tools)
					}
					_, _ = w.Write(toolTurn)
				case 2:
					_, _ = w.Write(finalTurn)
				default:
					t.Error("native loop made an unexpected additional model request")
					http.Error(w, "unexpected model request", http.StatusBadRequest)
				}
			}))
			t.Cleanup(provider.Close)

			registry := tools.NewRegistry()
			registry.Register(tool)
			var evaluations []authority.Evaluation
			var events []LoopEvent
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err = Run(ctx, &LoopConfig{
				Client:             NewClient(WithBaseURL(provider.URL)),
				Registry:           registry,
				ModelID:            "local-transport-test",
				Prompt:             "Send one local test request.",
				WorkDir:            home,
				AutoApproveTools:   true,
				AuthorityEvaluator: policy,
				AuthorityRecorder:  func(evaluation authority.Evaluation) { evaluations = append(evaluations, evaluation) },
				Emit:               func(event LoopEvent) { events = append(events, event) },
				MaxTokens:          256,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if requests.Load() != 2 {
				t.Fatalf("model requests = %d, want 2", requests.Load())
			}
			if len(evaluations) == 0 {
				t.Fatal("authority decision was not recorded")
			}
			for _, event := range events {
				if event.Type == "permission_request" {
					t.Fatal("unattended execution emitted a permission request")
				}
			}
			if granted {
				if writes.Load() != 1 || evaluations[0].Decision != authority.DecisionAllow || evaluations[0].GrantID != "loopback-post" {
					t.Fatalf("writes=%d evaluation=%+v, want one scoped-grant execution", writes.Load(), evaluations[0])
				}
				return
			}
			if writes.Load() != 0 || evaluations[0].Decision != authority.DecisionDeny || !evaluations[0].Context.Unattended {
				t.Fatalf("writes=%d evaluation=%+v, want an unattended denial before execution", writes.Load(), evaluations[0])
			}
			for _, event := range events {
				if event.Type == "tool_result" && event.ToolUseID == "local-post" && event.Failed && strings.HasPrefix(event.Content, "Blocked:") {
					return
				}
			}
			t.Fatal("missing failed authority result for the blocked tool")
		})
	}
}
