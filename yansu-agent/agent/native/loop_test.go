package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"yansu-agent/agent/tools"
)

// This file covers loop-level behavior with a mock Anthropic server
// fronting the Client. The focus is the silent-turn recovery path: when
// the model ends a turn with no text and no tool_use (e.g. Sonnet +
// extended thinking after an underwhelming tool_result), the loop must
// send exactly one recovery nudge before giving up — otherwise the user
// sees "agent went silent, no answer" which was a real production bug.

// scriptedServer replays a fixed sequence of SSE responses, one per
// incoming /v1/messages request. Tests stage the response bodies that
// represent each turn; the server pops the next one on each request.
// Requests past the scripted end return an empty 500 so the loop fails
// loud rather than hanging.
type scriptedServer struct {
	t         *testing.T
	responses [][]byte
	seen      int32
}

func newScriptedServer(t *testing.T, responses ...[]byte) *httptest.Server {
	t.Helper()
	s := &scriptedServer{t: t, responses: responses}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := atomic.AddInt32(&s.seen, 1) - 1
		if int(idx) >= len(s.responses) {
			http.Error(w, "too many requests — loop did not terminate", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.responses[idx])
	}))
}

// sseEventsFor is a tiny DSL for building SSE turn bodies.
type sseBuilder struct {
	b strings.Builder
}

func newSSE() *sseBuilder { return &sseBuilder{} }

func (s *sseBuilder) event(name, data string) *sseBuilder {
	fmt.Fprintf(&s.b, "event: %s\ndata: %s\n\n", name, data)
	return s
}

func (s *sseBuilder) messageStart() *sseBuilder {
	return s.messageStartWithUsage(10, 1)
}

func (s *sseBuilder) messageStartWithUsage(inputTokens, outputTokens int) *sseBuilder {
	return s.event("message_start",
		fmt.Sprintf(
			`{"type":"message_start","message":{"id":"msg","model":"claude","role":"assistant","content":[],"usage":{"input_tokens":%d,"output_tokens":%d}}}`,
			inputTokens,
			outputTokens,
		))
}

func (s *sseBuilder) messageStartWithContainer(containerID string) *sseBuilder {
	payload, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":      "msg",
			"model":   "claude",
			"role":    "assistant",
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 1,
			},
			"container": map[string]any{"id": containerID},
		},
	})
	return s.event("message_start", string(payload))
}

func (s *sseBuilder) messageStartWithTopLevelContainer(containerID string) *sseBuilder {
	payload, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":      "msg",
			"model":   "claude",
			"role":    "assistant",
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 1,
			},
		},
		"container": map[string]any{"id": containerID},
	})
	return s.event("message_start", string(payload))
}

func (s *sseBuilder) textBlock(text string) *sseBuilder {
	return s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`).
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text)).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
}

func (s *sseBuilder) thinkingBlock(text string) *sseBuilder {
	return s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`).
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":%q}}`, text)).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
}

func (s *sseBuilder) signedThinkingBlock(text, signature string) *sseBuilder {
	return s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`).
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":%q}}`, text)).
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":%q}}`, signature)).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
}

// toolUseBlock emits a tool_use with empty input (no partial_json deltas).
// The loop's parseToolInput treats empty as "no args" which is fine when
// the tool doesn't require inputs.
func (s *sseBuilder) toolUseBlock(name, id string) *sseBuilder {
	return s.toolUseBlockWithInput(name, id, "{}")
}

// toolUseBlockWithInput emits a tool_use with a specific raw partial_json
// payload. Lets tests drive scenarios where the same tool is invoked with
// different inputs across turns (e.g. exercising the same-input guard).
func (s *sseBuilder) toolUseBlockWithInput(name, id, partialJSON string) *sseBuilder {
	deltaPayload, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 1,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	})
	return s.event("content_block_start",
		fmt.Sprintf(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":%q,"name":%q}}`, id, name)).
		event("content_block_delta", string(deltaPayload)).
		event("content_block_stop", `{"type":"content_block_stop","index":1}`)
}

func (s *sseBuilder) serverToolUseBlockWithInput(id, name, partialJSON string) *sseBuilder {
	deltaPayload, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	})
	return s.event("content_block_start",
		fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":%q,"name":%q}}`, id, name)).
		event("content_block_delta", string(deltaPayload)).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
}

func (s *sseBuilder) programmaticToolUseBlockWithInput(name, id, callerToolID, partialJSON string) *sseBuilder {
	deltaPayload, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 1,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	})
	startPayload, _ := json.Marshal(map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type": "tool_use",
			"id":   id,
			"name": name,
			"caller": map[string]any{
				"type":    ProgrammaticToolCaller,
				"tool_id": callerToolID,
			},
		},
	})
	return s.event("content_block_start", string(startPayload)).
		event("content_block_delta", string(deltaPayload)).
		event("content_block_stop", `{"type":"content_block_stop","index":1}`)
}

func (s *sseBuilder) messageDeltaStop(reason string) *sseBuilder {
	return s.event("message_delta",
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"input_tokens":0,"output_tokens":5}}`, reason))
}

func (s *sseBuilder) messageStop() *sseBuilder {
	return s.event("message_stop", `{"type":"message_stop"}`)
}

func (s *sseBuilder) build() []byte { return []byte(s.b.String()) }

// stubNoOpTool satisfies the tools.Tool interface with a no-op for the
// test — the loop only cares that a tool by this name exists in the
// registry, Execute returning empty content is fine.
type stubNoOpTool struct{ name string }

func (s *stubNoOpTool) Name() string        { return s.name }
func (s *stubNoOpTool) Description() string { return "stub" }
func (s *stubNoOpTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *stubNoOpTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (s *stubNoOpTool) Execute(_ context.Context, _ tools.ToolCall) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "ok"}, nil
}

// collectEvents runs a LoopConfig with the scripted server and returns
// every LoopEvent that the loop emitted, plus any error.
func runScriptedLoop(t *testing.T, responses ...[]byte) ([]LoopEvent, error) {
	t.Helper()
	srv := newScriptedServer(t, responses...)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client: client,
		// No registry means no tools. That's what we want for the
		// nudge tests: we're testing the text-vs-no-text termination
		// path, not tool dispatch.
		NoTools: true,
		RunID:   "test-run",
		ModelID: "claude-test",
		Prompt:  "irrelevant",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	return events, err
}

// eventTypeCounts returns a histogram of event.Type seen across the
// collected LoopEvents, plus the concatenated assistant text.
func eventTypeCounts(events []LoopEvent) (map[string]int, string) {
	counts := map[string]int{}
	var text strings.Builder
	for _, e := range events {
		counts[e.Type]++
		if e.Type == "assistant" {
			text.WriteString(e.Content)
		}
	}
	return counts, text.String()
}

func TestLoopMaxTotalTokensClampsOutputAndStopsBeforeTools(t *testing.T) {
	overBudgetTurn := newSSE().
		messageStartWithUsage(5_000, 1).
		toolUseBlock("Probe", "toolu-budget").
		messageDeltaStop("tool_use").
		messageStop().
		build()
	var requestMaxTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requestMaxTokens = request.MaxTokens
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(overBudgetTurn)
	}))
	defer srv.Close()

	var calls atomic.Int32
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Probe",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			calls.Add(1)
			return tools.ToolResult{Content: "should not execute"}, nil
		},
	})
	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	err := Run(context.Background(), &LoopConfig{
		Client:         client,
		Registry:       registry,
		RunID:          "token-budget-run",
		ModelID:        "claude-test",
		Prompt:         "Perform one bounded probe.",
		MaxTokens:      1_024,
		MaxTotalTokens: 4_096,
		Emit: func(event LoopEvent) {
			events = append(events, event)
		},
	})
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("Run error = %v, want token budget exceeded", err)
	}
	if requestMaxTokens <= 0 || requestMaxTokens >= 4_096 {
		t.Fatalf("request max_tokens = %d, want a positive reserved allowance", requestMaxTokens)
	}
	if calls.Load() != 0 {
		t.Fatalf("over-budget model turn executed %d tool calls", calls.Load())
	}
	var doneUsage *Usage
	for _, event := range events {
		if event.Type == "done" {
			doneUsage = event.Usage
		}
	}
	if doneUsage == nil || EstimateRequestTokens(doneUsage) != 5_006 {
		t.Fatalf("aggregate done usage = %+v, want 5006 tokens", doneUsage)
	}
}

func TestLoopMaxTotalTokensRejectsOversizedInputBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "request must not be sent", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Run(context.Background(), &LoopConfig{
		Client:         NewClient(WithBaseURL(srv.URL)),
		NoTools:        true,
		RunID:          "oversized-input-run",
		ModelID:        "claude-test",
		Prompt:         strings.Repeat("bounded context ", 64),
		MaxTokens:      1_024,
		MaxTotalTokens: 512,
		Emit:           func(LoopEvent) {},
	})
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("Run error = %v, want token budget exceeded", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("oversized input sent %d requests", requests.Load())
	}
}

// TestLoop_SilentTurnTriggersNudge reproduces the production bug: turn 1
// emits only thinking + stop_reason=end_turn, no text, no tool_use. The
// loop must not accept that as a completed answer — it sends a recovery
// nudge, turn 2 then produces real text, and the loop emits done.
func TestLoop_SilentTurnTriggersNudge(t *testing.T) {
	silentTurn := newSSE().
		messageStart().
		thinkingBlock("I'm thinking but won't produce a final answer...").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	recoveryTurn := newSSE().
		messageStart().
		textBlock("Here is the final answer.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	events, err := runScriptedLoop(t, silentTurn, recoveryTurn)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q", counts, text)

	if !strings.Contains(text, "Here is the final answer") {
		t.Errorf("recovery turn's text did not reach the callback; got %q", text)
	}
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d (counts=%v)", counts["done"], counts)
	}
	if counts["error"] != 0 {
		t.Errorf("unexpected error events on a recovered run: %d (counts=%v)", counts["error"], counts)
	}
}

// TestLoop_SilentThenSilentFails: the loop allows up to 2 nudges per
// run before giving up. Three consecutive silent turns must trigger an
// error event (not spin to MaxTurns and not emit a confusing empty
// done). Two silent turns are no longer enough to fail — see also
// TestLoop_TwoSilentTurnsBothGetNudged for the in-between case.
func TestLoop_SilentThenSilentFails(t *testing.T) {
	silentTurn := newSSE().
		messageStart().
		thinkingBlock("still thinking, still no answer").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	// Three silent turns: nudge after #1, nudge after #2, give up after #3.
	events, err := runScriptedLoop(t, silentTurn, silentTurn, silentTurn)
	if err == nil {
		t.Fatalf("RunLoop err = nil, want non-nil silent-turn failure")
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q", counts, text)

	if counts["error"] == 0 {
		t.Errorf("expected an error event after three silent turns; counts=%v", counts)
	}
	if strings.TrimSpace(text) != "" {
		t.Errorf("expected no assistant text when all turns were silent; got %q", text)
	}
}

// TestLoop_NudgeDisablesThinking: the nudge response turn must not
// carry the thinking config. Sonnet with extended thinking enabled
// will otherwise just produce another silent thinking-only turn,
// ignoring the recovery prompt — the exact failure mode the nudge
// is meant to break out of.
func TestLoop_NudgeDisablesThinking(t *testing.T) {
	silentTurn := newSSE().
		messageStart().
		thinkingBlock("thinking, no answer").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	recoveryTurn := newSSE().
		messageStart().
		textBlock("Final answer.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{silentTurn, recoveryTurn}
	requestBodies := make([][]byte, 0, 2)
	var reqCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:          client,
		NoTools:         true,
		ThinkingEnabled: true,
		RunID:           "test-nudge-thinking",
		ModelID:         "claude-test",
		Prompt:          "do something",
		MaxTokens:       1024,
		Emit:            func(LoopEvent) {},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run err = %v", err)
	}

	if len(requestBodies) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(requestBodies))
	}

	// Request 1 (initial turn): thinking should be enabled.
	var first map[string]any
	if err := json.Unmarshal(requestBodies[0], &first); err != nil {
		t.Fatalf("request[0] decode: %v", err)
	}
	if _, ok := first["thinking"]; !ok {
		t.Errorf("request[0]: expected thinking config when ThinkingEnabled=true")
	}

	// Request 2 (nudge response turn): thinking MUST be omitted so the
	// model can't go silent in another thinking-only turn.
	var second map[string]any
	if err := json.Unmarshal(requestBodies[1], &second); err != nil {
		t.Fatalf("request[1] decode: %v", err)
	}
	if _, ok := second["thinking"]; ok {
		t.Errorf("request[1]: thinking config should be DISABLED on the nudge response, got %v", second["thinking"])
	}
}

// TestLoop_TwoSilentTurnsBothGetNudged: two silent turns in a row
// should both receive a recovery nudge (not give up after the first).
// This is the user-visible failure mode where a long tool-using session
// goes silent twice — the old single-nudge cap meant the second silence
// killed the run with no more help.
func TestLoop_TwoSilentTurnsBothGetNudged(t *testing.T) {
	silentTurn := newSSE().
		messageStart().
		thinkingBlock("thinking, no answer yet").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	recoveryTurn := newSSE().
		messageStart().
		textBlock("Here is the final answer.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	// silent #1 → nudge #1; silent #2 → nudge #2; recovery turn produces text → done.
	events, err := runScriptedLoop(t, silentTurn, silentTurn, recoveryTurn)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q", counts, text)

	if !strings.Contains(text, "Here is the final answer") {
		t.Errorf("recovery text missing — second nudge was probably skipped; got %q", text)
	}
	if counts["error"] != 0 {
		t.Errorf("expected no error events when second nudge succeeded; counts=%v", counts)
	}
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d", counts["done"])
	}
}

// TestLoop_ToolProgressResetsSilentNudges covers generation-style tasks:
// an early pair of silent turns may consume both recovery nudges before
// the model starts writing files. Once a tool call succeeds, later
// silent turns should get their own recovery budget instead of failing
// immediately with stale nudge state.
func TestLoop_ToolProgressResetsSilentNudges(t *testing.T) {
	silentTurn := newSSE().
		messageStart().
		thinkingBlock("thinking without visible work").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	toolTurn := newSSE().
		messageStart().
		toolUseBlock("NoOp", "toolu_noop").
		messageDeltaStop("tool_use").
		messageStop().
		build()
	recoveryTurn := newSSE().
		messageStart().
		textBlock("Completed after reset.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	srv := newScriptedServer(t, silentTurn, silentTurn, toolTurn, silentTurn, recoveryTurn)
	t.Cleanup(srv.Close)

	reg := tools.NewRegistry()
	reg.Register(&stubNoOpTool{name: "NoOp"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: reg,
		RunID:    "test-nudge-reset",
		ModelID:  "claude-test",
		Prompt:   "generate files",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}

	counts, text := eventTypeCounts(events)
	if counts["error"] != 0 {
		t.Errorf("expected no error after tool progress reset; counts=%v", counts)
	}
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d", counts["done"])
	}
	if !strings.Contains(text, "Completed after reset") {
		t.Errorf("expected recovery text after reset; got %q", text)
	}
}

func TestLoop_ReplaysThinkingSignatureAfterToolUse(t *testing.T) {
	const wantSignature = "sig_test_123"

	toolTurn := newSSE().
		messageStart().
		signedThinkingBlock("I need to fetch data first.", wantSignature).
		toolUseBlock("NoOp", "toolu_signed_thinking").
		messageDeltaStop("tool_use").
		messageStop().
		build()
	finalTurn := newSSE().
		messageStart().
		textBlock("Done.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{toolTurn, finalTurn}
	requestBodies := make([][]byte, 0, 2)
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&stubNoOpTool{name: "NoOp"})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:          client,
		Registry:        registry,
		ThinkingEnabled: true,
		RunID:           "test-thinking-signature",
		ModelID:         "claude-test",
		Prompt:          "fetch data",
		MaxTokens:       1024,
		Emit:            func(LoopEvent) {},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 2 {
		t.Fatalf("expected at least 2 outbound requests, got %d", len(requestBodies))
	}

	var second struct {
		Messages []struct {
			Role    string         `json:"role"`
			Content []ContentBlock `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(requestBodies[1], &second); err != nil {
		t.Fatalf("decode second request: %v\nbody=%s", err, truncateForLog(string(requestBodies[1]), 600))
	}
	for _, msg := range second.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "thinking" {
				continue
			}
			if block.Signature != wantSignature {
				t.Fatalf("thinking signature = %q, want %q (assistant blocks=%+v)", block.Signature, wantSignature, msg.Content)
			}
			return
		}
	}
	t.Fatalf("second request did not replay an assistant thinking block: %s", truncateForLog(string(requestBodies[1]), 600))
}

