package native

import (
	"errors"
	"strings"
	"time"
)

const (
	// apiCircuitBreakerThreshold is the number of consecutive transient
	// API failures (429/5xx) across turns before the loop terminates.
	// Each turn already retries 3 times inside Stream(); this guard adds
	// a cross-turn layer with longer backoff so a brief rate-limit window
	// doesn't kill a long-running task, while a persistently-down upstream
	// terminates cleanly instead of amplifying load.
	//
	// 5 turns × 3 Stream retries = 15 total API requests max, spread over
	// ~130s of backoff — enough time for a rate-limit window to pass, low
	// enough to avoid the 429-storm amplification seen in production.
	apiCircuitBreakerThreshold = 5
	// apiCircuitBreakerInitialBackoff is the sleep before the first cross-
	// turn retry. Doubles each subsequent failure, capped at max.
	apiCircuitBreakerInitialBackoff = 10 * time.Second
	apiCircuitBreakerMaxBackoff     = 60 * time.Second

	// Tier-aware fallback targets when the requested model has no
	// available accounts on sub2api (see modelFallbackTargetFor). Opus 4.6
	// shares the same context window (1M) and output cap (128k) as newer
	// Opus variants, so the sonnet→opus switch is seamless — no compaction
	// or token-limit adjustment needed.
	modelFallbackSonnet = "claude-sonnet-4-6"
	modelFallbackOpus   = "claude-opus-4-6"
)

// modelFallbackTargetFor returns the model to retry with when `model` has
// no available accounts on sub2api, or "" when there is no sensible
// fallback. Tier-aware so a light-tier background task never silently
// escalates to the most expensive tier: haiku falls back to sonnet, sonnet
// falls back to opus. Opus itself — and custom/BYOA ids whose tier can't
// be classified — get no fallback and flow into the circuit breaker, which
// is safer than blindly retrying on a model the endpoint may not serve.
func modelFallbackTargetFor(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "haiku") {
		return modelFallbackSonnet
	}
	if strings.Contains(m, "sonnet") {
		return modelFallbackOpus
	}
	return ""
}

// ErrAPICircuitBreaker is returned when consecutive transient API failures
// exhaust the circuit breaker threshold. The loop has already retried with
// exponential backoff; further retries would just amplify load on an
// unhealthy upstream.
var ErrAPICircuitBreaker = errors.New("API service unavailable after repeated failures")

// isTransientAPIError returns true for HTTP status codes that indicate a
// transient upstream issue worth retrying with backoff — the same set
// shouldRetry uses inside Stream(), but applied at the cross-turn level.
func isTransientAPIError(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || he == nil {
		return false
	}
	switch he.Status {
	case 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// isModelUnavailableError returns true when the upstream (sub2api) reports
// that no account can serve the requested model. This is a permanent
// failure for the current model — retrying won't help, but switching to
// a different model will.
func isModelUnavailableError(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || he == nil {
		return false
	}
	if he.Status != 503 {
		return false
	}
	body := strings.ToLower(he.Body)
	return strings.Contains(body, "no available accounts") ||
		strings.Contains(body, "no available account")
}

// apiCircuitBreakerBackoff returns the backoff duration for the given
// streak count. Exponential: 10s, 20s, 40s, 60s (capped).
func apiCircuitBreakerBackoff(streak int) time.Duration {
	if streak <= 0 {
		return apiCircuitBreakerInitialBackoff
	}
	d := apiCircuitBreakerInitialBackoff
	for i := 1; i < streak; i++ {
		d *= 2
		if d >= apiCircuitBreakerMaxBackoff {
			return apiCircuitBreakerMaxBackoff
		}
	}
	return d
}
