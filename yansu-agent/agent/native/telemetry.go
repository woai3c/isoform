package native

import (
	"context"
	"sync/atomic"
)

// traceIDCtxKeyType is the unexported context-key type for cross-process
// trace id propagation. Loop.Run stamps it onto the request context;
// the HTTP client (doStream) reads it via TraceIDFromContext and sets
// it as an x-yansu-trace-id header on outbound requests so backend /
// proxy layers can stitch the same trace together.
type traceIDCtxKeyType struct{}

var traceIDCtxKey traceIDCtxKeyType

// suppressAgentloopCtxKeyType is the unexported context-key type for the
// "this run opted out of agentloop telemetry" signal. doStream reads it
// via SuppressAgentloopFromContext and sets x-yansu-suppress-agentloop:
// 1 on outbound requests so the backend's telemetry layer can drop its
// own agentloop.backend.* events for these runs — without this header
// the backend's trace_id middleware auto-mints a UUID when none is
// supplied, so a plain "trace_id missing → drop" rule on the backend
// is unreachable and suppressed runs still emit backend events.
type suppressAgentloopCtxKeyType struct{}

var suppressAgentloopCtxKey suppressAgentloopCtxKeyType

// ContextWithTraceID returns ctx with traceID attached. Empty traceID
// is a no-op — callers don't need to special-case missing ids.
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDCtxKey, traceID)
}

// TraceIDFromContext returns the trace id stamped onto ctx (or empty).
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// ContextWithSuppressAgentloop returns ctx flagged as "do not emit
// agentloop.* events for this request chain". The HTTP layer reads this
// via SuppressAgentloopFromContext and sets x-yansu-suppress-agentloop
// on outbound requests so backend telemetry can match the local
// suppression decision.
func ContextWithSuppressAgentloop(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressAgentloopCtxKey, true)
}

// SuppressAgentloopFromContext reports whether ctx is flagged as
// agentloop-suppressed. Absent key returns false.
func SuppressAgentloopFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(suppressAgentloopCtxKey).(bool)
	return v
}

// retryCounterCtxKeyType is the unexported key for the per-LLM-call
// external retry counter introduced in Phase 0.6. The agent loop
// creates one counter per turn before invoking runOneTurn → Stream,
// Stream atomically increments it for every retry, the loop reads
// the final value when emitting agentloop.step.llm.completed /
// step.llm.error. Counter-via-ctx (vs a wider Stream signature
// change) keeps the public API stable and matches how upstream
// suppression and trace_id are already plumbed in this package.
type retryCounterCtxKeyType struct{}

var retryCounterCtxKey retryCounterCtxKeyType

// ContextWithRetryCounter attaches a fresh zero-initialised counter
// to ctx so the next Stream call records its retry count there.
// Returns the new ctx and a pointer to the counter for the caller
// to read after the request completes. Callers SHOULD create a new
// counter per logical LLM request — sharing one across multiple
// runOneTurn calls would conflate retries from different requests.
func ContextWithRetryCounter(ctx context.Context) (context.Context, *atomic.Int32) {
	counter := &atomic.Int32{}
	return context.WithValue(ctx, retryCounterCtxKey, counter), counter
}

// retryCounterFromContext returns the counter attached to ctx, or
// nil when none was attached. Stream uses this to record retries
// without LoopConfig having to leak into the anthropic client.
// Returns the typed pointer rather than `any` so the increment
// site doesn't have to assert.
func retryCounterFromContext(ctx context.Context) *atomic.Int32 {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(retryCounterCtxKey).(*atomic.Int32)
	return v
}

// runIDCtxKeyType / stepIndexCtxKeyType carry the agent run id and the
// 0-based step (turn) index of the in-flight LLM call across to the HTTP
// layer. doStream reads them via RunIDFromContext / StepIndexFromContext
// and sets x-yansu-run-id / x-yansu-step-index on outbound requests so
// the backend conversation capture (plan §12) can place each captured
// request/response at the right step within a trace. Plumbed via ctx for
// the same reason as trace_id and the retry counter — it keeps the
// Stream / doStream signatures stable.
type runIDCtxKeyType struct{}

var runIDCtxKey runIDCtxKeyType

type stepIndexCtxKeyType struct{}

var stepIndexCtxKey stepIndexCtxKeyType

// ContextWithRunID returns ctx with runID attached. Empty runID is a
// no-op so callers don't special-case missing ids.
func ContextWithRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDCtxKey, runID)
}

// RunIDFromContext returns the run id stamped onto ctx (or empty).
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// ContextWithStepIndex returns ctx with the 0-based step index attached.
// A negative index is a no-op so callers can pass "unknown" without a
// guard.
func ContextWithStepIndex(ctx context.Context, stepIndex int) context.Context {
	if stepIndex < 0 {
		return ctx
	}
	return context.WithValue(ctx, stepIndexCtxKey, stepIndex)
}

// StepIndexFromContext returns the step index stamped onto ctx, or -1
// when absent.
func StepIndexFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(stepIndexCtxKey).(int); ok {
		return v
	}
	return -1
}

// Telemetry is the abstract event-capture hook the loop uses to emit
// observability events (PostHog, statsd, otel — anything). yansu-agent
// deliberately does NOT depend on a specific telemetry SDK: this
// interface lets the caller (wails-gui, tests, etc.) inject any
// implementation while keeping the agent module independent.
//
// Event names follow a dotted-namespace convention: agent.loop.started,
// agent.tool_pairing.detected, etc. Properties is a flat map of
// JSON-friendly scalars (string/int/bool/float).
//
// Implementations must be safe to call from any goroutine and must NOT
// block — the loop calls Capture inline and a slow telemetry path
// directly impacts user-visible latency.
type Telemetry interface {
	Capture(event string, properties map[string]any)
}

// NoOpTelemetry is the default zero-value implementation. Callers that
// don't wire up telemetry get this — every Capture is a no-op. The loop
// never checks for nil; it just uses NoOp{} when LoopConfig.Telemetry
// is nil. Saves repeated `if cfg.Telemetry != nil` guards at every
// emit site.
type NoOpTelemetry struct{}

// Capture implements Telemetry.
func (NoOpTelemetry) Capture(string, map[string]any) {}
