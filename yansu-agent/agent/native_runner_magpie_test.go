package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNativeRunnerBeforeDispatchChecksAfterRunInitialization(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, routingTestSSE)
	}))
	defer server.Close()
	runner := newGatewayRunner(t, server.URL)
	denied := errors.New("source access changed")
	withdrawn := false
	_, err := runner.ExecuteTask(context.Background(), &TaskInput{
		Prompt: "private source", NoTools: true, NoSystem: true,
		BeforeDispatch: func(context.Context) error {
			if withdrawn {
				return denied
			}
			return nil
		},
	}, func(event *StreamEvent) error {
		if event.Type == "run_created" {
			withdrawn = true
		}
		return nil
	})
	if !errors.Is(err, denied) || calls.Load() != 0 {
		t.Fatalf("native dispatch after initialization: err=%v HTTP calls=%d", err, calls.Load())
	}
}

func TestResolveModelForPriority(t *testing.T) {
	r := &NativeAgentRunner{
		modelAliases: map[string]string{
			"sonnet": "claude-sonnet-4-6",
			"haiku":  "group/yansu-light",
		},
		backgroundModelAliases: map[string]string{
			"sonnet": "group/yansu-background",
		},
	}
	cases := []struct {
		name     string
		model    string
		priority string
		want     string
	}{
		{"interactive uses ModelAliases", "sonnet", PriorityInteractive, "claude-sonnet-4-6"},
		{"background prefers BackgroundModelAliases", "sonnet", PriorityBackground, "group/yansu-background"},
		{"background falls through to ModelAliases", "haiku", PriorityBackground, "group/yansu-light"},
		{"background passes full ids through", "claude-opus-4-6", PriorityBackground, "claude-opus-4-6"},
		{"interactive ignores BackgroundModelAliases", "sonnet", PriorityInteractive, "claude-sonnet-4-6"},
		{"empty model stays empty", "", PriorityBackground, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.resolveModelForPriority(c.model, c.priority); got != c.want {
				t.Fatalf("resolveModelForPriority(%q, %q) = %q, want %q", c.model, c.priority, got, c.want)
			}
		})
	}
}

// The user's choice for background work too: every background task, light
// tier included, runs on the chosen route; chat is unchanged.
func TestResolveModelForPriorityFollowsChoice(t *testing.T) {
	use := ""
	r := &NativeAgentRunner{
		modelAliases:           map[string]string{"sonnet": "group/isoform", "haiku": "group/yansu-light"},
		backgroundModelAliases: map[string]string{"sonnet": "group/yansu-background"},
		backgroundModel:        func() string { return use },
	}
	if got := r.resolveModelForPriority("haiku", PriorityBackground); got != "group/yansu-light" {
		t.Fatalf("switch off: %q", got)
	}
	use = "group/isoform"
	for _, model := range []string{"haiku", "sonnet"} {
		if got := r.resolveModelForPriority(model, PriorityBackground); got != "group/isoform" {
			t.Fatalf("switch on, %s: %q", model, got)
		}
	}
	if got := r.resolveModelForPriority("haiku", PriorityInteractive); got != "group/yansu-light" {
		t.Fatalf("interactive light tasks keep their route: %q", got)
	}
}

func TestResolveModelKeepsInteractiveBehavior(t *testing.T) {
	r := &NativeAgentRunner{
		modelAliases:           map[string]string{"sonnet": "claude-sonnet-4-6"},
		backgroundModelAliases: map[string]string{"sonnet": "group/yansu-background"},
	}
	if got := r.resolveModel("sonnet"); got != "claude-sonnet-4-6" {
		t.Fatalf("resolveModel = %q, want claude-sonnet-4-6", got)
	}
}

func TestNewModelRouteObserverEmitsOnRouteChange(t *testing.T) {
	type route struct{ provider, model string }
	var emitted []route
	observe := newModelRouteObserver(func(provider, model string) {
		emitted = append(emitted, route{provider, model})
	})

	observe("claude-sub", "claude-sonnet-4-6")
	observe("claude-sub", "claude-sonnet-4-6") // same route: not reported again
	observe("codex-sub", "gpt-5")              // failover mid-run: reported
	observe("codex-sub", "gpt-5")

	want := []route{{"claude-sub", "claude-sonnet-4-6"}, {"codex-sub", "gpt-5"}}
	if len(emitted) != len(want) {
		t.Fatalf("emitted = %v, want %v", emitted, want)
	}
	for i := range want {
		if emitted[i] != want[i] {
			t.Fatalf("emitted[%d] = %v, want %v", i, emitted[i], want[i])
		}
	}
}

