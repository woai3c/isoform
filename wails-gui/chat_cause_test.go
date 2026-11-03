package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"wails-gui/storage"
	"yansu-agent/agent"
	"yansu-agent/agent/native"
)

type chatNoCallRunner struct {
	upcomingCaptureRunner
	t *testing.T
}

func (r *chatNoCallRunner) ExecuteTask(context.Context, *agent.TaskInput, agent.StreamCallback) (string, error) {
	r.t.Error("a send with no AI reached the runner")
	return "", errors.New("runner called")
}

type chatFailingTransport struct{ t *testing.T }

func (f chatFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Errorf("a send with no AI made a request to %s", req.URL)
	return nil, errors.New("request made")
}

func withChatAIReady(t *testing.T, ready bool) {
	t.Helper()
	prev := chatSendAIReady
	chatSendAIReady = func(*App) bool { return ready }
	t.Cleanup(func() { chatSendAIReady = prev })
}

func withChatProof(t *testing.T, proof handoffStopProof) {
	t.Helper()
	prev := handoffStopProbe
	handoffStopProbe = func(*App) handoffStopProof { return proof }
	t.Cleanup(func() { handoffStopProbe = prev })
}

func recordChatEvents(a *App) (func() []AgentEvent, func()) {
	var mu sync.Mutex
	var events []AgentEvent
	stop := a.onAgentEvent(func(evt AgentEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	return func() []AgentEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]AgentEvent(nil), events...)
	}, stop
}

// K1: with no AI configured, a send makes no request to any provider. It ends
// with no_assistant and close, and the caller keeps the message.
func TestChatSendWithoutAIMakesNoProviderRequest(t *testing.T) {
	a, tag := topicChatTestApp(t)
	a.runner = &chatNoCallRunner{t: t}
	withChatAIReady(t, false)
	prevTransport := http.DefaultTransport
	http.DefaultTransport = chatFailingTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	events, stop := recordChatEvents(a)
	defer stop()

	sends := map[string]func() error{
		"SendPrompt": func() error {
			return a.SendPrompt("hello", "", "", "", tag, false, false, "", "", "", "", "trace-k1")
		},
		"SendPromptWithImages": func() error {
			return a.SendPromptWithImages("hello", "", "", "", tag, nil, false, false, "", "", "", "", "trace-k1-img")
		},
		"SendMainChat": func() error { _, err := a.SendMainChat("hello"); return err },
	}
	for name, send := range sends {
		if err := send(); !errors.Is(err, errChatNoAssistant) {
			t.Fatalf("%s err = %v, want no_assistant", name, err)
		}
	}
	got := events()
	if len(got) != 2*len(sends) {
		t.Fatalf("events = %+v", got)
	}
	for i := 0; i < len(got); i += 2 {
		if got[i].Type != "error" || got[i].Code != chatCodeNoAssistant || got[i].Tag != tag || got[i+1].Type != "close" {
			t.Fatalf("events = %+v", got)
		}
	}
	a.mu.Lock()
	busy := a.activeTags[tag]
	a.mu.Unlock()
	if busy {
		t.Fatal("a kept send must not hold the chat")
	}
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a kept send must not be stored as sent: %+v", rows)
	}
}

