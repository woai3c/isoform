package agent

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"yansu-agent/agent/native"
)

// The breaker keeps a run from waiting on an assistant known to fail. A failed
// precondition (the CLI cannot prove its tool profile) pauses the assistant at
// once; other errors pause it after breakerFailureLimit attempts in a row. A
// paused assistant is skipped without spawning until a background probe
// passes. The cool-down doubles on every pause in a row, up to the maximum.
const (
	breakerFailureLimit = 3
	breakerBaseCooldown = 10 * time.Minute
	breakerMaxCooldown  = 2 * time.Hour
	breakerProbeTimeout = 45 * time.Second
)

// Health codes name an assistant's state for the UI, which words them.
const (
	HealthReady              = "ready"
	HealthNotInstalled       = "not_installed"
	HealthSignedOut          = "signed_out"
	HealthSignInCheckFailed  = "sign_in_check_failed"
	HealthUnsupportedVersion = "unsupported_version"
	HealthToolsUnverified    = "tools_unverified"
	HealthToolConnection     = "tool_connection"
	HealthUnavailable        = "unavailable"
	HealthCheckFailed        = "check_failed"
	HealthRepeatedErrors     = "repeated_errors"
	HealthUsageLimit         = "usage_limit"
)

// Route reasons for a paused assistant, kept in the run's route record.
const (
	pausedCheckReason  = "assistant is paused after a failed check"
	pausedErrorsReason = "assistant is paused after repeated errors"
)

// AssistantPreconditionError marks a failure that proves the installed CLI
// cannot carry an Isoform run before any request reaches its model: an
// unsupported version, missing control flags, or a live init that does not
// match the verified tool profile.
type AssistantPreconditionError struct{ Cause error }

func (e *AssistantPreconditionError) Error() string { return e.Cause.Error() }
func (e *AssistantPreconditionError) Unwrap() error { return e.Cause }

// AssistantUsageLimitError reports that the assistant's own account ran out of
// usage during the run, as the CLI's rate-limit event said.
type AssistantUsageLimitError struct{ Cause error }

func (e *AssistantUsageLimitError) Error() string { return "usage limit reached: " + e.Cause.Error() }
func (e *AssistantUsageLimitError) Unwrap() error { return e.Cause }

// answerDeadlineError marks Isoform's own time limit ending a run the
// assistant had already started answering: a long answer, not a broken CLI.
type answerDeadlineError struct{ Cause error }

func (e *answerDeadlineError) Error() string { return e.Cause.Error() }
func (e *answerDeadlineError) Unwrap() error { return e.Cause }

// HealthProber checks an assistant's control profile without a user's task.
type HealthProber interface {
	ProbeHealth(context.Context) error
}

// errAttemptNotJudged releases an attempt whose outcome says nothing about the
// assistant: the user cancelled, the consumer went away, or a read policy
// refused dispatch before the CLI started.
var errAttemptNotJudged = errors.New("attempt outcome does not reflect assistant health")

// AssistantHealth is one assistant's line in Settings.
type AssistantHealth struct {
	Format   string    `json:"format"`
	Code     string    `json:"code"`
	Reason   string    `json:"reason,omitempty"`
	Checking bool      `json:"checking"`
	Since    time.Time `json:"since"`
	RetryAt  time.Time `json:"retryAt"`
}

type assistantBreaker struct {
	failures int
	paused   bool
	code     string
	reason   string
	since    time.Time
	until    time.Time
	trips    int
	probing  bool
	// probeGen identifies the probe in flight; a newer verdict (a real run
	// that succeeded) bumps it so the older probe's result is discarded.
	probeGen    int
	probeCancel context.CancelFunc
	timer       *time.Timer
	version     string
	signedIn    bool
}

func (r *RoutingAgentRunner) breakerLocked(format string) *assistantBreaker {
	if r.breakers == nil {
		r.breakers = make(map[string]*assistantBreaker)
	}
	b := r.breakers[format]
	if b == nil {
		b = &assistantBreaker{}
		r.breakers[format] = b
	}
	return b
}

func (r *RoutingAgentRunner) pausedLocked(format string) bool {
	b := r.breakers[format]
	return b != nil && b.paused
}

// SetHealthObserver is told whenever an assistant's health changes.
func (r *RoutingAgentRunner) SetHealthObserver(observer func()) {
	r.mu.Lock()
	r.healthObserver = observer
	r.mu.Unlock()
}

func (r *RoutingAgentRunner) notifyHealth() {
	r.mu.Lock()
	observer := r.healthObserver
	r.mu.Unlock()
	if observer != nil {
		observer()
	}
}

