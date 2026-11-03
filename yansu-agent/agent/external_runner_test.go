package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

type parseFunc func([]byte, func(*StreamEvent) error) (bool, error)

// runParse feeds each JSONL line through a protocol parser and collects the
// emitted events. Provider session ids are deliberately ignored.
func runParse(t *testing.T, parse parseFunc, lines []string) []*StreamEvent {
	t.Helper()
	var events []*StreamEvent
	emit := func(ev *StreamEvent) error {
		cp := *ev
		events = append(events, &cp)
		return nil
	}
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if _, err := parse([]byte(ln), emit); err != nil {
			t.Fatalf("parse(%q) error: %v", ln, err)
		}
	}
	return events
}

func findEvent(events []*StreamEvent, typ string) *StreamEvent {
	for _, e := range events {
		if e.Type == typ {
			return e
		}
	}
	return nil
}

func concatEvents(events []*StreamEvent, typ string) string {
	var b strings.Builder
	for _, e := range events {
		if e.Type == typ {
			b.WriteString(e.Content)
		}
	}
	return b.String()
}

func TestClaudeCodeProtocol_Parse(t *testing.T) {
	// Token-level text comes from stream_event deltas; tool_use from the
	// message-level assistant event; tool_result from the user event;
	// result ends the turn. Message-level assistant TEXT must be skipped
	// (already streamed via deltas) to avoid double-rendering.
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"sess-1","tools":["Bash"]}`,
		`{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}}`,
		`{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}}`,
		`{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
		`{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"text","text":"Hello"},{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","session_id":"sess-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file.txt"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-1","total_cost_usd":0.01,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2}}`,
	}
	events := runParse(t, parseClaudeLine, lines)

	// Text streamed incrementally and reconstructs to "Hello" (NOT doubled
	// by the message-level assistant text block).
	if got := concatEvents(events, "assistant"); got != "Hello" {
		t.Errorf("assistant text = %q, want %q (deltas only, no message-level dup)", got, "Hello")
	}
	if got := concatEvents(events, "thinking"); got != "hmm" {
		t.Errorf("thinking = %q, want hmm", got)
	}
	tus := findEvent(events, "tool_use_start")
	if tus == nil || tus.ToolName != "Bash" || tus.ToolUseID != "tu_1" {
		t.Errorf("tool_use_start wrong: %+v", tus)
	}
	tu := findEvent(events, "tool_use")
	if tu == nil || tu.ToolName != "Bash" || tu.ToolUseID != "tu_1" {
		t.Fatalf("tool_use wrong: %+v", tu)
	}
	if m, ok := tu.ToolInput.(map[string]any); !ok || m["command"] != "ls" {
		t.Errorf("tool_use input wrong: %+v", tu.ToolInput)
	}
	tr := findEvent(events, "tool_result")
	if tr == nil || tr.ToolUseID != "tu_1" || tr.Content != "file.txt" {
		t.Errorf("tool_result wrong: %+v", tr)
	}
	done := findEvent(events, "done")
	if done == nil || done.Usage == nil {
		t.Fatalf("done/usage missing: %+v", done)
	}
	if done.Usage.InputTokens != 10 || done.Usage.OutputTokens != 5 || done.Usage.CacheReadInputTokens != 2 {
		t.Errorf("usage wrong: %+v", done.Usage)
	}

	if d, _ := parseClaudeLine([]byte(lines[6]), func(*StreamEvent) error { return nil }); !d {
		t.Errorf("result line should report done=true")
	}
}