// K1: a configured AI's 401 or 403 is signed out; the same words in a 500's
// body are the AI's error. Only the number and the CLI's own check decide.
func TestChatErrorCodeComesFromProof(t *testing.T) {
	a, _ := topicChatTestApp(t)
	authWords := `{"error":"unauthorized: authentication failed, run claude login, reconnecting 401 403"}`
	for _, tc := range []struct {
		name  string
		err   error
		proof handoffStopProof
		want  string
	}{
		{"401", &native.HTTPError{Status: 401, Body: "nope"}, handoffStopProof{}, chatCodeSignedOut},
		{"wrapped 403", fmt.Errorf("stream: %w", &native.HTTPError{Status: 403}), handoffStopProof{}, chatCodeSignedOut},
		{"500 with auth words", &native.HTTPError{Status: 500, Body: authWords}, handoffStopProof{}, chatCodeAssistantError},
		{"503", &native.HTTPError{Status: 503}, handoffStopProof{}, chatCodeServer},
		{"words, no status", errors.New(authWords), handoffStopProof{}, ""},
		{"CLI says signed out", errors.New("exit status 1"), handoffStopProof{External: true}, chatCodeSignedOut},
		{"CLI check failed", errors.New("exit status 1"), handoffStopProof{External: true, SignInErr: errors.New("probe")}, ""},
		{"CLI signed in", errors.New(authWords), handoffStopProof{External: true, SignedIn: true}, ""},
		{"no AI at start", errors.New("x"), handoffStopProof{NoAIAtStart: true}, chatCodeNoAssistant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withChatProof(t, tc.proof)
			evt := a.chatFailureEvent("tag", "run", tc.err, "")
			if evt.Code != tc.want || evt.Error != tc.err.Error() {
				t.Fatalf("event = %+v, want code %q", evt, tc.want)
			}
		})
	}
	// The streamed error event carries the provider's status as a number.
	withChatProof(t, handoffStopProof{})
	if evt := a.withChatCause(AgentEvent{Type: "error", Error: authWords}, 500); evt.Code != chatCodeAssistantError {
		t.Fatalf("500 event = %+v", evt)
	}
	if evt := a.withChatCause(AgentEvent{Type: "error", Error: "x"}, 401); evt.Code != chatCodeSignedOut {
		t.Fatalf("401 event = %+v", evt)
	}
	if evt := a.withChatCause(AgentEvent{Type: "done", Error: "x"}, 401); evt.Code != "" {
		t.Fatalf("only errors carry a cause: %+v", evt)
	}
}

// K1: a list item whose runner isn't available ends with runner_unavailable
// and names that runner.
func TestChatListRunnerUnavailableIsCoded(t *testing.T) {
	a, _ := topicChatTestApp(t)
	withChatProof(t, handoffStopProof{})
	for backend, name := range map[string]string{string(agent.BackendClaudeCode): "Claude Code", string(agent.BackendCodex): "Codex"} {
		routeErr := checkListHandoffRoute(backend, &agent.StreamEvent{Type: "backend_route", Backend: "native"})
		if !errors.Is(routeErr, errListRunnerUnavailable) {
			t.Fatalf("route err = %v", routeErr)
		}
		evt := a.chatFailureEvent("tag", "run", routeErr, backend)
		if evt.Code != chatCodeRunnerUnavailable || evt.Assistant != name {
			t.Fatalf("event = %+v", evt)
		}
	}
}

// The phone's chat projection carries the same cause as the computer's row.
func TestMobileChatErrorRowCarriesCode(t *testing.T) {
	var reply mobileChatReply
	reply.note(AgentEvent{Type: "error", Error: "anthropic: HTTP 401", Code: chatCodeSignedOut, Assistant: "Anthropic"})
	if len(reply.rows) != 1 {
		t.Fatalf("rows = %+v", reply.rows)
	}
	view, ok := mobileChatItemFromRow(reply.rows[0])
	if !ok || view.Kind != "error" || view.Code != chatCodeSignedOut || view.Assistant != "Anthropic" || view.ToolName != "" {
		t.Fatalf("view = %+v", view)
	}
	plain, ok := mobileChatItemFromRow(storage.StreamItemRow{Kind: "error", Content: "boom"})
	if !ok || plain.Code != "" || plain.Assistant != "" {
		t.Fatalf("plain view = %+v", plain)
	}
}

func TestChatAssistantName(t *testing.T) {
	for _, tc := range []struct{ provider, want string }{
		{"claude-code", "Claude Code"}, {"codex", "Codex"}, {"mystery", ""},
	} {
		if got := chatAssistantName(AIProviderConfig{Provider: tc.provider}); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.provider, got, tc.want)
		}
	}
}
