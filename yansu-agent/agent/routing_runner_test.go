package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/native"
)

// fakeAgentRunner stands in for the native or external runner. ExecuteTask
// emits run_created with the configured ids, then runs onRun so a test can
// call the router while the run is in flight.
type fakeAgentRunner struct {
	runID        string
	sessionID    string
	initErr      error
	shutErr      error
	ready        bool
	onRun        func()
	executeErr   error
	events       []*StreamEvent
	beforeEvents bool

	inits       int
	executed    []*TaskInput
	cancelled   []string
	responses   []string
	cancelPerms []string
	shutdowns   int
}

func (f *fakeAgentRunner) Init(context.Context) error {
	f.inits++
	return f.initErr
}

func (f *fakeAgentRunner) ExecuteTask(_ context.Context, input *TaskInput, callback StreamCallback) (string, error) {
	f.executed = append(f.executed, input)
	if f.beforeEvents {
		return f.runID, f.executeErr
	}
	if err := callback(&StreamEvent{Type: "run_created", RunID: f.runID, SessionID: f.sessionID}); err != nil {
		return f.runID, err
	}
	if f.onRun != nil {
		f.onRun()
	}
	for _, event := range f.events {
		if err := callback(event); err != nil {
			return f.runID, err
		}
	}
	_ = callback(&StreamEvent{Type: "close", RunID: f.runID})
	return f.runID, f.executeErr
}

func (f *fakeAgentRunner) CancelRun(runID string) error {
	f.cancelled = append(f.cancelled, runID)
	return nil
}

func (f *fakeAgentRunner) RespondToPermission(sessionID, optionID string) error {
	f.responses = append(f.responses, sessionID+":"+optionID)
	return nil
}

func (f *fakeAgentRunner) CancelPermission(sessionID string) {
	f.cancelPerms = append(f.cancelPerms, sessionID)
}

func (f *fakeAgentRunner) Shutdown() error {
	f.shutdowns++
	return f.shutErr
}

func (f *fakeAgentRunner) IsReady() bool { return f.ready }

type sourceRevokingInitRunner struct {
	fakeAgentRunner
	onInit func()
}

func (r *sourceRevokingInitRunner) Init(ctx context.Context) error {
	r.onInit()
	return r.fakeAgentRunner.Init(ctx)
}

func TestRoutingBeforeDispatchRechecksAfterProviderInitialization(t *testing.T) {
	denied := errors.New("source access changed")
	withdrawn, composed := false, false
	external := &sourceRevokingInitRunner{onInit: func() { withdrawn = true }}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	_, err := router.ExecuteTask(context.Background(), &TaskInput{
		PromptFunc: func() (string, string) { composed = true; return "private source", "private history" },
		BeforeDispatch: func(context.Context) error {
			if withdrawn {
				return denied
			}
			return nil
		},
	}, nil)
	if !composed || external.inits != 1 || !errors.Is(err, denied) || len(external.executed) != 0 || len(native.executed) != 0 {
		t.Fatalf("provider initialization gate: composed=%v init=%d err=%v external=%d native=%d", composed, external.inits, err, len(external.executed), len(native.executed))
	}
}

func TestRoutingBeforeDispatchRechecksBeforeFallback(t *testing.T) {
	denied := errors.New("source access changed")
	withdrawn := false
	external := &fakeAgentRunner{runID: "external", executeErr: routingLedgerFailure{cause: errors.New("retry later")}, onRun: func() { withdrawn = true }}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	_, err := router.ExecuteTask(context.Background(), &TaskInput{
		Prompt: "private source", BeforeDispatch: func(context.Context) error {
			if withdrawn {
				return denied
			}
			return nil
		},
	}, nil)
	if !errors.Is(err, denied) || len(external.executed) != 1 || len(native.executed) != 0 {
		t.Fatalf("fallback gate: err=%v external=%d native=%d", err, len(external.executed), len(native.executed))
	}
}

func TestRoutingBeforeDispatchDenialNeverFallsBack(t *testing.T) {
	denied := errors.New("source access changed")
	external := &fakeAgentRunner{runID: "external", executeErr: &ExternalAttemptError{
		Cause: &native.DispatchDeniedError{Cause: denied},
	}}
	fallback := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(fallback, external, signedInPolicy)
	_, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "private source"}, nil)
	if !errors.Is(err, denied) || len(external.executed) != 1 || len(fallback.executed) != 0 {
		t.Fatalf("dispatch refusal replayed: err=%v external=%d fallback=%d", err, len(external.executed), len(fallback.executed))
	}
}

func TestRoutingSourceBoundContextUsesOnlyNativeTransport(t *testing.T) {
	for _, preference := range []string{"", "external", "claude-code", "codex"} {
		t.Run("preference="+preference, func(t *testing.T) {
			failure := errors.New("native transport unavailable")
			external := &fakeAgentRunner{runID: "external"}
			nativeRunner := &fakeAgentRunner{runID: "native", ready: true, executeErr: failure, beforeEvents: true}
			router := NewRoutingAgentRunner(nativeRunner, external, signedInPolicy)
			checks := 0
			_, err := router.ExecuteTask(context.Background(), &TaskInput{
				PromptFunc:            func() (string, string) { return "source prompt", "source history" },
				RequireNativeDispatch: true, ForceBackend: preference,
				BeforeDispatch: func(context.Context) error { checks++; return nil },
			}, nil)
			if !errors.Is(err, failure) || len(nativeRunner.executed) != 1 || external.inits != 0 || len(external.executed) != 0 || checks != 1 {
				t.Fatalf("source route escaped native: err=%v native=%d assistant init=%d execute=%d checks=%d", err, len(nativeRunner.executed), external.inits, len(external.executed), checks)
			}
			input := nativeRunner.executed[0]
			if !input.RequireNativeDispatch || input.BeforeDispatch == nil || input.Prompt != "source prompt" || input.History != "source history" {
				t.Fatalf("deferred task copy dropped source restrictions: %+v", input)
			}
			// The restriction belongs to this task, never the user's route policy.
			if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "ordinary chat"}, nil); err != nil || len(external.executed) != 1 {
				t.Fatalf("ordinary chat no longer uses the preferred assistant: err=%v executions=%d", err, len(external.executed))
			}
		})
	}
}

func TestRoutingBackgroundLearningStaysOnNativeRunner(t *testing.T) {
	for _, preference := range []string{"", "claude-code", "codex"} {
		t.Run("preference="+preference, func(t *testing.T) {
			failure := errors.New("native transport unavailable")
			external := &fakeAgentRunner{runID: "external"}
			nativeRunner := &fakeAgentRunner{runID: "native", ready: true, executeErr: failure, beforeEvents: true}
			router := NewRoutingAgentRunner(nativeRunner, external, signedInPolicy)
			_, err := router.ExecuteTask(context.Background(), &TaskInput{
				Prompt: "merge these memory entries", BackendHint: BackendHintLearning,
				ForceBackend: preference, Ephemeral: true, NoTools: true,
			}, nil)
			if !errors.Is(err, failure) || len(nativeRunner.executed) != 1 || len(external.executed) != 0 {
				t.Fatalf("learning run escaped native: err=%v native=%d assistant execute=%d", err, len(nativeRunner.executed), len(external.executed))
			}
			if input := nativeRunner.executed[0]; input.BackendHint != BackendHintLearning {
				t.Fatalf("deferred task copy dropped the learning hint: %+v", input)
			}
			// The restriction belongs to this run kind, never the user's route policy.
			if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "ordinary chat"}, nil); err != nil || len(external.executed) != 1 {
				t.Fatalf("ordinary chat no longer uses the preferred assistant: err=%v executions=%d", err, len(external.executed))
			}
		})
	}
}