// recordOutcomeLocked feeds one finished attempt into the breaker. It reports
// whether the assistant's health changed.
func (r *RoutingAgentRunner) recordOutcomeLocked(format string, err error) bool {
	b := r.breakerLocked(format)
	var precondition *AssistantPreconditionError
	var denied *native.DispatchDeniedError
	var deadline *answerDeadlineError
	switch true {
	case err == nil:
		b.failures, b.trips = 0, 0
		if !b.paused {
			return false
		}
		// A run admitted before the pause has just succeeded: that is
		// better evidence than any probe, including one still running.
		b.paused, b.code, b.reason = false, "", ""
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		r.abandonProbeLocked(b)
		return true
	case errors.Is(err, errAttemptNotJudged), errors.Is(err, context.Canceled), errors.As(err, &denied), errors.As(err, &deadline), quotaFailure(err):
		// A usage limit has its own cool-down; the rest say nothing.
		return false
	case b.paused:
		// Runs admitted before the pause end one by one; only the first
		// failure counts, or concurrent runs would stretch the cool-down.
		return false
	case errors.As(err, &precondition):
		r.tripLocked(format, controlProfileCode(err.Error()), err)
		return true
	}
	b.failures++
	if b.failures < breakerFailureLimit {
		return false
	}
	r.tripLocked(format, HealthRepeatedErrors, err)
	return true
}

func (r *RoutingAgentRunner) tripLocked(format, code string, cause error) {
	b := r.breakerLocked(format)
	now := r.now()
	if !b.paused {
		b.since = now
	}
	b.trips++
	cooldown := breakerBaseCooldown
	for i := 1; i < b.trips && cooldown < breakerMaxCooldown; i++ {
		cooldown *= 2
	}
	if cooldown > breakerMaxCooldown {
		cooldown = breakerMaxCooldown
	}
	b.paused, b.code, b.reason, b.failures = true, code, cause.Error(), 0
	b.until = now.Add(cooldown)
	state := assistantState(r.policy, format)
	b.version, b.signedIn = state.Version, state.SignedIn
	log.Printf("[routing] %s paused for %s: %s", format, cooldown, code)
	r.scheduleProbeLocked(format, cooldown)
}

func (r *RoutingAgentRunner) scheduleProbeLocked(format string, delay time.Duration) {
	b := r.breakerLocked(format)
	if b.timer != nil {
		b.timer.Stop()
	}
	after := r.afterFunc
	if after == nil {
		after = time.AfterFunc
	}
	b.timer = after(delay, func() { r.probeAssistant(format) })
}

// abandonProbeLocked cancels the probe in flight and discards its result.
func (r *RoutingAgentRunner) abandonProbeLocked(b *assistantBreaker) {
	if b.probeCancel != nil {
		b.probeCancel()
		b.probeCancel = nil
	}
	b.probeGen++
	b.probing = false
}

// probeAssistant runs in the background. The assistant stays paused while it
// runs, so no user's run waits on it. The probe's context is created with its
// admission, under the router's lock, from the router's probe lifecycle:
// Shutdown cancels that lifecycle, and nothing creates a new one while the
// router is stopped, so no check starts or keeps running after Shutdown.
func (r *RoutingAgentRunner) probeAssistant(format string) {
	r.mu.Lock()
	b := r.breakers[format]
	runner := r.assistants[format]
	if r.stopped || b == nil || !b.paused || b.probing || runner == nil {
		r.mu.Unlock()
		return
	}
	if r.probeCtx == nil {
		r.probeCtx, r.probeCancel = context.WithCancel(context.Background())
	}
	ctx, cancel := context.WithTimeout(r.probeCtx, breakerProbeTimeout)
	defer cancel()
	b.probing, b.probeCancel = true, cancel
	b.probeGen++
	gen := b.probeGen
	r.mu.Unlock()
	r.notifyHealth()

	var err error
	if prober, ok := runner.(HealthProber); ok {
		// Initialization ignores cancellation, so check before each step.
		if err = ctx.Err(); err == nil {
			err = r.ensureInitialized(ctx, format)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = prober.ProbeHealth(ctx)
		}
	}

	r.mu.Lock()
	if b.probeGen != gen {
		// A real run's verdict superseded this check while it ran.
		r.mu.Unlock()
		return
	}
	b.probing, b.probeCancel = false, nil
	if r.stopped || errors.Is(ctx.Err(), context.Canceled) {
		// Shutdown cut the check short; it proves nothing either way.
		r.mu.Unlock()
		return
	}
	if err != nil {
		// A failed check reads as one: "needs an update" when the version
		// is the cause, otherwise "paused after a failed check".
		r.tripLocked(format, controlProfileCode(err.Error()), err)
	} else {
		// A passing check proves the profile, not the model. The next real
		// run decides: one more error pauses the assistant again.
		b.paused, b.code, b.reason = false, "", ""
		b.failures = breakerFailureLimit - 1
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		log.Printf("[routing] %s passed its check; resuming", format)
	}
	r.mu.Unlock()
	r.notifyHealth()
}

