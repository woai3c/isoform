package native

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestNoOpTelemetryAcceptsAnyInput(t *testing.T) {
	var tel Telemetry = NoOpTelemetry{}
	// Must not panic on any input shape.
	tel.Capture("any.event", nil)
	tel.Capture("", map[string]any{"k": "v"})
	tel.Capture("agentloop.run.started", map[string]any{
		"trace_id": "abc",
		"int":      42,
		"bool":     true,
		"float":    3.14,
	})
}

// fakeTelemetry records every Capture call so tests can assert on emit
// order and properties without booting a real PostHog client.
type fakeTelemetry struct {
	events []captured
}

type captured struct {
	event      string
	properties map[string]any
}

func (f *fakeTelemetry) Capture(event string, properties map[string]any) {
	// Copy the properties map so callers mutating their input after the
	// call don't retroactively change recorded state.
	cp := make(map[string]any, len(properties))
	for k, v := range properties {
		cp[k] = v
	}
	f.events = append(f.events, captured{event: event, properties: cp})
}

func (f *fakeTelemetry) last() captured {
	if len(f.events) == 0 {
		return captured{}
	}
	return f.events[len(f.events)-1]
}

func TestContextWithTraceIDRoundtrip(t *testing.T) {
	ctx := context.Background()
	if got := TraceIDFromContext(ctx); got != "" {
		t.Errorf("empty ctx should yield empty trace id, got %q", got)
	}
	ctx = ContextWithTraceID(ctx, "trace-123")
	if got := TraceIDFromContext(ctx); got != "trace-123" {
		t.Errorf("expected trace-123, got %q", got)
	}
	// Empty traceID is a no-op.
	ctx2 := ContextWithTraceID(context.Background(), "")
	if got := TraceIDFromContext(ctx2); got != "" {
		t.Errorf("empty traceID should not be stored, got %q", got)
	}
}