func TestRoutingBeforeDispatchRefusalReleasesCooldownProbe(t *testing.T) {
	external := &fakeAgentRunner{runID: "external"}
	fallback := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(fallback, external, signedInPolicy)
	now := time.Now()
	router.now = func() time.Time { return now }
	router.finishAttempt("claude-code", errors.New("rate_limit_error"))
	now = now.Add(31 * time.Minute)
	denied := errors.New("source access changed")
	_, err := router.ExecuteTask(context.Background(), &TaskInput{
		Prompt: "source prompt", BeforeDispatch: func(context.Context) error { return denied },
	}, nil)
	if !errors.Is(err, denied) || len(external.executed) != 0 || len(fallback.executed) != 0 {
		t.Fatalf("refused cooldown probe executed: err=%v external=%d fallback=%d", err, len(external.executed), len(fallback.executed))
	}
	if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "ordinary chat"}, nil); err != nil || len(external.executed) != 1 || len(fallback.executed) != 0 {
		t.Fatalf("refusal stranded cooldown probe: err=%v external=%d fallback=%d", err, len(external.executed), len(fallback.executed))
	}
}

// routingTask builds a TaskInput and applies mutate to it. A nil mutate gives
// the plain task.
func routingTask(mutate func(*TaskInput)) *TaskInput {
	in := &TaskInput{Prompt: "do the thing"}
	if mutate != nil {
		mutate(in)
	}
	return in
}

// handoffTask is a handoff with a work directory, the case that routes to the
// external CLI when the policy allows it.
func handoffTask(in *TaskInput) {
	in.BackendHint = BackendHintHandoff
	in.WorkDir = "/work/repo"
}

var signedInPolicy = RoutingPolicy{
	ExternalFormat:            "claude-code",
	ExternalSignedIn:          true,
	PreferExternalForHandoffs: true,
}

func TestChooseRouteEveryTaskShapeAndAssistantState(t *testing.T) {
	shapes := []struct {
		name  string
		input TaskInput
	}{
		{"chat", TaskInput{BackendHint: BackendHintChat}},
		{"scheduled", TaskInput{BackendHint: BackendHintCron, AutoApproveTools: true}},
		{"playbook", TaskInput{BackendHint: BackendHintCron, AutoApproveTools: true, AuthorityInitiativeID: "playbook", AuthorityGrants: []authority.Grant{{}}}},
		{"initiative", TaskInput{BackendHint: BackendHintInitiative, AuthorityInitiativeID: "initiative"}},
		{"handoff", TaskInput{BackendHint: BackendHintHandoff, WorkDir: "/work"}},
		{"ephemeral", TaskInput{BackendHint: BackendHintEphemeral, Ephemeral: true}},
		{"write restricted", TaskInput{DenyExternalWrites: true}},
		{"computer use hint", TaskInput{BackendHint: BackendHintComputerUse}},
		{"computer use tool", TaskInput{AllowedTools: []string{"cua_driver"}}},
		{"allowed tools", TaskInput{AllowedTools: []string{"Read", "Write"}}},
		{"disabled tools", TaskInput{DisabledTools: []string{"Bash"}}},
		{"no tools", TaskInput{NoTools: true}},
		{"plan mode", TaskInput{PlanMode: true}},
		{"programmatic tools", TaskInput{ProgrammaticToolCalling: true}},
		{"plain", TaskInput{}},
	}
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	states := []struct {
		name       string
		order      []string
		assistants map[string]AssistantState
		available  map[string]bool
		want       []Backend
	}{
		{"none", nil, nil, nil, []Backend{BackendNative}},
		{"Claude", nil, map[string]AssistantState{"claude-code": ready}, map[string]bool{"claude-code": true}, []Backend{BackendClaudeCode, BackendNative}},
		{"Codex", nil, map[string]AssistantState{"codex": ready}, map[string]bool{"codex": true}, []Backend{BackendCodex, BackendNative}},
		{"both default order", nil, map[string]AssistantState{"claude-code": ready, "codex": ready}, map[string]bool{"claude-code": true, "codex": true}, []Backend{BackendClaudeCode, BackendCodex, BackendNative}},
		{"user order deduplicated", []string{"codex", "codex", "claude-code"}, map[string]AssistantState{"claude-code": ready, "codex": ready}, map[string]bool{"claude-code": true, "codex": true}, []Backend{BackendCodex, BackendClaudeCode, BackendNative}},
		{"signed out", nil, map[string]AssistantState{"claude-code": {Available: true, ControlProfile: true}}, map[string]bool{"claude-code": true}, []Backend{BackendNative}},
		{"binary absent", nil, map[string]AssistantState{"claude-code": {SignedIn: true, ControlProfile: true}}, map[string]bool{"claude-code": true}, []Backend{BackendNative}},
		{"control profile failed", nil, map[string]AssistantState{"codex": {Available: true, SignedIn: true}}, map[string]bool{"codex": true}, []Backend{BackendNative}},
		{"cooldown or not initialized", nil, map[string]AssistantState{"claude-code": ready, "codex": ready}, map[string]bool{"codex": true}, []Backend{BackendCodex, BackendNative}},
		{"tool unavailable", nil, map[string]AssistantState{"claude-code": {SignedIn: true, ControlProfile: true, Reason: "required tool unavailable"}}, map[string]bool{"claude-code": true}, []Backend{BackendNative}},
	}
	for _, shape := range shapes {
		for _, state := range states {
			t.Run(shape.name+"/"+state.name, func(t *testing.T) {
				route := chooseRoute(&shape.input, RoutingPolicy{ManagedOrder: state.order, Assistants: state.assistants}, state.available, authority.ExecutionModeNormal)
				var got []Backend
				for _, candidate := range route {
					got = append(got, candidate.Backend)
					if candidate.Reason == "" {
						t.Fatal("empty route reason")
					}
				}
				if !reflect.DeepEqual(got, state.want) {
					t.Fatalf("route = %v, want %v", got, state.want)
				}
			})
		}
	}
}

