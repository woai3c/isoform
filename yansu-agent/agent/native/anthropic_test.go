package native

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBeforeDispatchRefusesInitialHTTPAndCompaction(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			var client LLMClient = NewClient(WithBaseURL(server.URL))
			if provider == "openai" {
				client = NewOpenAIClient(server.URL, "", "")
			}
			denied := errors.New("source access changed")
			checks := 0
			ctx := ContextWithBeforeDispatch(context.Background(), func(context.Context) error { checks++; return denied })
			err := client.Stream(ctx, &Request{Model: "test", Messages: []Message{{Role: "user", Content: "private source"}}}, func(*StreamEvent) error { return nil })
			if !errors.Is(err, denied) || shouldRetry(err) || calls.Load() != 0 || checks != 1 {
				t.Fatalf("initial dispatch: err=%v calls=%d checks=%d", err, calls.Load(), checks)
			}
			_, _, err = summarizeMessages(ctx, client, "test", []Message{{Role: "user", Content: "private source"}})
			if !errors.Is(err, denied) || calls.Load() != 0 || checks != 2 {
				t.Fatalf("compaction dispatch: err=%v calls=%d checks=%d", err, calls.Load(), checks)
			}
		})
	}
}

func TestBeforeDispatchRechecksAuthorityBeforeHTTPRetry(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			var withdrawn atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				withdrawn.Store(true)
				http.Error(w, `{"error":{"message":"retry later"}}`, http.StatusTooManyRequests)
			}))
			defer server.Close()
			var client LLMClient = NewClient(WithBaseURL(server.URL))
			if provider == "openai" {
				client = NewOpenAIClient(server.URL, "", "")
			}
			denied := errors.New("source access changed")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = ContextWithBeforeDispatch(ctx, func(context.Context) error {
				if withdrawn.Load() {
					return denied
				}
				return nil
			})
			err := client.Stream(ctx, &Request{Model: "test", Messages: []Message{{Role: "user", Content: "private source"}}}, func(*StreamEvent) error { return nil })
			if !errors.Is(err, denied) || calls.Load() != 1 {
				t.Fatalf("withdrawal during retry: err=%v HTTP calls=%d", err, calls.Load())
			}
		})
	}
}

// These tests exercise pure, offline parts of the Anthropic client:
// the SSE line parser (ParseSSE) and the retry classifier
// (shouldRetry). Anything involving a live HTTP request lives in
// agent/native_live_test.go and runs only with the `live` build tag.

func TestParseSSE_BasicEvents(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg1","model":"claude","role":"assistant","content":[]}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	var events []StreamEvent
	err := ParseSSE(strings.NewReader(stream), func(ev *StreamEvent) error {
		events = append(events, *ev)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("events = %d, want 6", len(events))
	}
	if events[0].Type != "message_start" {
		t.Errorf("first = %s", events[0].Type)
	}
	if events[2].Delta.Text != "hello" {
		t.Errorf("delta text = %q", events[2].Delta.Text)
	}
	if events[4].Delta.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q", events[4].Delta.StopReason)
	}
}

