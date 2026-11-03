package native

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The local model gateway (magpie) sits between the clients in this package
// and the upstream accounts. The clients send the chat session id on every
// request and read the serving account from every response:
//
//   - X-Magpie-Session (request) keeps a session on one account when the
//     gateway group uses a session stay policy. Sent when the context carries
//     a session id (see ContextWithSessionID).
//   - X-Magpie-Provider and X-Magpie-Model (response) name the account and
//     model that served the call. Reported through the observer in the context
//     (see ContextWithModelRouteObserver) only when both are present.
//
// Upstreams that do not know these headers ignore them, so the session header
// is safe to send to any endpoint the user configures.
const (
	HeaderMagpieSession  = "X-Magpie-Session"
	HeaderMagpieProvider = "X-Magpie-Provider"
	HeaderMagpieModel    = "X-Magpie-Model"
)

// sessionIDCtxKeyType is the unexported context key for the chat session id.
// Callers stamp it with ContextWithSessionID; the HTTP clients read it with
// SessionIDFromContext. The key is unexported so the header name stays an
// implementation detail of this package.
type sessionIDCtxKeyType struct{}

var sessionIDCtxKey sessionIDCtxKeyType

// ContextWithSessionID returns ctx carrying the chat session id that the HTTP
// clients send as X-Magpie-Session. Empty ids are a no-op.
func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDCtxKey, sessionID)
}

// SessionIDFromContext returns the session id stamped onto ctx, or empty.
func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// ModelRouteObserver receives the provider and model that the gateway
// reported for one response. It is called synchronously on the goroutine that
// issues the request, so it must not block.
type ModelRouteObserver func(provider, model string)

// modelRouteObserverCtxKeyType is the unexported context key for the route
// observer. Callers stamp it with ContextWithModelRouteObserver.
type modelRouteObserverCtxKeyType struct{}

var modelRouteObserverCtxKey modelRouteObserverCtxKeyType

// ContextWithModelRouteObserver returns ctx carrying obs. A nil observer is a
// no-op. Each response that carries both X-Magpie-Provider and X-Magpie-Model
// reaches obs once per HTTP attempt, including attempts that end in an error.
func ContextWithModelRouteObserver(ctx context.Context, obs ModelRouteObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, modelRouteObserverCtxKey, obs)
}

// observeModelRoute reports the route in h to the observer on ctx, if any.
// Both headers must be present; a partial route is not reported.
func observeModelRoute(ctx context.Context, h http.Header) {
	obs, ok := ctx.Value(modelRouteObserverCtxKey).(ModelRouteObserver)
	if !ok || obs == nil {
		return
	}
	provider := h.Get(HeaderMagpieProvider)
	model := h.Get(HeaderMagpieModel)
	if provider == "" || model == "" {
		return
	}
	obs(provider, model)
}

// ModelCapacityError reports that the upstream could not serve a request
// because quota or accounts are exhausted. It is returned for HTTP 429, and
// for 502 or 503 whose error message says quota, allowance, exhausted, out
// of, or no accounts.
//
// Stream retries these statuses like any other transient failure. When the
// retries run out, the error returned is this type. Unwrap yields the
// underlying *HTTPError, so existing errors.As checks on *HTTPError (status
// telemetry, the cross-turn API guard) keep working.
type ModelCapacityError struct {
	// Status is the HTTP status of the final response.
	Status int
	// Message is the upstream error message, or the trimmed raw body when the
	// body is not an Anthropic-style error envelope.
	Message string
	// RetryAfter is the Retry-After delay the upstream asked for. Zero when
	// the header is absent or unparseable. Stream does not sleep for it yet.
	RetryAfter time.Duration

	cause *HTTPError
}

func (e *ModelCapacityError) Error() string {
	// Nil-receiver guard for the same typed-nil reason as HTTPError.Error.
	if e == nil {
		return "anthropic: <nil ModelCapacityError>"
	}
	return fmt.Sprintf("anthropic: HTTP %d: model capacity unavailable: %s", e.Status, e.Message)
}

// Unwrap returns the underlying *HTTPError, or nil when there is none.
func (e *ModelCapacityError) Unwrap() error {
	if e == nil || e.cause == nil {
		return nil
	}
	return e.cause
}

// capacityMarkers are the lowercase phrases that mark a 502 or 503 as a
// capacity failure. A 429 needs no phrase. "no available account" matches the
// wording the existing model-unavailable check already handles.
var capacityMarkers = []string{"quota", "allowance", "exhausted", "out of", "no accounts", "no available account"}

// emptyGroupMarker is magpie's 404 wording for a routing group it does not
// know. Yansu's groups exist only once they have a member, so before any
// account, key or local model is set up the gateway knows no "group/yansu-*"
// at all; that is a capacity failure too, not a wrong model name.
const emptyGroupMarker = "knows no model \"group/"

// modelCapacityFor returns a ModelCapacityError when a non-2xx response is a
// capacity failure, and nil otherwise. body is the full response body.
func modelCapacityFor(status int, body []byte, h http.Header) *ModelCapacityError {
	msg := upstreamErrorMessage(body)
	switch status {
	case http.StatusTooManyRequests:
		// Rate and quota limits are capacity failures regardless of wording.
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		if !mentionsCapacity(msg) {
			return nil
		}
	case http.StatusNotFound:
		if !strings.Contains(msg, emptyGroupMarker) {
			return nil
		}
	default:
		return nil
	}
	return &ModelCapacityError{
		Status:     status,
		Message:    msg,
		RetryAfter: parseRetryAfter(h.Get("Retry-After")),
		cause:      &HTTPError{Status: status, Body: string(body)},
	}
}

// upstreamErrorMessage extracts error.message from an Anthropic-style
// envelope ({"error":{"message":"..."},"type":"error"}). It falls back to the
// trimmed raw body when the envelope is absent.
func upstreamErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if msg := strings.TrimSpace(envelope.Error.Message); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(string(body))
}

func mentionsCapacity(msg string) bool {
	lower := strings.ToLower(msg)
	for _, marker := range capacityMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// parseRetryAfter accepts both forms of Retry-After: delay-seconds and an
// HTTP date. It returns zero for anything else.
// maxRetryAfter bounds a Retry-After header so a huge value can neither
// overflow the duration nor stall a caller for days.
const maxRetryAfter = 24 * time.Hour

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