func TestChooseRouteOverrides(t *testing.T) {
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	policy := RoutingPolicy{Assistants: map[string]AssistantState{"claude-code": ready, "codex": ready}}
	available := map[string]bool{"claude-code": true, "codex": true}
	cases := []struct {
		name  string
		input TaskInput
		mode  authority.ExecutionMode
		want  []Backend
	}{
		{"shadow", TaskInput{}, authority.ExecutionModeShadow, []Backend{BackendNative}},
		{"unknown mode", TaskInput{}, authority.ExecutionMode("unknown"), []Backend{BackendNative}},
		{"learning", TaskInput{BackendHint: BackendHintLearning}, "", []Backend{BackendNative}},
		{"learning beats assistant override", TaskInput{BackendHint: BackendHintLearning, ForceBackend: "codex"}, "", []Backend{BackendNative}},
		{"native override", TaskInput{ForceBackend: "native"}, "", []Backend{BackendNative}},
		{"external override preserves order", TaskInput{ForceBackend: "external"}, "", []Backend{BackendClaudeCode, BackendCodex, BackendNative}},
		{"named assistant override", TaskInput{ForceBackend: "codex"}, "", []Backend{BackendCodex, BackendClaudeCode, BackendNative}},
		{"mode beats override", TaskInput{ForceBackend: "codex"}, authority.ExecutionModeShadow, []Backend{BackendNative}},
		{"unknown override preserves order", TaskInput{ForceBackend: "other"}, "", []Backend{BackendClaudeCode, BackendCodex, BackendNative}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route := chooseRoute(&tc.input, policy, available, tc.mode)
			var got []Backend
			for _, candidate := range route {
				got = append(got, candidate.Backend)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("route = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoutingExecuteTaskRoutesHandoffToExternalAndTagsRunCreated(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext"}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	var events []*StreamEvent
	runID, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), func(ev *StreamEvent) error {
		cp := *ev
		events = append(events, &cp)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runID != "ext-run" {
		t.Fatalf("runID = %q, want ext-run", runID)
	}
	if len(external.executed) != 1 || len(native.executed) != 0 {
		t.Fatalf("external=%d native=%d executions, want 1 and 0", len(external.executed), len(native.executed))
	}
	if len(events) < 2 || events[1].Type != "run_created" {
		t.Fatalf("first event = %+v, want run_created", events)
	}
	if events[1].Backend != string(BackendClaudeCode) {
		t.Errorf("run_created Backend = %q, want external", events[0].Backend)
	}
	if events[1].RunID != "ext-run" || events[1].SessionID != "s-ext" {
		t.Errorf("run_created ids = %q/%q", events[0].RunID, events[0].SessionID)
	}
}

func TestRoutingExecuteTaskNativeOverrideAndTagsRunCreated(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext"}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	var events []*StreamEvent
	runID, err := router.ExecuteTask(context.Background(), routingTask(func(in *TaskInput) { in.ForceBackend = "native" }), func(ev *StreamEvent) error {
		cp := *ev
		events = append(events, &cp)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runID != "native-run" || len(native.executed) != 1 || len(external.executed) != 0 {
		t.Fatalf("runID=%q native=%d external=%d", runID, len(native.executed), len(external.executed))
	}
	if events[0].Backend != string(BackendNative) {
		t.Errorf("run_created Backend = %q, want native", events[0].Backend)
	}
}

func TestRoutingExecuteTaskRejectsNilInput(t *testing.T) {
	router := NewRoutingAgentRunner(&fakeAgentRunner{}, nil, RoutingPolicy{})
	if _, err := router.ExecuteTask(context.Background(), nil, func(*StreamEvent) error { return nil }); err == nil {
		t.Fatal("expected an error for nil input")
	}
}

func TestRoutingCancelRunReachesOwningBackendMidRun(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext"}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	external.onRun = func() {
		if err := router.CancelRun("ext-run"); err != nil {
			t.Errorf("CancelRun: %v", err)
		}
	}
	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(external.cancelled) != 1 || external.cancelled[0] != "ext-run" {
		t.Fatalf("external cancelled = %v, want [ext-run]", external.cancelled)
	}
	if len(native.cancelled) != 0 {
		t.Fatalf("native cancelled = %v, want none", native.cancelled)
	}

	// The run has finished, so its ownership is gone. A later cancel falls
	// back to native, which treats an unknown run as a no-op.
	if err := router.CancelRun("ext-run"); err != nil {
		t.Fatal(err)
	}
	if len(native.cancelled) != 1 || native.cancelled[0] != "ext-run" {
		t.Fatalf("native cancelled after run ended = %v, want [ext-run]", native.cancelled)
	}
}

func TestRoutingCancelUnknownRunFallsBackToNative(t *testing.T) {
	native := &fakeAgentRunner{}
	external := &fakeAgentRunner{}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.CancelRun("never-started"); err != nil {
		t.Fatal(err)
	}
	if len(native.cancelled) != 1 || len(external.cancelled) != 0 {
		t.Fatalf("native=%v external=%v", native.cancelled, external.cancelled)
	}
}

func TestRoutingAssistantPermissionsReachSharedNativeGate(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext"}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	external.onRun = func() {
		_ = router.RespondToPermission("s-ext", "allow_once")
		router.CancelPermission("s-ext")
	}
	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(native.responses) != 1 || native.responses[0] != "s-ext:allow_once" {
		t.Fatalf("shared gate responses = %v", native.responses)
	}
	if len(native.cancelPerms) != 1 || native.cancelPerms[0] != "s-ext" {
		t.Fatalf("shared gate cancelPerms = %v", native.cancelPerms)
	}

	// Once the run has ended, the session has no owner, so the answer goes
	// to native.
	if err := router.RespondToPermission("s-ext", "reject_once"); err != nil {
		t.Fatal(err)
	}
	if len(native.responses) != 2 || native.responses[1] != "s-ext:reject_once" {
		t.Fatalf("native responses after run ended = %v", native.responses)
	}
}

func TestRoutingSessionMappingSurvivesUntilLastRun(t *testing.T) {
	native := &fakeAgentRunner{}
	external := &fakeAgentRunner{}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)

	router.attach("run-a", "shared", BackendExternal)
	router.attach("run-b", "shared", BackendExternal)
	router.detach("run-a")
	if got := router.runnerForSession("shared"); got != external {
		t.Fatal("session should still route to external while run-b is in flight")
	}
	router.detach("run-b")
	if got := router.runnerForSession("shared"); got != native {
		t.Fatal("session should fall back to native after its last run")
	}
	if len(router.sessions) != 0 || len(router.sessionRefs) != 0 || len(router.runs) != 0 {
		t.Fatalf("maps not cleaned: sessions=%v refs=%v runs=%v", router.sessions, router.sessionRefs, router.runs)
	}
}

func TestRoutingInitExternalFailureDisablesExternalOnly(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext", initErr: errors.New("claude not found on PATH")}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)

	if err := router.Init(context.Background()); err != nil {
		t.Fatalf("Init failed on external error: %v", err)
	}
	if !router.IsReady() {
		t.Fatal("router should be ready when native is ready")
	}
	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(native.executed) != 1 || len(external.executed) != 0 {
		t.Fatalf("handoff should run natively when external is disabled: native=%d external=%d", len(native.executed), len(external.executed))
	}
}

func TestRoutingInitNativeFailureFailsInit(t *testing.T) {
	nativeErr := errors.New("native broken")
	native := &fakeAgentRunner{initErr: nativeErr}
	external := &fakeAgentRunner{}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)

	if err := router.Init(context.Background()); !errors.Is(err, nativeErr) {
		t.Fatalf("Init error = %v, want %v", err, nativeErr)
	}
	if external.inits != 0 {
		t.Fatal("external should not be initialized after native fails")
	}
}

func TestRoutingWithoutExternalRunsEverythingNative(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s", ready: true}
	router := NewRoutingAgentRunner(native, nil, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), func(*StreamEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(native.executed) != 1 {
		t.Fatalf("native executed = %d, want 1", len(native.executed))
	}
	if err := router.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingSetPolicyEnablesExternalOnSignIn(t *testing.T) {
	native := &fakeAgentRunner{runID: "native-run", sessionID: "s-native", ready: true}
	external := &fakeAgentRunner{runID: "ext-run", sessionID: "s-ext"}
	router := NewRoutingAgentRunner(native, external, RoutingPolicy{
		ExternalFormat:            "claude-code",
		PreferExternalForHandoffs: true,
	})
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	noop := func(*StreamEvent) error { return nil }

	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), noop); err != nil {
		t.Fatal(err)
	}
	if len(external.executed) != 0 {
		t.Fatal("handoff routed externally before sign-in")
	}

	router.SetPolicy(signedInPolicy)
	if _, err := router.ExecuteTask(context.Background(), routingTask(handoffTask), noop); err != nil {
		t.Fatal(err)
	}
	if len(external.executed) != 1 {
		t.Fatal("handoff not routed externally after sign-in")
	}
}

func TestRoutingShutdownReturnsFirstErrorAndShutsDownBoth(t *testing.T) {
	nativeErr := errors.New("native shutdown")
	externalErr := errors.New("external shutdown")

	t.Run("both fail returns native error", func(t *testing.T) {
		native := &fakeAgentRunner{shutErr: nativeErr}
		external := &fakeAgentRunner{shutErr: externalErr}
		router := NewRoutingAgentRunner(native, external, RoutingPolicy{})
		if err := router.Shutdown(); !errors.Is(err, nativeErr) {
			t.Fatalf("Shutdown = %v, want native error", err)
		}
		if native.shutdowns != 1 || external.shutdowns != 1 {
			t.Fatalf("shutdowns native=%d external=%d", native.shutdowns, external.shutdowns)
		}
	})

	t.Run("only external fails returns external error", func(t *testing.T) {
		native := &fakeAgentRunner{}
		external := &fakeAgentRunner{shutErr: externalErr}
		router := NewRoutingAgentRunner(native, external, RoutingPolicy{})
		if err := router.Shutdown(); !errors.Is(err, externalErr) {
			t.Fatalf("Shutdown = %v, want external error", err)
		}
		if native.shutdowns != 1 {
			t.Fatal("native not shut down")
		}
	})
}

func TestRoutingIsReadyFollowsNative(t *testing.T) {
	native := &fakeAgentRunner{ready: false}
	external := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, RoutingPolicy{})
	if router.IsReady() {
		t.Fatal("router ready while native is not")
	}
	native.ready = true
	external.ready = false
	if !router.IsReady() {
		t.Fatal("router not ready while native is ready")
	}
}

