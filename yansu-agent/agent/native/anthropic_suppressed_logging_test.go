package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnthropicSuppressedCallsDoNotLogResponseContent(t *testing.T) {
	const privateText = "forgotten-canonical-fact-private-marker"
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "stream",
			status: http.StatusOK,
			body:   "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"" + privateText + "\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name:   "upstream_error",
			status: http.StatusBadRequest,
			body:   `{"error":{"type":"invalid_request_error","message":"` + privateText + `"}}`,
		},
	} {
		for _, mode := range []struct {
			name       string
			suppressed bool
		}{
			{name: "suppressed", suppressed: true},
			{name: "ordinary", suppressed: false},
		} {
			t.Run(response.name+"/"+mode.name, func(t *testing.T) {
				t.Setenv("YANSU_SSE_DEBUG", "1")
				var logs bytes.Buffer
				previousOutput := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(previousOutput) })

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					if got := r.Header.Get("x-yansu-suppress-agentloop") == "1"; got != mode.suppressed {
						t.Errorf("suppression header = %v, want %v", got, mode.suppressed)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(response.status)
					_, _ = io.WriteString(w, response.body)
				}))
				t.Cleanup(server.Close)
				client := NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client()))
				ctx := context.Background()
				if mode.suppressed {
					ctx = ContextWithSuppressAgentloop(ctx)
				}
				var text strings.Builder
				events := 0
				err := client.Stream(ctx, &Request{
					Model:     "canonical-fact-test",
					Messages:  []Message{{Role: "user", Content: "Normalize the complete fact."}},
					MaxTokens: 64,
				}, func(event *StreamEvent) error {
					events++
					if event.Delta != nil {
						text.WriteString(event.Delta.Text)
					}
					return nil
				})
				switch response.status {
				case http.StatusOK:
					if err != nil || text.String() != privateText || events != 2 {
						t.Fatalf("stream behavior changed: err=%v events=%d text=%q", err, events, text.String())
					}
				default:
					var upstream *HTTPError
					if !errors.As(err, &upstream) || upstream.Status != response.status || upstream.Body != response.body {
						t.Fatalf("upstream error behavior changed: %v", err)
					}
				}
				loggedContent := strings.Contains(logs.String(), privateText)
				if mode.suppressed && loggedContent {
					t.Fatal("suppressed response content leaked into standard logs")
				}
				if !mode.suppressed && !loggedContent {
					t.Fatal("ordinary response debug logging unexpectedly changed")
				}
			})
		}
	}
}

func TestParseSSEPreservesExplicitDebugLogging(t *testing.T) {
	t.Setenv("YANSU_SSE_DEBUG", "1")
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	const stream = "data: {\"type\":\"message_stop\"}\n\n"
	if err := ParseSSE(strings.NewReader(stream), func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `data: {"type":"message_stop"}`) {
		t.Fatal("exported ParseSSE no longer honors ordinary SSE debug logging")
	}
}

func TestLoopSuppressedCallsDoNotPersistResponseContent(t *testing.T) {
	const privateText = "forgotten-canonical-fact-private-marker"
	const errorBody = `{"error":{"message":"` + privateText + `"}}`
	success := newSSE().messageStart().textBlock("Recovered.").messageDeltaStop("end_turn").messageStop().build()
	for _, scenario := range []string{"http_error", "stream_error", "pairing_retry", "compaction_error"} {
		for _, suppressed := range []bool{true, false} {
			mode := "ordinary"
			if suppressed {
				mode = "suppressed"
			}
			t.Run(scenario+"/"+mode, func(t *testing.T) {
				t.Setenv("YANSU_SSE_DEBUG", "1")
				t.Setenv("YANSU_DUMP_PAIRING_BODY", "1")
				dumpRoot := t.TempDir()
				t.Setenv("TMPDIR", dumpRoot)
				var logs bytes.Buffer
				previousOutput := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(previousOutput) })
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got := r.Header.Get("x-yansu-suppress-agentloop") == "1"; got != suppressed {
						t.Errorf("suppression header = %v, want %v", got, suppressed)
					}
					request := requests.Add(1)
					switch scenario {
					case "http_error":
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, errorBody)
						return
					case "stream_error":
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\""+privateText+"\"}}\n\n")
						return
					case "pairing_retry":
						if request == 1 {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = io.WriteString(w, `{"error":{"message":"tool_use_id not found: `+privateText+`"}}`)
							return
						}
					case "compaction_error":
						switch request {
						case 1:
							w.WriteHeader(http.StatusBadRequest)
							_, _ = io.WriteString(w, `{"error":{"message":"prompt is too long"}}`)
							return
						case 2:
							w.WriteHeader(http.StatusBadRequest)
							_, _ = io.WriteString(w, errorBody)
							return
						}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write(success)
				}))
				t.Cleanup(server.Close)
				ctx := context.Background()
				if suppressed {
					ctx = ContextWithSuppressAgentloop(ctx)
				}
				err := Run(ctx, &LoopConfig{
					Client:  NewClient(WithBaseURL(server.URL), WithHTTPClient(server.Client())),
					NoTools: true, NoSystem: true, ModelID: "claude-test", Prompt: privateText,
					MaxTokens: 64, Emit: func(LoopEvent) {},
					PriorHistory: []HistoryItem{{Kind: "user", Content: "Earlier request"}, {Kind: "assistant", Content: "Earlier answer"}},
				})
				switch scenario {
				case "http_error":
					var upstream *HTTPError
					if !errors.As(err, &upstream) || upstream.Status != http.StatusBadRequest || upstream.Body != errorBody || classifyStepError(err) != "upstream_4xx" {
						t.Fatal("upstream error classification or caller diagnostics changed")
					}
				case "stream_error":
					if err == nil || !strings.Contains(err.Error(), privateText) {
						t.Fatal("caller lost the stream error")
					}
				case "pairing_retry":
					if err != nil || requests.Load() != 2 {
						t.Fatalf("pairing recovery changed: err=%v requests=%d", err, requests.Load())
					}
				case "compaction_error":
					if err != nil || requests.Load() != 3 || !strings.Contains(logs.String(), "auto-compact failed") {
						t.Fatalf("compaction fallback changed: err=%v requests=%d", err, requests.Load())
					}
				}
				if strings.Contains(logs.String(), privateText) == suppressed {
					t.Error("response logging did not respect suppression")
				}
				dumps, err := filepath.Glob(filepath.Join(dumpRoot, "yansu-pairing-dumps", "*.json"))
				if err != nil {
					t.Fatal(err)
				}
				if suppressed && len(dumps) != 0 {
					t.Error("suppressed request was written to a pairing diagnostic dump")
				}
				if !suppressed && scenario == "pairing_retry" {
					if len(dumps) != 1 {
						t.Fatalf("ordinary pairing dump count = %d, want 1", len(dumps))
					}
					body, err := os.ReadFile(dumps[0])
					if err != nil || !bytes.Contains(body, []byte(privateText)) {
						t.Fatal("ordinary opt-in pairing diagnostics changed")
					}
				}
			})
		}
	}
}
