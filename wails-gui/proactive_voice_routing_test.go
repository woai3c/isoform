package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
	"yansu-agent/agent"
)

// This unit test exercises the real routing runner's provider failover boundary;
// no CLI, provider request, or application process is started.
type proactiveVoiceRoutingBackend struct {
	agent.AgentRunner
	calls int
	input *agent.TaskInput
	err   error
	raw   string
}

func (r *proactiveVoiceRoutingBackend) Init(context.Context) error { return nil }
func (r *proactiveVoiceRoutingBackend) IsReady() bool              { return true }
func (r *proactiveVoiceRoutingBackend) ExecuteTask(_ context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	r.calls++
	copy := *input
	r.input = &copy
	if r.err != nil {
		return "", r.err
	}
	return "voice-routing-unit", emit(&agent.StreamEvent{Type: "assistant", Content: r.raw})
}

func TestProactiveVoiceRoutingNeverRetriesOrChangesPrompt(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "pvr.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	for _, failure := range []bool{false, true} {
		name := "successful first assistant"
		if failure {
			name = "failed first assistant"
		}
		t.Run(name, func(t *testing.T) {
			primary := &proactiveVoiceRoutingBackend{raw: `{"result":"one answer"}`}
			if failure {
				primary.err = errors.New("provider unavailable before output")
			}
			backup := &proactiveVoiceRoutingBackend{}
			native := &proactiveVoiceRoutingBackend{}
			available := agent.AssistantState{SignedIn: true, Available: true, ControlProfile: true}
			router := agent.NewOrderedRoutingAgentRunner(native, map[string]agent.AgentRunner{
				string(agent.BackendClaudeCode): primary,
				string(agent.BackendCodex):      backup,
			}, agent.RoutingPolicy{
				ManagedOrder: []string{string(agent.BackendClaudeCode), string(agent.BackendCodex)},
				Assistants: map[string]agent.AssistantState{
					string(agent.BackendClaudeCode): available,
					string(agent.BackendCodex):      available,
				},
			})
			app := &App{runner: router}
			job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), voiceMailSource())
			if reason != "" {
				t.Fatal(reason)
			}
			wantPrompt, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			got, err := app.runProactiveVoice(context.Background(), job)
			if (err != nil) != failure {
				t.Fatalf("failure=%v result=%q error=%v", failure, got, err)
			}
			if primary.calls != 1 || backup.calls != 0 || native.calls != 0 {
				t.Fatalf("provider attempts: primary=%d backup=%d native=%d", primary.calls, backup.calls, native.calls)
			}
			if primary.input.Prompt != string(wantPrompt) || primary.input.SystemText != proactivevoice.SystemPrompt || primary.input.History != "" || primary.input.PromptFunc != nil {
				t.Fatal("routing changed the bounded job or system prompt")
			}
			if failure && got != nil {
				t.Fatalf("failed assistant returned partial output: %q", got)
			}
			if !failure && string(got) != primary.raw {
				t.Fatalf("first assistant output changed: %q", got)
			}
		})
	}
}

// A rejected control profile is an eligibility outcome, not a failed provider
// attempt. The production Codex runner currently reports this same limitation.
type proactiveVoiceFilteredBackend struct{ *proactiveVoiceRoutingBackend }

func (r *proactiveVoiceFilteredBackend) RoutingEligibility(*agent.TaskInput) (bool, string) {
	return false, "tool isolation has not been verified"
}

func TestProactiveVoiceRoutingUnavailableAssistantNeverCallsNative(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	available := agent.AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	for _, tc := range []struct {
		name     string
		format   string
		state    agent.AssistantState
		filtered bool
	}{
		{name: "unavailable", format: string(agent.BackendClaudeCode)},
		{name: "signed out", format: string(agent.BackendClaudeCode), state: agent.AssistantState{Available: true, ControlProfile: true}},
		{name: "control profile absent", format: string(agent.BackendClaudeCode), state: agent.AssistantState{Available: true, SignedIn: true}},
		{name: "control profile filtered", format: string(agent.BackendCodex), state: available, filtered: true},
		{name: "unknown backend", format: "unapproved-assistant", state: available},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary, native := &proactiveVoiceRoutingBackend{}, &proactiveVoiceRoutingBackend{}
			var configured agent.AgentRunner = primary
			if tc.filtered {
				configured = &proactiveVoiceFilteredBackend{primary}
			}
			router := agent.NewOrderedRoutingAgentRunner(native, map[string]agent.AgentRunner{tc.format: configured}, agent.RoutingPolicy{ManagedOrder: []string{tc.format}, Assistants: map[string]agent.AssistantState{tc.format: tc.state}})
			app := &App{runner: router}
			job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), voiceMailSource())
			if reason != "" {
				t.Fatal(reason)
			}
			if raw, err := app.runProactiveVoice(context.Background(), job); err == nil || raw != nil {
				t.Fatalf("unavailable route produced output: %q %v", raw, err)
			}
			if primary.calls != 0 || native.calls != 0 {
				t.Fatalf("provider disclosure: assistant=%d native=%d", primary.calls, native.calls)
			}
		})
	}
}

func TestProactiveVoiceUnavailableRouteKeepsTemplate(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
	}
	app := mainChatTestApp(t)
	moment := engagement.Moment{ID: "unavailable-route", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: time.Now().Add(-2 * time.Hour), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
	doorbell := engagementText{PushBody: "Capture is paused.", Title: "Screen capture has been off for 120 minutes."}
	baseline, ok := app.proactiveItem(moment, doorbell)
	if !ok {
		t.Fatal("template unavailable")
	}
	primary, native := &proactiveVoiceRoutingBackend{}, &proactiveVoiceRoutingBackend{}
	app.runner = agent.NewOrderedRoutingAgentRunner(native, map[string]agent.AgentRunner{string(agent.BackendClaudeCode): primary}, agent.RoutingPolicy{ManagedOrder: []string{string(agent.BackendClaudeCode)}})
	item, ok := app.proactiveItem(moment, doorbell)
	if !ok || item.Headline != baseline.Headline || item.Body != baseline.Body || item.VoiceVersion != "" || item.Draft || !reflect.DeepEqual(item.ActionIDs, baseline.ActionIDs) {
		t.Fatalf("unavailable assistant changed template: %+v", item)
	}
	if primary.calls != 0 || native.calls != 0 {
		t.Fatalf("fallback disclosed job: assistant=%d native=%d", primary.calls, native.calls)
	}
}

func TestProactiveVoiceRejectsDirectNativeRunnerBeforeProvider(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	runner := agent.NewNativeAgentRunner(agent.NativeRunnerConfig{BaseURL: server.URL, APIKey: "unit-test-key", DisableEnvFallback: true})
	if err := runner.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := &App{runner: runner}
	job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), voiceMailSource())
	if reason != "" {
		t.Fatal(reason)
	}
	if raw, err := app.runProactiveVoice(context.Background(), job); err == nil || err.Error() != "unavailable" || raw != nil {
		t.Fatalf("direct native runner accepted: %q %v", raw, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("native provider calls=%d", requests.Load())
	}
}

func TestProactiveVoiceRejectsUnmanagedRunnerBeforeExecution(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	runner := &proactiveVoiceRoutingBackend{raw: "private answer"}
	app := &App{runner: runner}
	if raw, err := app.runProactiveVoice(context.Background(), proactivevoice.Job{}); err == nil || raw != nil {
		t.Fatalf("unmanaged runner accepted: %q %v", raw, err)
	}
	if runner.calls != 0 {
		t.Fatal("unmanaged runner received evidence before route validation")
	}
}