func TestExternalRunnerReportsTheSignedInCLIOnceReady(t *testing.T) {
	ctx := context.Background()
	external := &fakeAgentRunner{ready: true}
	r := NewRoutingAgentRunner(&fakeAgentRunner{ready: true}, external, RoutingPolicy{ExternalFormat: "claude-code", ExternalSignedIn: true})
	if _, ok := r.ExternalRunner(); ok {
		t.Fatal("the external runner is not offered before Init")
	}
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	format, ok := r.ExternalRunner()
	if !ok || format != "claude-code" {
		t.Fatalf("ExternalRunner = %q, %v", format, ok)
	}
	r.SetPolicy(RoutingPolicy{ExternalFormat: "claude-code"})
	if _, ok := r.ExternalRunner(); ok {
		t.Fatal("a policy that is not signed in withdraws the offer")
	}
	none := NewRoutingAgentRunner(&fakeAgentRunner{ready: true}, nil, RoutingPolicy{})
	if err := none.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := none.ExternalRunner(); ok {
		t.Fatal("without an external runner nothing is offered")
	}
	failing := NewRoutingAgentRunner(&fakeAgentRunner{ready: true}, &fakeAgentRunner{initErr: errors.New("not signed in")}, RoutingPolicy{ExternalFormat: "codex", ExternalSignedIn: true})
	if err := failing.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := failing.ExternalRunner(); ok {
		t.Fatal("an external runner that failed to initialize is not offered")
	}
}

// routingLedgerFailure is the controlled runner's durable ledger snapshot in router unit tests.
type routingLedgerFailure struct {
	cause      error
	actions    int
	unfinished bool
}

func (e routingLedgerFailure) Error() string              { return e.cause.Error() }
func (e routingLedgerFailure) Unwrap() error              { return e.cause }
func (e routingLedgerFailure) ActionCount() int           { return e.actions }
func (e routingLedgerFailure) HasUnfinishedActions() bool { return e.unfinished }

func TestRoutingFallbackBeforeFirstAction(t *testing.T) {
	claude := &fakeAgentRunner{runID: "claude-run", executeErr: routingLedgerFailure{cause: errors.New("rate_limit_error")}, events: []*StreamEvent{{Type: "assistant", Content: "discard me"}, {Type: "error", Error: "rate_limit_error"}}}
	codex := &fakeAgentRunner{runID: "codex-run", executeErr: routingLedgerFailure{cause: errors.New("assistant crashed")}}
	native := &fakeAgentRunner{runID: "native-run", ready: true, events: []*StreamEvent{{Type: "assistant", Content: "native answer"}}}
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	router := NewOrderedRoutingAgentRunner(native, map[string]AgentRunner{"claude-code": claude, "codex": codex}, RoutingPolicy{Assistants: map[string]AssistantState{"claude-code": ready, "codex": ready}})
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var events []*StreamEvent
	resolutions := 0
	input := &TaskInput{BackendHint: BackendHintCron, AuthorityGrants: []authority.Grant{{}}, DenyExternalWrites: true, PromptFunc: func() (string, string) { resolutions++; return "resolved", "prior history" }}
	runID, err := router.ExecuteTask(context.Background(), input, func(event *StreamEvent) error { events = append(events, event); return nil })
	if err != nil || runID != "native-run" {
		t.Fatalf("run=%q err=%v", runID, err)
	}
	if len(claude.executed) != 1 || len(codex.executed) != 1 || len(native.executed) != 1 {
		t.Fatal("expected each assistant followed by native")
	}
	if resolutions != 1 || native.executed[0].History != "prior history" || native.executed[0].Prompt != "resolved" || native.executed[0].PromptFunc != nil {
		t.Fatalf("prompt resolution count=%d input=%+v", resolutions, native.executed[0])
	}
	var text string
	var closes int
	for _, event := range events {
		if event.Type == "error" {
			t.Fatalf("failed candidate leaked error: %+v", event)
		}
		if event.Type == "assistant" {
			text += event.Content
		}
		if event.Type == "close" {
			closes++
		}
		if event.Backend == "native" {
			if !reflect.DeepEqual(event.FallbackFrom, []string{"claude-code", "codex"}) {
				t.Fatalf("fallback_from=%v", event.FallbackFrom)
			}
			if !reflect.DeepEqual(event.CandidateBackends, []string{"claude-code", "codex", "native"}) {
				t.Fatalf("candidates=%v", event.CandidateBackends)
			}
			if !strings.Contains(event.BackendReason, "rate_limit_error") {
				t.Fatalf("reason=%q", event.BackendReason)
			}
		}
	}
	if text != "native answer" || closes != 1 {
		t.Fatalf("text=%q closes=%d", text, closes)
	}
}

