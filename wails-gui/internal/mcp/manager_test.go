package mcp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// helperEnv toggles the test binary into a stdio MCP server. When this
// env is set, TestMain spawns a real MCP server using mcp-go's stdio
// helper instead of running the test suite, which lets us drive the
// manager against a genuine subprocess without an external dependency.
const helperEnv = "YANSU_MCP_TEST_HELPER"

// helperBehaviorEnv selects between scripted helper modes.
//   - "echo"   (default): expose an "echo" tool that returns its text
//   - "panic"           : crash on first tool call (tests is_error path)
//   - "noisy"           : emit lines to stderr before serving (tests log capture)
//   - "slow_init"       : sleep before serving (tests init timeout)
const helperBehaviorEnv = "YANSU_MCP_TEST_HELPER_MODE"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		runHelperServer()
		return
	}
	os.Exit(m.Run())
}

func runHelperServer() {
	mode := os.Getenv(helperBehaviorEnv)
	if mode == "noisy" {
		// Emit a few stderr lines so log-capture tests have something
		// to assert on. Real MCP servers commonly log their startup
		// banner here.
		for range 3 {
			_, _ = os.Stderr.WriteString("hello-from-server\n")
		}
	}
	if mode == "slow_init" {
		// Don't even start serving; let the manager's init timeout fire.
		time.Sleep(30 * time.Second)
		return
	}

	srv := server.NewMCPServer("yansu-test", "0.0.0")
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("echo the text argument back unchanged"),
		mcp.WithString("text", mcp.Required(), mcp.Description("text to echo")),
	)
	srv.AddTool(echoTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		if mode == "panic" {
			panic("test helper crashed on tool call")
		}
		text, _ := args["text"].(string)
		return mcp.NewToolResultText(text), nil
	})

	_ = server.ServeStdio(srv)
}

// helperSpec builds a Spec that re-execs the test binary as an MCP
// server. The "-test.run" filter doesn't match any test so the
// subprocess does not re-run the whole suite — TestMain detects the
// helper env first and returns before the `m.Run()` line.
func helperSpec(id, mode string) Spec {
	env := map[string]string{
		helperEnv: "1",
	}
	if mode != "" {
		env[helperBehaviorEnv] = mode
	}
	return Spec{
		ID:      id,
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     env,
	}
}

func remoteHelperSpec(id, url string) Spec {
	return Spec{
		ID:        id,
		Transport: "streamable_http",
		URL:       url,
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}
}

func TestSpecValidateTransportModes(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		wantErr string
	}{
		{
			name:    "stdio empty transport still requires command",
			spec:    Spec{ID: "stdio"},
			wantErr: "command required",
		},
		{
			name: "stdio accepts command",
			spec: Spec{ID: "stdio", Command: os.Args[0]},
		},
		{
			name:    "streamable http requires url",
			spec:    Spec{ID: "remote", Transport: "streamable_http"},
			wantErr: "url required",
		},
		{
			name: "streamable http does not require command",
			spec: Spec{ID: "remote", Transport: "streamable_http", URL: "http://127.0.0.1/mcp"},
		},
		{
			name:    "unsupported transport rejected",
			spec:    Spec{ID: "weird", Transport: "sse", URL: "http://127.0.0.1/mcp"},
			wantErr: "unsupported transport",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func newTestManager(t *testing.T, opts ...Option) *Manager {
	t.Helper()
	defaults := []Option{
		WithInitTimeout(15 * time.Second),
		WithLogCapacity(50),
	}
	return New(append(defaults, opts...)...)
}

func mustSpawn(t *testing.T, m *Manager, spec Spec) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Spawn(ctx, spec); err != nil {
		t.Fatalf("Spawn(%s): %v", spec.ID, err)
	}
}

func TestManager_SpawnInitializesAndListsTools(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("echo", "echo"))

	tools, err := m.Tools("echo")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected single echo tool, got %+v", tools)
	}
	health, err := m.Health("echo")
	if err != nil || health != HealthHealthy {
		t.Fatalf("expected healthy after spawn, got %v err=%v", health, err)
	}
}

