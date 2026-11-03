package native

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent/tools"
)

// Real local provider transport plus the real file-writing tool verifies the
// execution boundary even when a provider returns an unadvertised tool call.
func TestNoToolsRejectsBufferedUnadvertisedToolBeforeExecution(t *testing.T) {
	output := filepath.Join(t.TempDir(), "must-not-exist.txt")
	input, _ := json.Marshal(map[string]string{"path": output, "content": "unexpected write"})
	body := newSSE().messageStart().toolUseBlockWithInput("Write", "call-unadvertised", string(input)).messageDeltaStop("tool_use").messageStop().build()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) != 0 {
			t.Error("NoTools advertised tool definitions")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteTool(nil))
	completed := false
	err := Run(context.Background(), &LoopConfig{Client: NewClient(WithBaseURL(server.URL)), Registry: registry, ModelID: "local-transport-test", Prompt: "A text-only conversation.", NoTools: true, AutoApproveTools: true, MaxTokens: 256, Emit: func(event LoopEvent) {
		if event.Type == "done" {
			completed = true
		}
	}})
	if err == nil || !strings.Contains(err.Error(), "tool execution is disabled") || completed {
		t.Fatalf("unexpected outcome: completed=%v err=%v", completed, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("unadvertised tool wrote a file: %v", err)
	}
}
func TestCancelledRunCannotDispatchSynchronousTool(t *testing.T) {
	output := filepath.Join(t.TempDir(), "must-not-exist.txt")
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteTool(nil))
	input, _ := json.Marshal(map[string]string{"path": output, "content": "unexpected write"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := executeToolUse(ctx, &LoopConfig{Registry: registry, AutoApproveTools: true}, toolUseReq{ID: "cancelled", Name: "Write", Input: string(input)})
	if err == nil {
		t.Fatal("cancelled dispatch accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("cancelled tool wrote a file: %v", err)
	}
}
