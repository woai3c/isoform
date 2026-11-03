package native

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent/tools"
)

func TestProtectedPathDenialEmitsFailedToolResult(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", dataRoot)
	protected := filepath.Join(dataRoot, "cron-secret")
	for _, tc := range []struct {
		name  string
		tool  tools.Tool
		input map[string]string
	}{
		{"read", tools.NewReadTool(), map[string]string{"path": protected}},
		{"bash", tools.NewBashTool(), map[string]string{"command": "cat '" + protected + "'"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewRegistry()
			registry.Register(tc.tool)
			input, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			var events []LoopEvent
			result, outcome, err := executeToolUse(context.Background(), &LoopConfig{
				Registry:         registry,
				WorkDir:          t.TempDir(),
				AutoApproveTools: true,
				Emit:             func(event LoopEvent) { events = append(events, event) },
			}, toolUseReq{ID: "protected-call", Name: tc.tool.Name(), Input: string(input)})
			if err != nil || outcome != toolOutcomePermissionDenied || !result.IsError {
				t.Fatalf("err=%v outcome=%q result=%+v, want protected-path denial", err, outcome, result)
			}
			if len(events) != 1 || events[0].Type != "tool_result" || events[0].ToolUseID != "protected-call" ||
				events[0].ToolName != tc.tool.Name() || !strings.HasPrefix(events[0].Content, "Blocked:") {
				t.Fatalf("events = %+v, want the protected-path tool result", events)
			}
			if !events[0].Failed || events[0].ToolSucceeded {
				t.Fatalf("blocked tool result lost failure status: %+v", events[0])
			}
		})
	}
}

func TestToolResultFailedReadsTheToolsOwnStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		result tools.ToolResult
		want   bool
	}{
		"ok":            {tools.ToolResult{Content: "fine", Metadata: map[string]any{"exit_code": 0, "timed_out": false}}, false},
		"no metadata":   {tools.ToolResult{Content: "fine"}, false},
		"is_error":      {tools.ToolResult{Content: "404", IsError: true}, true},
		"timed out":     {tools.ToolResult{Content: "[command timed out]", Metadata: map[string]any{"exit_code": -1, "timed_out": true}}, true},
		"non-zero exit": {tools.ToolResult{Content: "x\n(exit code: 1)", Metadata: map[string]any{"exit_code": 1}}, true},
		// The text cannot fake a failure: only the status counts.
		"text says blocked": {tools.ToolResult{Content: "(exit code: 69) BROWSER_CONNECT", Metadata: map[string]any{"exit_code": 0}}, false},
	} {
		if got := toolResultFailed(tc.result); got != tc.want {
			t.Fatalf("%s: failed = %v, want %v", name, got, tc.want)
		}
	}
}
