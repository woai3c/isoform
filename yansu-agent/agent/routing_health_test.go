package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent/native"
)

// probingRunner is an assistant that can be checked without a task.
type probingRunner struct {
	fakeAgentRunner
	probeErr error
	probes   int
}

func (p *probingRunner) ProbeHealth(context.Context) error {
	p.probes++
	return p.probeErr
}

type scheduledProbe struct {
	delay time.Duration
	run   func()
}

// breakerRouter routes chat to claude-code first, then native. Probes are
// captured instead of timed so a test fires them by hand.
func breakerRouter(t *testing.T, claude AgentRunner) (*RoutingAgentRunner, *fakeAgentRunner, *[]scheduledProbe, *time.Time) {
	t.Helper()
	native := &fakeAgentRunner{runID: "native", ready: true}
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true, Version: "2.1.296 (Claude Code)"}
	router := NewOrderedRoutingAgentRunner(native, map[string]AgentRunner{"claude-code": claude}, RoutingPolicy{
		ManagedOrder: []string{"claude-code"},
		Assistants:   map[string]AssistantState{"claude-code": ready},
	})
	clock := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	router.now = func() time.Time { return clock }
	probes := &[]scheduledProbe{}
	router.afterFunc = func(delay time.Duration, run func()) *time.Timer {
		*probes = append(*probes, scheduledProbe{delay: delay, run: run})
		return time.NewTimer(time.Hour)
	}
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return router, native, probes, &clock
}

func healthOf(t *testing.T, router *RoutingAgentRunner, format string) AssistantHealth {
	t.Helper()
	for _, health := range router.AssistantHealth(context.Background()) {
		if health.Format == format {
			return health
		}
	}
	t.Fatalf("no health line for %s", format)
	return AssistantHealth{}
}

func TestRoutingBreakerTable(t *testing.T) {
	precondition := &AssistantPreconditionError{Cause: errors.New("assistant did not report its control profile")}
	plain := errors.New("assistant exited without a verified completed result")
	for _, tc := range []struct {
		name     string
		outcomes []error
		code     string
	}{
		{"precondition pauses at once", []error{precondition}, HealthCheckFailed},
		{"wrapped precondition pauses at once", []error{&ExternalAttemptError{Cause: precondition}}, HealthCheckFailed},
		{"two errors stay ready", []error{plain, plain}, HealthReady},
		{"three errors in a row pause", []error{plain, plain, plain}, HealthRepeatedErrors},
		{"a success resets the count", []error{plain, plain, nil, plain, plain}, HealthReady},
		{"cancellation is no verdict", []error{plain, plain, context.Canceled}, HealthReady},
		{"a released attempt is no verdict", []error{plain, plain, errAttemptNotJudged}, HealthReady},
		{"a dispatch refusal is no verdict", []error{plain, plain, &native.DispatchDeniedError{Cause: errors.New("source changed")}}, HealthReady},
		{"a usage limit cools down instead", []error{plain, plain, errors.New("rate_limit_error")}, HealthUsageLimit},
		{"a late success resumes", []error{precondition, nil}, HealthReady},
		{"an old CLI says so", []error{&AssistantPreconditionError{Cause: errors.New("Claude Code 2.1.296 or newer is required for isolated tools")}}, HealthUnsupportedVersion},
		{"a failed spawn is one strike", []error{errors.New("start Claude Code: fork/exec: no such file")}, HealthReady},
		{"a long answer cut by Isoform's deadline is no verdict", []error{plain, plain, &answerDeadlineError{Cause: context.DeadlineExceeded}}, HealthReady},
		{"a deadline before init is a strike", []error{plain, plain, context.DeadlineExceeded}, HealthRepeatedErrors},
		{"the CLI's rate-limit event cools down", []error{plain, plain, &AssistantUsageLimitError{Cause: errors.New("You've hit your limit")}}, HealthUsageLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, _, _ := breakerRouter(t, &fakeAgentRunner{runID: "claude"})
			for _, outcome := range tc.outcomes {
				router.finishAttempt("claude-code", outcome)
			}
			if got := healthOf(t, router, "claude-code").Code; got != tc.code {
				t.Fatalf("health=%q, want %q", got, tc.code)
			}
		})
	}
}

