package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestAssistantFormatOrder(t *testing.T) {
	for _, tc := range []struct {
		name        string
		order, want []string
	}{
		{name: "default", want: []string{"claude-code", "codex"}},
		{name: "codex first", order: []string{"codex", "claude"}, want: []string{"codex", "claude-code"}},
		{name: "one assistant", order: []string{"codex"}, want: []string{"codex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := assistantFormatOrder(AIProviderConfig{ManagedOrder: tc.order}); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAssistantProbeCacheSharesConcurrentProbeAndRefreshes(t *testing.T) {
	var probes atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	cache := &assistantProbeCache{probe: func(context.Context) map[string]agent.AssistantState {
		if probes.Add(1) == 1 {
			close(started)
			<-release
		}
		return map[string]agent.AssistantState{"claude-code": {Available: true, SignedIn: true, ControlProfile: true}}
	}}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states := cache.snapshot(context.Background())
			if !states["claude-code"].SignedIn {
				t.Error("signed-in state lost")
			}
			delete(states, "claude-code") // callers must not mutate the cache
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if probes.Load() != 1 {
		t.Fatalf("concurrent probes = %d", probes.Load())
	}
	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	if !cache.snapshot(context.Background())["claude-code"].SignedIn || probes.Load() != 2 {
		t.Fatal("expired sign-in state was not refreshed")
	}
}

func TestAssistantProbeWaitRespectsCancellation(t *testing.T) {
	cache := &assistantProbeCache{flight: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := cache.snapshot(ctx); got != nil {
		t.Fatalf("cancelled waiter got %v", got)
	}
}

func TestAssistantProbeCancellationDoesNotPoisonCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var probes atomic.Int32
	cache := &assistantProbeCache{probe: func(ctx context.Context) map[string]agent.AssistantState {
		if probes.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return map[string]agent.AssistantState{"claude-code": {Available: true, SignedIn: true, Reason: ctx.Err().Error()}}
		}
		return map[string]agent.AssistantState{"claude-code": {Available: true, SignedIn: true, ControlProfile: true}}
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.snapshot(ctx)
	}()
	<-started
	cancel()
	<-done
	got := cache.snapshot(context.Background())["claude-code"]
	if !got.ControlProfile || probes.Load() != 2 {
		t.Fatalf("cancelled probe poisoned next run: state=%+v, probes=%d", got, probes.Load())
	}
	if !cache.snapshot(context.Background())["claude-code"].ControlProfile || probes.Load() != 2 {
		t.Fatal("successful replacement probe was not cached")
	}
}

func TestAssistantProbeAlreadyCancelledDoesNotStartProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cache := &assistantProbeCache{probe: func(context.Context) map[string]agent.AssistantState {
		t.Error("already cancelled caller started a shared probe")
		return nil
	}}
	if got := cache.snapshot(ctx); got != nil {
		t.Fatalf("cancelled caller got %v", got)
	}
}

func TestAssistantProbeRejectsUntrustedShimBeforeCLI(t *testing.T) {
	for _, shim := range []string{"", "yansu", "./bin/yansu"} {
		t.Run(shim, func(t *testing.T) {
			dir := t.TempDir()
			command := filepath.Join(dir, "claude")
			marker := filepath.Join(dir, "invoked")
			if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			state := probeAssistantRoute(context.Background(), agent.ExternalAgentConfig{
				Format: "claude-code", Command: command, ToolShimCommand: shim,
			})
			if state.Available || state.ControlProfile {
				t.Fatalf("untrusted shim %q was eligible: %+v", shim, state)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("CLI was probed without an absolute bundled shim: %v", err)
			}
		})
	}
}

func TestRemoveExternalMcpConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(agentHomeEnv, "")
	path := filepath.Join(home, configDirName, "external-mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	removeExternalMcpConfig()
	removeExternalMcpConfig()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("obsolete passthrough remains: %v", err)
	}
}

func TestRemoveExternalMcpConfigUsesFlavorRoot(t *testing.T) {
	previousDir := configDirName
	configDirName = configDirForFlavor(appFlavorIsoform)
	t.Cleanup(func() { configDirName = previousDir })
	for _, tc := range []struct {
		name          string
		override      string
		removed       string
		linkedDefault bool
	}{
		{name: "default root", removed: "default"},
		{name: "explicit root", override: "custom", removed: "custom"},
		{name: "relative root is invalid", override: "relative"},
		{name: "published root is forbidden", override: "published"},
		{name: "explicit root preserves linked default", override: "custom", removed: "custom", linkedDefault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			roots := map[string]string{
				"default":   filepath.Join(home, configDirName),
				"custom":    filepath.Join(home, "isolated"),
				"published": filepath.Join(home, ".yansu-agent"),
			}
			if tc.linkedDefault {
				if err := os.MkdirAll(roots["published"], 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(roots["published"], roots["default"]); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			for _, root := range roots {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "external-mcp.json"), []byte("credential sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			override := roots[tc.override]
			if tc.override == "relative" {
				override = "relative-agent-home"
			}
			t.Setenv(agentHomeEnv, override)
			removeExternalMcpConfig()
			removeExternalMcpConfig()
			for name, root := range roots {
				data, err := os.ReadFile(filepath.Join(root, "external-mcp.json"))
				if name == tc.removed {
					if !os.IsNotExist(err) {
						t.Errorf("selected %s export remains: %v", name, err)
					}
					continue
				}
				if err != nil || string(data) != "credential sentinel" {
					t.Errorf("unselected %s export changed: %q, %v", name, data, err)
				}
			}
		})
	}
}

func TestAgentRouteObserverRetainsFailureReason(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := &App{store: store}
	for _, event := range []*agent.StreamEvent{
		{Type: "run_created", RunID: "chat-run", Backend: "claude-code", BackendReason: "assistant order"},
		{Type: "action_started", RunID: "chat-run", Backend: "claude-code", ActionsStarted: 1},
		{Type: "error", RunID: "chat-run", Backend: "claude-code", BackendReason: "assistant order", Blocked: true, Error: "unknown outcome"},
		{Type: "close", RunID: "chat-run", Backend: "claude-code", BackendReason: "assistant order", Blocked: true},
	} {
		if err := app.recordAgentRunRoute(event); err != nil {
			t.Fatal(err)
		}
	}
	row, err := store.AgentRunRoute(context.Background(), "chat-run")
	if err != nil || row.Status != "blocked" || row.ActionsStarted != 1 || row.BackendReason != "assistant order; unknown outcome" {
		t.Fatalf("lost blocked provenance: %+v, %v", row, err)
	}
}

// A plain go test never starts the user's claude: the router built for a
// running agent sees an installed Isoform CLI (an absolute tool shim) and a
// claude on PATH, yet TestMain's probe keeps the CLI from running. The real
// probe, called directly, does run it (the control).
func TestAssistantRouterNeverRunsTheRealCLIInTests(t *testing.T) {
	if os.Getenv("YANSU_CLAUDE_INTEGRATION") == "1" {
		t.Skip("YANSU_CLAUDE_INTEGRATION=1 asks for the real probe")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(agentHomeEnv, "")
	dataDir, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dataDir, yansuCLIInstallRoot, bundledYansuCLIExecutableName())
	if err := os.MkdirAll(filepath.Dir(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if installedYansuCLIPath() != shim {
		t.Fatalf("installed CLI not found at %s", shim)
	}
	bin := t.TempDir()
	marker := filepath.Join(home, "claude-ran")
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	app := &App{ctx: context.Background()}
	app.runner = app.buildAssistantRoutingRunner(agent.NewNativeAgentRunner(agent.NativeRunnerConfig{DisableEnvFallback: true}), true)
	_ = app.GetAssistantHealth()
	_ = app.RetryAssistantHealth("claude-code")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a test started the claude on PATH: %v", err)
	}

	probeAssistantRoute(context.Background(), agent.ExternalAgentConfig{Format: "claude-code", ToolShimCommand: shim})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: the real probe should have run the claude on PATH: %v", err)
	}
	if claudeResearchProbe() {
		t.Fatal("the research sign-in probe answered from a real CLI")
	}
}

func TestAssistantRoutingDesktopAlwaysConfiguresControlledTransport(t *testing.T) {
	for _, provider := range []string{"", "claude-code", "codex", "anthropic"} {
		t.Run("provider="+provider, func(t *testing.T) {
			t.Setenv(agentHomeEnv, t.TempDir())
			if err := saveAIProviderConfig(AIProviderConfig{Provider: provider, DisableSandbox: true}); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			seen := map[string]bool{}
			previous := assistantRouteProbe
			assistantRouteProbe = func(_ context.Context, cfg agent.ExternalAgentConfig) agent.AssistantState {
				mu.Lock()
				defer mu.Unlock()
				seen[cfg.Format] = true
				if cfg.ToolServerFactory == nil || cfg.ToolServerSupports == nil || cfg.RespondToPermission == nil || cfg.CancelPermission == nil || cfg.DisableSandbox {
					t.Error("desktop built an external runner without controlled tools and permissions")
				}
				return agent.AssistantState{}
			}
			t.Cleanup(func() { assistantRouteProbe = previous })
			app := &App{ctx: context.Background()}
			router := app.buildAssistantRoutingRunner(agent.NewNativeAgentRunner(agent.NativeRunnerConfig{DisableEnvFallback: true}), true)
			router.RefreshPolicy(context.Background())
			if !seen["claude-code"] || !seen["codex"] || len(seen) != 2 {
				t.Fatalf("desktop candidates = %v", seen)
			}
		})
	}
}