func TestControlledClaudeModelAliases(t *testing.T) {
	in := &TaskInput{Model: "sonnet", WorkDir: "/tmp/x"}
	args := controlledClaudeArgs(in, ExternalAgentConfig{}, "/run/mcp.json", nil)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--print",
		"--input-format stream-json",
		"--output-format stream-json",
		"--verbose",
		"--include-partial-messages",
		"--model sonnet",
		"--permission-mode dontAsk",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q; got %q", want, joined)
		}
	}
	// The prompt is NOT an argument (it goes to stdin).
	if strings.Contains(joined, "-p ") || strings.Contains(joined, "hello") {
		t.Errorf("prompt must not be an arg in stream-json mode: %q", joined)
	}
	if strings.Contains(joined, "--resume") {
		t.Errorf("fresh session should not pass --resume: %q", joined)
	}
	// With a big model configured, the chat/code role
	// alias the runner emits ("sonnet") maps onto the configured big model.
	cfg := ExternalAgentConfig{
		Model: "claude-opus-4-1",
		ModelAliases: map[string]string{
			"sonnet": "claude-opus-4-1",
			"opus":   "claude-opus-4-1",
			"haiku":  "claude-haiku-4-5",
		},
	}
	args = controlledClaudeArgs(in, cfg, "/run/mcp.json", nil)
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--model claude-opus-4-1") {
		t.Errorf("alias remap wrong: %q", joined)
	}
	// A light/background task ("haiku") maps onto the configured light model,
	// so cheap chores don't burn the big model.
	lightArgs := controlledClaudeArgs(&TaskInput{Model: "haiku"}, cfg, "/run/mcp.json", nil)
	if lj := strings.Join(lightArgs, " "); !strings.Contains(lj, "--model claude-haiku-4-5") {
		t.Errorf("haiku should map to the light model: %q", lj)
	}
	// An unmapped/empty config passes the task model through verbatim (e.g.
	// Claude resolves its own tier aliases).
	passArgs := controlledClaudeArgs(&TaskInput{Model: "sonnet"}, ExternalAgentConfig{}, "/run/mcp.json", nil)
	if pj := strings.Join(passArgs, " "); !strings.Contains(pj, "--model sonnet") {
		t.Errorf("empty config should pass task model through: %q", pj)
	}
}

func TestBuildClaudeUserMessage(t *testing.T) {
	// Text-only → plain string content.
	line, err := buildClaudeUserMessage("hello world", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("user message must be newline-terminated")
	}
	var m struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m.Type != "user" || m.Message.Role != "user" {
		t.Errorf("envelope wrong: %+v", m)
	}
	var s string
	if err := json.Unmarshal(m.Message.Content, &s); err != nil || s != "hello world" {
		t.Errorf("text content should be plain string %q, got %s", "hello world", m.Message.Content)
	}

	// With an image → content-block array with text + image blocks.
	line, err = buildClaudeUserMessage("look", []ImageAttachment{{MediaType: "image/png", DataBase64: "AAAA"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(m.Message.Content, &blocks); err != nil {
		t.Fatalf("content should be an array: %v", err)
	}
	if len(blocks) != 2 || blocks[0]["type"] != "text" || blocks[1]["type"] != "image" {
		t.Fatalf("blocks wrong: %+v", blocks)
	}
	src, ok := blocks[1]["source"].(map[string]any)
	if !ok || src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AAAA" {
		t.Errorf("image source wrong: %+v", blocks[1])
	}
}

func TestChildEnv_UsesSubscriptionAllowlist(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "user-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://my-proxy.example/anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "user-own-key")
	t.Setenv("YANSU_HOST_BRIDGE_TOKEN", "desktop-secret")
	t.Setenv("NODE_OPTIONS", "--require /tmp/injected.js")
	t.Setenv("PATH", "/usr/bin")
	r := NewExternalAgentRunner(ExternalAgentConfig{ExtraEnv: []string{"ANTHROPIC_API_KEY=override", "LANG=en_US.UTF-8"}})
	env := r.childEnv([]string{"OPENAI_API_KEY=task-secret", "LANG=zh_CN.UTF-8"})
	joined := strings.Join(env, "\n")
	for _, excluded := range []string{"ANTHROPIC_", "OPENAI_", "YANSU_", "NODE_OPTIONS="} {
		if strings.Contains(joined, excluded) {
			t.Errorf("credential/config leaked: %s", excluded)
		}
	}
	for _, wanted := range []string{"PATH=/usr/bin", "LANG=zh_CN.UTF-8", "DISABLE_TELEMETRY=1"} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("missing allowed env: %s", wanted)
		}
	}
}