func TestManager_CallToolRoundTrip(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("echo", "echo"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := m.CallTool(ctx, "echo", "echo", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected content from echo tool")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok || tc.Text != "ping" {
		t.Fatalf("expected text content 'ping', got %#v", res.Content[0])
	}
}

func TestManager_StreamableHTTPSpawnListAndCall(t *testing.T) {
	var authSeen atomic.Int32
	srv := server.NewMCPServer("yansu-remote-test", "0.0.0")
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("echo the text argument back unchanged"),
		mcp.WithString("text", mcp.Required(), mcp.Description("text to echo")),
	)
	srv.AddTool(echoTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, _ := req.GetArguments()["text"].(string)
		return mcp.NewToolResultText(text), nil
	})
	httpServer := server.NewTestStreamableHTTPServer(srv, server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
		if r.Header.Get("Authorization") == "Bearer test-token" {
			authSeen.Add(1)
		}
		return ctx
	}))
	t.Cleanup(httpServer.Close)

	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })
	mustSpawn(t, m, remoteHelperSpec("remote", httpServer.URL))

	mc, ok := lookupClient(m, "remote")
	if !ok {
		t.Fatal("expected remote managed client after spawn")
	}
	if mc.cmd != nil || mc.stderrDone != nil || mc.done != nil {
		t.Fatalf("remote client should not have subprocess fields: cmd=%v stderrDone=%v done=%v", mc.cmd, mc.stderrDone, mc.done)
	}

	tools, err := m.Tools("remote")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected single echo tool, got %+v", tools)
	}
	health, err := m.Health("remote")
	if err != nil || health != HealthHealthy {
		t.Fatalf("expected healthy after spawn, got %v err=%v", health, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := m.CallTool(ctx, "remote", "echo", map[string]any{"text": "pong"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected content from remote echo tool")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok || tc.Text != "pong" {
		t.Fatalf("expected text content 'pong', got %#v", res.Content[0])
	}
	if authSeen.Load() == 0 {
		t.Fatal("streamable http client did not send configured headers")
	}

	if err := m.Shutdown(context.Background(), "remote"); err != nil {
		t.Fatalf("Shutdown remote: %v", err)
	}
	if _, ok := lookupClient(m, "remote"); ok {
		t.Fatal("remote client should be deleted after Shutdown")
	}
}

func TestManager_ShutdownTerminatesProcess(t *testing.T) {
	m := newTestManager(t)
	mustSpawn(t, m, helperSpec("echo", "echo"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx, "echo"); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Subsequent operations must report missing.
	if _, err := m.Tools("echo"); err == nil {
		t.Error("Tools should error after Shutdown")
	}
}

func TestManager_RestartReplacesClient(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("echo", "echo"))
	if err := m.Restart(context.Background(), helperSpec("echo", "echo")); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	tools, err := m.Tools("echo")
	if err != nil {
		t.Fatalf("Tools after Restart: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected tools after restart")
	}
}

func TestManager_DuplicateSpawnRejected(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("echo", "echo"))
	err := m.Spawn(context.Background(), helperSpec("echo", "echo"))
	if err == nil || !strings.Contains(err.Error(), "already managed") {
		t.Fatalf("expected duplicate-spawn error, got %v", err)
	}
}

func TestManager_LogsCaptureStderr(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("noisy", "noisy"))

	// stderr is line-buffered through bufio.Scanner; give the capture
	// goroutine a moment to drain the lines that arrived before init.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lines, err := m.Logs("noisy", 0)
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		if len(lines) >= 3 {
			for _, line := range lines {
				if !strings.Contains(line, "hello-from-server") {
					t.Errorf("unexpected stderr line: %q", line)
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting for stderr lines to appear in ring buffer")
}

func TestManager_InitTimeoutMarksFailure(t *testing.T) {
	m := newTestManager(t, WithInitTimeout(500*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := m.Spawn(ctx, helperSpec("slow", "slow_init"))
	if err == nil {
		t.Fatal("expected init timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "initialize") {
		t.Errorf("expected timeout-shaped error, got: %v", err)
	}
	// Slot must be cleaned up so a retry can succeed.
	if _, ok := lookupClient(m, "slow"); ok {
		t.Error("manager should have rolled back the failed spawn")
	}
}

func TestManager_BadCommandReturnsError(t *testing.T) {
	m := newTestManager(t)
	spec := helperSpec("bogus", "echo")
	spec.Command = "/no/such/binary/anywhere"
	err := m.Spawn(context.Background(), spec)
	if err == nil {
		t.Fatal("expected spawn error for missing binary")
	}
}

func TestManager_FilteredEnvIsApplied(t *testing.T) {
	// Sanity-check the parent-env filter: only PATH-class keys are
	// passed through, custom env (like API tokens) are added on top.
	m := newTestManager(t)
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	t.Setenv("EXTRA_SECRET_DO_NOT_PASSTHROUGH", "boom")
	mustSpawn(t, m, helperSpec("env", "echo"))

	// We can't introspect child env directly, but we can assert the
	// parent-side filter doesn't leak unknown keys: filteredParentEnv
	// is deterministic.
	for _, kv := range filteredParentEnv() {
		key := kv[:strings.IndexByte(kv, '=')]
		if key == "EXTRA_SECRET_DO_NOT_PASSTHROUGH" {
			t.Fatal("filteredParentEnv leaked a non-allowlisted key")
		}
	}
}

func TestManager_WithCommandFactoryUsesOverride(t *testing.T) {
	called := false
	factory := func(command string, args []string) *exec.Cmd {
		called = true
		return exec.Command(command, args...)
	}
	m := New(WithCommandFactory(factory), WithInitTimeout(500*time.Millisecond))
	_ = m.Spawn(context.Background(), Spec{
		ID:      "factory",
		Command: "/no/such/binary",
	})
	if !called {
		t.Error("custom command factory was never invoked")
	}
}

// lookupClient is a test-only accessor that peeks into the private
// clients map without the public Tools/Health roundtripping (those
// would themselves error on a missing slot).
func lookupClient(m *Manager, id string) (*managedClient, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mc, ok := m.clients[id]
	return mc, ok
}

// TestManager_ExitCallbackFiresOnUnexpectedExit verifies that a
// subprocess dying outside of our control fires the configured exit
// callback exactly once with reason=ExitUnexpected. This is the path
// the App-level bridge uses to drop tools whose server crashed.
func TestManager_ExitCallbackFiresOnUnexpectedExit(t *testing.T) {
	type exitEvent struct {
		id     string
		reason ExitReason
		err    error
	}
	events := make(chan exitEvent, 4)
	cb := func(id string, reason ExitReason, err error) {
		events <- exitEvent{id: id, reason: reason, err: err}
	}
	m := newTestManager(t, WithExitCallback(cb))
	t.Cleanup(func() { m.ShutdownAll(context.Background()) })

	mustSpawn(t, m, helperSpec("echo", "echo"))

	// Kill the subprocess externally (not via Manager.Shutdown) so
	// watchExit observes intentional=false and fires the callback.
	mc, ok := lookupClient(m, "echo")
	if !ok {
		t.Fatal("expected managed client after spawn")
	}
	if err := mc.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}

	select {
	case ev := <-events:
		if ev.id != "echo" {
			t.Errorf("event id = %q, want echo", ev.id)
		}
		if ev.reason != ExitUnexpected {
			t.Errorf("reason = %v, want ExitUnexpected", ev.reason)
		}
		// ev.err is the cmd.Wait() error (signal: killed); allow nil
		// or non-nil, just don't crash.
	case <-time.After(5 * time.Second):
		t.Fatal("exit callback never fired after external kill")
	}

	// Health should be unhealthy too — same code path flips it.
	if got, _ := m.Health("echo"); got != HealthUnhealthy {
		t.Errorf("health = %v, want unhealthy", got)
	}

	// And the callback must fire exactly once: a stray second event
	// would mean Shutdown's intentional flag isn't being respected.
	select {
	case extra := <-events:
		t.Errorf("unexpected extra exit event: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestManager_ShutdownDoesNotFireCallback ensures the intentional
// flag suppresses the callback on caller-driven teardown. Without it,
// every disable/restart would double-unregister the bridge tools.
func TestManager_ShutdownDoesNotFireCallback(t *testing.T) {
	var fired atomic.Int32
	cb := func(_ string, _ ExitReason, _ error) {
		fired.Add(1)
	}
	m := newTestManager(t, WithExitCallback(cb))

	mustSpawn(t, m, helperSpec("echo", "echo"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx, "echo"); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Allow watchExit time to mis-fire if the suppression is broken.
	time.Sleep(200 * time.Millisecond)
	if got := fired.Load(); got != 0 {
		t.Errorf("callback fired %d times for an intentional shutdown", got)
	}
}

// TestManager_ShutdownAfterExternalKillIsClean exercises the case
// where watchExit has already run (process died unexpectedly) and the
// caller still calls Shutdown — for instance, the user clicked
// "Disable" right after a crash. The done channel must be safe to
// read from multiple goroutines, which the previous single-buffered
// cmdExitCh was not.
func TestManager_ShutdownAfterExternalKillIsClean(t *testing.T) {
	m := newTestManager(t)
	mustSpawn(t, m, helperSpec("echo", "echo"))

	mc, ok := lookupClient(m, "echo")
	if !ok {
		t.Fatal("expected managed client")
	}
	if err := mc.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	// Give watchExit a moment to consume the done channel close.
	time.Sleep(100 * time.Millisecond)

	// Shutdown must complete promptly (no waiting for a chan that's
	// already drained) and must not return an error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Shutdown(ctx, "echo") }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown after external kill: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown blocked after external kill — done channel contended?")
	}
}