func TestRoutingFailureLedgerTable(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		beforeEvents bool
		toolEvent    bool
		wantNative   bool
		wantActions  int
		wantUnknown  bool
	}{
		{"startup before events", errors.New("binary missing"), true, false, true, 0, false},
		{"empty ledger crash", routingLedgerFailure{cause: errors.New("crash")}, false, false, true, 0, false},
		{"empty ledger quota", routingLedgerFailure{cause: errors.New("rate_limit_error")}, false, false, true, 0, false},
		{"finished action", routingLedgerFailure{cause: errors.New("crash"), actions: 1}, false, false, false, 1, false},
		{"started action", routingLedgerFailure{cause: errors.New("crash"), actions: 1, unfinished: true}, false, false, false, 1, true},
		{"unfinished inconsistent count", routingLedgerFailure{cause: errors.New("crash"), unfinished: true}, false, false, false, 0, true},
		{"no ledger after event", errors.New("crash"), false, false, false, 0, true},
		{"no ledger tool event", errors.New("crash"), false, true, false, 1, true},
		{"ledger wins over prose", routingLedgerFailure{cause: errors.New("crash"), actions: 3}, false, false, false, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			external := &fakeAgentRunner{runID: "assistant-run", executeErr: tc.err, beforeEvents: tc.beforeEvents}
			if tc.toolEvent {
				external.events = []*StreamEvent{{Type: "tool_use", ToolName: "Write"}}
			}
			native := &fakeAgentRunner{runID: "native-run", ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			var blockedEvents int
			_, err := router.ExecuteTask(context.Background(), &TaskInput{BackendHint: BackendHintCron}, func(ev *StreamEvent) error {
				if ev.Blocked && ev.Type == "error" {
					blockedEvents++
				}
				return nil
			})
			if tc.wantNative {
				if err != nil || len(native.executed) != 1 || blockedEvents != 0 {
					t.Fatalf("err=%v native=%d blocked=%d", err, len(native.executed), blockedEvents)
				}
				return
			}
			var blocked *BlockedRunError
			if !errors.As(err, &blocked) || len(native.executed) != 0 || blockedEvents != 1 {
				t.Fatalf("err=%v native=%d blocked=%d", err, len(native.executed), blockedEvents)
			}
			if blocked.Actions != tc.wantActions || blocked.UnknownOutcome != tc.wantUnknown {
				t.Fatalf("blocked=%+v", blocked)
			}
		})
	}
}

func TestRoutingAfterActionCancellationAndDisconnectedConsumerStayBlocked(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(disconnect), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: context.Canceled, actions: 1}, onRun: cancel}
			native := &fakeAgentRunner{ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(ctx); err != nil {
				t.Fatal(err)
			}
			_, err := router.ExecuteTask(ctx, &TaskInput{}, func(ev *StreamEvent) error {
				if disconnect && ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			})
			var blocked *BlockedRunError
			if !errors.As(err, &blocked) || blocked.Actions != 1 || len(native.executed) != 0 {
				t.Fatalf("err=%v native=%d", err, len(native.executed))
			}
		})
	}
}

func TestRoutingCancellationBeforeActionsNeverFallsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: context.Canceled}, onRun: cancel}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := router.ExecuteTask(ctx, &TaskInput{}, nil)
	if !errors.Is(err, context.Canceled) || len(native.executed) != 0 {
		t.Fatalf("err=%v native=%d", err, len(native.executed))
	}
}

func TestRoutingRefreshesPolicyAndRecoversInstalledCLI(t *testing.T) {
	external := &fakeAgentRunner{runID: "ext", initErr: errors.New("not installed")}
	native := &fakeAgentRunner{runID: "native", ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	signedIn := false
	calls := 0
	router.SetPolicyProvider(func(context.Context) RoutingPolicy {
		calls++
		policy := signedInPolicy
		policy.ExternalSignedIn = signedIn
		return policy
	})
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil); err != nil {
		t.Fatal(err)
	}
	external.initErr = nil
	signedIn = true
	if _, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(native.executed) != 1 || len(external.executed) != 1 || calls != 3 {
		t.Fatalf("native=%d external=%d probes=%d", len(native.executed), len(external.executed), calls)
	}
	if format, ok := router.ExternalRunner(); !ok || format != "claude-code" {
		t.Fatalf("external=%q,%t", format, ok)
	}
}

func TestRoutingCooldownSharesOneProbe(t *testing.T) {
	clock := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	router := NewOrderedRoutingAgentRunner(&fakeAgentRunner{}, nil, RoutingPolicy{})
	router.now = func() time.Time { return clock }
	router.finishAttempt("claude-code", errors.New("rate_limit_error"))
	if router.beginAttempt("claude-code") {
		t.Fatal("candidate admitted during cooldown")
	}
	clock = clock.Add(30 * time.Minute)
	var wg sync.WaitGroup
	var mu sync.Mutex
	probes := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if router.beginAttempt("claude-code") {
				mu.Lock()
				probes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if probes != 1 {
		t.Fatalf("probes=%d, want one", probes)
	}
	router.finishAttempt("claude-code", nil)
	if !router.beginAttempt("claude-code") {
		t.Fatal("successful probe did not clear cooldown")
	}
	router.finishAttempt("claude-code", errors.New("an unknown error"))
	if !router.beginAttempt("claude-code") {
		t.Fatal("unknown text incorrectly sets quota cooldown")
	}
}

func TestChooseRouteRespectsAdmittedAssistantSubset(t *testing.T) {
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	policy := RoutingPolicy{ManagedOrder: []string{"codex"}, Assistants: map[string]AssistantState{"claude-code": ready, "codex": ready}}
	route := chooseRoute(&TaskInput{}, policy, map[string]bool{"claude-code": true, "codex": true}, "")
	if len(route) != 2 || route[0].Backend != BackendCodex || route[1].Backend != BackendNative {
		t.Fatalf("route=%v", route)
	}
}

func TestRoutingSuccessfulActionThenResultConsumerFailureIsBlocked(t *testing.T) {
	external := &fakeAgentRunner{runID: "ext", events: []*StreamEvent{{Type: "assistant", Content: "finished"}, {Type: "done", ActionsStarted: 2}}}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	disconnected := errors.New("consumer disconnected")
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Type == "assistant" {
			return disconnected
		}
		return nil
	})
	var blocked *BlockedRunError
	if !errors.As(err, &blocked) || blocked.Actions != 2 || !errors.Is(err, disconnected) || len(native.executed) != 0 {
		t.Fatalf("err=%v native=%d", err, len(native.executed))
	}
}

