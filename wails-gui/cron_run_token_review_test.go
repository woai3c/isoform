package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"yansu-agent/agent"
	"yansu-agent/agent/authority"
	"yansu-agent/runtoken"
)

// Pause the actual native runner after the router returns a callback error.
// Its ExecuteTask has not returned, so the router's outer defer cannot help.
type callbackBarrierRunner struct {
	agent.AgentRunner
	failed   chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (r *callbackBarrierRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	defer close(r.returned)
	var once sync.Once
	return r.AgentRunner.ExecuteTask(ctx, input, func(event *agent.StreamEvent) error {
		err := callback(event)
		if err != nil {
			once.Do(func() { close(r.failed) })
			<-r.release
		}
		return err
	})
}

func TestScopedCronCallbackFailureRevokesBeforeNativeReturns(t *testing.T) {
	for _, eventType := range []string{"run_created", "model_route", "assistant"} {
		t.Run(eventType, func(t *testing.T) {
			app, api := scopedCronTestServer(t)
			// Local HTTP protocol fixture; no provider, installed CLI or app.
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Magpie-Provider", "fixture")
				w.Header().Set("X-Magpie-Model", "fixture-model")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"+
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"+
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer model.Close()
			backend := &callbackBarrierRunner{
				AgentRunner: agent.NewNativeAgentRunner(agent.NativeRunnerConfig{BaseURL: model.URL, APIKey: "test-key", DisableEnvFallback: true, DefaultModel: "fixture-model"}),
				failed:      make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{}),
			}
			router := agent.NewRoutingAgentRunner(backend, nil, agent.RoutingPolicy{})
			if err := router.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			issued := make(chan context.Context, 1)
			router.SetRunScope(func(ctx context.Context, input *agent.TaskInput) (context.Context, func(), error) {
				ctx, end, err := app.issueAgentRunScope(ctx, input)
				issued <- ctx
				return ctx, end, err
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			finished := make(chan error, 1)
			consumerErr := errors.New("consumer disconnected")
			go func() {
				_, err := router.ExecuteTask(ctx, &agent.TaskInput{Prompt: "fixture", WorkDir: t.TempDir(), AllowedTools: []string{"Bash"}, SuppressTelemetry: true}, func(event *agent.StreamEvent) error {
					if event.Type == eventType {
						return consumerErr
					}
					return nil
				})
				finished <- err
			}()
			defer func() {
				close(backend.release)
				cancel()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("native runner did not stop after callback failure")
				}
				_ = router.Shutdown()
			}()
			select {
			case <-backend.failed:
			case <-time.After(5 * time.Second):
				t.Fatal("native runner never returned the failing consumer callback")
			}
			runCtx := <-issued
			credential, _ := runtoken.FromContext(runCtx)
			if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs", ""); status != http.StatusUnauthorized {
				t.Errorf("callback failed but live backend still has HTTP access: status=%d", status)
			}
			if !errors.Is(runCtx.Err(), context.Canceled) {
				t.Error("callback failure did not cancel run context synchronously")
			}
			select {
			case <-backend.returned:
				t.Fatal("backend returned before revocation was checked")
			default:
			}
		})
	}
}

func TestScopedCronAuthorityResolvesWorkspaceAliases(t *testing.T) {
	app, api := scopedCronTestServer(t)
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, workspace, prefix string
		status                  int
	}{
		{"same alias", alias, alias, http.StatusOK},
		{"aliased grant", workspace, alias, http.StatusOK},
		{"aliased workspace", alias, workspace, http.StatusOK},
		{"foreign workspace", alias, t.TempDir(), http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{
				WorkDir: test.workspace, AutoApproveTools: true,
				AuthorityGrants: []authority.Grant{{ID: "approved", Capability: "schedule.read", ResourcePrefix: test.prefix}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer end()
			credential, _ := runtoken.FromContext(ctx)
			if status, _ := scopedCronRequest(t, api, credential.Token, "GET", "/cron/jobs", ""); status != test.status {
				t.Fatalf("aliased authority: status=%d want=%d", status, test.status)
			}
		})
	}
}