// routingTestSSE is a one-turn text reply in the Anthropic Messages stream
// format.
const routingTestSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`

// gatewayCapture records what the fake gateway received on the last request.
type gatewayCapture struct {
	mu      sync.Mutex
	session string
	model   string
}

func (g *gatewayCapture) last() (session, model string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.session, g.model
}

// newFakeGateway stands in for magpie. It records the session header and the
// model in the request, and answers with the route headers and a text reply.
func newFakeGateway(t *testing.T, capture *gatewayCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		capture.mu.Lock()
		capture.session = r.Header.Get("X-Magpie-Session")
		capture.model = body.Model
		capture.mu.Unlock()

		w.Header().Set("X-Magpie-Provider", "claude-sub")
		w.Header().Set("X-Magpie-Model", "claude-sonnet-4-6")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, routingTestSSE)
	}))
}

func newGatewayRunner(t *testing.T, baseURL string) *NativeAgentRunner {
	t.Helper()
	runner := NewNativeAgentRunner(NativeRunnerConfig{
		BaseURL:            baseURL,
		APIKey:             "test-key",
		DisableEnvFallback: true,
		DefaultModel:       "sonnet",
		ModelAliases: map[string]string{
			"sonnet": "claude-sonnet-4-6",
			"haiku":  "group/yansu-light",
		},
		BackgroundModelAliases: map[string]string{
			"haiku": "group/yansu-background",
		},
	})
	if err := runner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestNativeRunnerSendsSessionAndReportsModelRoute(t *testing.T) {
	capture := &gatewayCapture{}
	srv := newFakeGateway(t, capture)
	defer srv.Close()
	runner := newGatewayRunner(t, srv.URL)

	var events []*StreamEvent
	runID, err := runner.ExecuteTask(context.Background(), &TaskInput{
		SessionID: "chat-9",
		Prompt:    "say hello",
		Model:     "sonnet",
		NoTools:   true,
		NoSystem:  true,
	}, func(ev *StreamEvent) error {
		cp := *ev
		events = append(events, &cp)
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	session, model := capture.last()
	if session != "chat-9" {
		t.Errorf("gateway saw X-Magpie-Session = %q, want chat-9", session)
	}
	if model != "claude-sonnet-4-6" {
		t.Errorf("gateway saw model = %q, want claude-sonnet-4-6", model)
	}

	var routes []*StreamEvent
	var runCreated *StreamEvent
	for _, ev := range events {
		switch ev.Type {
		case "model_route":
			routes = append(routes, ev)
		case "run_created":
			runCreated = ev
		}
	}
	if runCreated == nil || runCreated.Backend != string(BackendNative) {
		t.Fatalf("run_created = %+v, want Backend native", runCreated)
	}
	if len(routes) != 1 {
		t.Fatalf("model_route events = %d, want 1", len(routes))
	}
	route := routes[0]
	if route.Provider != "claude-sub" || route.ServedModel != "claude-sonnet-4-6" {
		t.Errorf("model_route provider/model = %q/%q", route.Provider, route.ServedModel)
	}
	if route.RunID != runID || route.SessionID != "chat-9" {
		t.Errorf("model_route run/session = %q/%q, want %q/chat-9", route.RunID, route.SessionID, runID)
	}
}

func TestNativeRunnerBackgroundPriorityUsesBackgroundRoute(t *testing.T) {
	capture := &gatewayCapture{}
	srv := newFakeGateway(t, capture)
	defer srv.Close()
	runner := newGatewayRunner(t, srv.URL)

	_, err := runner.ExecuteTask(context.Background(), &TaskInput{
		SessionID: "bg-1",
		Prompt:    "summarize",
		Model:     "haiku",
		Priority:  PriorityBackground,
		NoTools:   true,
		NoSystem:  true,
	}, func(*StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if _, model := capture.last(); model != "group/yansu-background" {
		t.Fatalf("background task sent model %q, want group/yansu-background", model)
	}
}

func TestNativeRunnerSendsSelectedModelAndEffortPerRequest(t *testing.T) {
	var received struct {
		Model        string `json:"model"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, routingTestSSE)
	}))
	defer server.Close()
	selected, effort := "local/first", "medium"
	runner := NewNativeAgentRunner(NativeRunnerConfig{
		BaseURL: server.URL, APIKey: "test-key", DisableEnvFallback: true, DefaultModel: "sonnet",
		InteractiveModel: func(string) string { return selected },
		ModelEffort:      func(string, string) string { return effort },
	})
	if err := runner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ model, effort string }{{"local/first", "medium"}, {"codex/gpt-6-astra", "xhigh"}} {
		selected, effort = tc.model, tc.effort
		_, err := runner.ExecuteTask(context.Background(), &TaskInput{Prompt: "hello", Model: "sonnet", NoTools: true, NoSystem: true}, func(*StreamEvent) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if received.Model != tc.model || received.OutputConfig.Effort != tc.effort {
			t.Fatalf("request = %+v", received)
		}
	}
}
