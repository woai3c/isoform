package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"yansu-agent/runtoken"
)

// Unit protocol fixtures inspect the environment of the actual spawned
// process; they make no claims about model or installed CLI isolation.
func TestCLIRunnersReceiveOnlyIssuedRunCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX protocol fixture")
	}
	t.Setenv("YANSU_CRON_SECRET", "inherited-master")
	t.Setenv(runtoken.TokenEnv, "inherited-forgery")
	t.Setenv("YANSU_AGENT_HOME", externalFixtureState(t))
	for _, format := range []string{"claude-code"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			init := validControlledInit()
			init["tools"] = []string{}
			initJSON, _ := json.Marshal(init)
			script := "#!/bin/sh\ncase \"$1\" in\n--version) echo '2.1.296 (Claude Code)'; exit;;\n--help) echo '" + strings.Join(controlledClaudeFlags, " ") + "'; exit;;\nesac\n" +
				"printf '%s\\n' \"$YANSU_RUN_TOKEN\" \"$YANSU_RUN_PORT\" \"$YANSU_RUN_SCOPED\" \"$YANSU_CRON_SECRET\" > credential.txt\ncat >/dev/null\n"
			script += "echo '" + string(initJSON) + "'\n"
			script += "echo '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"done\",\"is_error\":false}'\n"
			binary := filepath.Join(dir, "fixture")
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cfg := ExternalAgentConfig{Format: format, Command: binary, ExtraEnv: []string{"YANSU_CRON_SECRET=config-master", runtoken.TokenEnv + "=config-forgery"}}
			{
				native := NewNativeAgentRunner(NativeRunnerConfig{})
				cfg.ToolServerFactory, cfg.ToolShimCommand = native.NewToolServer, "/unused/yansu"
			}
			runner := NewExternalAgentRunner(cfg)
			if err := runner.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer runner.Shutdown()
			ctx := runtoken.WithCredential(context.Background(), runtoken.Credential{Token: "run-issued", Port: 12345})
			_, err := runner.ExecuteTask(ctx, &TaskInput{Prompt: "fixture", WorkDir: dir, NoTools: true, Env: []string{"YANSU_CRON_SECRET=task-master", runtoken.TokenEnv + "=task-forgery"}}, func(*StreamEvent) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "credential.txt"))
			if err != nil || string(got) != "run-issued\n12345\n1\n\n" {
				t.Fatal("CLI received ambient credentials or lost run credential")
			}
		})
	}
}

func TestRoutingRunScopeEndsOnEveryExit(t *testing.T) {
	for _, kind := range []string{"success", "error", "callback", "cancel", "shutdown", "preflight shutdown"} {
		t.Run(kind, func(t *testing.T) {
			var registry runtoken.Registry
			var token string
			var err error
			native := &fakeAgentRunner{runID: "native", ready: true}
			router := NewRoutingAgentRunner(native, nil, RoutingPolicy{})
			router.SetRunScope(func(ctx context.Context, input *TaskInput) (context.Context, func(), error) {
				ctx, cancel := context.WithTimeout(ctx, time.Hour)
				var revoke func()
				token, revoke, err = registry.Mint(ctx, runtoken.Scope{RunID: "run", SessionID: "chat", Workspace: input.WorkDir})
				return ctx, func() { revoke(); cancel() }, err
			})
			if kind == "preflight shutdown" {
				router.SetPolicyProvider(func(context.Context) RoutingPolicy {
					_ = router.Shutdown()
					if _, err := registry.Verify(token); err == nil {
						t.Fatal("preflight scope survived router shutdown")
					}
					return RoutingPolicy{}
				})
			}
			native.onRun = func() {
				if _, err := registry.Verify(token); err != nil {
					t.Fatal("run token not live during execution")
				}
				switch kind {
				case "cancel":
					_ = router.CancelRun("native")
				case "shutdown":
					_ = router.Shutdown()
				}
				if kind == "cancel" || kind == "shutdown" {
					if _, err := registry.Verify(token); err == nil {
						t.Fatal("stop did not immediately revoke token")
					}
				}
			}
			if kind == "error" {
				native.executeErr = errors.New("failure")
			}
			_, _ = router.ExecuteTask(context.Background(), &TaskInput{Prompt: "unit", WorkDir: t.TempDir()}, func(event *StreamEvent) error {
				if kind == "callback" && event.Type == "run_created" {
					return errors.New("consumer disconnected")
				}
				return nil
			})
			if token == "" {
				t.Fatal("run scope was not issued")
			}
			if _, err := registry.Verify(token); err == nil {
				t.Fatal("token survived run end")
			}
		})
	}
}
