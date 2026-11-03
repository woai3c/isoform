package daemon

import (
	"context"
	"strings"
	"testing"

	"yansu-agent/internal/config"
)

func TestRelayClaudeExecutorAllowsOnlyWebSearch(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		cmd, err := executorCommand(context.Background(), config.DaemonExecutorClaude, "test prompt", stdin)
		if err != nil {
			t.Fatal(err)
		}
		flags := map[string]string{}
		present := map[string]bool{}
		for i, arg := range cmd.Args {
			present[arg] = true
			if i+1 < len(cmd.Args) {
				flags[arg] = cmd.Args[i+1]
			}
		}
		for _, flag := range []string{"--tools", "--allowedTools"} {
			if flags[flag] != "WebSearch" {
				t.Errorf("stdin=%v: %s=%q", stdin, flag, flags[flag])
			}
			for _, name := range strings.Split(flags[flag], ",") {
				if name != "WebSearch" {
					t.Fatalf("relay free-text executor allowed %s through %s", name, flag)
				}
			}
		}
		if !present["--strict-mcp-config"] || flags["--mcp-config"] != `{"mcpServers":{}}` {
			t.Fatalf("stdin=%v: relay executor can inherit MCP tools: %v", stdin, cmd.Args)
		}
		for _, flag := range []string{"--safe-mode", "--restricted", "--no-session-persistence"} {
			if !present[flag] {
				t.Errorf("stdin=%v: missing isolation flag %s", stdin, flag)
			}
		}
		if flags["--permission-mode"] != "dontAsk" || present["--setting-sources"] {
			t.Fatalf("stdin=%v: relay executor can inherit broader permissions: %v", stdin, cmd.Args)
		}
	}
}

func TestRelayCodexExecutorRefusesBeforeLaunch(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		cmd, err := executorCommand(context.Background(), config.DaemonExecutorCodex, "test prompt", stdin)
		if cmd != nil || err == nil || !strings.Contains(err.Error(), "restricted to web search") {
			t.Fatalf("stdin=%v: expected no child command and policy refusal; command=%v, error=%v", stdin, cmd, err)
		}
	}
	adapter := &routerTestAdapter{}
	// No session manager or stream is needed: refusal must happen before the
	// route can acquire a slot, fetch attachments/context or launch a child.
	router := &Router{daemonCfg: &config.DaemonConfig{Executor: config.DaemonExecutorCodex}, adapter: adapter}
	router.runAITask(context.Background(), Event{}, "", "test prompt", true)
	if len(adapter.replies) != 1 || !strings.Contains(adapter.replies[0], "restricted to web search") {
		t.Fatalf("expected immediate route refusal, got %v", adapter.replies)
	}
}