func TestRunOneTurn_DropsUnsignedThinkingFromReplay(t *testing.T) {
	body := newSSE().
		messageStart().
		thinkingBlock("unsigned thinking should stay UI-only").
		toolUseBlock("NoOp", "toolu_unsigned_thinking").
		messageDeltaStop("tool_use").
		messageStop().
		build()

	srv := newScriptedServer(t, body)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var thinkingEvents int
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-unsigned-thinking",
		ModelID:   "claude-test",
		MaxTokens: 1024,
		Emit: func(ev LoopEvent) {
			if ev.Type == "thinking" {
				thinkingEvents++
			}
		},
	}
	req := &Request{Model: "claude-test", Messages: []Message{}, MaxTokens: 1024}
	result, err := runOneTurn(context.Background(), cfg, req)
	if err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}
	if thinkingEvents == 0 {
		t.Fatal("expected unsigned thinking to still stream to the UI")
	}
	for _, block := range result.AssistantBlocks {
		if block.Type == "thinking" {
			t.Fatalf("unsigned thinking should not be replayed to Anthropic: %+v", result.AssistantBlocks)
		}
	}
}

// TestLoop_MaxTokensWithText_AutoContinues: the model produced visible
// text and then hit max_tokens mid-stream. Instead of stopping with an
// error and forcing the user to type "continue", the loop now pushes a
// "Resume directly" nudge and continues automatically. Capped at 3
// continuation attempts. This test scripts two truncated turns then a
// clean finish and asserts the loop walked through both nudges and
// surfaced the final answer.
func TestLoop_MaxTokensWithText_AutoContinues(t *testing.T) {
	truncatedTurn := func(text string) []byte {
		return newSSE().
			messageStart().
			textBlock(text).
			messageDeltaStop("max_tokens").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("...and that's the conclusion.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	events, err := runScriptedLoop(t,
		truncatedTurn("partial answer that got cut off mid-"),
		truncatedTurn("continuing the answer but cut off again-"),
		finalTurn,
	)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("counts=%v text=%q", counts, text)

	// No error event — auto-continuation absorbed both truncations.
	if counts["error"] != 0 {
		t.Errorf("expected no error event when continuation succeeds; counts=%v", counts)
	}
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d", counts["done"])
	}
	// Combined text should include all three pieces because every
	// text-delta is forwarded to the callback.
	for _, want := range []string{"partial answer", "continuing the answer", "and that's the conclusion"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected text to contain %q; got %q", want, text)
		}
	}
}

// TestLoop_MaxTokensWithText_GivesUpAfterCap: when continuation budget
// is exhausted, the loop emits an error event describing the truncation
// alongside done so the caller can render a "response was cut off"
// notice. 4 truncated turns in a row → 3 nudges → still truncated →
// surface the error.
func TestLoop_MaxTokensWithText_GivesUpAfterCap(t *testing.T) {
	truncated := newSSE().
		messageStart().
		textBlock("never finishes-").
		messageDeltaStop("max_tokens").
		messageStop().
		build()

	// Initial turn + 3 continuation attempts = 4 scripted truncations.
	events, err := runScriptedLoop(t, truncated, truncated, truncated, truncated)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, _ := eventTypeCounts(events)
	if counts["error"] == 0 {
		t.Errorf("expected truncation error event after exhausting continuations; counts=%v", counts)
	}
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d", counts["done"])
	}
}

// TestLoop_MaxTokensWithoutText surfaces a specific stop_reason case:
// the model hit max_tokens before any text block streamed out. The loop
// must emit a clear error describing the truncation rather than sending
// a confusing recovery nudge (which would just produce a second
// truncated turn).
func TestLoop_MaxTokensWithoutText(t *testing.T) {
	truncated := newSSE().
		messageStart().
		thinkingBlock("burning all my output budget on thinking").
		messageDeltaStop("max_tokens").
		messageStop().
		build()

	events, err := runScriptedLoop(t, truncated)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, _ := eventTypeCounts(events)
	if counts["error"] == 0 {
		t.Errorf("expected an error event for max_tokens without text; counts=%v", counts)
	}
	// Should NOT nudge — max_tokens means the NEXT turn would hit the
	// same limit. Exactly one request should have fired.
	// Exactly one request means the scripted server only saw response[0].
	// We can't easily assert the count here without exposing the server,
	// but if the loop had nudged the server would serve the same response
	// again — that's an acceptable behavior too. The key invariant is
	// that an error event fires on the first silent+max_tokens turn.
}

// TestLoop_TrulyEmptyTurnNilContentFix guards against the regression where
// a completely silent turn (message_start → message_stop, zero content
// blocks) caused runOneTurn to return AssistantBlocks = nil. Appending
// Message{Content: nil} into the history then serialised as JSON null,
// which the Anthropic API rejects with a 400 on the NEXT turn's request.
//
// The fix initialises assistantBlocks as []ContentBlock{} so the silent
// turn's assistant message serialises as {"content": []} rather than
// {"content": null}. This test uses a validating scripted server that
// mirrors the Anthropic API's rejection of null content to confirm the
// fix is in place.
func TestLoop_TrulyEmptyTurnNilContentFix(t *testing.T) {
	// A turn with no content blocks whatsoever — not even thinking.
	// This is the minimal SSE response that causes AssistantBlocks = nil
	// without the fix.
	bareStop := newSSE().
		messageStart().
		messageDeltaStop("end_turn").
		messageStop().
		build()

	recovery := newSSE().
		messageStart().
		textBlock("Here is my answer.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{bareStop, recovery}
	var reqCount int32

	// A validating server that rejects any request where a message's
	// content field is JSON null — exactly what the Anthropic API does.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1

		// Parse the request body to check for null content.
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			for i, msg := range body.Messages {
				if bytes.Equal(msg.Content, []byte("null")) {
					http.Error(w, fmt.Sprintf(
						`{"type":"error","error":{"type":"invalid_request_error","message":"messages.%d.content: Input should be a valid list"}}`,
						i,
					), http.StatusBadRequest)
					return
				}
			}
		}

		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-nil-fix",
		ModelID:   "claude-test",
		Prompt:    "do something",
		MaxTokens: 1024,
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	counts, text := eventTypeCounts(events)
	t.Logf("events=%v text=%q", counts, text)

	if counts["error"] != 0 {
		t.Errorf("unexpected error events — null content in history likely caused a 400: counts=%v", counts)
	}
	if counts["done"] != 1 {
		t.Errorf("expected 1 done event, got %d", counts["done"])
	}
	if !strings.Contains(text, "Here is my answer") {
		t.Errorf("expected recovery text, got %q", text)
	}
}

// TestRunOneTurn_ToolUseEmitCarriesParsedInput guards the persistence
// path: the `tool_use` LoopEvent must fire AFTER the input has been
// streamed and parsed, with ToolInput populated. Previously it fired
// at content_block_start with no input, which left tool_use rows in
// the DB with an empty ToolInput column — and history reload then
// 400'd with "messages.N.content.0.tool_use.input: Field required"
// because Anthropic requires the input field to be a JSON object.
func TestRunOneTurn_ToolUseEmitCarriesParsedInput(t *testing.T) {
	body := newSSE().
		messageStart().
		event("content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_x1","name":"Bash"}}`).
		event("content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`).
		event("content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`).
		messageDeltaStop("tool_use").
		messageStop().
		build()

	srv := newScriptedServer(t, body)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-emit",
		ModelID:   "claude-test",
		MaxTokens: 1024,
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
	}
	req := &Request{Model: "claude-test", Messages: []Message{}, MaxTokens: 1024}
	if _, err := runOneTurn(context.Background(), cfg, req); err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}

	var toolUseEvents []LoopEvent
	for _, ev := range events {
		if ev.Type == "tool_use" {
			toolUseEvents = append(toolUseEvents, ev)
		}
	}
	if len(toolUseEvents) != 1 {
		t.Fatalf("expected exactly 1 tool_use event, got %d: %+v", len(toolUseEvents), toolUseEvents)
	}
	ev := toolUseEvents[0]
	if ev.ToolName != "Bash" || ev.ToolUseID != "toolu_x1" {
		t.Errorf("event ids wrong: name=%q id=%q", ev.ToolName, ev.ToolUseID)
	}
	if ev.ToolInput == nil {
		t.Fatal("ToolInput is nil — persistence layer would store empty string")
	}
	m, ok := ev.ToolInput.(map[string]any)
	if !ok {
		t.Fatalf("ToolInput type = %T, want map[string]any", ev.ToolInput)
	}
	if m["command"] != "ls" {
		t.Errorf("ToolInput.command = %v, want \"ls\"", m["command"])
	}
}

// TestRunOneTurn_ToolUseNoInputDeltaStillEmitsObject covers the case
// where the model opens a tool_use block but sends no input deltas
// (e.g. a no-arg tool). The emit must still carry a non-nil input
// (empty {}) so persistence stores something the API will accept.
func TestRunOneTurn_ToolUseNoInputDeltaStillEmitsObject(t *testing.T) {
	body := newSSE().
		messageStart().
		event("content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_noargs","name":"NoArgTool"}}`).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`).
		messageDeltaStop("tool_use").
		messageStop().
		build()

	srv := newScriptedServer(t, body)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-noargs",
		ModelID:   "claude-test",
		MaxTokens: 1024,
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
	}
	req := &Request{Model: "claude-test", Messages: []Message{}, MaxTokens: 1024}
	if _, err := runOneTurn(context.Background(), cfg, req); err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}

	for _, ev := range events {
		if ev.Type != "tool_use" {
			continue
		}
		if ev.ToolInput == nil {
			t.Fatal("ToolInput is nil for no-arg tool — must default to {}")
		}
		m, ok := ev.ToolInput.(map[string]any)
		if !ok || len(m) != 0 {
			t.Errorf("ToolInput = %v (type %T), want empty map", ev.ToolInput, ev.ToolInput)
		}
		return
	}
	t.Fatal("no tool_use event emitted")
}

// TestEmitError_AppendsTraceID verifies the central error emit helper
// stamps the run's trace id BOTH structurally (LoopEvent.TraceID) and
// as a visible `[trace=<id>]` suffix on the Error string. The suffix
// is what makes user-reported failures jumpable to the matching
// PostHog event without having to ask the user for additional context.
func TestEmitError_AppendsTraceID(t *testing.T) {
	var captured LoopEvent
	cfg := &LoopConfig{
		TraceID: "trace-abc-123",
		Emit:    func(ev LoopEvent) { captured = ev },
	}
	emitError(cfg, "boom")
	if captured.Type != "error" {
		t.Fatalf("emit type = %q, want error", captured.Type)
	}
	if captured.TraceID != "trace-abc-123" {
		t.Errorf("structural TraceID = %q, want trace-abc-123", captured.TraceID)
	}
	want := "boom [trace=trace-abc-123]"
	if captured.Error != want {
		t.Errorf("error string = %q, want %q", captured.Error, want)
	}
}

// TestEmitError_OmitsSuffixWhenNoTrace: when cfg.TraceID is empty
// (legacy callers / NoOp test configs) we must NOT emit a noisy
// `[trace=]` suffix on the Error string.
func TestEmitError_OmitsSuffixWhenNoTrace(t *testing.T) {
	var captured LoopEvent
	cfg := &LoopConfig{
		Emit: func(ev LoopEvent) { captured = ev },
	}
	emitError(cfg, "boom")
	if captured.Error != "boom" {
		t.Errorf("error string = %q, want %q (no suffix)", captured.Error, "boom")
	}
	if captured.TraceID != "" {
		t.Errorf("structural TraceID = %q, want empty", captured.TraceID)
	}
}

// TestRunOneTurn_EmptyToolUseIDGetsSynthesized covers the sub2api /
// Kimi K2 proxy bug where content_block_start arrives with id="".
// The stream parser must synthesize a non-empty fallback id so the
// resulting assistant block is sendable on the next turn — without
// this, the next request 400s with `tool_call_id  is not found`
// (double-space template signature) and `agentloop.run.error` fires.
func TestRunOneTurn_EmptyToolUseIDGetsSynthesized(t *testing.T) {
	body := newSSE().
		messageStart().
		event("content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"","name":"Bash"}}`).
		event("content_block_stop", `{"type":"content_block_stop","index":0}`).
		messageDeltaStop("tool_use").
		messageStop().
		build()

	srv := newScriptedServer(t, body)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	tele := &fakeTelemetry{}
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-empty-id",
		TraceID:   "trace-empty-id",
		ModelID:   "claude-test",
		MaxTokens: 1024,
		Telemetry: tele,
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
	}
	req := &Request{Model: "claude-test", Messages: []Message{}, MaxTokens: 1024}
	res, err := runOneTurn(context.Background(), cfg, req)
	if err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}

	// Every emitted tool_use* event must carry a non-empty id.
	var sawToolUse bool
	for _, ev := range events {
		if ev.Type != "tool_use" && ev.Type != "tool_use_start" {
			continue
		}
		sawToolUse = true
		if ev.ToolUseID == "" {
			t.Errorf("emit type=%s carried empty ToolUseID — synth fallback didn't run", ev.Type)
		}
		if !strings.HasPrefix(ev.ToolUseID, "toolu_synth_") {
			t.Errorf("synth id has unexpected prefix: %q", ev.ToolUseID)
		}
	}
	if !sawToolUse {
		t.Fatal("no tool_use / tool_use_start event emitted")
	}

	// The block written into AssistantBlocks must also carry the
	// synthesized id — that's what gets persisted and re-sent next turn.
	if len(res.AssistantBlocks) != 1 || res.AssistantBlocks[0].Type != "tool_use" {
		t.Fatalf("expected single tool_use AssistantBlock, got %+v", res.AssistantBlocks)
	}
	if res.AssistantBlocks[0].ID == "" {
		t.Error("AssistantBlocks[0].ID still empty after synth")
	}
	// And the parallel ToolUses slice (used by executeToolUse) must
	// agree — otherwise the result block would carry the synth id but
	// the assistant block would carry "", de-pairing them.
	if len(res.ToolUses) != 1 {
		t.Fatalf("expected single tool use, got %d", len(res.ToolUses))
	}
	if res.ToolUses[0].ID != res.AssistantBlocks[0].ID {
		t.Errorf("ToolUses[0].ID=%q != AssistantBlocks[0].ID=%q", res.ToolUses[0].ID, res.AssistantBlocks[0].ID)
	}

	// The observation event must fire exactly once per empty-id
	// occurrence — this is how we'll quantify "upstream gave us empty
	// id" vs "tool_call_id-not-found 400 happened anyway" on the
	// dashboard. Without this assertion a refactor that silently drops
	// the Capture call (e.g. by moving the empty-id branch elsewhere)
	// would leave us blind to the upstream bug rate.
	var sawEmptyIDEvent bool
	for _, ev := range tele.events {
		if ev.event != "agentloop.upstream_empty_tool_use_id" {
			continue
		}
		sawEmptyIDEvent = true
		if ev.properties["tool_name"] != "Bash" {
			t.Errorf("event tool_name = %v, want Bash", ev.properties["tool_name"])
		}
		if ev.properties["trace_id"] != "trace-empty-id" {
			t.Errorf("event trace_id = %v, want trace-empty-id", ev.properties["trace_id"])
		}
		synth, _ := ev.properties["synthesized"].(string)
		if !strings.HasPrefix(synth, "toolu_synth_") {
			t.Errorf("event synthesized = %q, want toolu_synth_ prefix", synth)
		}
	}
	if !sawEmptyIDEvent {
		t.Error("expected agentloop.upstream_empty_tool_use_id telemetry event to fire")
	}
}