func TestRoutingAssistantLocalCancellationNeverStartsBackup(t *testing.T) {
	for _, cause := range []error{context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: cause}}
			native := &fakeAgentRunner{ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil)
			if !errors.Is(err, cause) || len(native.executed) != 0 {
				t.Fatalf("err=%v native=%d", err, len(native.executed))
			}
		})
	}
}

type unavailableToolRunner struct{ fakeAgentRunner }

func (r *unavailableToolRunner) RoutingEligibility(*TaskInput) (bool, string) {
	return false, "required tool is not on the run server"
}
func TestRoutingTaskEligibilityIsCheckedBeforeAssistantStarts(t *testing.T) {
	external := &unavailableToolRunner{fakeAgentRunner{runID: "ext"}}
	native := &fakeAgentRunner{runID: "native", ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reason string
	_, err := router.ExecuteTask(context.Background(), &TaskInput{AllowedTools: []string{"missing"}}, func(event *StreamEvent) error { reason = event.BackendReason; return nil })
	if err != nil || len(external.executed) != 0 || len(native.executed) != 1 || !strings.Contains(reason, "required tool") {
		t.Fatalf("err=%v ext=%d native=%d reason=%q", err, len(external.executed), len(native.executed), reason)
	}
}

func TestRoutingStreamsProseAfterDurableActionStarts(t *testing.T) {
	external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: errors.New("stopped"), actions: 1}, events: []*StreamEvent{{Type: "assistant", Content: "before "}, {Type: "action_started", ActionsStarted: 1}, {Type: "assistant", Content: "after"}}}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var text string
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Type == "assistant" {
			text += event.Content
		}
		return nil
	})
	var blocked *BlockedRunError
	if !errors.As(err, &blocked) || text != "before after" || len(native.executed) != 0 {
		t.Fatalf("err=%v text=%q native=%d", err, text, len(native.executed))
	}
}

func TestRoutingObserverRecordsBeforeConsumer(t *testing.T) {
	external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: errors.New("stopped"), actions: 1}}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var observed *StreamEvent
	blocked := false
	router.SetObserver(func(event *StreamEvent) error { observed = event; blocked = blocked || event.Blocked; return nil })
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if observed != event {
			t.Fatal("consumer ran before observer")
		}
		return nil
	})
	var stopped *BlockedRunError
	if !errors.As(err, &stopped) || !blocked {
		t.Fatalf("err=%v recorded blocked=%t", err, blocked)
	}
}

func TestRoutingObserverFinalizesHiddenFailedAttempt(t *testing.T) {
	external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: errors.New("unavailable")}}
	native := &fakeAgentRunner{runID: "native", ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var observedErrors, consumerErrors int
	router.SetObserver(func(event *StreamEvent) error {
		if event.Type == "error" && event.RunID == "ext" {
			observedErrors++
		}
		return nil
	})
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Type == "error" {
			consumerErrors++
		}
		return nil
	})
	if err != nil || observedErrors != 1 || consumerErrors != 0 || len(native.executed) != 1 {
		t.Fatalf("err=%v observed=%d consumer=%d native=%d", err, observedErrors, consumerErrors, len(native.executed))
	}
}

func TestRoutingAttemptTimeoutVersusOuterDeadline(t *testing.T) {
	for _, outerExpired := range []bool{false, true} {
		t.Run(fmt.Sprint(outerExpired), func(t *testing.T) {
			ctx := context.Background()
			if outerExpired {
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				ctx = expired
			}
			external := &fakeAgentRunner{runID: "ext", executeErr: routingLedgerFailure{cause: context.DeadlineExceeded}}
			native := &fakeAgentRunner{runID: "native", ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := router.ExecuteTask(ctx, &TaskInput{}, nil)
			if outerExpired {
				if !errors.Is(err, context.DeadlineExceeded) || len(native.executed) != 0 {
					t.Fatalf("outer deadline err=%v native=%d", err, len(native.executed))
				}
			} else {
				if err != nil || len(native.executed) != 1 {
					t.Fatalf("attempt timeout err=%v native=%d", err, len(native.executed))
				}
			}
		})
	}
}

func TestRoutingNativeAfterActionErrorsAreBlocked(t *testing.T) {
	for _, tc := range []struct {
		name         string
		runErr       error
		rejectResult bool
	}{
		{"tool failure", errors.New("tool failed after starting"), false},
		{"cancelled", context.Canceled, false},
		{"result consumer failed", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := &fakeAgentRunner{runID: "native", ready: true, executeErr: tc.runErr, events: []*StreamEvent{{Type: "action_started", ActionsStarted: 1}, {Type: "done", ActionsStarted: 1}}}
			router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			observedBlocked := false
			router.SetObserver(func(event *StreamEvent) error {
				if event.Blocked && event.Type == "error" {
					observedBlocked = true
				}
				return nil
			})
			_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
				if tc.rejectResult && event.Type == "done" {
					return errors.New("result consumer failed")
				}
				return nil
			})
			var blocked *BlockedRunError
			if !errors.As(err, &blocked) || blocked.Backend != BackendNative || blocked.Actions != 1 || !observedBlocked {
				t.Fatalf("err=%v recorded blocked=%t", err, observedBlocked)
			}
		})
	}
}

func TestRoutingPreservesProseAndToolEventOrder(t *testing.T) {
	source := []*StreamEvent{
		{Type: "assistant", Content: "Let me save the draft."},
		{Type: "thinking", Content: "Choose a location."},
		{Type: "tool_use_start", ToolUseID: "write", ToolName: "Write"},
		{Type: "tool_use", ToolUseID: "write", ToolName: "Write"},
		{Type: "authority_decision", ToolUseID: "write", Content: "allowed"},
		{Type: "action_started", ToolUseID: "write", ActionsStarted: 1},
		{Type: "tool_result", ToolUseID: "write", Content: "saved"},
		{Type: "assistant", Content: "The draft is saved."},
		{Type: "done", ActionsStarted: 1},
	}
	external := &fakeAgentRunner{runID: "external", events: source}
	router := NewRoutingAgentRunner(&fakeAgentRunner{ready: true}, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []string
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Type != "backend_route" && event.Type != "run_created" {
			got = append(got, event.Type+":"+event.Content)
		}
		return nil
	})
	var want []string
	for _, event := range source {
		want = append(want, event.Type+":"+event.Content)
	}
	want = append(want, "close:")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("err=%v\ngot %v\nwant %v", err, got, want)
	}
}