func TestClassifyPairingWording(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "anthropic_tool_use_id",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"unexpected tool_use_id ..."}}`},
			want: "anthropic",
		},
		{
			name: "anthropic_plural_tool_use_ids",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"tool_use ids were not found"}}`},
			want: "anthropic",
		},
		{
			name: "openai_tool_call_id",
			err:  &HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"tool_call_id is not found"}}`},
			want: "openai",
		},
		{
			name: "openai_takes_priority_when_both_present",
			err:  &HTTPError{Status: 400, Body: `tool_use_id ... tool_call_id ...`},
			want: "openai",
		},
		{
			name: "unknown_phrasing",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"max_tokens too high"}}`},
			want: "unknown",
		},
		{
			name: "nil_err",
			err:  nil,
			want: "unknown",
		},
		{
			name: "non_HTTPError",
			err:  errors.New("plain"),
			want: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyPairingWording(tc.err); got != tc.want {
				t.Errorf("classifyPairingWording = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractOrphanToolName(t *testing.T) {
	// Build a messages slice with three assistant tool_use blocks the
	// helper might match against.
	messages := []Message{
		{
			Role: "assistant",
			Content: []ContentBlock{
				{Type: "text", Text: "let me try"},
				{Type: "tool_use", ID: "toolu_aaa", Name: "Read"},
				{Type: "tool_use", ID: "call_bbb", Name: "Bash"},
			},
		},
		{
			Role: "assistant",
			Content: []ContentBlock{
				{Type: "tool_use", ID: "toolu_ccc", Name: "WebFetch"},
			},
		},
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "openai_phrasing_finds_bash",
			err:  &HTTPError{Status: 400, Body: `{"error":{"message":"tool_call_id call_bbb is not found"}}`},
			want: "Bash",
		},
		{
			name: "anthropic_phrasing_finds_read",
			err:  &HTTPError{Status: 400, Body: "unexpected `tool_use_id` toolu_aaa in tool_result blocks"},
			want: "Read",
		},
		{
			name: "blocks_colon_phrasing_finds_webfetch",
			err:  &HTTPError{Status: 400, Body: "tool_use ids were not found in tool_result blocks: toolu_ccc"},
			want: "WebFetch",
		},
		{
			name: "id_not_in_messages_returns_empty",
			err:  &HTTPError{Status: 400, Body: "tool_call_id call_ghost is not found"},
			want: "",
		},
		{
			name: "no_id_pattern_returns_empty",
			err:  &HTTPError{Status: 400, Body: "max_tokens too high"},
			want: "",
		},
		{
			name: "nil_err",
			err:  nil,
			want: "",
		},
		{
			name: "non_HTTPError",
			err:  errors.New("plain"),
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractOrphanToolName(tc.err, messages); got != tc.want {
				t.Errorf("extractOrphanToolName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPairingBodyDigestStable(t *testing.T) {
	e1 := &HTTPError{Status: 400, Body: "tool_call_id call_abc"}
	e2 := &HTTPError{Status: 400, Body: "tool_call_id call_abc"}
	e3 := &HTTPError{Status: 400, Body: "tool_call_id call_xyz"}

	d1 := pairingBodyDigest(e1)
	d2 := pairingBodyDigest(e2)
	d3 := pairingBodyDigest(e3)

	if d1 == "" {
		t.Fatal("digest must not be empty for non-empty body")
	}
	if len(d1) != 16 {
		t.Errorf("digest length = %d, want 16", len(d1))
	}
	if d1 != d2 {
		t.Errorf("same body should yield same digest, got %q vs %q", d1, d2)
	}
	if d1 == d3 {
		t.Errorf("different bodies should yield different digests, both = %q", d1)
	}
	if pairingBodyDigest(nil) != "" {
		t.Error("nil error should yield empty digest")
	}
}

func TestClassifyCompletionReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil_is_done", nil, "done"},
		{"silent_turn", ErrSilentTurn, "silent_turn"},
		{"ctx_canceled_is_interrupted", context.Canceled, "interrupted"},
		{"ctx_deadline_is_interrupted", context.DeadlineExceeded, "interrupted"},
		{"max_turns_is_max_steps", errors.New("max turns reached (40); ending the task"), "max_steps"},
		{"http_error_is_generic_error", &HTTPError{Status: 500, Body: "internal"}, "error"},
		{"random_text_is_error", errors.New("some random thing"), "error"},
		{"panic_text_is_error", errors.New("panic: nil deref"), "error"},
		{
			// errors.Is unwraps %w so wrapped ErrSilentTurn must still
			// classify as silent_turn — guards against future code that
			// adds context to the sentinel error.
			"wrapped_silent_turn_is_silent_turn",
			fmt.Errorf("outer wrap: %w", ErrSilentTurn),
			"silent_turn",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCompletionReason(tc.err); got != tc.want {
				t.Errorf("classifyCompletionReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyLoopError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"silent_turn", ErrSilentTurn, "silent_turn"},
		{"tool_loop_guard", ErrLoopGuardStuck, "tool_loop_guard"},
		{"ctx_cancel", context.Canceled, "ctx_cancel"},
		{"ctx_deadline", context.DeadlineExceeded, "ctx_cancel"},
		{"max_turns", errors.New("max turns reached"), "max_turns"},
		{"history_load", errors.New("load history: io: file not found"), "history_load"},
		{"client_init_missing_client", errors.New("client required"), "client_init"},
		{"client_init_missing_emit", errors.New("LoopConfig.Emit is required"), "client_init"},
		{"panic", errors.New("panic: runtime error"), "panic"},
		{"http_4xx", &HTTPError{Status: 400, Body: "bad request"}, "http_4xx"},
		{"http_429", &HTTPError{Status: 429, Body: "rate limited"}, "http_4xx"},
		{"http_5xx", &HTTPError{Status: 502, Body: "bad gateway"}, "http_5xx"},
		{"unknown", errors.New("something else"), "unknown"},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLoopError(tc.err); got != tc.want {
				t.Errorf("classifyLoopError = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyStepError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"silent_turn", ErrSilentTurn, "silent_turn"},
		{"ctx_canceled", context.Canceled, "ctx_cancel"},
		{"ctx_deadline_is_timeout", context.DeadlineExceeded, "timeout"},
		{
			// pairing must win over generic upstream_4xx — same HTTP 400
			// status but the recovery action differs.
			"pairing_anthropic",
			&HTTPError{Status: 400, Body: `unexpected tool_use_id in tool_result blocks: not found`},
			"pairing",
		},
		{
			"pairing_openai",
			&HTTPError{Status: 400, Body: `{"error":{"message":"tool_call_id ... is not found in tool_result blocks"}}`},
			"pairing",
		},
		{
			"rate_limit_429",
			&HTTPError{Status: 429, Body: "rate limited"},
			"rate_limit",
		},
		{
			"context_overflow_prompt_too_long",
			&HTTPError{Status: 400, Body: `{"error":{"message":"prompt is too long"}}`},
			"context_overflow",
		},
		{
			"context_overflow_context_length",
			&HTTPError{Status: 400, Body: `{"error":{"message":"context_length_exceeded"}}`},
			"context_overflow",
		},
		{
			"context_overflow_too_many_tokens",
			&HTTPError{Status: 400, Body: `too many tokens in input`},
			"context_overflow",
		},
		{
			// Anthropic production phrasing observed in real traces.
			// Body carries "invalid_request_error" too, so the
			// "exceeded ... token limit" match MUST win over the
			// schema_mismatch branch below.
			"context_overflow_exceeded_model_token_limit",
			&HTTPError{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"Invalid request: Your request exceeded model token limit: 262144 (requested: 407967)"},"type":"error"}`},
			"context_overflow",
		},
		{
			"schema_mismatch_did_not_match",
			&HTTPError{Status: 400, Body: `{"error":{"message":"tools.0.input_schema did not match"}}`},
			"schema_mismatch",
		},
		{
			"schema_mismatch_validation",
			&HTTPError{Status: 400, Body: `{"error":{"message":"validation failed for messages.2"}}`},
			"schema_mismatch",
		},
		{
			// invalid_request_error catches Anthropic's generic 400 envelope
			// when no more-specific signal matches the body.
			"schema_mismatch_invalid_request_envelope",
			&HTTPError{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"unrelated"}}`},
			"schema_mismatch",
		},
		{
			// Kimi/Moonshot byte-size 400. Body carries "invalid_request_error"
			// too, so request_too_large MUST win over schema_mismatch.
			"request_too_large_kimi_byte_limit",
			&HTTPError{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"total message size 3561473 exceeds limit 2097152"}}`},
			"request_too_large",
		},
		{
			"request_too_large_413_entity",
			&HTTPError{Status: 413, Body: "Request Entity Too Large"},
			"request_too_large",
		},
		{
			"upstream_5xx_502",
			&HTTPError{Status: 502, Body: "bad gateway"},
			"upstream_5xx",
		},
		{
			"upstream_5xx_529_overloaded",
			&HTTPError{Status: 529, Body: "overloaded"},
			"upstream_5xx",
		},
		{
			"upstream_4xx_403",
			&HTTPError{Status: 403, Body: "forbidden"},
			"upstream_4xx",
		},
		{
			"upstream_4xx_400_with_no_specific_phrasing",
			&HTTPError{Status: 400, Body: "weird body"},
			"upstream_4xx",
		},
		{
			"network_connection_refused",
			errors.New(`Post "https://api.anthropic.com/v1/messages": dial tcp: connection refused`),
			"network",
		},
		{
			"network_eof",
			errors.New("unexpected EOF"),
			"network",
		},
		{
			"timeout_text_in_message",
			errors.New("client timeout exceeded while awaiting headers"),
			"timeout",
		},
		{
			"other_fallback",
			errors.New("something completely unrecognised"),
			"other",
		},
		{
			// Run-terminal sentinel from MaxTurns guard. Without an
			// explicit Contains check this would fall through to
			// "other" and lose triage signal in the bucketing table.
			"max_turns_string_sentinel",
			errors.New("max turns reached"),
			"max_turns",
		},
		{
			// loop.go formats the actual emitted error with the
			// turn count; ensure the affix doesn't break matching.
			"max_turns_with_turn_count",
			errors.New("max turns reached (40); ending the task"),
			"max_turns",
		},
		{
			// Tool loop guard sentinel — same tool called repeatedly
			// with identical args. Must classify here (not "other")
			// so the pattern checker can distinguish a stuck-tool
			// run termination from generic failures.
			"tool_loop_guard_sentinel",
			ErrLoopGuardStuck,
			"tool_loop_guard",
		},
		{
			// Defensive: a future caller wrapping the loop-guard
			// sentinel must still classify correctly.
			"tool_loop_guard_wrapped",
			fmt.Errorf("loop guard stopped repeated WebFetch call after 6 identical attempts: %w", ErrLoopGuardStuck),
			"tool_loop_guard",
		},
		{
			// Defensive: errors.As must unwrap %w wrappers so a future
			// caller wrapping a Stream error with fmt.Errorf doesn't
			// silently demote the classification to "other". Pins the
			// fix landed for PR #1893 review.
			"wrapped_HTTPError_via_fmt_Errorf",
			fmt.Errorf("stream attempt 2: %w", &HTTPError{Status: 429, Body: "rate limited"}),
			"rate_limit",
		},
		{
			// Defensive: a *HTTPError(nil) stored in an error interface
			// (the typed-nil trap) must not panic on Status / Body
			// access. classifyStepError should report "other" and move
			// on. Pins the fix landed for PR #1893 review.
			"typed_nil_HTTPError",
			func() error { var he *HTTPError; return he }(),
			"other",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyStepError(tc.err); got != tc.want {
				t.Errorf("classifyStepError = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPairingDumpUnsafeChars_NeutralisesPathTraversal pins the
// sanitization regex used to compose the pairing-dump filename. The
// path-component construction in dumpRequestBodyForPairingError
// concatenates cfg.TraceID into a filename; even though TraceID is
// currently our own UUID, defensive sanitization protects against
// future wiring changes where TraceID could carry forwarded HTTP
// header content. Any character not in [a-zA-Z0-9_-] must turn into
// `_` so attempts at directory traversal, separator injection, null
// bytes, or Windows drive prefixes can't escape the dump directory.
func TestPairingDumpUnsafeChars_NeutralisesPathTraversal(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"clean_uuid", "400c9e51-14c7-4689-b1fb-35e2373e6f32", "400c9e51-14c7-4689-b1fb-35e2373e6f32"},
		// `.` itself is unsafe (not in [a-zA-Z0-9_-]) so the dot
		// sequences in traversal payloads also turn into `_`. That's
		// what we want — any attempt to use `.` to walk parent dirs
		// or change the extension is neutralised.
		{"dot_dot_slash", "../../etc/passwd", "______etc_passwd"},
		{"backslash", "..\\..\\windows\\system32", "______windows_system32"},
		{"null_byte", "trace\x00.json", "trace__json"},
		{"drive_prefix", "C:/Windows/Temp", "C__Windows_Temp"},
		{"unicode_separator", "trace∕escape", "trace_escape"},
		{"underscores_preserved", "abc_def-123", "abc_def-123"},
		{"empty_unchanged", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pairingDumpUnsafeChars.ReplaceAllString(tc.input, "_"); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
