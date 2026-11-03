package native

import (
	"encoding/json"
	"strings"
	"testing"
)

// collectStream runs the OpenAI SSE body through parseSSE and returns the
// reconstructed Anthropic StreamEvents in order.
func collectStream(t *testing.T, body string) []*StreamEvent {
	t.Helper()
	c := NewOpenAIClient("", "k", "")
	var events []*StreamEvent
	err := c.parseSSE(strings.NewReader(body), func(ev *StreamEvent) error {
		// copy: the loop treats each event as a value snapshot
		cp := *ev
		events = append(events, &cp)
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE returned error: %v", err)
	}
	return events
}

func TestOpenAIParseSSE_TextAndToolCall(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"ci"}}]},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"SF\"}"}}]},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	events := collectStream(t, body)

	if len(events) == 0 || events[0].Type != "message_start" {
		t.Fatalf("expected first event message_start, got %+v", events)
	}
	if events[len(events)-1].Type != "message_stop" {
		t.Fatalf("expected last event message_stop, got %q", events[len(events)-1].Type)
	}

	var text strings.Builder
	var toolJSON strings.Builder
	var sawStart, sawStop, sawStopReason, sawUsage bool
	for _, ev := range events {
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta == nil {
				t.Fatalf("content_block_delta with nil delta")
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Index != 0 {
					t.Errorf("text_delta should be at index 0, got %d", ev.Index)
				}
				text.WriteString(ev.Delta.Text)
			case "input_json_delta":
				if ev.Index != 1 {
					t.Errorf("tool input_json_delta should be at index 1, got %d", ev.Index)
				}
				toolJSON.WriteString(ev.Delta.PartialJSON)
			}
		case "content_block_start":
			sawStart = true
			if ev.Index != 1 {
				t.Errorf("tool content_block_start should be at index 1, got %d", ev.Index)
			}
			if ev.ContentBlock == nil || ev.ContentBlock.Type != "tool_use" {
				t.Fatalf("expected tool_use content_block_start, got %+v", ev.ContentBlock)
			}
			if ev.ContentBlock.ID != "call_1" || ev.ContentBlock.Name != "get_weather" {
				t.Errorf("tool id/name mismatch: %+v", ev.ContentBlock)
			}
		case "content_block_stop":
			if ev.Index == 1 {
				sawStop = true
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				sawStopReason = true
				if ev.Delta.StopReason != "tool_use" {
					t.Errorf("expected stop_reason tool_use, got %q", ev.Delta.StopReason)
				}
			}
			if ev.Usage != nil {
				sawUsage = true
				if ev.Usage.InputTokens != 10 || ev.Usage.OutputTokens != 5 {
					t.Errorf("usage mismatch: %+v", ev.Usage)
				}
				if ev.Usage.CacheReadInputTokens != 3 {
					t.Errorf("expected cache_read 3, got %d", ev.Usage.CacheReadInputTokens)
				}
			}
		}
	}

	if text.String() != "Hello" {
		t.Errorf("text mismatch: %q", text.String())
	}
	if toolJSON.String() != `{"city":"SF"}` {
		t.Errorf("tool json mismatch: %q", toolJSON.String())
	}
	if !sawStart || !sawStop {
		t.Errorf("missing tool block start/stop: start=%v stop=%v", sawStart, sawStop)
	}
	if !sawStopReason {
		t.Errorf("missing message_delta stop_reason")
	}
	if !sawUsage {
		t.Errorf("missing message_delta usage")
	}
}