func TestRoutingSafeFallbackDiscardsDeniedToolCardsAndOtherProvisionalEvents(t *testing.T) {
	external := &fakeAgentRunner{runID: "external", executeErr: routingLedgerFailure{cause: errors.New("rate_limit_error")}, events: []*StreamEvent{
		{Type: "assistant", Content: "I will write."},
		{Type: "tool_use_start", ToolUseID: "denied", ToolName: "Write"},
		{Type: "tool_use", ToolUseID: "denied", ToolName: "Write"},
		{Type: "authority_decision", ToolUseID: "denied", Content: "denied"},
		{Type: "tool_result", ToolUseID: "denied", Content: "not allowed", IsError: true},
		{Type: "status", Content: "trying again"},
		{Type: "error", Error: "rate_limit_error"},
	}}
	native := &fakeAgentRunner{runID: "native", ready: true, events: []*StreamEvent{{Type: "assistant", Content: "Native answer."}, {Type: "done"}}}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var visible []string
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Type != "backend_route" && event.Type != "run_created" {
			visible = append(visible, event.Type+":"+event.Content)
		}
		return nil
	})
	want := []string{"assistant:Native answer.", "done:", "close:"}
	if err != nil || !reflect.DeepEqual(visible, want) {
		t.Fatalf("err=%v visible=%v, want %v", err, visible, want)
	}
}

// This runner waits for a callback response before completing; buffering the
// permission request until completion would deadlock this unit.
type routingPermissionRunner struct {
	fakeAgentRunner
	answered chan struct{}
}

func (r *routingPermissionRunner) ExecuteTask(ctx context.Context, input *TaskInput, callback StreamCallback) (string, error) {
	r.executed = append(r.executed, input)
	for _, event := range []*StreamEvent{{Type: "run_created", RunID: "approval", SessionID: "approval"}, {Type: "assistant", Content: "May I save?"}, {Type: "tool_use", ToolUseID: "write", ToolName: "Write"}, {Type: "permission_request", ToolName: "Write"}} {
		if err := callback(event); err != nil {
			return "approval", err
		}
	}
	select {
	case <-r.answered:
	case <-ctx.Done():
		return "approval", ctx.Err()
	}
	if err := callback(&StreamEvent{Type: "tool_result", ToolUseID: "write", Content: "permission denied", IsError: true}); err != nil {
		return "approval", err
	}
	return "approval", routingLedgerFailure{cause: errors.New("assistant failed after the user denied permission")}
}

func TestRoutingPermissionBoundaryPreservesOrderAndNeverReplaysApproval(t *testing.T) {
	external := &routingPermissionRunner{answered: make(chan struct{})}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := router.Init(ctx); err != nil {
		t.Fatal(err)
	}
	var visible []string
	_, err := router.ExecuteTask(ctx, &TaskInput{}, func(event *StreamEvent) error {
		if event.Type != "backend_route" && event.Type != "run_created" {
			visible = append(visible, event.Type)
		}
		if event.Type == "permission_request" {
			close(external.answered)
		}
		return nil
	})
	want := []string{"assistant", "tool_use", "permission_request", "tool_result", "error", "close"}
	var blocked *BlockedRunError
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &blocked) || len(native.executed) != 0 || !reflect.DeepEqual(visible, want) {
		t.Fatalf("err=%v native=%d visible=%v, want %v", err, len(native.executed), visible, want)
	}
}

func TestRoutingNativeBlockedOutcomeUsesCompletedActionIDs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		results     []*StreamEvent
		wantUnknown bool
	}{
		{"completed write then model timeout", []*StreamEvent{{Type: "tool_result", ToolUseID: "write", ToolSucceeded: true}}, false},
		{"completed failed tool then model timeout", []*StreamEvent{{Type: "tool_result", ToolUseID: "write", IsError: true}}, false},
		{"interrupted write has no result", nil, true},
		{"unrelated denied result does not finish write", []*StreamEvent{{Type: "tool_result", ToolUseID: "denied", IsError: true}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []*StreamEvent{{Type: "tool_use", ToolUseID: "write", ToolName: "Write"}, {Type: "action_started", ToolUseID: "write", ToolName: "Write", ActionsStarted: 1}}
			events = append(events, tc.results...)
			native := &fakeAgentRunner{runID: "native", ready: true, executeErr: context.DeadlineExceeded, events: events}
			router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil)
			var blocked *BlockedRunError
			if !errors.As(err, &blocked) || blocked.UnknownOutcome != tc.wantUnknown || blocked.Actions != 1 || !strings.HasPrefix(err.Error(), "Isoform stopped after 1 action") || strings.Contains(err.Error(), "assistant") {
				t.Fatalf("blocked=%+v err=%v, want unknown=%t", blocked, err, tc.wantUnknown)
			}
		})
	}
}

func TestRoutingActionReceiptCannotBeOverruledByEmptySnapshot(t *testing.T) {
	external := &fakeAgentRunner{runID: "external", executeErr: routingLedgerFailure{cause: errors.New("inconsistent ledger")}, events: []*StreamEvent{{Type: "action_started", ActionsStarted: 1}}}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil)
	var blocked *BlockedRunError
	if !errors.As(err, &blocked) || !blocked.UnknownOutcome || blocked.Actions != 1 || len(native.executed) != 0 {
		t.Fatalf("err=%v native=%d", err, len(native.executed))
	}
}

func TestRoutingObserverFailuresNeverAbortDispatchOrCompletion(t *testing.T) {
	for _, failAt := range []string{"backend_route", "run_created", "action_started", "done", "close"} {
		t.Run(failAt, func(t *testing.T) {
			external := &fakeAgentRunner{runID: "external", events: []*StreamEvent{{Type: "action_started", ToolUseID: "write", ActionsStarted: 1}, {Type: "tool_result", ToolUseID: "write", ToolSucceeded: true}, {Type: "done", ActionsStarted: 1}}}
			native := &fakeAgentRunner{ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			observerFailed := false
			router.SetObserver(func(event *StreamEvent) error {
				if event.Type == failAt {
					observerFailed = true
					return errors.New("history database write timed out")
				}
				return nil
			})
			var types []string
			_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error { types = append(types, event.Type); return nil })
			want := []string{"backend_route", "run_created", "action_started", "tool_result", "done", "close"}
			if err != nil || !observerFailed || len(external.executed) != 1 || len(native.executed) != 0 || !reflect.DeepEqual(types, want) {
				t.Fatalf("err=%v observer failed=%t external=%d native=%d events=%v", err, observerFailed, len(external.executed), len(native.executed), types)
			}
		})
	}
}

func TestRoutingObserverNeverSeesErrorTextOfAPrivateRun(t *testing.T) {
	const secret = "the memory said I take vitamin D"
	for _, private := range []bool{true, false} {
		external := &fakeAgentRunner{runID: "external", events: []*StreamEvent{{Type: "error", Error: "HTTP 400: " + secret}, {Type: "done"}}}
		native := &fakeAgentRunner{ready: true}
		router := NewRoutingAgentRunner(native, external, signedInPolicy)
		if err := router.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		var observed, delivered []string
		router.SetObserver(func(event *StreamEvent) error {
			if event.Error != "" {
				observed = append(observed, event.Error)
			}
			return nil
		})
		// The run may end in an error: only what route history is shown matters.
		_, _ = router.ExecuteTask(context.Background(), &TaskInput{SuppressTelemetry: private}, func(event *StreamEvent) error {
			if event.Error != "" {
				delivered = append(delivered, event.Error)
			}
			return nil
		})
		seen := strings.Contains(strings.Join(observed, "\n"), secret)
		if len(observed) == 0 || seen == private {
			t.Fatalf("private=%t: route history saw %v (secret shown=%t)", private, observed, seen)
		}
		if !strings.Contains(strings.Join(delivered, "\n"), secret) {
			t.Fatalf("private=%t: the caller no longer receives its own error: %v", private, delivered)
		}
	}
}

