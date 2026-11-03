package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAISuppressedCallsDoNotLogErrorBody(t *testing.T) {
	const privateText = "private-memory-correction-response"
	const body = `{"error":{"message":"` + privateText + `"}}`
	for _, throughLoop := range []bool{false, true} {
		path := "stream"
		if throughLoop {
			path = "native_loop"
		}
		for _, suppressed := range []bool{true, false} {
			mode := "ordinary"
			if suppressed {
				mode = "suppressed"
			}
			t.Run(path+"/"+mode, func(t *testing.T) {
				var logs bytes.Buffer
				previousOutput := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(previousOutput) })
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					if got := r.Header.Get("x-yansu-suppress-agentloop") == "1"; got != suppressed {
						t.Errorf("suppression header = %v, want %v", got, suppressed)
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				client := NewOpenAIClient(server.URL, "", "")
				client.http = server.Client()
				ctx := context.Background()
				if suppressed {
					ctx = ContextWithSuppressAgentloop(ctx)
				}
				var err error
				if throughLoop {
					err = Run(ctx, &LoopConfig{
						Client: client, NoTools: true, NoSystem: true, ModelID: "privacy-test",
						Prompt: privateText, MaxTokens: 64, Emit: func(LoopEvent) {},
					})
				} else {
					err = client.Stream(ctx, &Request{
						Model: "privacy-test", MaxTokens: 64,
						Messages: []Message{{Role: "user", Content: privateText}},
					}, func(*StreamEvent) error { return nil })
				}
				var upstream *HTTPError
				if !errors.As(err, &upstream) || upstream.Status != http.StatusBadRequest || upstream.Body != body || classifyStepError(err) != "upstream_4xx" {
					t.Fatal("upstream error classification or caller diagnostics changed")
				}
				loggedContent := strings.Contains(logs.String(), privateText)
				if suppressed && loggedContent {
					t.Error("suppressed OpenAI response body leaked into standard logs")
				}
				if !suppressed && !loggedContent {
					t.Error("ordinary OpenAI error diagnostics unexpectedly changed")
				}
			})
		}
	}
}