func TestScopedCronEveryRouteRefusesUnscopedExpiredAndRevokedTokens(t *testing.T) {
	app, api := scopedCronTestServer(t)
	workspace := t.TempDir()
	issue := func(ctx context.Context, noTools bool) (string, func()) {
		t.Helper()
		ctx, end, err := app.issueAgentRunScope(ctx, &agent.TaskInput{WorkDir: workspace, NoTools: noTools})
		if err != nil {
			t.Fatal(err)
		}
		credential, _ := runtoken.FromContext(ctx)
		return credential.Token, end
	}
	textOnly, endText := issue(context.Background(), true)
	defer endText()
	expires, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	expired, endExpired := issue(expires, false)
	defer endExpired()
	<-expires.Done()
	revoked, endRevoked := issue(context.Background(), false)
	endRevoked()
	for _, test := range []struct {
		name, token string
		status      int
	}{
		{"text only", textOnly, http.StatusForbidden},
		{"expired", expired, http.StatusUnauthorized},
		{"run ended", revoked, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Every scheduler/desktop API route, including both prefix routes.
			for _, path := range []string{
				"/cron/jobs", "/cron/jobs/fixture", "/activity/sessions", "/activity/sessions/fixture", "/activity/summary", "/activity/search",
				"/memory/list", "/memory/search", "/memory/show/fixture", "/skills", "/skills/sources", "/skills/search", "/skills/install", "/skills/by-id/fixture",
				"/mcp/sources", "/mcp/discover", "/mcp/install", "/mcp/registry", "/mcp", "/mcp/fixture",
			} {
				t.Run(path, func(t *testing.T) {
					for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
						if status, _ := scopedCronRequest(t, api, test.token, method, path, `{}`); status != test.status {
							t.Errorf("%s %s: status=%d want=%d", method, path, status, test.status)
						}
					}
				})
			}
		})
	}
	// A shell-enabled token is valid for its scheduler reads, but never for
	// configuration (including every skill/MCP subroute and HTTP method).
	live, endLive := issue(context.Background(), false)
	defer endLive()
	if status, _ := scopedCronRequest(t, api, live, "GET", "/cron/jobs", ""); status != http.StatusOK {
		t.Fatalf("live control token status=%d", status)
	}
	for _, path := range []string{
		"/skills", "/skills/sources", "/skills/search", "/skills/install", "/skills/by-id/fixture", "/skills/by-id/fixture/content",
		"/mcp", "/mcp/sources", "/mcp/discover", "/mcp/install", "/mcp/registry", "/mcp/fixture",
		"/mcp/fixture/tools", "/mcp/fixture/logs", "/mcp/fixture/enable", "/mcp/fixture/disable", "/mcp/fixture/restart",
	} {
		t.Run("configuration"+path, func(t *testing.T) {
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				if status, _ := scopedCronRequest(t, api, live, method, path, `{}`); status != http.StatusForbidden {
					t.Errorf("%s %s: status=%d want=403", method, path, status)
				}
			}
		})
	}
}

func TestHostBridgeEveryGuardedRouteRefusesRunToken(t *testing.T) {
	app, _ := scopedCronTestServer(t)
	ctx, end, err := app.issueAgentRunScope(context.Background(), &agent.TaskInput{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	credential, _ := runtoken.FromContext(ctx)
	hb, handler := newTestHostBridge(t)
	for _, rt := range guardedRoutes(t, hb) {
		t.Run(rt.path, func(t *testing.T) {
			req := hostBridgeRequest(rt)
			switch rt.access {
			case hostAccessHelper:
				req.Header.Set(hostBridgeTokenHeader, credential.Token)
			case hostAccessSpotlight:
				req.Header.Set(spotlightTokenHeader, credential.Token)
			case hostAccessMedia:
				q := req.URL.Query()
				q.Set(hostBridgeMediaTokenKey, credential.Token)
				req.URL.RawQuery = q.Encode()
			}
			if rec := serve(handler, req); rec.Code != http.StatusUnauthorized {
				t.Fatalf("run token reached %s: status=%d", rt.path, rec.Code)
			}
		})
	}
}
