package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"yansu-agent/agent/tools"
)

const privatePromptPrefix = "The unannounced project is named copper meadow"

func TestErrorTelemetryProperties(t *testing.T) {
	const body = privatePromptPrefix + " 保密"
	var typedNil *HTTPError
	for _, tc := range []struct {
		name string
		err  error
		want map[string]any
	}{
		{
			name: "wrapped_upstream_error",
			err:  fmt.Errorf("private wrapper: %w", &HTTPError{Status: 400, Body: body}),
			want: map[string]any{"error_kind": "upstream_4xx", "status_code": 400, "upstream_body_bytes": len(body)},
		},
		{
			name: "stream_error_with_prompt",
			err:  fmt.Errorf("stream error: %s", body),
			want: map[string]any{"error_kind": "other"},
		},
		{
			name: "typed_nil",
			err:  typedNil,
			want: map[string]any{"error_kind": "other"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := make(map[string]any)
			applyErrorTelemetryProperties(props, tc.err)
			if !reflect.DeepEqual(props, tc.want) {
				t.Errorf("error metadata = %#v, want %#v", props, tc.want)
			}
		})
	}
}

func assertNoUpstreamBodyTelemetry(t *testing.T, tel *fakeTelemetry) {
	t.Helper()
	for _, event := range tel.events {
		payload, err := json.Marshal(event.properties)
		if err != nil {
			t.Fatalf("marshal %s: %v", event.event, err)
		}
		if strings.Contains(string(payload), privatePromptPrefix) {
			t.Errorf("%s contains the private prompt prefix", event.event)
		}
		for _, key := range []string{"error", "last_error", "ce_upstream_body_redacted", "body_digest"} {
			if _, present := event.properties[key]; present {
				t.Errorf("%s contains free-form error property %q", event.event, key)
			}
		}
	}
}

func assertUpstreamErrorMetadata(t *testing.T, event captured, body, kind string) {
	t.Helper()
	for key, want := range map[string]any{
		"error_kind":          kind,
		"status_code":         http.StatusBadRequest,
		"upstream_body_bytes": len(body),
	} {
		if got := event.properties[key]; got != want {
			t.Errorf("%s.%s = %v, want %v", event.event, key, got, want)
		}
	}
}

func TestLoop_UpstreamErrorTelemetryNeverIncludesBody(t *testing.T) {
	const body = `{"error":{"message":"Rejected prompt prefix: ` + privatePromptPrefix + `"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	for _, tier := range []string{"none", "basic", "detailed"} {
		t.Run(tier, func(t *testing.T) {
			tel := &fakeTelemetry{}
			redactCalls := 0
			var localEvents []LoopEvent
			cfg := &LoopConfig{
				Client:    NewClient(WithBaseURL(srv.URL)),
				NoTools:   true,
				ModelID:   "claude-test",
				Prompt:    privatePromptPrefix,
				Telemetry: tel,
				MaxTokens: 1024,
				Emit:      func(event LoopEvent) { localEvents = append(localEvents, event) },
				Redactor: func(text string) string {
					redactCalls++
					return text // Ordinary private prose cannot be protected by PII regexes.
				},
			}
			if tier != "none" {
				cfg.ContextEngineCollector = func(ContextEngineRequest) *ContextEngineSnapshot {
					return &ContextEngineSnapshot{Tier: tier, KnowledgeFileCount: -1}
				}
			}
			err := Run(context.Background(), cfg)
			var upstream *HTTPError
			if !errors.As(err, &upstream) || upstream.Body != body {
				t.Fatalf("local caller lost the upstream error: %v", err)
			}
			foundLocalError := false
			for _, event := range localEvents {
				if event.Type == "error" && strings.Contains(event.Error, privatePromptPrefix) {
					foundLocalError = true
				}
			}
			if !foundLocalError {
				t.Error("local error event lost the upstream diagnostic")
			}
			assertNoUpstreamBodyTelemetry(t, tel)
			if redactCalls != 0 {
				t.Errorf("upstream body reached the telemetry redactor %d times", redactCalls)
			}
			for _, name := range []string{"agentloop.step.llm.error", "agentloop.run.error"} {
				count, events := countEvents(tel, name)
				if count != 1 {
					t.Fatalf("%s count = %d, want 1", name, count)
				}
				assertUpstreamErrorMetadata(t, events[0], body, "upstream_4xx")
			}
		})
	}
}

func TestLoop_PairingRetryTelemetryNeverIncludesBody(t *testing.T) {
	const body = `{"error":{"message":"tool_use_id not found; prompt prefix: ` + privatePromptPrefix + `"}}`
	success := newSSE().messageStart().textBlock("Recovered.").messageDeltaStop("end_turn").messageStop().build()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(success)
	}))
	t.Cleanup(srv.Close)
	tel := &fakeTelemetry{}
	err := Run(context.Background(), &LoopConfig{
		Client: NewClient(WithBaseURL(srv.URL)), NoTools: true, ModelID: "claude-test",
		Prompt: privatePromptPrefix, Telemetry: tel, MaxTokens: 1024, Emit: func(LoopEvent) {},
	})
	if err != nil {
		t.Fatalf("pairing retry did not recover: %v", err)
	}
	assertNoUpstreamBodyTelemetry(t, tel)
	for _, name := range []string{"agentloop.step.llm.error", "agentloop.tool_pairing.detected"} {
		count, events := countEvents(tel, name)
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", name, count)
		}
		assertUpstreamErrorMetadata(t, events[0], body, "pairing")
	}
}

func TestLoop_CompactionErrorTelemetryNeverIncludesBody(t *testing.T) {
	const body = `{"error":{"message":"Rejected prompt prefix: ` + privatePromptPrefix + `"}}`
	toolTurn := newSSE().messageStartWithUsage(1_000_000, 1).
		toolUseBlock("Unavailable", "toolu-compact").messageDeltaStop("tool_use").messageStop().build()
	success := newSSE().messageStart().textBlock("Recovered.").messageDeltaStop("end_turn").messageStop().build()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(toolTurn)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(success)
		}
	}))
	t.Cleanup(srv.Close)
	tel := &fakeTelemetry{}
	err := Run(context.Background(), &LoopConfig{
		Client: NewClient(WithBaseURL(srv.URL)), Registry: tools.NewRegistry(), ModelID: "claude-test",
		Prompt: privatePromptPrefix, Telemetry: tel, MaxTokens: 1024, Emit: func(LoopEvent) {},
		PriorHistory: []HistoryItem{{Kind: "user", Content: "Earlier request"}, {Kind: "assistant", Content: "Earlier answer"}},
	})
	if err != nil {
		t.Fatalf("compaction fallback did not recover: %v", err)
	}
	assertNoUpstreamBodyTelemetry(t, tel)
	count, events := countEvents(tel, "agentloop.context.compact.fallback_dropped_oldest_half")
	if count != 1 {
		t.Fatalf("compaction fallback count = %d, want 1", count)
	}
	assertUpstreamErrorMetadata(t, events[0], body, "upstream_4xx")
}
