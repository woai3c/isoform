package native

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const magpieTestSSE = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// magpieRecorder captures the request headers a test server saw. The handler
// runs on the server goroutine, so reads go through the mutex.
type magpieRecorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func (rec *magpieRecorder) record(h http.Header) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.headers = append(rec.headers, h.Clone())
}

func (rec *magpieRecorder) last(t *testing.T) http.Header {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.headers) == 0 {
		t.Fatal("server saw no request")
	}
	return rec.headers[len(rec.headers)-1]
}

func magpieTinyRequest() *Request {
	return &Request{
		Model:     "claude-test",
		MaxTokens: 16,
		Messages:  []Message{{Role: "user", Content: "hi"}},
	}
}

func noopHandler(*StreamEvent) error { return nil }

func TestContextWithSessionID_EmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	if got := SessionIDFromContext(ContextWithSessionID(ctx, "")); got != "" {
		t.Fatalf("empty session id stored as %q", got)
	}
	if got := SessionIDFromContext(ContextWithSessionID(ctx, "chat-1")); got != "chat-1" {
		t.Fatalf("session id = %q, want chat-1", got)
	}
}

func TestAnthropicClientSendsMagpieSessionHeader(t *testing.T) {
	rec := &magpieRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.Header)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(magpieTestSSE))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	ctx := ContextWithSessionID(context.Background(), "chat-42")
	if err := c.doStream(ctx, magpieTinyRequest(), noopHandler); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t).Get(HeaderMagpieSession); got != "chat-42" {
		t.Fatalf("X-Magpie-Session = %q, want chat-42", got)
	}
}

func TestAnthropicClientOmitsMagpieSessionHeaderWithoutSession(t *testing.T) {
	rec := &magpieRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.Header)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(magpieTestSSE))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	if err := c.doStream(context.Background(), magpieTinyRequest(), noopHandler); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.last(t)[http.CanonicalHeaderKey(HeaderMagpieSession)]; ok {
		t.Fatal("X-Magpie-Session sent without a session id in context")
	}
}

func TestAnthropicClientReportsModelRouteWhenBothHeadersPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderMagpieProvider, "claude-sub")
		w.Header().Set(HeaderMagpieModel, "claude-sonnet-4-6")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(magpieTestSSE))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	type route struct{ provider, model string }
	var routes []route
	ctx := ContextWithModelRouteObserver(context.Background(), func(provider, model string) {
		routes = append(routes, route{provider, model})
	})
	if err := c.doStream(ctx, magpieTinyRequest(), noopHandler); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("observer calls = %d, want 1", len(routes))
	}
	if routes[0] != (route{"claude-sub", "claude-sonnet-4-6"}) {
		t.Fatalf("route = %+v", routes[0])
	}
}

func TestAnthropicClientSkipsModelRouteWithPartialHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderMagpieProvider, "claude-sub")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(magpieTestSSE))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	calls := 0
	ctx := ContextWithModelRouteObserver(context.Background(), func(string, string) { calls++ })
	if err := c.doStream(ctx, magpieTinyRequest(), noopHandler); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("observer called %d times with only X-Magpie-Provider", calls)
	}
}

func TestAnthropicClientReportsModelRouteOnErrorResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderMagpieProvider, "codex-sub")
		w.Header().Set(HeaderMagpieModel, "gpt-5")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad"}}`))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	var served []string
	ctx := ContextWithModelRouteObserver(context.Background(), func(provider, model string) {
		served = append(served, provider+"/"+model)
	})
	if err := c.doStream(ctx, magpieTinyRequest(), noopHandler); err == nil {
		t.Fatal("expected HTTP 400 error")
	}
	if len(served) != 1 || served[0] != "codex-sub/gpt-5" {
		t.Fatalf("served = %v, want [codex-sub/gpt-5]", served)
	}
}

// capacityServer answers every request with the given status, body and
// extra headers.
func capacityServer(status int, body string, headers map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestModelCapacityErrorClassification(t *testing.T) {
	cases := []struct {
		name           string
		status         int
		body           string
		retryAfter     string
		wantCapacity   bool
		wantRetryAfter time.Duration
	}{
		{
			name:         "429 rate limit is capacity",
			status:       http.StatusTooManyRequests,
			body:         `{"error":{"message":"rate limited","type":"rate_limit_error"},"type":"error"}`,
			wantCapacity: true,
		},
		{
			name:           "429 with delay-seconds Retry-After",
			status:         http.StatusTooManyRequests,
			body:           `{"error":{"message":"rate limited","type":"rate_limit_error"},"type":"error"}`,
			retryAfter:     "12",
			wantCapacity:   true,
			wantRetryAfter: 12 * time.Second,
		},
		{
			name:         "502 quota exhausted is capacity",
			status:       http.StatusBadGateway,
			body:         `{"error":{"message":"all group members exhausted their quota","type":"api_error"},"type":"error"}`,
			wantCapacity: true,
		},
		{
			name:         "502 allowance is capacity",
			status:       http.StatusBadGateway,
			body:         `{"error":{"message":"weekly allowance reached on every account","type":"api_error"},"type":"error"}`,
			wantCapacity: true,
		},
		{
			name:         "503 no accounts is capacity",
			status:       http.StatusServiceUnavailable,
			body:         `{"error":{"message":"no accounts can serve this model","type":"api_error"},"type":"error"}`,
			wantCapacity: true,
		},
		{
			name:         "503 no available accounts is capacity",
			status:       http.StatusServiceUnavailable,
			body:         `{"error":{"message":"no available accounts","type":"api_error"},"type":"error"}`,
			wantCapacity: true,
		},
		{
			name:         "502 plain upstream failure is not capacity",
			status:       http.StatusBadGateway,
			body:         `{"error":{"message":"upstream connect error","type":"api_error"},"type":"error"}`,
			wantCapacity: false,
		},
		{
			name:         "500 with quota wording is not capacity",
			status:       http.StatusInternalServerError,
			body:         `{"error":{"message":"quota exhausted"},"type":"error"}`,
			wantCapacity: false,
		},
		{
			name:         "400 with quota wording is not capacity",
			status:       http.StatusBadRequest,
			body:         `{"error":{"message":"quota exceeded"},"type":"error"}`,
			wantCapacity: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.retryAfter != "" {
				headers["Retry-After"] = tc.retryAfter
			}
			srv := capacityServer(tc.status, tc.body, headers)
			defer srv.Close()
			c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

			err := c.doStream(context.Background(), magpieTinyRequest(), noopHandler)
			if err == nil {
				t.Fatal("expected an error")
			}

			var capErr *ModelCapacityError
			gotCapacity := errors.As(err, &capErr)
			if gotCapacity != tc.wantCapacity {
				t.Fatalf("ModelCapacityError = %v, want %v (err=%v)", gotCapacity, tc.wantCapacity, err)
			}

			// Either way the status must still be reachable as *HTTPError, so
			// telemetry and the cross-turn guard keep classifying the call.
			var he *HTTPError
			if !errors.As(err, &he) || he.Status != tc.status {
				t.Fatalf("HTTPError status not reachable: %v", err)
			}

			if !tc.wantCapacity {
				return
			}
			if capErr.Status != tc.status {
				t.Errorf("Status = %d, want %d", capErr.Status, tc.status)
			}
			if capErr.Message == "" {
				t.Error("Message is empty")
			}
			if capErr.RetryAfter != tc.wantRetryAfter {
				t.Errorf("RetryAfter = %s, want %s", capErr.RetryAfter, tc.wantRetryAfter)
			}
		})
	}
}