// Runs admitted before a pause end one by one; they must not stretch it.
func TestRoutingBreakerConcurrentFailuresTripOnce(t *testing.T) {
	router, _, probes, _ := breakerRouter(t, &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude"}})
	for i := 0; i < 5; i++ {
		router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	}
	if len(*probes) != 1 || (*probes)[0].delay != breakerBaseCooldown {
		t.Fatalf("one pause, one probe after the base cool-down; got %+v", *probes)
	}
}

func TestRoutingBreakerSkipsPausedAssistantWithoutSpawning(t *testing.T) {
	claude := &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude", beforeEvents: true,
		executeErr: &AssistantPreconditionError{Cause: errors.New("assistant did not report its control profile")}}}
	router, native, probes, _ := breakerRouter(t, claude)
	if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "first"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(claude.executed) != 1 || len(native.executed) != 1 {
		t.Fatalf("first run: claude=%d native=%d", len(claude.executed), len(native.executed))
	}
	if len(*probes) != 1 || (*probes)[0].delay != breakerBaseCooldown {
		t.Fatalf("probe not scheduled after the cool-down: %+v", *probes)
	}
	var reason string
	if _, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "second"}, func(event *StreamEvent) error {
		if event.Type == "backend_route" {
			reason = event.BackendReason
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(claude.executed) != 1 || len(native.executed) != 2 {
		t.Fatalf("paused assistant was spawned: claude=%d native=%d", len(claude.executed), len(native.executed))
	}
	if !strings.Contains(reason, "claude-code: "+pausedCheckReason) {
		t.Fatalf("route reason %q does not name the pause", reason)
	}
	if format, ok := router.ExternalRunner(); ok {
		t.Fatalf("paused assistant still offered as %q", format)
	}
}

func TestRoutingBreakerBackgroundProbe(t *testing.T) {
	plain := errors.New("assistant exited without a verified completed result")
	for _, tc := range []struct {
		name       string
		probeErr   error
		wantCode   string
		wantDelays []time.Duration
	}{
		{"passing probe resumes", nil, HealthReady, []time.Duration{breakerBaseCooldown}},
		{"failing probe doubles the cool-down", errors.New("assistant loaded plugins outside Isoform"), HealthCheckFailed, []time.Duration{breakerBaseCooldown, 2 * breakerBaseCooldown}},
		{"a probe that finds an old CLI says so", errors.New("Claude Code 2.1.296 or newer is required for isolated tools"), HealthUnsupportedVersion, []time.Duration{breakerBaseCooldown, 2 * breakerBaseCooldown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claude := &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude"}, probeErr: tc.probeErr}
			router, _, probes, _ := breakerRouter(t, claude)
			for i := 0; i < breakerFailureLimit; i++ {
				router.finishAttempt("claude-code", plain)
			}
			(*probes)[0].run()
			if claude.probes != 1 {
				t.Fatalf("probes=%d", claude.probes)
			}
			if got := healthOf(t, router, "claude-code").Code; got != tc.wantCode {
				t.Fatalf("health=%q, want %q", got, tc.wantCode)
			}
			var delays []time.Duration
			for _, probe := range *probes {
				delays = append(delays, probe.delay)
			}
			if fmt.Sprint(delays) != fmt.Sprint(tc.wantDelays) {
				t.Fatalf("delays=%v, want %v", delays, tc.wantDelays)
			}
			if tc.probeErr == nil {
				// A passing check proves the profile, not the model: one more
				// error pauses the assistant again.
				router.finishAttempt("claude-code", plain)
				if got := healthOf(t, router, "claude-code").Code; got != HealthRepeatedErrors {
					t.Fatalf("after resume and one error: %q", got)
				}
			}
		})
	}
}

func TestRoutingBreakerReprobesOnChange(t *testing.T) {
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true, Version: "2.1.296 (Claude Code)"}
	for _, tc := range []struct {
		name  string
		state AssistantState
		probe bool
	}{
		{"same version", ready, false},
		{"new version", AssistantState{SignedIn: true, Available: true, ControlProfile: true, Version: "2.1.300 (Claude Code)"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, probes, _ := breakerRouter(t, &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude"}})
			router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
			router.SetPolicy(RoutingPolicy{ManagedOrder: []string{"claude-code"}, Assistants: map[string]AssistantState{"claude-code": tc.state}})
			immediate := len(*probes) == 2 && (*probes)[1].delay == 0
			if immediate != tc.probe {
				t.Fatalf("immediate probe=%v, want %v (%+v)", immediate, tc.probe, *probes)
			}
		})
	}
	t.Run("sign in", func(t *testing.T) {
		router, _, probes, _ := breakerRouter(t, &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude"}})
		signedOut := ready
		signedOut.SignedIn = false
		router.SetPolicy(RoutingPolicy{ManagedOrder: []string{"claude-code"}, Assistants: map[string]AssistantState{"claude-code": signedOut}})
		router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
		router.SetPolicy(RoutingPolicy{ManagedOrder: []string{"claude-code"}, Assistants: map[string]AssistantState{"claude-code": ready}})
		if len(*probes) != 2 || (*probes)[1].delay != 0 {
			t.Fatalf("sign-in did not re-check: %+v", *probes)
		}
	})
}

func TestRoutingRetryAssistantChecksNow(t *testing.T) {
	claude := &probingRunner{fakeAgentRunner: fakeAgentRunner{runID: "claude"}}
	router, _, probes, _ := breakerRouter(t, claude)
	router.RetryAssistant("claude-code")
	if len(*probes) != 0 {
		t.Fatal("a ready assistant was probed")
	}
	router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	router.RetryAssistant("claude-code")
	if len(*probes) != 2 || (*probes)[1].delay != 0 {
		t.Fatalf("Try again did not check now: %+v", *probes)
	}
	(*probes)[1].run()
	if got := healthOf(t, router, "claude-code").Code; got != HealthReady || claude.probes != 1 {
		t.Fatalf("health=%q probes=%d", got, claude.probes)
	}
}

func TestRoutingAssistantHealthCodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state AssistantState
		code  string
	}{
		{"ready", AssistantState{SignedIn: true, Available: true, ControlProfile: true}, HealthReady},
		{"not installed", AssistantState{Reason: "binary missing"}, HealthNotInstalled},
		{"no tool shim", AssistantState{Reason: "Isoform tool shim is unavailable"}, HealthToolConnection},
		{"signed out", AssistantState{Available: true, Reason: "signed out"}, HealthSignedOut},
		{"sign-in check failed", AssistantState{Available: true, Reason: "sign-in probe failed"}, HealthSignInCheckFailed},
		{"old version", AssistantState{Available: true, SignedIn: true, Reason: "Claude Code 2.1.296 or newer is required for isolated tools"}, HealthUnsupportedVersion},
		{"missing flag", AssistantState{Available: true, SignedIn: true, Reason: "Claude Code is missing required control flag --restricted"}, HealthUnsupportedVersion},
		{"codex isolation", AssistantState{Available: true, SignedIn: true, Reason: "Codex tool isolation has not been verified"}, HealthToolsUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := NewOrderedRoutingAgentRunner(&fakeAgentRunner{ready: true}, map[string]AgentRunner{"claude-code": &fakeAgentRunner{}}, RoutingPolicy{
				ManagedOrder: []string{"claude-code"}, Assistants: map[string]AssistantState{"claude-code": tc.state},
			})
			if got := healthOf(t, router, "claude-code").Code; got != tc.code {
				t.Fatalf("code=%q, want %q", got, tc.code)
			}
		})
	}
}