func TestOpenAITranslateRequest_MessagesAndTools(t *testing.T) {
	temp := 0.5
	req := &Request{
		Model:       "gpt-4o",
		MaxTokens:   256,
		System:      "be helpful",
		Temperature: &temp,
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: []ContentBlock{
				{Type: "text", Text: "let me check"},
				{Type: "tool_use", ID: "call_1", Name: "get_weather", Input: map[string]any{"city": "SF"}},
			}},
			{Role: "user", Content: []ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "sunny"},
			}},
		},
		Tools: []ToolDef{
			{Name: "get_weather", Description: "weather", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &ToolChoice{Type: "any"},
		// Anthropic-only fields that must be silently dropped:
		Thinking: &ThinkingConfig{Type: "enabled", BudgetTokens: 100},
	}

	c := NewOpenAIClient("", "k", "")
	o := c.translateRequest(req)

	if o.Model != "gpt-4o" {
		t.Fatalf("model mismatch: %+v", o)
	}
	// The OpenAI-compatible path must NEVER send an output cap: some gateways
	// reject max_tokens with an opaque 500. Lock that in at the wire level.
	if wire, _ := json.Marshal(o); strings.Contains(string(wire), "max_tokens") ||
		strings.Contains(string(wire), "max_completion_tokens") {
		t.Fatalf("request must not carry an output-cap parameter: %s", wire)
	}
	if o.Temperature == nil || *o.Temperature != 0.5 {
		t.Errorf("temperature not carried through")
	}
	if !o.Stream || o.StreamOptions == nil || !o.StreamOptions.IncludeUsage {
		t.Errorf("stream options not set for usage")
	}
	if o.ToolChoice != "required" {
		t.Errorf("tool_choice any should map to required, got %v", o.ToolChoice)
	}
	if len(o.Tools) != 1 || o.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools translation mismatch: %+v", o.Tools)
	}

	// Expected message sequence: system, user(hi), assistant(tool_calls),
	// tool(result).
	if len(o.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(o.Messages), o.Messages)
	}
	if o.Messages[0].Role != "system" || o.Messages[0].Content != "be helpful" {
		t.Errorf("system message mismatch: %+v", o.Messages[0])
	}
	if o.Messages[1].Role != "user" || o.Messages[1].Content != "hi" {
		t.Errorf("user message mismatch: %+v", o.Messages[1])
	}
	asst := o.Messages[2]
	if asst.Role != "assistant" || asst.Content != "let me check" {
		t.Errorf("assistant text mismatch: %+v", asst)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" ||
		asst.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("assistant tool_calls mismatch: %+v", asst.ToolCalls)
	}
	if asst.ToolCalls[0].Function.Arguments != `{"city":"SF"}` {
		t.Errorf("tool args mismatch: %q", asst.ToolCalls[0].Function.Arguments)
	}
	tool := o.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || tool.Content != "sunny" {
		t.Errorf("tool result message mismatch: %+v", tool)
	}
}

func TestOpenAITranslateRequest_GPT56ToolsDisableReasoning(t *testing.T) {
	tool := ToolDef{
		Name:        "get_weather",
		Description: "weather",
		InputSchema: map[string]any{"type": "object"},
	}
	tests := []struct {
		name            string
		model           string
		tools           []ToolDef
		wantEffort      string
		wantWireSetting bool
	}{
		{
			name:            "gpt-5.6-sol with tools",
			model:           "gpt-5.6-sol",
			tools:           []ToolDef{tool},
			wantEffort:      "none",
			wantWireSetting: true,
		},
		{
			name:            "gpt-5.6 alias with tools",
			model:           "gpt-5.6",
			tools:           []ToolDef{tool},
			wantEffort:      "none",
			wantWireSetting: true,
		},
		{
			name:            "gpt-5.6 snapshot with tools",
			model:           "gpt-5.6-sol-2026-07-01",
			tools:           []ToolDef{tool},
			wantEffort:      "none",
			wantWireSetting: true,
		},
		{
			name:            "gpt-5.6 without tools",
			model:           "gpt-5.6-sol",
			wantEffort:      "",
			wantWireSetting: false,
		},
		{
			name:            "other model with tools",
			model:           "gpt-4o",
			tools:           []ToolDef{tool},
			wantEffort:      "",
			wantWireSetting: false,
		},
	}

	c := NewOpenAIClient("", "k", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := c.translateRequest(&Request{
				Model:    tt.model,
				Messages: []Message{{Role: "user", Content: "hi"}},
				Tools:    tt.tools,
			})
			if o.ReasoningEffort != tt.wantEffort {
				t.Fatalf("reasoning effort = %q, want %q", o.ReasoningEffort, tt.wantEffort)
			}

			wire, err := json.Marshal(o)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			hasWireSetting := strings.Contains(string(wire), `"reasoning_effort":"none"`)
			if hasWireSetting != tt.wantWireSetting {
				t.Fatalf(
					"reasoning_effort wire setting present = %v, want %v: %s",
					hasWireSetting,
					tt.wantWireSetting,
					wire,
				)
			}
		})
	}
}