func TestStreamReturnsModelCapacityErrorAfterRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota reached","type":"api_error"},"type":"error"}`))
	}))
	defer srv.Close()
	c := NewClient(WithBaseURL(srv.URL), WithAPIKey("test-key"))

	// Stream retries 429 the same way it always has, so the server sees
	// every attempt before the typed error is returned.
	err := c.Stream(context.Background(), magpieTinyRequest(), noopHandler)

	var capErr *ModelCapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("Stream error = %v, want *ModelCapacityError", err)
	}
	if capErr.Status != http.StatusTooManyRequests || capErr.RetryAfter != 5*time.Second {
		t.Fatalf("capErr = %+v", capErr)
	}
	if got := hits.Load(); got != int32(defaultAnthropicMaxTries) {
		t.Fatalf("server hits = %d, want %d", got, defaultAnthropicMaxTries)
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"7", 7 * time.Second},
		{"-3", 0},
		{"soon", 0},
		{"Mon, 02 Jan 2006 15:04:05 GMT", 0}, // past date
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", c.in, got, c.want)
		}
	}
	if got := parseRetryAfter(future); got <= 0 || got > 31*time.Second {
		t.Errorf("parseRetryAfter(future date) = %s, want about 30s", got)
	}
}

func TestOpenAIClientMagpieSessionAndRouteHeaders(t *testing.T) {
	rec := &magpieRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.Header)
		w.Header().Set(HeaderMagpieProvider, "openai-key")
		w.Header().Set(HeaderMagpieModel, "gpt-test")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := NewOpenAIClient(srv.URL, "key", "")

	type route struct{ provider, model string }
	var routes []route
	ctx := ContextWithSessionID(context.Background(), "chat-7")
	ctx = ContextWithModelRouteObserver(ctx, func(provider, model string) {
		routes = append(routes, route{provider, model})
	})
	req := &oaiRequest{Model: "gpt-test", Messages: []oaiMessage{{Role: "user", Content: "hi"}}}
	if err := c.doStream(ctx, req, noopHandler); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t).Get(HeaderMagpieSession); got != "chat-7" {
		t.Fatalf("X-Magpie-Session = %q, want chat-7", got)
	}
	if len(routes) != 1 || routes[0] != (route{"openai-key", "gpt-test"}) {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestOpenAIClientReturnsModelCapacityError(t *testing.T) {
	srv := capacityServer(http.StatusServiceUnavailable,
		`{"error":{"message":"no accounts available","type":"api_error"},"type":"error"}`, nil)
	defer srv.Close()
	c := NewOpenAIClient(srv.URL, "key", "")

	req := &oaiRequest{Model: "gpt-test", Messages: []oaiMessage{{Role: "user", Content: "hi"}}}
	err := c.doStream(context.Background(), req, noopHandler)

	var capErr *ModelCapacityError
	if !errors.As(err, &capErr) || capErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want ModelCapacityError with status 503", err)
	}
}

func TestEmptyRoutingGroupIsACapacityFailure(t *testing.T) {
	body := []byte(`{"error":{"message":"magpie knows no model \"group/yansu-light\" Name a model as provider/model (as /v1/models lists them) or group/<id>"}}`)
	capErr := modelCapacityFor(http.StatusNotFound, body, http.Header{})
	if capErr == nil || capErr.Status != http.StatusNotFound || !strings.Contains(capErr.Message, "group/yansu-light") {
		t.Fatalf("a group without members must read as capacity: %+v", capErr)
	}
	if modelCapacityFor(http.StatusNotFound, []byte(`{"error":{"message":"magpie knows no model \"claude/claude-opus-9\""}}`), http.Header{}) != nil {
		t.Fatal("an unknown provider model is a wrong name, not capacity")
	}
	if modelCapacityFor(http.StatusNotFound, []byte(`{"error":{"message":"not found"}}`), http.Header{}) != nil {
		t.Fatal("a plain 404 is not capacity")
	}
}