func TestRoutingObserverFailureCannotStopEmptyLedgerFallback(t *testing.T) {
	external := &fakeAgentRunner{runID: "external", executeErr: routingLedgerFailure{cause: errors.New("rate_limit_error")}}
	native := &fakeAgentRunner{runID: "native", ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	observerFailed := false
	router.SetObserver(func(event *StreamEvent) error {
		if event.Type == "error" && event.Backend == "claude-code" {
			observerFailed = true
			return errors.New("cannot persist failed attempt")
		}
		return nil
	})
	runID, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil)
	if err != nil || !observerFailed || runID != "native" || len(native.executed) != 1 {
		t.Fatalf("err=%v observer failed=%t run=%q native=%d", err, observerFailed, runID, len(native.executed))
	}
}

func TestRoutingConsumerFailureStillStopsWhenObserverFails(t *testing.T) {
	for _, failAt := range []string{"backend_route", "action_started", "done"} {
		t.Run(failAt, func(t *testing.T) {
			external := &fakeAgentRunner{runID: "external", events: []*StreamEvent{{Type: "action_started", ActionsStarted: 1}, {Type: "done", ActionsStarted: 1}}}
			native := &fakeAgentRunner{ready: true}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			consumerErr := errors.New("consumer disconnected")
			router.SetObserver(func(*StreamEvent) error { return errors.New("history write failed") })
			_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
				if event.Type == failAt {
					return consumerErr
				}
				return nil
			})
			if !errors.Is(err, consumerErr) || len(native.executed) != 0 {
				t.Fatalf("err=%v native=%d", err, len(native.executed))
			}
		})
	}
}

func TestRoutingDurableLedgerFailureStillBlocksWhenObserverFails(t *testing.T) {
	ledgerErr := errors.New("durable action ledger fsync failed")
	external := &fakeAgentRunner{runID: "external", executeErr: routingLedgerFailure{cause: ledgerErr, unfinished: true}}
	native := &fakeAgentRunner{ready: true}
	router := NewRoutingAgentRunner(native, external, signedInPolicy)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	router.SetObserver(func(*StreamEvent) error { return errors.New("history write failed") })
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, nil)
	var blocked *BlockedRunError
	if !errors.As(err, &blocked) || !blocked.UnknownOutcome || !errors.Is(err, ledgerErr) || len(native.executed) != 0 {
		t.Fatalf("err=%v native=%d", err, len(native.executed))
	}
}

type contextRoutingRunner struct {
	fakeAgentRunner
	run func(context.Context, StreamCallback) (string, error)
}

func (r *contextRoutingRunner) ExecuteTask(ctx context.Context, input *TaskInput, callback StreamCallback) (string, error) {
	r.executed = append(r.executed, input)
	return r.run(ctx, callback)
}

func TestRoutingNativeFallbackBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		budget         time.Duration
		parentDuration time.Duration
		wantDuration   time.Duration
	}{
		{"native budget", 80 * time.Millisecond, time.Second, 80 * time.Millisecond},
		{"earlier parent deadline", time.Second, 80 * time.Millisecond, 80 * time.Millisecond},
		{"no explicit budget", 0, 80 * time.Millisecond, 80 * time.Millisecond},
		{"negative budget ignored", -time.Second, 80 * time.Millisecond, 80 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), tc.parentDuration)
			defer cancel()
			ctx := WithNativeFallbackBudget(parent, tc.budget)
			external := &contextRoutingRunner{run: func(got context.Context, _ StreamCallback) (string, error) {
				if got != ctx {
					t.Error("native budget changed the assistant's caller context")
				}
				return "external", routingLedgerFailure{cause: errors.New("assistant unavailable")}
			}}
			native := &contextRoutingRunner{run: func(got context.Context, _ StreamCallback) (string, error) {
				deadline, ok := got.Deadline()
				if !ok || time.Until(deadline) > tc.wantDuration+20*time.Millisecond {
					t.Fatalf("native remaining deadline = %v, want at most %v", time.Until(deadline), tc.wantDuration)
				}
				<-got.Done()
				return "native", got.Err()
			}}
			router := NewRoutingAgentRunner(native, external, signedInPolicy)
			if err := router.Init(ctx); err != nil {
				t.Fatal(err)
			}
			_, err := router.ExecuteTask(ctx, &TaskInput{}, nil)
			if !errors.Is(err, context.DeadlineExceeded) || len(native.executed) != 1 {
				t.Fatalf("err=%v native=%d", err, len(native.executed))
			}
			if tc.name == "native budget" && parent.Err() != nil {
				t.Fatal("native budget canceled the outer chat context")
			}
		})
	}
}

func TestRoutingNativeBudgetPreservesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := WithNativeFallbackBudget(parent, time.Hour)
	native := &contextRoutingRunner{run: func(got context.Context, _ StreamCallback) (string, error) {
		cancel()
		<-got.Done()
		return "native", got.Err()
	}}
	router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
	_, err := router.ExecuteTask(ctx, &TaskInput{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
}

func TestRoutingNativeBudgetReleasesTimerAfterSuccess(t *testing.T) {
	ctx := WithNativeFallbackBudget(context.Background(), time.Hour)
	var child context.Context
	native := &contextRoutingRunner{run: func(got context.Context, _ StreamCallback) (string, error) {
		child = got
		return "native", nil
	}}
	router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
	_, err := router.ExecuteTask(ctx, &TaskInput{}, nil)
	if err != nil || child == nil || !errors.Is(child.Err(), context.Canceled) || ctx.Err() != nil {
		t.Fatalf("err=%v child=%v parent=%v", err, child, ctx.Err())
	}
}

func TestRoutingCooldownRaceDoesNotClaimUnattemptedFallback(t *testing.T) {
	claude := &fakeAgentRunner{runID: "claude", executeErr: routingLedgerFailure{cause: errors.New("rate_limit_error")}}
	codex := &fakeAgentRunner{runID: "codex"}
	native := &fakeAgentRunner{runID: "native", ready: true}
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	router := NewOrderedRoutingAgentRunner(native, map[string]AgentRunner{"claude-code": claude, "codex": codex}, RoutingPolicy{Assistants: map[string]AssistantState{"claude-code": ready, "codex": ready}})
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	claude.onRun = func() {
		// Another run can put the next candidate in cooldown after selection.
		router.finishAttempt("codex", errors.New("rate_limit_error"))
	}
	var fallback []string
	_, err := router.ExecuteTask(context.Background(), &TaskInput{}, func(event *StreamEvent) error {
		if event.Backend == "native" {
			fallback = event.FallbackFrom
		}
		return nil
	})
	if err != nil || len(codex.executed) != 0 || !reflect.DeepEqual(fallback, []string{"claude-code"}) {
		t.Fatalf("err=%v codex=%d fallback=%v", err, len(codex.executed), fallback)
	}
}