// TestPickKeyOverLimit is a pure-function check on the helper that
// drives the consecutive-error guard. Exercises the empty / under-limit
// / at-limit / above-limit paths.
func TestPickKeyOverLimit(t *testing.T) {
	if _, _, tripped := pickKeyOverLimit(map[string]int{}, 5); tripped {
		t.Errorf("empty map must not trip")
	}
	if _, _, tripped := pickKeyOverLimit(map[string]int{"WebFetch": 4}, 5); tripped {
		t.Errorf("4 under limit 5 must not trip")
	}
	name, streak, tripped := pickKeyOverLimit(map[string]int{"WebFetch": 5}, 5)
	if !tripped || name != "WebFetch" || streak != 5 {
		t.Errorf("at-limit case: name=%q streak=%d tripped=%v", name, streak, tripped)
	}
	name, streak, tripped = pickKeyOverLimit(map[string]int{"WebFetch": 7, "Bash": 0}, 5)
	if !tripped || name != "WebFetch" || streak != 7 {
		t.Errorf("over-limit picks the failing tool: name=%q streak=%d", name, streak)
	}
}

// alwaysErrorTool is a tools.Tool that always returns IsError=true. Used
// by TestLoop_StreakGuardInjectsStopNudge to drive the streak counter
// without needing real network failures.
type alwaysErrorTool struct{ name string }

func (a *alwaysErrorTool) Name() string        { return a.name }
func (a *alwaysErrorTool) Description() string { return "always errors for tests" }
func (a *alwaysErrorTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (a *alwaysErrorTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (a *alwaysErrorTool) Execute(_ context.Context, _ tools.ToolCall) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "[HTTP 404 Not Found] simulated", IsError: true}, nil
}

// TestLoop_StreakGuardInjectsStopNudge: when a tool returns IsError=true
// for `toolErrorStreakLimit` consecutive turns, the loop must inject a
// user-message asking the model to stop calling that tool. The
// assertion that matters is "the nudge text appears in the next
// outbound request body" — checking only the final `done` event would
// pass even with the guard removed, because the mock would still hand
// back the scripted final-text response in turn 6 regardless of what
// the loop appended. We therefore record every request body and verify
// the 6th one (sent after the streak trips) contains the nudge.
func TestLoop_StreakGuardInjectsStopNudge(t *testing.T) {
	// Each tool_use needs a unique block id AND a unique input — the
	// loop-guard would short-circuit identical-input retries at the
	// soft threshold (3) before the error streak (5) ever fires. Use
	// distinct fabricated URLs per call so only the error-streak guard
	// is in play, which is what this test is for.
	mkFailingTurn := func(id, url string) []byte {
		return newSSE().
			messageStart().
			toolUseBlockWithInput("WebFetch", id, fmt.Sprintf(`{"url":%q}`, url)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	// Final turn: model sees the streak-guard nudge and emits text.
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping fetches; here is what I have so far.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkFailingTurn("call-1", "https://a.example.com/article"),
		mkFailingTurn("call-2", "https://b.example.com/article"),
		mkFailingTurn("call-3", "https://c.example.com/article"),
		mkFailingTurn("call-4", "https://d.example.com/article"),
		mkFailingTurn("call-5", "https://e.example.com/article"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysErrorTool{name: "WebFetch"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-streak-guard",
		ModelID:  "claude-test",
		Prompt:   "fetch and report",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v (want clean termination after nudge)", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q request_count=%d", counts, text, atomic.LoadInt32(&reqCount))

	// First-order check: loop completed normally.
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event, got %d (counts=%v)", counts["done"], counts)
	}
	if !strings.Contains(text, "Stopping fetches") {
		t.Errorf("recovery turn's text did not reach the callback; got %q", text)
	}

	// Real assertion: the 6th request body (sent after the 5th IsError
	// completes the streak) must contain the stop-nudge text. If the
	// streak guard were removed the body would only contain assistant
	// + tool_result blocks for the 5 failing tool_use calls — the
	// nudge string would be absent and this assertion fires.
	if len(requestBodies) < 6 {
		t.Fatalf("expected at least 6 outbound requests, got %d", len(requestBodies))
	}
	sixth := string(requestBodies[5])
	if !strings.Contains(sixth, "Stop calling WebFetch now") {
		t.Errorf("6th request body missing streak-guard nudge; got body without 'Stop calling WebFetch now':\n%s", truncateForLog(sixth, 600))
	}
	// And the body must actually be carrying it as a user-role text
	// block, not as part of some other structure we accidentally
	// hashed against. Defensive check on the JSON shape.
	if !strings.Contains(sixth, `"role":"user"`) {
		t.Errorf("6th request body has no user-role message, expected the nudge to be appended as one")
	}

	// Conversely, requests 1–5 (sent before any streak trip) must NOT
	// contain the nudge — guards against a regression where we
	// accidentally inject the nudge on every turn.
	for i := 0; i < 5; i++ {
		if strings.Contains(string(requestBodies[i]), "Stop calling WebFetch now") {
			t.Errorf("request %d contained streak nudge before the streak completed", i+1)
		}
	}

	// Alternating-role check: every request body's `messages` array
	// must satisfy strict user/assistant alternation. Anthropic
	// requires this and the previous streak-guard implementation
	// shipped two consecutive user messages on the trip turn — the
	// proxy in production tolerated it, but it's a spec violation
	// that future API tightening (or a different provider) would
	// reject. The fix is to attach the nudge as an extra text block
	// inside the same user message that carries tool_results; this
	// assertion guards against regression.
	for i, body := range requestBodies {
		assertAlternatingRoles(t, i+1, body)
	}
}

// assertAlternatingRoles parses the JSON body of a /v1/messages POST
// and verifies messages[*].role strictly alternates user/assistant
// starting with user. Helper kept inline because no other test needs it.
func assertAlternatingRoles(t *testing.T, requestNum int, body []byte) {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Errorf("request %d: cannot decode messages: %v", requestNum, err)
		return
	}
	if len(parsed.Messages) == 0 {
		t.Errorf("request %d: empty messages array", requestNum)
		return
	}
	want := "user"
	for i, m := range parsed.Messages {
		if m.Role != want {
			t.Errorf("request %d: messages[%d].role = %q, want %q (full sequence: %s)",
				requestNum, i, m.Role, want, rolesOf(parsed.Messages))
			return
		}
		if want == "user" {
			want = "assistant"
		} else {
			want = "user"
		}
	}
}

// rolesOf is a tiny formatter for the alternating-role error message —
// flattens the role sequence so a failure prints "user→assistant→user→user"
// instead of an opaque struct dump.
func rolesOf(msgs []struct {
	Role string `json:"role"`
}) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		parts = append(parts, m.Role)
	}
	return strings.Join(parts, "→")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func lastMessageContentBlocks(t *testing.T, body []byte) []ContentBlock {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(parsed.Messages) == 0 {
		t.Fatalf("request has no messages")
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(parsed.Messages[len(parsed.Messages)-1].Content, &blocks); err != nil {
		t.Fatalf("decode last message content blocks: %v\nbody=%s", err, truncateForLog(string(body), 1000))
	}
	return blocks
}

// alwaysOkTool returns IsError=false with a fixed payload regardless of
// input. Lets a test drive the same-input streak guard without also
// tripping the error-streak guard.
type alwaysOkTool struct{ name string }

func (a *alwaysOkTool) Name() string        { return a.name }
func (a *alwaysOkTool) Description() string { return "always succeeds for tests" }
func (a *alwaysOkTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (a *alwaysOkTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (a *alwaysOkTool) Execute(_ context.Context, _ tools.ToolCall) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "fixed payload — same every time", IsError: false}, nil
}

// recordingTool is alwaysOkTool with a custom Execute hook so the test
// can observe which calls actually ran (vs were short-circuited by the
// loop guard). Used to assert the soft-block path skips execution.
type recordingTool struct {
	name   string
	execFn func(tools.ToolCall) (tools.ToolResult, error)
}

func (r *recordingTool) Name() string        { return r.name }
func (r *recordingTool) Description() string { return "records executions for tests" }
func (r *recordingTool) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (r *recordingTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (r *recordingTool) Execute(_ context.Context, call tools.ToolCall) (tools.ToolResult, error) {
	if r.execFn != nil {
		return r.execFn(call)
	}
	return tools.ToolResult{Content: "ok"}, nil
}

func TestLoop_ProgrammaticToolCallingAdvertisesCodeExecutionForBundledHelpers(t *testing.T) {
	finalTurn := newSSE().
		messageStart().
		textBlock("Done.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	var requestBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(finalTurn)
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "Bash"})
	registry.Register(&alwaysOkTool{name: "Read"})
	registry.Register(&alwaysOkTool{name: "WebFetch"})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-tool-defs",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run a bounded API probe",
		Emit:                    func(LoopEvent) {},
		MaxTokens:               1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}

	var parsed struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(requestBody, &parsed); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, truncateForLog(string(requestBody), 800))
	}
	if len(parsed.Tools) != 4 {
		t.Fatalf("expected code_execution + bundled helper tools, got %d: %#v", len(parsed.Tools), parsed.Tools)
	}

	sawCodeExecution := false
	sawProgrammaticHelper := map[string]bool{}
	for _, toolDef := range parsed.Tools {
		switch toolDef.Name {
		case codeExecutionToolName:
			sawCodeExecution = true
			if toolDef.Type != ProgrammaticToolCaller {
				t.Errorf("code_execution type = %q, want %q", toolDef.Type, ProgrammaticToolCaller)
			}
			if toolDef.InputSchema != nil {
				t.Errorf("code_execution should not carry a custom input_schema, got %#v", toolDef.InputSchema)
			}
		case "Bash", "Read", "WebFetch":
			sawProgrammaticHelper[toolDef.Name] = true
			if len(toolDef.AllowedCallers) != 1 || !containsString(toolDef.AllowedCallers, ProgrammaticToolCaller) {
				t.Errorf("%s allowed_callers = %#v, want only %s", toolDef.Name, toolDef.AllowedCallers, ProgrammaticToolCaller)
			}
		}
	}
	if !sawCodeExecution {
		t.Errorf("request missing code_execution tool: %#v", parsed.Tools)
	}
	for _, toolName := range []string{"Bash", "Read", "WebFetch"} {
		if !sawProgrammaticHelper[toolName] {
			t.Errorf("request missing %s tool: %#v", toolName, parsed.Tools)
		}
	}
}

func TestLoop_ProgrammaticToolCallingRequiresFlagSupportedModelAndBash(t *testing.T) {
	finalTurn := newSSE().
		messageStart().
		textBlock("Done.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	tests := []struct {
		name                     string
		programmaticEnabled      bool
		modelID                  string
		registerTool             string
		wantCodeExecution        bool
		wantRegisteredToolScoped bool
	}{
		{
			name:                "flag disabled",
			programmaticEnabled: false,
			modelID:             "claude-sonnet-4-6",
			registerTool:        "Bash",
		},
		{
			name:                "unsupported model",
			programmaticEnabled: true,
			modelID:             "claude-sonnet-4-4",
			registerTool:        "Bash",
		},
		{
			name:                "no programmatic-capable tool",
			programmaticEnabled: true,
			modelID:             "claude-sonnet-4-6",
			registerTool:        "NoOp",
		},
		{
			name:                     "enabled for supported model with Bash",
			programmaticEnabled:      true,
			modelID:                  "claude-sonnet-4-6",
			registerTool:             "Bash",
			wantCodeExecution:        true,
			wantRegisteredToolScoped: true,
		},
		{
			name:                     "enabled for supported model with WebFetch",
			programmaticEnabled:      true,
			modelID:                  "claude-sonnet-4-6",
			registerTool:             "WebFetch",
			wantCodeExecution:        true,
			wantRegisteredToolScoped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requestBody = body
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(finalTurn)
			}))
			t.Cleanup(srv.Close)

			registry := tools.NewRegistry()
			registry.Register(&alwaysOkTool{name: tt.registerTool})

			client := NewClient(WithBaseURL(srv.URL))
			cfg := &LoopConfig{
				Client:                  client,
				Registry:                registry,
				ProgrammaticToolCalling: tt.programmaticEnabled,
				RunID:                   "test-programmatic-tool-gating",
				ModelID:                 tt.modelID,
				Prompt:                  "run a bounded probe",
				Emit:                    func(LoopEvent) {},
				MaxTokens:               1024,
			}
			if err := Run(context.Background(), cfg); err != nil {
				t.Fatalf("RunLoop err = %v", err)
			}

			var parsed struct {
				Tools []ToolDef `json:"tools"`
			}
			if err := json.Unmarshal(requestBody, &parsed); err != nil {
				t.Fatalf("decode request: %v\nbody=%s", err, truncateForLog(string(requestBody), 800))
			}

			var sawCodeExecution bool
			var registeredAllowedCallers []string
			for _, toolDef := range parsed.Tools {
				switch toolDef.Name {
				case codeExecutionToolName:
					sawCodeExecution = true
				case tt.registerTool:
					registeredAllowedCallers = toolDef.AllowedCallers
				}
			}
			if sawCodeExecution != tt.wantCodeExecution {
				t.Fatalf("saw code_execution = %v, want %v; tools=%#v", sawCodeExecution, tt.wantCodeExecution, parsed.Tools)
			}
			gotRegisteredToolScoped := len(registeredAllowedCallers) == 1 && containsString(registeredAllowedCallers, ProgrammaticToolCaller)
			if gotRegisteredToolScoped != tt.wantRegisteredToolScoped {
				t.Fatalf("%s allowed_callers = %#v, want enabled=%v", tt.registerTool, registeredAllowedCallers, tt.wantRegisteredToolScoped)
			}
		})
	}
}