// A consumer that leaves mid-run must still end the run's route record.
func TestRoutingRecordsEndWhenConsumerLeaves(t *testing.T) {
	native := &fakeAgentRunner{runID: "native", ready: true, events: []*StreamEvent{{Type: "assistant", Content: "hi"}, {Type: "done"}}}
	router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
	statuses := map[string]string{}
	router.SetObserver(func(event *StreamEvent) error {
		switch event.Type {
		case "run_created":
			statuses[event.RunID] = "running"
		case "done":
			statuses[event.RunID] = "done"
		case "error":
			statuses[event.RunID] = "error"
		}
		return nil
	})
	gone := errors.New("consumer left")
	_, err := router.ExecuteTask(context.Background(), &TaskInput{Prompt: "hello"}, func(event *StreamEvent) error {
		if event.Type == "assistant" {
			return gone
		}
		return nil
	})
	if !errors.Is(err, gone) || statuses["native"] != "error" {
		t.Fatalf("err=%v statuses=%v", err, statuses)
	}
}

// blockingProber holds its check until the router shuts down.
type blockingProber struct {
	fakeAgentRunner
	started chan struct{}
}

func (p *blockingProber) ProbeHealth(ctx context.Context) error {
	close(p.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestRoutingShutdownStopsAProbe(t *testing.T) {
	claude := &blockingProber{fakeAgentRunner: fakeAgentRunner{runID: "claude"}, started: make(chan struct{})}
	router, _, probes, _ := breakerRouter(t, claude)
	router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	done := make(chan struct{})
	go func() { (*probes)[0].run(); close(done) }()
	<-claude.started
	if err := router.Shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("probe outlived Shutdown")
	}
	// A cut-short check proves nothing: no new pause, no new probe.
	if len(*probes) != 1 {
		t.Fatalf("probes=%+v", *probes)
	}
}

// The desktop wires the native tool server's coverage check into the
// assistant. Settings and the background probe must ask about the assistant,
// not about a missing task.
func TestRoutingHealthWithProductionEligibility(t *testing.T) {
	native := NewNativeAgentRunner(NativeRunnerConfig{DisableEnvFallback: true})
	claude := NewExternalAgentRunner(ExternalAgentConfig{
		Format: "claude-code", ToolShimCommand: "/Applications/Isoform.app/Contents/MacOS/yansu",
		ToolServerFactory: native.NewToolServer, ToolServerSupports: native.ToolServerSupports,
	})
	router := NewOrderedRoutingAgentRunner(native, map[string]AgentRunner{"claude-code": claude}, RoutingPolicy{
		ManagedOrder: []string{"claude-code"},
		Assistants:   map[string]AssistantState{"claude-code": {Available: true, SignedIn: true, ControlProfile: true}},
	})
	if got := healthOf(t, router, "claude-code"); got.Code != HealthReady {
		t.Fatalf("health=%+v, want ready", got)
	}
}

type funcProber struct {
	fakeAgentRunner
	probe func(context.Context) error
}

func (p *funcProber) ProbeHealth(ctx context.Context) error { return p.probe(ctx) }

// Shutdown lands after a probe is admitted but before it starts its check:
// the check must never start. (PR #63 desk regression.)
func TestRoutingShutdownBeforeProbeStarts(t *testing.T) {
	calls := make(chan error, 1)
	claude := &funcProber{fakeAgentRunner: fakeAgentRunner{runID: "claude"}, probe: func(ctx context.Context) error {
		calls <- ctx.Err()
		return nil
	}}
	router, _, probes, _ := breakerRouter(t, claude)
	router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	router.SetHealthObserver(func() {
		close(entered)
		<-release
	})
	go func() { (*probes)[0].run(); close(done) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("probe never reached its health observer")
	}
	if err := router.Shutdown(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not finish")
	}
	select {
	case err := <-calls:
		t.Fatalf("ProbeHealth started after Shutdown returned; context error = %v", err)
	default:
	}
	// A restart may probe again with a fresh lifecycle.
	router.SetHealthObserver(nil)
	if err := router.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	router.RetryAssistant("claude-code")
	(*probes)[len(*probes)-1].run()
	if err := <-calls; err != nil {
		t.Fatalf("probe after restart ran with a dead context: %v", err)
	}
}

// A real run that succeeds while an older probe is still running wins; the
// probe is cancelled and its late failure discarded. (PR #63 desk regression.)
func TestRoutingLateSuccessDiscardsOlderProbe(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var probeCtx context.Context
	claude := &funcProber{fakeAgentRunner: fakeAgentRunner{runID: "claude"}, probe: func(ctx context.Context) error {
		probeCtx = ctx
		close(started)
		<-release
		return errors.New("old check failed")
	}}
	router, _, probes, _ := breakerRouter(t, claude)
	defer router.Shutdown()
	router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	go func() { (*probes)[0].run(); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("probe never started")
	}
	router.finishAttempt("claude-code", nil)
	if got := healthOf(t, router, "claude-code"); got.Code != HealthReady || got.Checking {
		t.Fatalf("real success did not resume the assistant: %+v", got)
	}
	if probeCtx.Err() == nil {
		t.Fatal("superseded probe was not cancelled")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not finish")
	}
	if got := healthOf(t, router, "claude-code").Code; got != HealthReady {
		t.Fatalf("stale probe overrode a later real success: %s", got)
	}
}

// initGateRunner holds Init until released, so Shutdown can land inside it.
type initGateRunner struct {
	funcProber
	entered, release chan struct{}
}

func (g *initGateRunner) Init(ctx context.Context) error {
	close(g.entered)
	<-g.release
	return g.funcProber.Init(ctx)
}

// Shutdown lands while a probe's Init runs: the assistant must not be left
// ready and the check must not start. (PR #63 desk round 2.)
func TestRoutingShutdownDuringProbeInit(t *testing.T) {
	probed := false
	claude := &initGateRunner{funcProber: funcProber{fakeAgentRunner: fakeAgentRunner{runID: "claude"}, probe: func(context.Context) error {
		probed = true
		return nil
	}}, entered: make(chan struct{}), release: make(chan struct{})}
	router, _, probes, _ := breakerRouter(t, &claude.funcProber)
	// breakerRouter initialized the plain prober; route the probe through the gate.
	router.assistants["claude-code"] = claude
	router.mu.Lock()
	router.ready["claude-code"] = false
	router.mu.Unlock()
	router.finishAttempt("claude-code", &AssistantPreconditionError{Cause: errors.New("bad init")})
	done := make(chan struct{})
	go func() { (*probes)[0].run(); close(done) }()
	select {
	case <-claude.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("probe never reached Init")
	}
	if err := router.Shutdown(); err != nil {
		t.Fatal(err)
	}
	close(claude.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not finish")
	}
	router.mu.Lock()
	ready := router.ready["claude-code"]
	router.mu.Unlock()
	if ready || probed {
		t.Fatalf("after Shutdown: ready=%v probed=%v", ready, probed)
	}
}