func TestExternalRunnerFailsClosedBeforeStartingShadowExecution(t *testing.T) {
	runner := NewExternalAgentRunner(ExternalAgentConfig{Format: "codex"})
	callbackCalled := false
	runID, err := runner.ExecuteTask(context.Background(), &TaskInput{
		Prompt:        "attempt a state-changing task",
		ExecutionMode: authority.ExecutionModeShadow,
	}, func(*StreamEvent) error {
		callbackCalled = true
		return nil
	})
	if !errors.Is(err, ErrShadowRuntimeRequiresNative) {
		t.Fatalf("error = %v, want ErrShadowRuntimeRequiresNative", err)
	}
	if runID != "" {
		t.Fatalf("run id = %q, want empty", runID)
	}
	if callbackCalled {
		t.Fatal("external runner emitted events before failing closed")
	}
}

func TestExternalRunnerFailsClosedForUnknownExecutionMode(t *testing.T) {
	runner := NewExternalAgentRunner(ExternalAgentConfig{Format: "codex"})
	callbackCalled := false
	runID, err := runner.ExecuteTask(context.Background(), &TaskInput{
		Prompt:        "attempt a state-changing task",
		ExecutionMode: authority.ExecutionMode("future-mode"),
	}, func(*StreamEvent) error {
		callbackCalled = true
		return nil
	})
	if !errors.Is(err, ErrShadowRuntimeRequiresNative) {
		t.Fatalf("error = %v, want ErrShadowRuntimeRequiresNative", err)
	}
	if runID != "" {
		t.Fatalf("run id = %q, want empty", runID)
	}
	if callbackCalled {
		t.Fatal("external runner emitted events before failing closed")
	}
}

func TestExternalRunnerFailsClosedForBackgroundLearning(t *testing.T) {
	for _, format := range []string{"claude-code", "codex"} {
		t.Run(format, func(t *testing.T) {
			runner := NewExternalAgentRunner(ExternalAgentConfig{Format: format})
			callbackCalled := false
			runID, err := runner.ExecuteTask(context.Background(), &TaskInput{
				Prompt:      "merge these memory entries",
				BackendHint: BackendHintLearning,
			}, func(*StreamEvent) error {
				callbackCalled = true
				return nil
			})
			if !errors.Is(err, ErrLearningRunsRequireNative) {
				t.Fatalf("error = %v, want ErrLearningRunsRequireNative", err)
			}
			if runID != "" {
				t.Fatalf("run id = %q, want empty", runID)
			}
			if callbackCalled {
				t.Fatal("external runner emitted events before failing closed")
			}
		})
	}
}

func TestExternalRunnerFailsClosedForScopedInitiativeAuthority(t *testing.T) {
	runner := NewExternalAgentRunner(ExternalAgentConfig{Format: "codex"})
	callbackCalled := false
	runID, err := runner.ExecuteTask(context.Background(), &TaskInput{
		Prompt:                "execute an approved initiative",
		AuthorityInitiativeID: "initiative-1",
		AuthorityGrants: []authority.Grant{{
			ID:         "grant-1",
			Capability: "filesystem.write",
		}},
	}, func(*StreamEvent) error {
		callbackCalled = true
		return nil
	})
	if !errors.Is(err, ErrAuthorityRuntimeRequiresNative) {
		t.Fatalf("error = %v, want ErrAuthorityRuntimeRequiresNative", err)
	}
	if runID != "" {
		t.Fatalf("run id = %q, want empty", runID)
	}
	if callbackCalled {
		t.Fatal("external runner emitted events before failing closed")
	}
}

func TestAssistantErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string", `"stream error: broken pipe"`, "stream error: broken pipe"},
		{"object", `{"message":"model not found"}`, "model not found"},
		{"object_no_message", `{"error":"some error"}`, `{"error":"some error"}`},
		{"empty", ``, ""},
		{"null", `null`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assistantErrorMessage(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("assistantErrorMessage(%s) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