func TestLoop_DirectProgrammaticToolsGetBatchingNudge(t *testing.T) {
	mkBashTurn := func(id, command string) []byte {
		toolInput, _ := json.Marshal(map[string]any{
			"command":     command,
			"description": "Run one-off probe",
		})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("Bash", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Done after batching guidance.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkBashTurn("bash-a", "printf alpha"),
		mkBashTurn("bash-b", "printf beta"),
		mkBashTurn("bash-c", "printf gamma"),
		mkBashTurn("bash-d", "printf delta"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — direct Programmatic nudge did not settle", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "Bash"})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-direct-programmatic-nudge",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run several direct probes",
		Emit:                    func(LoopEvent) {},
		MaxTokens:               1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 5 {
		t.Fatalf("expected at least 5 outbound requests, got %d", len(requestBodies))
	}
	if strings.Contains(string(requestBodies[3]), "Programmatic is available for this task") {
		t.Fatalf("Programmatic batching nudge was injected before threshold:\n%s", truncateForLog(string(requestBodies[3]), 1200))
	}
	fifth := string(requestBodies[4])
	if !strings.Contains(fifth, "Programmatic is available for this task") {
		t.Fatalf("5th request body missing Programmatic batching nudge:\n%s", truncateForLog(fifth, 1200))
	}
	if !strings.Contains(fifth, "Bash=4 calls") {
		t.Fatalf("Programmatic batching nudge missing tool counts:\n%s", truncateForLog(fifth, 1200))
	}
}

func TestLoop_CuaDriverErrorStreakNudgesAfterThreeFailures(t *testing.T) {
	mkCuaTurn := func(id string) []byte {
		toolInput, _ := json.Marshal(map[string]any{
			"name": "screenshot",
			"args": map[string]any{},
		})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("cua_driver", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping CUA calls.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkCuaTurn("cua-a"),
		mkCuaTurn("cua-b"),
		mkCuaTurn("cua-c"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — CUA error streak did not settle", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "cua_driver",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: "cua-driver: missing \"out\"", IsError: true}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:    client,
		Registry:  registry,
		RunID:     "test-cua-error-streak",
		ModelID:   "claude-sonnet-4-6",
		Prompt:    "inspect Chrome",
		Emit:      func(LoopEvent) {},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 4 {
		t.Fatalf("expected at least 4 outbound requests, got %d", len(requestBodies))
	}
	fourth := string(requestBodies[3])
	if !strings.Contains(fourth, "3 consecutive cua_driver calls have failed") {
		t.Fatalf("4th request body missing early CUA stop nudge:\n%s", truncateForLog(fourth, 1200))
	}
}

func TestLoop_CuaDriverErrorStreakStopsWhenNudgeIgnored(t *testing.T) {
	mkCuaTurn := func(id string) []byte {
		toolInput, _ := json.Marshal(map[string]any{
			"name": "screenshot",
			"args": map[string]any{},
		})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("cua_driver", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	scripts := [][]byte{
		mkCuaTurn("cua-a"),
		mkCuaTurn("cua-b"),
		mkCuaTurn("cua-c"),
		mkCuaTurn("cua-d"),
		mkCuaTurn("cua-e"),
		mkCuaTurn("cua-f"),
	}

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — CUA hard stop did not fire", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "cua_driver",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: "cua-driver: missing \"out\"", IsError: true}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var errorText string
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-cua-error-streak-stops",
		ModelID:  "claude-sonnet-4-6",
		Prompt:   "inspect Chrome",
		Emit: func(ev LoopEvent) {
			if ev.Type == "error" {
				errorText = ev.Error
			}
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("RunLoop err = nil, want CUA error-streak hard stop")
	}
	if !errors.Is(err, ErrLoopGuardStuck) {
		t.Fatalf("RunLoop err = %v, want ErrLoopGuardStuck", err)
	}
	if !strings.Contains(errorText, "tool error guard stopped repeated failed cua_driver calls") {
		t.Fatalf("missing CUA tool-error hard stop event, got %q", errorText)
	}
	if got := atomic.LoadInt32(&reqCount); got != 6 {
		t.Fatalf("request count = %d, want 6", got)
	}
}

func TestLoop_UIAutomationBudgetStopsRunawayCuaPolling(t *testing.T) {
	mkCuaTurn := func(id string) []byte {
		toolInput, _ := json.Marshal(map[string]any{
			"name": "get_window_state",
			"args": map[string]any{"bundle": "com.google.Chrome"},
		})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("cua_driver", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	scripts := make([][]byte, 0, UIAutomationHardThreshold+1)
	for i := 0; i < UIAutomationHardThreshold+1; i++ {
		scripts = append(scripts, mkCuaTurn(fmt.Sprintf("cua-%02d", i)))
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — UI automation budget hard stop did not fire", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	var executed int32
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "cua_driver",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			atomic.AddInt32(&executed, 1)
			return tools.ToolResult{Content: `{"app":{"name":"Google Chrome"},"ui_elements":[]}`, IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var errorText string
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-ui-automation-budget",
		ModelID:  "claude-sonnet-4-6",
		Prompt:   "inspect Chrome repeatedly",
		Emit: func(ev LoopEvent) {
			if ev.Type == "error" {
				errorText = ev.Error
			}
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("RunLoop err = nil, want UI automation budget hard stop")
	}
	if !errors.Is(err, ErrLoopGuardStuck) {
		t.Fatalf("RunLoop err = %v, want ErrLoopGuardStuck", err)
	}
	if !strings.Contains(errorText, "UI automation budget guard stopped") {
		t.Fatalf("missing UI automation budget hard stop event, got %q", errorText)
	}
	if got := atomic.LoadInt32(&reqCount); got != UIAutomationHardThreshold+1 {
		t.Fatalf("request count = %d, want %d", got, UIAutomationHardThreshold+1)
	}
	// HardThreshold-1 model-driven cua_driver calls execute before the hard
	// stop, PLUS one system-level list_apps fetched by MaybeInjectInventory on
	// the first cua_driver result (the opening-map prelude). That inventory
	// fetch goes through registry.Execute directly, bypassing the loop's
	// tool_use accounting, so it does NOT consume the model's automation budget
	// — the hard stop still fires on the model's own call count — but the raw
	// executed counter here does see it, hence +1.
	if got := atomic.LoadInt32(&executed); got != UIAutomationHardThreshold {
		t.Fatalf("executed CUA calls = %d, want %d", got, UIAutomationHardThreshold)
	}
	if len(requestBodies) <= UIAutomationNudgeThreshold {
		t.Fatalf("expected request after nudge threshold, got %d bodies", len(requestBodies))
	}
	nudgedRequest := string(requestBodies[UIAutomationNudgeThreshold])
	if !strings.Contains(nudgedRequest, "GUI/browser automation tool calls") {
		t.Fatalf("request after nudge threshold missing UI automation nudge:\n%s", truncateForLog(nudgedRequest, 1200))
	}
}

func TestLoop_ProgrammaticToolCallingPreservesServerBlocksAndContainer(t *testing.T) {
	toolInput, _ := json.Marshal(map[string]any{
		"command":     "printf ok",
		"description": "Print ok",
	})
	firstTurn := newSSE().
		messageStartWithContainer("container_abc123").
		serverToolUseBlockWithInput("srvtoolu_1", codeExecutionToolName, `{"code":"result = Bash(command='printf ok', description='Print ok')"}`).
		programmaticToolUseBlockWithInput("Bash", "toolu_bash_1", "srvtoolu_1", string(toolInput)).
		messageDeltaStop("tool_use").
		messageStop().
		build()
	finalTurn := newSSE().
		messageStartWithContainer("container_abc123").
		textBlock("Finished from code execution.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	requestBodies := make([][]byte, 0, 2)
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if idx > 1 {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if idx == 0 {
			_, _ = w.Write(firstTurn)
			return
		}
		_, _ = w.Write(finalTurn)
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "Bash"})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-preserve",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run a bounded API probe",
		Emit:                    func(LoopEvent) {},
		MaxTokens:               1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 2 {
		t.Fatalf("expected at least 2 outbound requests, got %d", len(requestBodies))
	}
	second := string(requestBodies[1])
	for _, want := range []string{
		`"container":"container_abc123"`,
		`"type":"server_tool_use"`,
		`"caller":{"type":"code_execution_20260120","tool_id":"srvtoolu_1"}`,
		`"type":"tool_result"`,
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("second request missing %s:\n%s", want, truncateForLog(second, 1400))
		}
	}
}

func TestLoop_ProgrammaticToolCallingReusesTopLevelContainer(t *testing.T) {
	toolInput, _ := json.Marshal(map[string]any{
		"command":     "printf ok",
		"description": "Print ok",
	})
	firstTurn := newSSE().
		messageStartWithTopLevelContainer("container_top_level").
		serverToolUseBlockWithInput("srvtoolu_1", codeExecutionToolName, `{"code":"result = Bash(command='printf ok', description='Print ok')"}`).
		programmaticToolUseBlockWithInput("Bash", "toolu_bash_1", "srvtoolu_1", string(toolInput)).
		messageDeltaStop("tool_use").
		messageStop().
		build()
	finalTurn := newSSE().
		messageStartWithTopLevelContainer("container_top_level").
		textBlock("Finished from the existing container.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	requestBodies := make([][]byte, 0, 2)
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if idx > 1 {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if idx == 0 {
			_, _ = w.Write(firstTurn)
			return
		}
		_, _ = w.Write(finalTurn)
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "Bash"})

	client := NewClient(WithBaseURL(srv.URL))
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-top-level-container",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run a bounded API probe",
		Emit:                    func(LoopEvent) {},
		MaxTokens:               1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 2 {
		t.Fatalf("expected at least 2 outbound requests, got %d", len(requestBodies))
	}
	second := string(requestBodies[1])
	if !strings.Contains(second, `"container":"container_top_level"`) {
		t.Fatalf("second request did not reuse top-level container id:\n%s", truncateForLog(second, 1400))
	}
}

func TestLoop_NumericVariantGuardShortCircuitsBashSweep(t *testing.T) {
	mkTurn := func(id string, period int) []byte {
		command := fmt.Sprintf(`curl -s -X POST https://www.kimi.com/apiv2/kimi.gateway.billing.v1.BillingService/GetUsages -d '{"scope":[0],"period":%d}' | jq .`, period)
		toolInput, _ := json.Marshal(map[string]any{"command": command})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("Bash", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping the sweep.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkTurn("call-1", 78),
		mkTurn("call-2", 79),
		mkTurn("call-3", 80),
		mkTurn("call-4", 81),
		mkTurn("call-5", 82),
		mkTurn("call-6", 83),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — numeric variant guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: `{"error":"invalid period"}`, IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-numeric-variant-loop-guard",
		ModelID:  "claude-test",
		Prompt:   "find the right period",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(executed) != NumericVariantLoopThreshold {
		t.Fatalf("expected %d actual Bash executions before guard, got %d: %v", NumericVariantLoopThreshold, len(executed), executed)
	}
	if len(requestBodies) < 7 {
		t.Fatalf("expected at least 7 outbound requests, got %d", len(requestBodies))
	}
	seventh := string(requestBodies[6])
	if !strings.Contains(seventh, "[numeric-variant-loop-guard]") {
		t.Fatalf("7th request body missing numeric variant synthetic result:\n%s", truncateForLog(seventh, 1000))
	}
	if !strings.Contains(seventh, "Stop enumerating numeric parameters now") {
		t.Fatalf("7th request body missing numeric variant stop nudge:\n%s", truncateForLog(seventh, 1000))
	}
	for i := 0; i < 6; i++ {
		if strings.Contains(string(requestBodies[i]), "[numeric-variant-loop-guard]") {
			t.Errorf("request %d contained numeric variant guard before threshold", i+1)
		}
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping the sweep") {
		t.Errorf("expected final text after guard, got %q", text)
	}
	for i, body := range requestBodies {
		assertAlternatingRoles(t, i+1, body)
	}
}

func TestLoop_NumericVariantGuardStopsWhenNudgeIgnored(t *testing.T) {
	mkTurn := func(id string, period int) []byte {
		command := fmt.Sprintf(`curl -s -X POST https://www.kimi.com/apiv2/kimi.gateway.billing.v1.BillingService/GetUsages -d '{"scope":[0],"period":%d}' | jq .`, period)
		toolInput, _ := json.Marshal(map[string]any{"command": command})
		return newSSE().
			messageStart().
			toolUseBlockWithInput("Bash", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	scripts := [][]byte{
		mkTurn("call-1", 78),
		mkTurn("call-2", 79),
		mkTurn("call-3", 80),
		mkTurn("call-4", 81),
		mkTurn("call-5", 82),
		mkTurn("call-6", 83),
		mkTurn("call-7", 84),
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — numeric variant guard did not stop after ignored nudge", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: `{"error":"invalid period"}`, IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-numeric-variant-loop-guard-stops",
		ModelID:  "claude-test",
		Prompt:   "find the right period",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	if !errors.Is(err, ErrLoopGuardStuck) {
		t.Fatalf("RunLoop err = %v, want ErrLoopGuardStuck", err)
	}
	if len(executed) != NumericVariantLoopThreshold {
		t.Fatalf("expected %d actual Bash executions before guard, got %d: %v", NumericVariantLoopThreshold, len(executed), executed)
	}
	if len(requestBodies) != 7 {
		t.Fatalf("expected exactly 7 outbound requests before abort, got %d", len(requestBodies))
	}

	var errorText string
	for _, ev := range events {
		if ev.Type == "error" {
			errorText += ev.Error
		}
	}
	if strings.Contains(errorText, "max turns reached") {
		t.Fatalf("unexpected max-turns error after ignored numeric-variant nudge: %q", errorText)
	}
	if !strings.Contains(errorText, "numeric variant loop guard stopped repeated Bash numeric sweep after 6 similar attempts") {
		t.Fatalf("missing numeric loop-guard abort error, got %q", errorText)
	}
}

func TestLoop_SearchNarrowingGuardShortCircuitsEmptyBashSearches(t *testing.T) {
	mkTurn := func(id string, filter string) []byte {
		command := `find /repo -maxdepth 6 -name "*.go" 2>/dev/null | xargs grep -l "sub2api" 2>/dev/null | xargs grep -l "usage" 2>/dev/null | xargs grep -l "` + filter + `" 2>/dev/null | head -20`
		return newSSE().
			messageStart().
			toolUseBlockWithInput("Bash", id, bashInput(command)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping the empty search and using the available evidence.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkTurn("call-1", "model"),
		mkTurn("call-2", "token"),
		mkTurn("call-3", "provider"),
		mkTurn("call-4", "latency"),
		mkTurn("call-5", "duration"),
		mkTurn("call-6", "ratio"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — search narrowing guard did not fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: ""}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-search-narrowing-loop-guard",
		ModelID:  "claude-test",
		Prompt:   "find usage log fields",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(executed) != SearchNarrowingLoopThreshold {
		t.Fatalf("expected %d actual Bash executions before guard, got %d: %v", SearchNarrowingLoopThreshold, len(executed), executed)
	}
	if len(requestBodies) < 7 {
		t.Fatalf("expected at least 7 outbound requests, got %d", len(requestBodies))
	}
	seventh := string(requestBodies[6])
	if !strings.Contains(seventh, "[search-narrowing-loop-guard]") {
		t.Fatalf("7th request body missing search narrowing synthetic result:\n%s", truncateForLog(seventh, 1200))
	}
	if !strings.Contains(seventh, "Stop narrowing the same empty search now") {
		t.Fatalf("7th request body missing search narrowing stop nudge:\n%s", truncateForLog(seventh, 1200))
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping the empty search") {
		t.Errorf("expected final text after guard, got %q", text)
	}
}

func TestLoop_SearchNarrowingGuardStopsWhenNudgeIgnored(t *testing.T) {
	mkTurn := func(id string, filter string) []byte {
		command := `find /repo -maxdepth 6 -name "*.go" 2>/dev/null | xargs grep -l "sub2api" 2>/dev/null | xargs grep -l "usage" 2>/dev/null | xargs grep -l "` + filter + `" 2>/dev/null | head -20`
		return newSSE().
			messageStart().
			toolUseBlockWithInput("Bash", id, bashInput(command)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	scripts := [][]byte{
		mkTurn("call-1", "model"),
		mkTurn("call-2", "token"),
		mkTurn("call-3", "provider"),
		mkTurn("call-4", "latency"),
		mkTurn("call-5", "duration"),
		mkTurn("call-6", "ratio"),
		mkTurn("call-7", "time"),
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — search narrowing guard did not stop after ignored nudge", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: ""}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-search-narrowing-loop-guard-stops",
		ModelID:  "claude-test",
		Prompt:   "find usage log fields",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	if !errors.Is(err, ErrLoopGuardStuck) {
		t.Fatalf("RunLoop err = %v, want ErrLoopGuardStuck", err)
	}
	if len(requestBodies) != 7 {
		t.Fatalf("expected exactly 7 outbound requests before abort, got %d", len(requestBodies))
	}

	var errorText string
	for _, ev := range events {
		if ev.Type == "error" {
			errorText += ev.Error
		}
	}
	if strings.Contains(errorText, "max turns reached") || strings.Contains(errorText, "context deadline exceeded") {
		t.Fatalf("unexpected backstop error after ignored search-narrowing nudge: %q", errorText)
	}
	if !strings.Contains(errorText, "search narrowing loop guard stopped repeated Bash empty search after 6 attempts") {
		t.Fatalf("missing search-narrowing loop-guard abort error, got %q", errorText)
	}
}

func TestLoop_ProgrammaticNumericVariantGuardDoesNotAppendTextNudge(t *testing.T) {
	mkTurn := func(id string, period int) []byte {
		command := fmt.Sprintf(`curl -s -X POST https://www.kimi.com/apiv2/kimi.gateway.billing.v1.BillingService/GetUsages -d '{"scope":[0],"period":%d}' | jq .`, period)
		toolInput, _ := json.Marshal(map[string]any{"command": command})
		return newSSE().
			messageStartWithContainer("container_loop").
			serverToolUseBlockWithInput("srvtoolu_"+id, codeExecutionToolName, `{"code":"for period in periods: Bash(command='...', description='probe')"}`).
			programmaticToolUseBlockWithInput("Bash", id, "srvtoolu_"+id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStartWithContainer("container_loop").
		textBlock("Stopping the programmatic sweep.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkTurn("call-1", 78),
		mkTurn("call-2", 79),
		mkTurn("call-3", 80),
		mkTurn("call-4", 81),
		mkTurn("call-5", 82),
		mkTurn("call-6", 83),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — numeric variant guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: `{"error":"invalid period"}`, IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-numeric-variant-loop-guard",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "find the right period",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(executed) != NumericVariantLoopThreshold {
		t.Fatalf("expected %d actual Bash executions before guard, got %d: %v", NumericVariantLoopThreshold, len(executed), executed)
	}
	if len(requestBodies) < 7 {
		t.Fatalf("expected at least 7 outbound requests, got %d", len(requestBodies))
	}
	seventh := string(requestBodies[6])
	if !strings.Contains(seventh, "[numeric-variant-loop-guard]") {
		t.Fatalf("7th request body missing numeric variant synthetic result:\n%s", truncateForLog(seventh, 1000))
	}
	if strings.Contains(seventh, "Stop enumerating numeric parameters now") {
		t.Fatalf("7th request body included direct-call text nudge in programmatic result-only response:\n%s", truncateForLog(seventh, 1200))
	}
	blocks := lastMessageContentBlocks(t, requestBodies[6])
	if len(blocks) == 0 {
		t.Fatalf("7th request last message has no content blocks:\n%s", truncateForLog(seventh, 1200))
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			t.Fatalf("programmatic tool response must contain only tool_result blocks, got %q in %#v\n%s",
				block.Type, blocks, truncateForLog(seventh, 1200))
		}
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping the programmatic sweep") {
		t.Errorf("expected final text after guard, got %q", text)
	}
}

func TestLoop_ProgrammaticErrorStreakGuardDoesNotAppendTextNudge(t *testing.T) {
	mkFailingTurn := func(id, command string) []byte {
		toolInput, _ := json.Marshal(map[string]any{
			"command":     command,
			"description": "Run failing probe",
		})
		return newSSE().
			messageStartWithContainer("container_error_streak").
			serverToolUseBlockWithInput("srvtoolu_"+id, codeExecutionToolName, `{"code":"result = Bash(command='probe', description='Run failing probe')"}`).
			programmaticToolUseBlockWithInput("Bash", id, "srvtoolu_"+id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStartWithContainer("container_error_streak").
		textBlock("Stopping failing programmatic calls.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkFailingTurn("call-a", "curl -s https://a.example.com/probe"),
		mkFailingTurn("call-b", "curl -s https://b.example.com/probe"),
		mkFailingTurn("call-c", "curl -s https://c.example.com/probe"),
		mkFailingTurn("call-d", "curl -s https://d.example.com/probe"),
		mkFailingTurn("call-e", "curl -s https://e.example.com/probe"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — programmatic error streak did not settle", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: "[exit status 22] simulated", IsError: true}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-error-streak",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run failing probes",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 6 {
		t.Fatalf("expected at least 6 outbound requests, got %d", len(requestBodies))
	}
	sixth := string(requestBodies[5])
	if strings.Contains(sixth, "Stop calling Bash now") {
		t.Fatalf("programmatic error-streak response included direct-call text nudge:\n%s", truncateForLog(sixth, 1200))
	}
	blocks := lastMessageContentBlocks(t, requestBodies[5])
	if len(blocks) == 0 {
		t.Fatalf("6th request last message has no content blocks:\n%s", truncateForLog(sixth, 1200))
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			t.Fatalf("programmatic error-streak response must contain only tool_result blocks, got %q in %#v\n%s",
				block.Type, blocks, truncateForLog(sixth, 1200))
		}
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping failing programmatic calls") {
		t.Errorf("expected final text after error streak, got %q", text)
	}
}

func TestLoop_ProgrammaticHardLoopGuardDoesNotAppendTextNudge(t *testing.T) {
	toolInput, _ := json.Marshal(map[string]any{
		"command":     "curl -s https://api.example.com/fixed",
		"description": "Run fixed probe",
	})
	mkRepeatTurn := func(id string) []byte {
		return newSSE().
			messageStartWithContainer("container_loop_guard").
			serverToolUseBlockWithInput("srvtoolu_"+id, codeExecutionToolName, `{"code":"result = Bash(command='curl -s https://api.example.com/fixed', description='Run fixed probe')"}`).
			programmaticToolUseBlockWithInput("Bash", id, "srvtoolu_"+id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStartWithContainer("container_loop_guard").
		textBlock("Stopping repeated programmatic calls.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkRepeatTurn("call-1"),
		mkRepeatTurn("call-2"),
		mkRepeatTurn("call-3"),
		mkRepeatTurn("call-4"),
		mkRepeatTurn("call-5"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — programmatic loop guard did not settle", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: "same result", IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-hard-loop-guard",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "run fixed probe",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(executed) != SoftLoopThreshold-1 {
		t.Fatalf("expected %d actual Bash executions before soft guard, got %d: %v", SoftLoopThreshold-1, len(executed), executed)
	}
	if len(requestBodies) < 6 {
		t.Fatalf("expected at least 6 outbound requests, got %d", len(requestBodies))
	}
	sixth := string(requestBodies[5])
	if !strings.Contains(sixth, "[loop-guard]") {
		t.Fatalf("6th request body missing loop-guard synthetic result:\n%s", truncateForLog(sixth, 1200))
	}
	if strings.Contains(sixth, "you have called Bash with the exact same input") {
		t.Fatalf("programmatic hard-loop response included direct-call text nudge:\n%s", truncateForLog(sixth, 1200))
	}
	blocks := lastMessageContentBlocks(t, requestBodies[5])
	if len(blocks) == 0 {
		t.Fatalf("6th request last message has no content blocks:\n%s", truncateForLog(sixth, 1200))
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			t.Fatalf("programmatic hard-loop response must contain only tool_result blocks, got %q in %#v\n%s",
				block.Type, blocks, truncateForLog(sixth, 1200))
		}
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping repeated programmatic calls") {
		t.Errorf("expected final text after hard loop guard, got %q", text)
	}
}

func TestLoop_ProgrammaticEnabledDirectBashStillGetsDirectGuardNudge(t *testing.T) {
	mkTurn := func(id string, period int) []byte {
		command := fmt.Sprintf(`curl -s https://api.example.com/usages?period=%d`, period)
		toolInput, _ := json.Marshal(map[string]any{
			"command":     command,
			"description": "Probe usage period",
		})
		return newSSE().
			messageStartWithContainer("container_direct_fallback").
			toolUseBlockWithInput("Bash", id, string(toolInput)).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStartWithContainer("container_direct_fallback").
		textBlock("Stopping direct fallback sweep.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkTurn("call-1", 78),
		mkTurn("call-2", 79),
		mkTurn("call-3", 80),
		mkTurn("call-4", 81),
		mkTurn("call-5", 82),
		mkTurn("call-6", 83),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — direct fallback guard did not settle", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "Bash",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: `{"error":"invalid period"}`, IsError: false}, nil
		},
	})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:                  client,
		Registry:                registry,
		ProgrammaticToolCalling: true,
		RunID:                   "test-programmatic-enabled-direct-fallback",
		ModelID:                 "claude-sonnet-4-6",
		Prompt:                  "find the right period",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 7 {
		t.Fatalf("expected at least 7 outbound requests, got %d", len(requestBodies))
	}
	seventh := string(requestBodies[6])
	if !strings.Contains(seventh, "[numeric-variant-loop-guard]") {
		t.Fatalf("7th request body missing numeric variant synthetic result:\n%s", truncateForLog(seventh, 1200))
	}
	if !strings.Contains(seventh, "Stop enumerating numeric parameters now") {
		t.Fatalf("direct Bash fallback should receive direct-call text nudge even when programmatic is enabled:\n%s", truncateForLog(seventh, 1200))
	}

	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected done event after final turn, counts=%v", counts)
	}
	if !strings.Contains(text, "Stopping direct fallback sweep") {
		t.Errorf("expected final text after direct fallback guard, got %q", text)
	}
}

// TestLoop_SoftLoopGuardShortCircuits: when the model calls the same
// tool with byte-identical input SoftLoopThreshold (3) times, the loop
// must short-circuit the 3rd call by pushing a synthetic [loop-guard]
// tool_result WITHOUT executing the tool. This catches the ESPN-style
// failure mode where a fabricated URL routes to a generic 200-OK page,
// the error-streak guard stays at zero, and the model burns turns
// refetching the same URL — but unlike the old text-nudge design, the
// soft block also avoids the cost of actually running the failing tool
// a third time.
//
// The existing toolUseBlock helper emits a constant partial_json of
// "{}" for every call, so three consecutive turns naturally produce
// byte-identical inputs — exactly the condition the guard targets.
func TestLoop_SoftLoopGuardShortCircuits(t *testing.T) {
	mkRepeatTurn := func(id string) []byte {
		return newSSE().
			messageStart().
			toolUseBlock("WebFetch", id).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Giving up and reporting what I have.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkRepeatTurn("call-1"),
		mkRepeatTurn("call-2"),
		mkRepeatTurn("call-3"),
		finalTurn,
	}

	executed := make([]string, 0)
	registry := tools.NewRegistry()
	// Custom tool that records every actual execution so we can assert
	// the soft-block skipped the 3rd call.
	registry.Register(&recordingTool{
		name: "WebFetch",
		execFn: func(call tools.ToolCall) (tools.ToolResult, error) {
			executed = append(executed, call.ID)
			return tools.ToolResult{Content: "fixed payload — same every time", IsError: false}, nil
		},
	})

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-soft-loop-guard",
		ModelID:  "claude-test",
		Prompt:   "fetch a thing",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v (want clean termination after soft-block)", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q request_count=%d executed=%v", counts, text, atomic.LoadInt32(&reqCount), executed)

	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event, got %d (counts=%v)", counts["done"], counts)
	}
	if !strings.Contains(text, "Giving up") {
		t.Errorf("recovery turn's text did not reach the callback; got %q", text)
	}

	// Soft block: the first two calls execute (count reaches 2), the
	// 3rd is short-circuited (count would become 3 = SoftLoopThreshold).
	if len(executed) != 2 {
		t.Errorf("expected exactly 2 actual tool executions (3rd should be soft-blocked), got %d: %v", len(executed), executed)
	}

	// Request #4 (after the soft-blocked 3rd call) must carry the
	// synthetic [loop-guard] tool_result inside a user message,
	// referencing the 3rd call's id.
	if len(requestBodies) < 4 {
		t.Fatalf("expected at least 4 outbound requests, got %d", len(requestBodies))
	}
	fourth := string(requestBodies[3])
	if !strings.Contains(fourth, "[loop-guard]") {
		t.Errorf("4th request body missing [loop-guard] synthetic result; got:\n%s", truncateForLog(fourth, 600))
	}
	if !strings.Contains(fourth, "call-3") {
		t.Errorf("4th request body missing call-3 tool_use_id; got:\n%s", truncateForLog(fourth, 600))
	}

	// Earlier requests must NOT contain the synthetic result.
	for i := 0; i < 3; i++ {
		if strings.Contains(string(requestBodies[i]), "[loop-guard]") {
			t.Errorf("request %d contained [loop-guard] before the soft threshold", i+1)
		}
	}

	for i, body := range requestBodies {
		assertAlternatingRoles(t, i+1, body)
	}
}

// TestLoop_HardLoopGuardInjectsTextNudge: 5 byte-identical calls trip
// the hard threshold. On hard, the loop still short-circuits the call
// (synthetic [loop-guard] tool_result, no execution) AND appends the
// strong text nudge to the same user message. The text nudge is the
// signal the model needs to abandon the tool entirely, not just adjust
// arguments.
func TestLoop_HardLoopGuardInjectsTextNudge(t *testing.T) {
	mkRepeatTurn := func(id string) []byte {
		return newSSE().
			messageStart().
			toolUseBlock("WebFetch", id).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping for real now.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkRepeatTurn("call-1"), mkRepeatTurn("call-2"), mkRepeatTurn("call-3"),
		mkRepeatTurn("call-4"), mkRepeatTurn("call-5"), finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "WebFetch"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-hard-loop-guard",
		ModelID:  "claude-test",
		Prompt:   "fetch a thing",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	if len(requestBodies) < 6 {
		t.Fatalf("expected >= 6 outbound requests, got %d", len(requestBodies))
	}

	// The 6th request body is the one sent AFTER the 5th call's hard-
	// trip. It must contain BOTH the synthetic tool_result AND the
	// text nudge "same input ... times".
	sixth := string(requestBodies[5])
	if !strings.Contains(sixth, "[loop-guard]") {
		t.Errorf("6th request body missing [loop-guard] synthetic result for hard trip; got:\n%s", truncateForLog(sixth, 800))
	}
	if !strings.Contains(sixth, "same input 5 times") {
		t.Errorf("6th request body missing hard-trip text nudge; got:\n%s", truncateForLog(sixth, 800))
	}

	for i, body := range requestBodies {
		assertAlternatingRoles(t, i+1, body)
	}
}

func TestLoop_HardLoopGuardStopsWhenNudgeIgnored(t *testing.T) {
	mkRepeatTurn := func(id string) []byte {
		return newSSE().
			messageStart().
			toolUseBlock("WebFetch", id).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}

	scripts := [][]byte{
		mkRepeatTurn("call-1"), mkRepeatTurn("call-2"), mkRepeatTurn("call-3"),
		mkRepeatTurn("call-4"), mkRepeatTurn("call-5"), mkRepeatTurn("call-6"),
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — loop guard did not stop after ignored hard nudge", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "WebFetch"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-hard-loop-guard-stops",
		ModelID:  "claude-test",
		Prompt:   "fetch a thing",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	if !errors.Is(err, ErrLoopGuardStuck) {
		t.Fatalf("RunLoop err = %v, want ErrLoopGuardStuck", err)
	}
	if len(requestBodies) != 6 {
		t.Fatalf("expected exactly 6 outbound requests before abort, got %d", len(requestBodies))
	}

	var errorText string
	var maxTurnsError bool
	for _, ev := range events {
		if ev.Type == "error" {
			errorText += ev.Error
			if strings.Contains(ev.Error, "max turns reached") {
				maxTurnsError = true
			}
		}
	}
	if maxTurnsError {
		t.Fatalf("unexpected max-turns error after ignored hard nudge: %q", errorText)
	}
	if !strings.Contains(errorText, "loop guard stopped repeated WebFetch call after 6 identical attempts") {
		t.Fatalf("missing loop-guard abort error, got %q", errorText)
	}
}

// TestLoop_LoopGuardCatchesAlternating: the loop guard must count
// cumulatively, not just on consecutive identical calls. Models have
// been observed defeating a streak-based guard by interleaving two
// URLs (A,B,A,B,A across turns, or [A,B,A] bundles within one turn) —
// each "different" call resets a consecutive streak, but the
// cumulative count of A still climbs and represents the same stuck
// behaviour. This test scripts an A,B,A,B,A pattern across 5 turns
// and asserts the soft block fires on the 5th call (3rd A, count
// hits SoftLoopThreshold=3) — the request that follows must contain
// the synthetic [loop-guard] tool_result.
func TestLoop_LoopGuardCatchesAlternating(t *testing.T) {
	mkTurn := func(toolID, partialJSON string) []byte {
		return newSSE().
			messageStart().
			toolUseBlockWithInput("WebFetch", toolID, partialJSON).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	inputA := `{"url":"https://a.example.com/article"}`
	inputB := `{"url":"https://b.example.com/article"}`
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping; here's what I have.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	// A, B, A, B, A — interleaved so a consecutive-only guard never
	// trips, then a final text response after the cumulative trip.
	scripts := [][]byte{
		mkTurn("call-1", inputA),
		mkTurn("call-2", inputB),
		mkTurn("call-3", inputA),
		mkTurn("call-4", inputB),
		mkTurn("call-5", inputA),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — guard didn't fire in time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "WebFetch"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-same-input-alternating",
		ModelID:  "claude-test",
		Prompt:   "research",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v (want clean termination after nudge)", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q request_count=%d", counts, text, atomic.LoadInt32(&reqCount))

	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event, got %d (counts=%v)", counts["done"], counts)
	}
	if !strings.Contains(text, "Stopping") {
		t.Errorf("recovery turn's text did not reach the callback; got %q", text)
	}

	// 5 tool turns + 1 final = 6 requests. The 6th request body is
	// the one sent AFTER turn 4 (5th A-call) appends its synthetic
	// [loop-guard] tool_result. Anything earlier wouldn't have the
	// cumulative A-count high enough yet.
	if len(requestBodies) < 6 {
		t.Fatalf("expected at least 6 outbound requests, got %d", len(requestBodies))
	}
	sixth := string(requestBodies[5])
	if !strings.Contains(sixth, "[loop-guard]") {
		t.Errorf("6th request body missing [loop-guard] synthetic result; got:\n%s", truncateForLog(sixth, 600))
	}
	// Soft block at count=3, not hard (5), so the text nudge MUST NOT
	// be present yet.
	if strings.Contains(sixth, "same input 3 times") {
		t.Errorf("6th request body has hard-trip text nudge at count=3; should be soft-only")
	}

	// Earlier requests must NOT carry the synthetic result. In
	// particular, request #2 (after first A) and #4 (after second A,
	// count=2) are below the soft threshold and must stay clean.
	for i := 0; i < 5; i++ {
		if strings.Contains(string(requestBodies[i]), "[loop-guard]") {
			t.Errorf("request %d carried [loop-guard] before cumulative soft threshold was hit", i+1)
		}
	}

	for i, body := range requestBodies {
		assertAlternatingRoles(t, i+1, body)
	}
}

// TestLoop_SameInputGuardSkipsBashOutput: BashOutput is the designated
// polling primitive — identical-input retries are the documented usage
// pattern, returning bytes-since-last-call each time. The same-input
// guard MUST exempt it; firing the nudge here would tell the model to
// abandon a healthy long-running task (build, test run, server start).
// We drive 3 consecutive BashOutput({"shell_id":"sh-1"}) calls and
// assert (a) the loop runs all the way to the scripted final text
// (no early termination from the guard) and (b) no outbound request
// body carries the same-input nudge phrase.
func TestLoop_SameInputGuardSkipsBashOutput(t *testing.T) {
	mkPollTurn := func(id string) []byte {
		return newSSE().
			messageStart().
			toolUseBlock("BashOutput", id).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Background task complete; here's the report.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	scripts := [][]byte{
		mkPollTurn("call-1"),
		mkPollTurn("call-2"),
		mkPollTurn("call-3"),
		finalTurn,
	}

	requestBodies := make([][]byte, 0, len(scripts))
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests — final text never reached", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	registry := tools.NewRegistry()
	registry.Register(&alwaysOkTool{name: "BashOutput"})

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:   client,
		Registry: registry,
		RunID:    "test-same-input-exempt-bashoutput",
		ModelID:  "claude-test",
		Prompt:   "wait for the build to finish",
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("RunLoop err = %v (want clean termination after final text)", err)
	}
	counts, text := eventTypeCounts(events)
	t.Logf("events=%v final_text=%q request_count=%d", counts, text, atomic.LoadInt32(&reqCount))

	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event, got %d (counts=%v)", counts["done"], counts)
	}
	if !strings.Contains(text, "Background task complete") {
		t.Errorf("final text never reached the callback; got %q", text)
	}

	// Guard against regression: no request body may carry the same-input
	// nudge. If the exemption were dropped, request #4 would contain it
	// (matching TestLoop_SameInputGuardInjectsStopNudge).
	for i, body := range requestBodies {
		if strings.Contains(string(body), "same input") {
			t.Errorf("request %d carries same-input nudge despite BashOutput exemption:\n%s",
				i+1, truncateForLog(string(body), 600))
		}
	}
}

// TestHardLoopNudgeText: the loop-guard hard-trip text nudge must stay
// sensible across every guard-eligible tool, not just WebFetch. The
// original version of this nudge ended with a sentence about "fetched
// page" and "URL" that was carried over from the ESPN-style failure
// that motivated the guard — fine for WebFetch, off-topic for Read /
// Grep / Glob / Bash where the model has no URL to abandon. This test
// locks in: (a) WebFetch keeps the failure-mode-specific sentence,
// (b) every other tool gets a clean, generic nudge without it.
func TestHardLoopNudgeText(t *testing.T) {
	const deadURLSentence = "treat that URL as dead"

	t.Run("WebFetch_keeps_dead_URL_sentence", func(t *testing.T) {
		got := HardLoopNudgeText("WebFetch", 3)
		if !strings.Contains(got, "WebFetch") {
			t.Errorf("nudge missing tool name; got: %q", got)
		}
		if !strings.Contains(got, "same input 3 times") {
			t.Errorf("nudge missing count; got: %q", got)
		}
		if !strings.Contains(got, deadURLSentence) {
			t.Errorf("WebFetch nudge missing dead-URL sentence; got: %q", got)
		}
	})

	for _, tool := range []string{"Read", "Grep", "Glob", "WebSearch", "Bash"} {
		t.Run(tool+"_drops_dead_URL_sentence", func(t *testing.T) {
			got := HardLoopNudgeText(tool, 4)
			if !strings.Contains(got, tool) {
				t.Errorf("nudge missing tool name; got: %q", got)
			}
			if !strings.Contains(got, "same input 4 times") {
				t.Errorf("nudge missing count; got: %q", got)
			}
			if strings.Contains(got, deadURLSentence) {
				t.Errorf("%s nudge should NOT mention dead URLs; got: %q", tool, got)
			}
			// "URL" and "fetched page" are the two WebFetch-flavour
			// terms that the pre-fix wording leaked into every tool's
			// nudge. Guard against them creeping back in via a
			// future copy-paste.
			if strings.Contains(got, "URL") || strings.Contains(got, "fetched page") {
				t.Errorf("%s nudge contains WebFetch-specific wording; got: %q", tool, got)
			}
		})
	}
}

// TestLoop_TextOnlyCompletesCleanly is a control: a turn with visible
// text and no tools should terminate via `done` without any error
// events. Guards against the nudge branch firing on normal completions.
// runScriptedLoopWithTelemetry mirrors runScriptedLoop but threads a
// fake Telemetry through so Phase 0 step-event tests can assert on the
// emit sequence. The two helpers are split rather than collapsed because
// most existing tests are noise-sensitive: adding a Telemetry recorder
// to every call site would make their assertions on event order brittle.
func runScriptedLoopWithTelemetry(t *testing.T, tel Telemetry, responses ...[]byte) ([]LoopEvent, error) {
	t.Helper()
	srv := newScriptedServer(t, responses...)
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		TraceID:   "trace-step-test",
		RunID:     "run-step-test",
		ModelID:   "claude-test",
		Prompt:    "irrelevant",
		Telemetry: tel,
		Emit: func(ev LoopEvent) {
			events = append(events, ev)
		},
		MaxTokens: 1024,
	}
	err := Run(context.Background(), cfg)
	return events, err
}

// TestLoop_EmitsStepLLMEvents pins the Phase 0 contract: every
// LLM call boundary in the loop emits exactly one step.llm.started +
// one step.llm.completed (no error), carrying the trace_id / run_id /
// step_index / model fields the dashboard and pattern checker depend
// on. Adding a turn must keep the started ↔ completed pairing intact.
func TestLoop_EmitsStepLLMEvents(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("Hello.").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	tel := &fakeTelemetry{}
	if _, err := runScriptedLoopWithTelemetry(t, tel, normal); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}

	var startedCount, completedCount, errorCount int
	var started, completed captured
	for _, ev := range tel.events {
		switch ev.event {
		case "agentloop.step.llm.started":
			startedCount++
			started = ev
		case "agentloop.step.llm.completed":
			completedCount++
			completed = ev
		case "agentloop.step.llm.error":
			errorCount++
		}
	}
	if startedCount != 1 || completedCount != 1 || errorCount != 0 {
		t.Fatalf("expected 1 started + 1 completed + 0 error; got started=%d completed=%d error=%d",
			startedCount, completedCount, errorCount)
	}
	if started.properties["trace_id"] != "trace-step-test" {
		t.Errorf("started missing trace_id: %v", started.properties)
	}
	if started.properties["run_id"] != "run-step-test" {
		t.Errorf("started missing run_id: %v", started.properties)
	}
	if got, _ := started.properties["step_index"].(int); got != 0 {
		t.Errorf("started step_index = %v, want 0", started.properties["step_index"])
	}
	if completed.properties["stop_reason"] != "end_turn" {
		t.Errorf("completed stop_reason = %v, want end_turn", completed.properties["stop_reason"])
	}
	if _, ok := completed.properties["elapsed_ms"].(int64); !ok {
		t.Errorf("completed elapsed_ms should be int64, got %T", completed.properties["elapsed_ms"])
	}
	// Phase 0.6: phase defaults to "initial" on the first turn of a
	// run that wasn't reached via continuation / compact / nudge /
	// pairing-retry. Both started and completed carry the same value.
	if got := started.properties["phase"]; got != "initial" {
		t.Errorf("started.phase = %v, want initial", got)
	}
	if got := completed.properties["phase"]; got != "initial" {
		t.Errorf("completed.phase = %v, want initial", got)
	}
	// Phase 0.6: external_retry_count is 0 when Stream succeeds on
	// the first attempt — the scripted server always returns 200, so
	// no retry is needed. Asserted as int (not int32) because we
	// convert before putting it in the property map.
	if got, ok := completed.properties["external_retry_count"].(int); !ok || got != 0 {
		t.Errorf("completed.external_retry_count = %v (%T), want int(0)",
			completed.properties["external_retry_count"], completed.properties["external_retry_count"])
	}
}

// countEvents tallies recorded telemetry events by name, returning a
// histogram + a slice of every captured event for fields assertions.
// Helper rather than per-test loop because Phase 0.5 added 7 new event
// names and most new tests want both a count and a payload check.
func countEvents(tel *fakeTelemetry, name string) (int, []captured) {
	matches := []captured{}
	for _, e := range tel.events {
		if e.event == name {
			matches = append(matches, e)
		}
	}
	return len(matches), matches
}

// TestLoop_RunErrorCarriesErrorKindField pins that run.error emits both
// the legacy `kind` (classifyLoopError) and the new `error_kind`
// (classifyStepError) so /admin/desktop-logs keeps working while Phase 1+
// dashboards / Phase 2 pattern checker can query a unified taxonomy.
// We drive the loop into ErrSilentTurn (the easiest reliably-reachable
// terminal error from a scripted server) which classifies to:
//   - kind:       "silent_turn"  (classifyLoopError)
//   - error_kind: "silent_turn"  (classifyStepError — same bucket)
func TestLoop_RunErrorCarriesErrorKindField(t *testing.T) {
	silent := newSSE().
		messageStart().
		thinkingBlock("thinking only, no text...").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	// Three identical silent turns: the loop sends 2 nudges then gives
	// up with ErrSilentTurn (see TestLoop_SilentThenSilentFails for the
	// max-nudge cap shape).
	tel := &fakeTelemetry{}
	_, err := runScriptedLoopWithTelemetry(t, tel, silent, silent, silent)
	if err == nil || !errors.Is(err, ErrSilentTurn) {
		t.Fatalf("expected ErrSilentTurn, got %v", err)
	}

	count, matches := countEvents(tel, "agentloop.run.error")
	if count != 1 {
		t.Fatalf("expected exactly 1 run.error event; got %d", count)
	}
	props := matches[0].properties
	if got := props["kind"]; got != "silent_turn" {
		t.Errorf("legacy kind = %v, want silent_turn (dashboard compat)", got)
	}
	if got := props["error_kind"]; got != "silent_turn" {
		t.Errorf("new error_kind = %v, want silent_turn (Phase 2 taxonomy)", got)
	}
	// Phase 0.6: ErrSilentTurn must classify to completion_reason
	// "silent_turn" (its own bucket), distinct from generic "error".
	if got := props["completion_reason"]; got != "silent_turn" {
		t.Errorf("completion_reason = %v, want silent_turn", got)
	}
	if _, ok := props["tools_used"]; !ok {
		t.Errorf("run.error must still carry tools_used: %v", props)
	}
}

// TestLoop_RunCompletedCarriesDoneReason pins that the success path
// emits agentloop.run.completed with completion_reason="done", paired
// with the run.error coverage above so the dashboard's pie chart has
// both terminal labels available across the lifecycle.
func TestLoop_RunCompletedCarriesDoneReason(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("done.").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	tel := &fakeTelemetry{}
	if _, err := runScriptedLoopWithTelemetry(t, tel, normal); err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	count, matches := countEvents(tel, "agentloop.run.completed")
	if count != 1 {
		t.Fatalf("expected exactly 1 run.completed event; got %d", count)
	}
	if got := matches[0].properties["completion_reason"]; got != "done" {
		t.Errorf("completion_reason = %v, want done", got)
	}
}

// TestLoop_EmitsRecoveryNudgeEvent pins the nudge_sent contract.
// One silent turn followed by a recovering text turn: the loop must
// fire exactly one agentloop.recovery.nudge_sent event between them.
func TestLoop_EmitsRecoveryNudgeEvent(t *testing.T) {
	silent := newSSE().
		messageStart().
		thinkingBlock("considering...").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	rescued := newSSE().
		messageStart().
		textBlock("Final answer.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	tel := &fakeTelemetry{}
	_, err := runScriptedLoopWithTelemetry(t, tel, silent, rescued)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}

	count, matches := countEvents(tel, "agentloop.recovery.nudge_sent")
	if count != 1 {
		t.Fatalf("expected exactly 1 nudge_sent; got %d", count)
	}
	props := matches[0].properties
	if got, _ := props["nudges_used"].(int); got != 1 {
		t.Errorf("nudges_used = %v, want 1", props["nudges_used"])
	}
	if got := props["stop_reason"]; got != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", got)
	}
	if got, _ := props["custom_nudge"].(bool); got {
		t.Errorf("custom_nudge should be false by default, got %v", got)
	}
}

// TestExecuteToolUse_OutcomeClassification covers the Phase 0.5
// refactor: executeToolUse now returns a distinct outcome string for
// each branch (success / unknown_tool / parse_error / permission_*
// / registry_error / tool_error). Locks the taxonomy used by the
// step.tool.completed event — Phase 2 pattern checker depends on it.
func TestExecuteToolUse_OutcomeClassification(t *testing.T) {
	t.Run("unknown_tool", func(t *testing.T) {
		// Empty registry → cfg.Registry.Get returns nil
		cfg := &LoopConfig{
			Registry: tools.NewRegistry(),
			Emit:     func(LoopEvent) {},
		}
		_, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{ID: "id1", Name: "Nope", Input: "{}"})
		if err != nil {
			t.Fatalf("unknown tool path should not return error; got %v", err)
		}
		if outcome != toolOutcomeUnknownTool {
			t.Errorf("outcome = %q, want %q", outcome, toolOutcomeUnknownTool)
		}
	})
	t.Run("parse_error_with_valid_tool", func(t *testing.T) {
		reg := tools.NewRegistry()
		reg.Register(&stubNoOpTool{name: "X"})
		cfg := &LoopConfig{Registry: reg, Emit: func(LoopEvent) {}}
		// "[" is unambiguously malformed JSON the parser rejects at
		// every stage, including S4/S5 truncation salvage — guaranteed
		// to hit the parse_error branch.
		_, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{ID: "id1", Name: "X", Input: "["})
		if err != nil {
			t.Fatalf("parse error path should not return error; got %v", err)
		}
		if outcome != toolOutcomeParseError {
			t.Errorf("outcome = %q, want %q", outcome, toolOutcomeParseError)
		}
	})
	t.Run("success", func(t *testing.T) {
		reg := tools.NewRegistry()
		reg.Register(&stubNoOpTool{name: "X"})
		cfg := &LoopConfig{Registry: reg, Emit: func(LoopEvent) {}}
		blk, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{ID: "id1", Name: "X", Input: "{}"})
		if err != nil {
			t.Fatalf("success path err = %v", err)
		}
		if outcome != toolOutcomeSuccess {
			t.Errorf("outcome = %q, want %q", outcome, toolOutcomeSuccess)
		}
		if blk.IsError {
			t.Errorf("success branch produced IsError block")
		}
	})
}

func TestLoop_TextOnlyCompletesCleanly(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("Yes, the answer is 42.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	events, err := runScriptedLoop(t, normal)
	if err != nil {
		t.Fatalf("RunLoop err = %v", err)
	}
	counts, text := eventTypeCounts(events)
	if counts["done"] != 1 {
		t.Errorf("expected exactly 1 done event; got %d (counts=%v)", counts["done"], counts)
	}
	if counts["error"] != 0 {
		t.Errorf("unexpected error events on a normal completion: %v", counts)
	}
	if !strings.Contains(text, "42") {
		t.Errorf("expected assistant text; got %q", text)
	}
}

// TestLoop_ContextEngineSnapshotEmitsL1Fields verifies that when a
// ContextEngineCollector is wired in and the snapshot carries the
// "basic" tier, the step.llm.started + step.llm.completed events
// pick up the L1 structural fields (chars / messages_by_role /
// tools / user_memory_counts / knowledge_file_count) and tag the
// event with ce_tier="basic". L2 fields (tool_schemas / template
// name) must NOT be emitted at the basic tier even
// if the collector populated them — defence in depth against a
// collector that fills L2 fields by accident.
func TestLoop_ContextEngineSnapshotEmitsL1Fields(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("Hello.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	tel := &fakeTelemetry{}
	srv := newScriptedServer(t, normal)
	t.Cleanup(srv.Close)

	var collectorCalls []int
	cfg := &LoopConfig{
		Client:  NewClient(WithBaseURL(srv.URL)),
		NoTools: true,
		TraceID: "trace-ce-l1",
		RunID:   "run-ce-l1",
		ModelID: "claude-test",
		Prompt:  "irrelevant",
		ContextEngineCollector: func(req ContextEngineRequest) *ContextEngineSnapshot {
			collectorCalls = append(collectorCalls, req.StepIndex)
			return &ContextEngineSnapshot{
				Tier:                 "basic",
				SystemPromptChars:    4231,
				MessagesByRole:       map[string]int{"user": 1, "assistant": 0},
				Tools:                []string{"Read", "Edit"},
				UserMemoryCounts:     map[string]int{"persona": 1, "contexts": 12},
				KnowledgeFileCount:   4,
				SystemPromptTemplate: "react_default", // L2 — should be dropped under basic
				ToolSchemas:          map[string]any{"Read": map[string]any{}},
			}
		},
		Telemetry: tel,
		MaxTokens: 1024,
		Emit:      func(LoopEvent) {},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run err = %v", err)
	}

	if len(collectorCalls) != 1 || collectorCalls[0] != 0 {
		t.Fatalf("collector calls = %v, want exactly [0]", collectorCalls)
	}

	startedCount, started := countEvents(tel, "agentloop.step.llm.started")
	if startedCount != 1 {
		t.Fatalf("expected 1 started event, got %d", startedCount)
	}
	p := started[0].properties

	if got := p["ce_tier"]; got != "basic" {
		t.Errorf("ce_tier = %v, want basic", got)
	}
	if got, _ := p["ce_system_prompt_chars"].(int); got != 4231 {
		t.Errorf("ce_system_prompt_chars = %v, want 4231", p["ce_system_prompt_chars"])
	}
	if got, ok := p["ce_messages_by_role"].(map[string]int); !ok || got["user"] != 1 {
		t.Errorf("ce_messages_by_role = %v, want {user:1, assistant:0}", p["ce_messages_by_role"])
	}
	if got, ok := p["ce_tools"].([]string); !ok || len(got) != 2 {
		t.Errorf("ce_tools = %v, want 2 names", p["ce_tools"])
	}
	if got, _ := p["ce_knowledge_file_count"].(int); got != 4 {
		t.Errorf("ce_knowledge_file_count = %v, want 4", p["ce_knowledge_file_count"])
	}

	// L2 gating: even though the collector returned a template name +
	// tool schemas, the basic tier must suppress them.
	if _, present := p["ce_system_prompt_template"]; present {
		t.Errorf("ce_system_prompt_template leaked under basic tier: %v", p["ce_system_prompt_template"])
	}
	if _, present := p["ce_tool_schemas"]; present {
		t.Errorf("ce_tool_schemas leaked under basic tier: %v", p["ce_tool_schemas"])
	}
}

// TestLoop_ContextEngineSnapshotEmitsL2Fields verifies that the
// detailed tier additionally emits L2 fields: tool_schemas (only on
// step 0 to dedupe) and system_prompt_template. Pairs with the
// basic-tier test above to pin the gating behaviour.
func TestLoop_ContextEngineSnapshotEmitsL2Fields(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("Hello.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	tel := &fakeTelemetry{}
	srv := newScriptedServer(t, normal)
	t.Cleanup(srv.Close)

	cfg := &LoopConfig{
		Client:  NewClient(WithBaseURL(srv.URL)),
		NoTools: true,
		TraceID: "trace-ce-l2",
		RunID:   "run-ce-l2",
		ModelID: "claude-test",
		Prompt:  "irrelevant",
		ContextEngineCollector: func(req ContextEngineRequest) *ContextEngineSnapshot {
			return &ContextEngineSnapshot{
				Tier:                 "detailed",
				SystemPromptChars:    4231,
				SystemPromptTemplate: "plan_mode",
				ToolSchemas: map[string]any{
					"Read": map[string]any{"type": "object"},
				},
				KnowledgeFileCount: -1, // sentinel "not collected"
			}
		},
		Telemetry: tel,
		MaxTokens: 1024,
		Emit:      func(LoopEvent) {},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run err = %v", err)
	}

	startedCount, started := countEvents(tel, "agentloop.step.llm.started")
	if startedCount != 1 {
		t.Fatalf("expected 1 started event, got %d", startedCount)
	}
	p := started[0].properties

	if got := p["ce_tier"]; got != "detailed" {
		t.Errorf("ce_tier = %v, want detailed", got)
	}
	if got := p["ce_system_prompt_template"]; got != "plan_mode" {
		t.Errorf("ce_system_prompt_template = %v, want plan_mode", got)
	}
	schemas, ok := p["ce_tool_schemas"].(map[string]any)
	if !ok {
		t.Fatalf("ce_tool_schemas missing or wrong type: %T", p["ce_tool_schemas"])
	}
	if _, hasRead := schemas["Read"]; !hasRead {
		t.Errorf("ce_tool_schemas missing Read entry: %v", schemas)
	}
	// KnowledgeFileCount = -1 sentinel means "not collected" — the
	// loop must not emit the field even though the snapshot was
	// non-nil and the tier was detailed.
	if _, present := p["ce_knowledge_file_count"]; present {
		t.Errorf("ce_knowledge_file_count emitted despite -1 sentinel: %v", p["ce_knowledge_file_count"])
	}
}

// Upstream response bodies stay local, including at the detailed tier when
// a redactor is configured. Telemetry keeps only safe response metadata.
func TestLoop_ContextEngineUpstreamErrorMetadata(t *testing.T) {
	// Inline httptest server returning a 400 with a JSON body
	// containing a fake API key that must never reach telemetry.
	errBody := `{"error":{"type":"invalid_request_error","message":"Invalid key sk-ant-api03-FAKEKEY123 in header"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errBody))
	}))
	t.Cleanup(srv.Close)

	var redactCalls []string
	tel := &fakeTelemetry{}
	cfg := &LoopConfig{
		Client:  NewClient(WithBaseURL(srv.URL)),
		NoTools: true,
		TraceID: "trace-ce-redact",
		RunID:   "run-ce-redact",
		ModelID: "claude-test",
		Prompt:  "irrelevant",
		ContextEngineCollector: func(req ContextEngineRequest) *ContextEngineSnapshot {
			return &ContextEngineSnapshot{Tier: "detailed", KnowledgeFileCount: -1}
		},
		Redactor: func(s string) string {
			redactCalls = append(redactCalls, s)
			// Trivial fake redactor: rewrites the literal "FAKEKEY123".
			return strings.ReplaceAll(s, "FAKEKEY123", "[REDACTED:api_key]")
		},
		Telemetry: tel,
		MaxTokens: 1024,
		Emit:      func(LoopEvent) {},
	}
	_ = Run(context.Background(), cfg)

	if len(redactCalls) != 0 {
		t.Fatalf("Redactor call count = %d, want 0", len(redactCalls))
	}

	errorCount, errs := countEvents(tel, "agentloop.step.llm.error")
	if errorCount != 1 {
		t.Fatalf("expected 1 error event, got %d", errorCount)
	}
	assertNoUpstreamBodyTelemetry(t, tel)
	assertUpstreamErrorMetadata(t, errs[0], errBody, "schema_mismatch")
	for _, event := range tel.events {
		payload, err := json.Marshal(event.properties)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "FAKEKEY123") {
			t.Errorf("%s contains upstream key text", event.event)
		}
	}
}

// TestLoop_ContextEngineCollectorNil verifies the L0 default: when no
// collector is wired in, step.llm.started / completed events stay
// shape-identical to pre-Phase-1.5 (no ce_* fields). This protects
// existing consumers from accidentally seeing new fields they don't
// expect.
func TestLoop_ContextEngineCollectorNil(t *testing.T) {
	normal := newSSE().
		messageStart().
		textBlock("Hello.").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	tel := &fakeTelemetry{}
	if _, err := runScriptedLoopWithTelemetry(t, tel, normal); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	_, started := countEvents(tel, "agentloop.step.llm.started")
	for k := range started[0].properties {
		if strings.HasPrefix(k, "ce_") {
			t.Errorf("ce_* field leaked when collector is nil: %s", k)
		}
	}
}

// TestLoop_RequestTooLargeShrinksImagesAndRetries verifies the reactive
// byte-budget recovery: when the upstream 400s with the Kimi/Moonshot
// "total message size exceeds limit" error, the loop downsizes the attached
// image and retries the same turn ONCE, succeeding on the retry with a
// materially smaller request body. This is the production incident's fix —
// and it is reactive, so a request the upstream would have accepted is never
// pre-emptively shrunk.
func TestLoop_RequestTooLargeShrinksImagesAndRetries(t *testing.T) {
	var mu sync.Mutex
	var bodyLens []int
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodyLens = append(bodyLens, len(body))
		mu.Unlock()
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"total message size 6147162 exceeds limit 2097152"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newSSE().messageStart().textBlock("Here is what the image shows.").messageDeltaStop("end_turn").messageStop().build())
	}))
	t.Cleanup(srv.Close)

	bigImg := makeNoisyImageBase64(t, 2400, 1600)
	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:    client,
		NoTools:   true,
		RunID:     "test-run",
		ModelID:   "claude-test",
		Prompt:    "describe this image",
		Images:    []ImageSource{{Type: "base64", MediaType: "image/png", Data: bigImg}},
		Emit:      func(ev LoopEvent) { events = append(events, ev) },
		MaxTokens: 1024,
	}

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("expected byte-budget recovery to succeed, got error: %v", err)
	}

	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("expected exactly 2 upstream calls (400 then retry), got %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodyLens) != 2 {
		t.Fatalf("expected 2 recorded request bodies, got %d", len(bodyLens))
	}
	if bodyLens[0] < 1_000_000 {
		t.Fatalf("expected the first request to carry the large image (>1MB), got %d bytes", bodyLens[0])
	}
	if bodyLens[1] >= bodyLens[0] {
		t.Fatalf("retry body should be smaller after shrink: first=%d retry=%d", bodyLens[0], bodyLens[1])
	}
	counts, text := eventTypeCounts(events)
	if counts["done"] == 0 {
		t.Fatalf("expected a done event after recovery; events=%v", counts)
	}
	if !strings.Contains(text, "image shows") {
		t.Fatalf("expected assistant text from the successful retry, got %q", text)
	}
}

func TestTruncateToolResult_LargeStringTruncated(t *testing.T) {
	big := strings.Repeat("x", toolResultMaxBytes+10000)
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: big}
	truncated := truncateToolResult(&blk, "WebFetch")
	if !truncated {
		t.Fatal("expected truncation for oversized result")
	}
	s, ok := blk.Content.(string)
	if !ok {
		t.Fatalf("expected string content after truncation, got %T", blk.Content)
	}
	if len(s) >= len(big) {
		t.Fatalf("content should be smaller after truncation: original=%d actual=%d", len(big), len(s))
	}
	if !strings.Contains(s, "tool_result truncated") {
		t.Error("truncated content should contain the truncation notice")
	}
	if !strings.HasPrefix(s, "xxx") {
		t.Error("truncated content should start with head of original")
	}
	if !strings.HasSuffix(s, "xxx") {
		t.Error("truncated content should end with tail of original")
	}
}

func TestTruncateToolResult_SmallStringUntouched(t *testing.T) {
	small := "hello world"
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: small}
	truncated := truncateToolResult(&blk, "Bash")
	if truncated {
		t.Fatal("should not truncate small content")
	}
	if blk.Content.(string) != small {
		t.Fatalf("content should be unchanged: %q", blk.Content)
	}
}

func TestTruncateToolResult_ExactBoundaryUntouched(t *testing.T) {
	exact := strings.Repeat("a", toolResultMaxBytes)
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: exact}
	truncated := truncateToolResult(&blk, "Read")
	if truncated {
		t.Fatal("should not truncate content at exact boundary")
	}
}

func TestTruncateToolResult_NonStringContentUntouched(t *testing.T) {
	blocks := []ContentBlock{{Type: "text", Text: strings.Repeat("x", toolResultMaxBytes+1000)}}
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: blocks}
	truncated := truncateToolResult(&blk, "Read")
	if truncated {
		t.Fatal("should not truncate non-string (multi-block) content")
	}
}

func TestTruncationSplit_ToolTypeAware(t *testing.T) {
	cases := []struct {
		tool         string
		wantHeadMore bool // true = head > tail, false = tail > head
	}{
		{"Bash", false},
		{"BashOutput", false},
		{"Programmatic", false},
		{"WebSearch", true},
		{"WebFetch", true},
		{"Read", true},
		{"Grep", true},
		{"Glob", true},
		{"mcp__slack__search", false}, // unknown → balanced
	}
	for _, tc := range cases {
		h, tl := truncationSplit(tc.tool)
		if h+tl != toolResultKeptBytes {
			t.Errorf("%s: head(%d) + tail(%d) = %d, want %d", tc.tool, h, tl, h+tl, toolResultKeptBytes)
		}
		if tc.wantHeadMore && h <= tl {
			t.Errorf("%s: expected head-heavy (head=%d tail=%d)", tc.tool, h, tl)
		}
		if !tc.wantHeadMore && tc.tool != "mcp__slack__search" && h >= tl {
			t.Errorf("%s: expected tail-heavy (head=%d tail=%d)", tc.tool, h, tl)
		}
	}
}

func TestTruncateToolResult_BashKeepsTail(t *testing.T) {
	// Simulate test output: passing tests at the start, FAIL at the end.
	head := strings.Repeat("PASS ", 8000)
	tail := "FAIL: TestImportant (0.01s)\n    expected true, got false\n"
	big := head + strings.Repeat(".", toolResultMaxBytes) + tail
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: big}
	truncateToolResult(&blk, "Bash")
	s := blk.Content.(string)
	if !strings.Contains(s, "FAIL: TestImportant") {
		t.Error("Bash truncation should preserve tail containing the failure message")
	}
}

func TestTruncateToolResult_WebFetchKeepsHead(t *testing.T) {
	// Simulate HTML: title and summary at start, boilerplate at end.
	head := "<title>MRVL Stock Analysis</title><p>Key findings: revenue up 15%</p>"
	tail := strings.Repeat("<div class='ad'>advertisement</div>", 500)
	big := head + strings.Repeat("x", toolResultMaxBytes) + tail
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: big}
	truncateToolResult(&blk, "WebFetch")
	s := blk.Content.(string)
	if !strings.Contains(s, "MRVL Stock Analysis") {
		t.Error("WebFetch truncation should preserve head containing the title")
	}
	if !strings.Contains(s, "revenue up 15%") {
		t.Error("WebFetch truncation should preserve head containing key findings")
	}
}

func TestTruncateToolResult_UTF8Safety(t *testing.T) {
	// Build content with multi-byte UTF-8 chars (CJK) placed right
	// at the truncation boundary. The truncation must not split a
	// multi-byte character, which would produce invalid UTF-8.
	prefix := strings.Repeat("x", toolResultKeptBytes-4*1024-2)
	// 3-byte CJK char at the exact boundary
	boundary := "\xe4\xb8\xad" // "中"
	middle := strings.Repeat("y", toolResultMaxBytes)
	suffix := strings.Repeat("z", 4*1024)
	big := prefix + boundary + middle + suffix

	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: big}
	truncated := truncateToolResult(&blk, "WebFetch")
	if !truncated {
		t.Fatal("expected truncation")
	}
	s := blk.Content.(string)
	// The head and tail portions must be valid UTF-8.
	for i, r := range s {
		if r == 0xFFFD { // unicode replacement character
			t.Errorf("invalid UTF-8 at byte offset %d", i)
			break
		}
	}
}

func TestTruncateToolResult_NoticeContainsMetadata(t *testing.T) {
	big := strings.Repeat("a", toolResultMaxBytes+5000)
	blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: big}
	truncateToolResult(&blk, "WebFetch")
	s := blk.Content.(string)
	// Notice should tell the model: original size, tool name, and how
	// much head/tail was kept — all needed for the model to decide
	// whether to re-fetch with a narrower query.
	if !strings.Contains(s, fmt.Sprintf("%d bytes from WebFetch", len(big))) {
		t.Error("notice should contain original size and tool name")
	}
	if !strings.Contains(s, "re-run the tool") {
		t.Error("notice should suggest re-running with narrower query")
	}
}

func TestTruncateToolResult_OutputSizeConsistent(t *testing.T) {
	// Regardless of input size (as long as > max), the output should
	// be approximately toolResultKeptBytes + notice length.
	sizes := []int{toolResultMaxBytes + 1, toolResultMaxBytes * 2, toolResultMaxBytes * 10}
	var outputSizes []int
	for _, sz := range sizes {
		blk := ContentBlock{Type: "tool_result", ToolUseID: "tu_1", Content: strings.Repeat("x", sz)}
		truncateToolResult(&blk, "Bash")
		outputSizes = append(outputSizes, len(blk.Content.(string)))
	}
	// All outputs should be within a small range of each other (only
	// the notice text varies slightly with the original size digits).
	for i := 1; i < len(outputSizes); i++ {
		diff := outputSizes[i] - outputSizes[0]
		if diff < 0 {
			diff = -diff
		}
		if diff > 200 {
			t.Errorf("output sizes should be consistent: %v", outputSizes)
			break
		}
	}
}

func TestTruncationSplit_SumsToKeptBytes(t *testing.T) {
	// Every tool name, including unknown ones, must produce a split
	// that sums to exactly toolResultKeptBytes.
	tools := []string{
		"Bash", "BashOutput", "Programmatic",
		"WebSearch", "WebFetch", "Read", "Grep", "Glob",
		"mcp__slack__send", "cua_driver", "TodoWrite", "",
		"SomeRandomToolName",
	}
	for _, name := range tools {
		h, tl := truncationSplit(name)
		if h+tl != toolResultKeptBytes {
			t.Errorf("truncationSplit(%q): head(%d) + tail(%d) = %d, want %d",
				name, h, tl, h+tl, toolResultKeptBytes)
		}
		if h <= 0 || tl <= 0 {
			t.Errorf("truncationSplit(%q): neither head(%d) nor tail(%d) should be zero",
				name, h, tl)
		}
	}
}

// TestLoop_ContextOverflow400_CompactsAndRetries is the regression guard CTO
// asked CI to cover: when the upstream 400s with sub2api's (current upstream:
// DeepSeek) "exceeded model token limit" body — the exact failure that crashed
// long cua_driver runs — the loop must classify it as context_overflow, compact
// history, and retry the same turn ONCE instead of surfacing a hard error.
// Symmetric to the byte-budget reactive test above. If either the classifier
// stops recognizing that phrasing OR the reactive compaction net is removed,
// the first 400 falls straight through and this test fails.
func TestLoop_ContextOverflow400_CompactsAndRetries(t *testing.T) {
	successSSE := newSSE().
		messageStart().
		textBlock("recovered after compaction").
		messageDeltaStop("end_turn").
		messageStop().
		build()

	// The real crash body: token-window overflow that also carries
	// "invalid_request_error" (which is why it used to be mis-classified as
	// schema_mismatch and never compacted).
	const sub2apiTokenLimit400 = `{"type":"error","error":{"type":"invalid_request_error","message":"Your request exceeded model token limit: 262144 (requested: 279485)"}}`

	// A history large enough (> PreserveTailTokens = 40k) that runAutoCompact
	// genuinely summarises an older portion instead of returning it verbatim,
	// so the retry request provably carries FEWER messages — proving the
	// compaction did real work, not a no-op on a single-prompt conversation.
	bigLine := strings.Repeat("history token ", 500) // ~7 KB per item
	var prior []HistoryItem
	for i := 0; i < 80; i++ {
		kind := "user"
		if i%2 == 1 {
			kind = "assistant"
		}
		prior = append(prior, HistoryItem{Kind: kind, Content: bigLine})
	}

	var reqs, firstMsgCount, lastMsgCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []json.RawMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mc := int32(len(body.Messages))
		n := atomic.AddInt32(&reqs, 1) - 1
		if n == 0 {
			// First (main-turn) request → the token-window 400.
			atomic.StoreInt32(&firstMsgCount, mc)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(sub2apiTokenLimit400))
			return
		}
		// The compaction summarize call (if any) and the retried turn both
		// succeed; the LAST recorded count is the retry's (post-compaction).
		atomic.StoreInt32(&lastMsgCount, mc)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(successSSE)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(WithBaseURL(srv.URL))
	var events []LoopEvent
	cfg := &LoopConfig{
		Client:       client,
		NoTools:      true,
		RunID:        "test-context-overflow-reactive",
		ModelID:      "claude-test",
		Prompt:       "trigger overflow",
		PriorHistory: prior,
		MaxTokens:    1024,
		Emit:         func(ev LoopEvent) { events = append(events, ev) },
	}

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("reactive compaction should have recovered the token-limit 400, got err: %v", err)
	}
	counts, _ := eventTypeCounts(events)
	if counts["error"] != 0 {
		t.Errorf("expected no error event after reactive recovery; counts=%v", counts)
	}
	if counts["done"] == 0 {
		t.Errorf("expected the run to finish (done event) after recovery; counts=%v", counts)
	}
	// P1 guard: the reactive branch MUST emit a compaction-boundary event
	// (compact_end on success, compact_error on the drop-oldest-half fallback).
	// streaming.go persists the boundary on either; without it the GUI never
	// records the compaction and a session restart re-loads the un-compacted
	// history (and can 400 again).
	if counts["compact_end"]+counts["compact_error"] == 0 {
		t.Errorf("reactive compaction must emit compact_end or compact_error so the GUI persists a boundary; counts=%v", counts)
	}
	// P2 guard: the retry must carry a SMALLER history than the request that
	// overflowed — i.e. compaction actually shrank the conversation.
	first := atomic.LoadInt32(&firstMsgCount)
	last := atomic.LoadInt32(&lastMsgCount)
	if last == 0 || last >= first {
		t.Errorf("expected the retry request to carry fewer messages than the overflowing one; first=%d last=%d", first, last)
	}
	if got := atomic.LoadInt32(&reqs); got < 2 {
		t.Errorf("expected at least 2 upstream requests (initial 400 + retry), got %d", got)
	}
}

// TestLoop_ContextOverflow400_CancelDuringCompactStaysSilent: if the user
// cancels the run while the reactive compaction's summarize call is in flight,
// the loop must surface the cancellation WITHOUT emitting a misleading
// compact_error — matching the send-error path's ctx-cancel convention. Guards
// the ctx.Err() check added after runAutoCompact.
func TestLoop_ContextOverflow400_CancelDuringCompactStaysSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const tokenLimit400 = `{"type":"error","error":{"type":"invalid_request_error","message":"Your request exceeded model token limit: 262144 (requested: 279485)"}}`
	summarizeSSE := newSSE().messageStart().textBlock("summary").messageDeltaStop("end_turn").messageStop().build()

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&reqs, 1) - 1
		if n == 0 {
			// First (main-turn) request → the token-window 400 that triggers
			// reactive compaction.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(tokenLimit400))
			return
		}
		// Second request = the compaction summarize call. Cancel the run
		// mid-flight, then answer; runAutoCompact returns but the loop must
		// observe ctx.Err() and bail silently rather than emit compact_error.
		cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(summarizeSSE)
	}))
	t.Cleanup(srv.Close)

	// Large history so reactive compaction actually makes a summarize call
	// (rather than returning the short conversation verbatim).
	bigLine := strings.Repeat("history token ", 500)
	var prior []HistoryItem
	for i := 0; i < 80; i++ {
		kind := "user"
		if i%2 == 1 {
			kind = "assistant"
		}
		prior = append(prior, HistoryItem{Kind: kind, Content: bigLine})
	}

	var events []LoopEvent
	cfg := &LoopConfig{
		Client:       NewClient(WithBaseURL(srv.URL)),
		NoTools:      true,
		RunID:        "test-overflow-cancel",
		ModelID:      "claude-test",
		Prompt:       "trigger overflow",
		PriorHistory: prior,
		MaxTokens:    1024,
		Emit:         func(ev LoopEvent) { events = append(events, ev) },
	}

	if err := Run(ctx, cfg); err == nil {
		t.Fatal("expected a context error when the run is cancelled mid-compaction")
	}
	counts, _ := eventTypeCounts(events)
	if counts["compact_error"] != 0 {
		t.Errorf("must NOT emit compact_error on user cancellation; counts=%v", counts)
	}
}

// The loop guard blocks an identical repeated call; its log line must not
// carry the call's input, which can be a private goal, path or command.
func TestLoop_LoopGuardLogDoesNotCarryTheBlockedInput(t *testing.T) {
	const secret = "quietly book the surprise party for Dana on 14 March"
	input := fmt.Sprintf(`{"goal":%q}`, secret)
	mkTurn := func(id string) []byte {
		return newSSE().
			messageStart().
			toolUseBlockWithInput("GoalCreate", id, input).
			messageDeltaStop("tool_use").
			messageStop().
			build()
	}
	finalTurn := newSSE().
		messageStart().
		textBlock("Stopping the repeated call.").
		messageDeltaStop("end_turn").
		messageStop().
		build()
	scripts := [][]byte{mkTurn("call-1"), mkTurn("call-2"), mkTurn("call-3"), finalTurn}

	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := atomic.AddInt32(&reqCount, 1) - 1
		if int(idx) >= len(scripts) {
			http.Error(w, "too many requests", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(scripts[idx])
	}))
	t.Cleanup(srv.Close)

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	registry := tools.NewRegistry()
	registry.Register(&recordingTool{
		name: "GoalCreate",
		execFn: func(tools.ToolCall) (tools.ToolResult, error) {
			return tools.ToolResult{Content: "saved"}, nil
		},
	})
	cfg := &LoopConfig{
		Client:    NewClient(WithBaseURL(srv.URL)),
		Registry:  registry,
		RunID:     "test-loop-guard-log",
		ModelID:   "claude-test",
		Prompt:    "save the goal",
		Emit:      func(LoopEvent) {},
		MaxTokens: 1024,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "loop-guard=") {
		t.Fatalf("the loop guard never fired, so the test proves nothing; logs:\n%s", out)
	}
	if strings.Contains(out, secret) || strings.Contains(out, "Dana") {
		t.Fatalf("the loop guard log carries the blocked input:\n%s", out)
	}
}