// RetryAssistant checks a paused assistant now instead of after its cool-down.
func (r *RoutingAgentRunner) RetryAssistant(format string) {
	r.mu.Lock()
	b := r.breakers[format]
	if b == nil || !b.paused || b.probing {
		r.mu.Unlock()
		return
	}
	r.scheduleProbeLocked(format, 0)
	r.mu.Unlock()
}

// reprobeOnChangeLocked checks a paused assistant at once when its CLI version
// or sign-in changed, since either can clear the cause.
func (r *RoutingAgentRunner) reprobeOnChangeLocked() {
	for format, b := range r.breakers {
		if !b.paused || b.probing {
			continue
		}
		state := assistantState(r.policy, format)
		versionChanged := state.Version != "" && b.version != "" && state.Version != b.version
		signedIn := state.SignedIn && !b.signedIn
		b.version, b.signedIn = state.Version, state.SignedIn
		if versionChanged || signedIn {
			log.Printf("[routing] %s changed (version=%v sign-in=%v); checking again", format, versionChanged, signedIn)
			r.scheduleProbeLocked(format, 0)
		}
	}
}

// AssistantHealth reports every configured assistant, in the user's order.
func (r *RoutingAgentRunner) AssistantHealth(ctx context.Context) []AssistantHealth {
	r.RefreshPolicy(ctx)
	r.mu.Lock()
	policy := cloneRoutingPolicy(r.policy)
	formats := make([]string, 0, len(r.assistants))
	seen := make(map[string]bool, len(r.assistants))
	for _, format := range assistantOrder(policy) {
		if r.assistants[format] != nil && !seen[format] {
			seen[format] = true
			formats = append(formats, format)
		}
	}
	for _, format := range []string{string(BackendClaudeCode), string(BackendCodex)} {
		if r.assistants[format] != nil && !seen[format] {
			seen[format] = true
			formats = append(formats, format)
		}
	}
	runners := make(map[string]AgentRunner, len(formats))
	for _, format := range formats {
		runners[format] = r.assistants[format]
	}
	result := make([]AssistantHealth, 0, len(formats))
	for _, format := range formats {
		health := AssistantHealth{Format: format, Code: HealthReady}
		state := assistantState(policy, format)
		b := r.breakers[format]
		switch true {
		case !state.Available:
			health.Code, health.Reason = unavailableCode(state.Reason), state.Reason
		case !state.SignedIn:
			health.Code, health.Reason = HealthSignedOut, state.Reason
			if state.Reason == "sign-in probe failed" {
				health.Code = HealthSignInCheckFailed
			}
		case !state.ControlProfile:
			health.Code, health.Reason = controlProfileCode(state.Reason), state.Reason
		case b != nil && b.paused:
			health.Code, health.Reason = b.code, b.reason
			health.Checking, health.Since, health.RetryAt = b.probing, b.since, b.until
		case r.now().Before(r.cooldownUntil[format]):
			health.Code, health.RetryAt = HealthUsageLimit, r.cooldownUntil[format]
		}
		result = append(result, health)
	}
	r.mu.Unlock()
	for i := range result {
		if result[i].Code != HealthReady {
			continue
		}
		if eligibility, ok := runners[result[i].Format].(interface {
			RoutingEligibility(*TaskInput) (bool, string)
		}); ok {
			// A chat with no tools: eligibility for the assistant itself,
			// not for one task's tool list.
			if ok, reason := eligibility.RoutingEligibility(&TaskInput{NoTools: true}); !ok {
				result[i].Code, result[i].Reason = controlProfileCode(reason), reason
			}
		}
	}
	return result
}

func unavailableCode(reason string) string {
	switch reason {
	case "binary missing", "not installed or unavailable", "":
		return HealthNotInstalled
	case "Isoform tool shim is unavailable", "Isoform tool server is unavailable", "Isoform tool shim requires an absolute path":
		return HealthToolConnection
	}
	return HealthUnavailable
}

func controlProfileCode(reason string) string {
	switch true {
	case strings.Contains(reason, "tool isolation has not been verified"):
		return HealthToolsUnverified
	case strings.Contains(reason, "or newer is required"), strings.Contains(reason, "missing required control flag"):
		return HealthUnsupportedVersion
	case strings.HasPrefix(reason, "Isoform tool"):
		return HealthToolConnection
	}
	return HealthCheckFailed
}

func pausedRouteReason(code string) string {
	if code == HealthRepeatedErrors {
		return pausedErrorsReason
	}
	return pausedCheckReason
}