func TestParseSSE_IgnoresComments(t *testing.T) {
	stream := `: this is a ping

data: {"type":"ping"}

`
	var count int
	err := ParseSSE(strings.NewReader(stream), func(ev *StreamEvent) error {
		count++
		if ev.Type != "ping" {
			t.Errorf("unexpected type %q", ev.Type)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestParseSSE_MultiLineData(t *testing.T) {
	stream := "event: x\ndata: {\"type\"\ndata: :\"ping\"}\n\n"
	var got StreamEvent
	err := ParseSSE(strings.NewReader(stream), func(ev *StreamEvent) error {
		got = *ev
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "ping" {
		t.Errorf("multi-line concat failed: type = %q", got.Type)
	}
}

func TestParseSSE_HandlerErrorStops(t *testing.T) {
	stream := `data: {"type":"a"}

data: {"type":"b"}

`
	count := 0
	err := ParseSSE(strings.NewReader(stream), func(ev *StreamEvent) error {
		count++
		return io.ErrUnexpectedEOF
	})
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (stream should stop on first error)", count)
	}
}

func TestShouldRetry(t *testing.T) {
	cases := map[int]bool{
		200: false,
		400: false,
		401: false,
		403: false,
		408: true,
		429: true,
		500: true,
		502: true,
		529: true,
	}
	for code, want := range cases {
		got := shouldRetry(&HTTPError{Status: code})
		if got != want {
			t.Errorf("shouldRetry(%d) = %v, want %v", code, got, want)
		}
	}
	if shouldRetry(nil) {
		t.Error("nil should not retry")
	}
}

// TestIsToolPairingError covers both Anthropic phrasings of the
// tool_use ↔ tool_result mismatch 400, the OpenAI-style phrasing that
// leaks through sub2api / Kimi-Anthropic proxies when the same
// pairing failure is caught by an internal OpenAI-format validator,
// plus a few negatives. The loop uses this predicate to decide
// whether to run RepairOrphans + retry once before surfacing the error.
func TestIsToolPairingError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "reverse_orphan_phrasing",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"unexpected ` + "`messages.140.content.1: tool_use_id`" + ` found in ` + "`tool_result`" + ` blocks: call_00_xyz. Each ` + "`tool_result`" + ` block must have a corresponding ` + "`tool_use`" + ` block in the previous message."}}`},
			want: true,
		},
		{
			name: "forward_orphan_phrasing",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"tool_use ids were not found in tool_result blocks: toolu_abc"}}`},
			want: true,
		},
		{
			// sub2api / Kimi-via-anthropic-relay surfaces the same
			// pairing failure with OpenAI field names (tool_call_id)
			// wrapped in an Anthropic-style error envelope, because
			// the relay internally converts to OpenAI before
			// validating. The underlying condition is identical and
			// RepairOrphans + one retry recovers it.
			name: "sub2api_openai_phrasing_tool_call_id",
			err:  &HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"tool_call_id is not found"},"type":"error"}`},
			want: true,
		},
		{
			name: "sub2api_openai_phrasing_tool_call_ids_plural",
			err:  &HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"tool_call_ids were not found: call_abc"},"type":"error"}`},
			want: true,
		},
		{
			name: "unrelated_400",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"max_tokens too high"}}`},
			want: false,
		},
		{
			name: "unrelated_400_not_found_without_tool_id",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"model not_found"}}`},
			want: false,
		},
		{
			// Guards against a tautological second check: "tool_use"
			// and "tool_call" are substrings of the ID keywords, so
			// including them in the pairing-evidence set would make
			// every body that passes hasIDKeyword also pass the second
			// check, falsely classifying format-validation errors like
			// "invalid tool_call_id" as pairing errors.
			name: "format_validation_error_with_tool_id_keyword",
			err:  &HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"invalid tool_call_id"}}`},
			want: false,
		},
		{
			name: "format_validation_error_with_tool_use_id_keyword",
			err:  &HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"tool_use_id must match pattern ^[a-zA-Z0-9_-]+$"}}`},
			want: false,
		},
		{
			name: "rate_limit_429",
			err:  &HTTPError{Status: 429, Body: "rate limited"},
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "non_HTTPError",
			err:  io.EOF,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsToolPairingError(tc.err); got != tc.want {
				t.Errorf("IsToolPairingError = %v, want %v (err=%v)", got, tc.want, tc.err)
			}
		})
	}
}

// TestStream_ExternalRetryCount pins the Phase 0.6 contract: when a
// ctx-bound retry counter is present, Stream increments it once per
// retry (NOT once per attempt) so the agent loop can report
// external_retry_count in step.llm.* telemetry without modifying
// Stream's public signature. Test sequence: 429 → 200 SSE — Stream
// must succeed, return nil, and the counter must read exactly 1.
//
// Takes ~1s due to backoffDuration(0)=1s; acceptable for a unit
// test pinning a stable contract. Skipped under -short.
func TestStream_ExternalRetryCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping retry-count test in -short mode (uses 1s backoff)")
	}

	var calls atomic.Int32
	successBody := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"x\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First attempt: 429 triggers shouldRetry=true.
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
			return
		}
		// Second attempt: 200 with a minimal but well-formed SSE.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successBody))
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	ctx, counter := ContextWithRetryCounter(context.Background())
	// Belt-and-braces deadline so a never-completing test fails fast
	// instead of timing out the suite.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := client.Stream(ctx, &Request{Model: "x", Messages: []Message{{Role: "user", Content: "hi"}}}, func(*StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream returned err = %v after one retry succeeded", err)
	}
	if got := counter.Load(); got != 1 {
		t.Errorf("counter = %d, want 1 (one retry between 429 and 200)", got)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2", got)
	}
}

// TestStream_RetryCounterAbsentIsSilent pins that Stream does NOT
// require a counter to be attached. Older call sites (compact,
// any future code path) work unchanged when the ctx carries no
// counter — the increment branch is silent.
func TestStream_RetryCounterAbsentIsSilent(t *testing.T) {
	successBody := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"x\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successBody))
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	// Plain context.Background — no counter attached.
	err := client.Stream(context.Background(), &Request{Model: "x", Messages: []Message{{Role: "user", Content: "hi"}}}, func(*StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream returned err = %v on success path without counter", err)
	}
}
