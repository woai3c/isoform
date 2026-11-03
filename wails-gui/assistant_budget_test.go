package main

import (
	"context"
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestChatContextPreservesNativeFallbackBudget(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := newChatRunContext(parent)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 35*time.Minute-time.Second || time.Until(deadline) > 35*time.Minute {
		t.Errorf("chat deadline leaves %v, want 35-minute total envelope", time.Until(deadline))
	}
	if got := agent.NativeFallbackBudget(ctx); got != 30*time.Minute {
		t.Errorf("native fallback budget = %v, want 30m", got)
	}
	cancelParent()
	if ctx.Err() != context.Canceled {
		t.Fatalf("chat detached caller cancellation: %v", ctx.Err())
	}
}

func TestManagedAssistantBudgetAndSelectedCommand(t *testing.T) {
	for _, provider := range []string{"managed", "claude-code", "codex"} {
		for _, format := range []string{"claude-code", "codex"} {
			t.Run(provider+"/"+format, func(t *testing.T) {
				cfg := AIProviderConfig{Provider: provider, Command: "/custom/assistant", Model: "custom-model", AssistantMaxBudgetUSD: 25}
				runner := cfg.assistantRunnerConfig(format)
				if runner.Format != format || runner.MaxBudgetUSD != 25 {
					t.Fatalf("runner config = %+v", runner)
				}
				if provider == format && (runner.Command != cfg.Command || runner.Model != cfg.Model) {
					t.Fatal("selected command/model configuration lost")
				}
				if provider != format && (runner.Command != "" || runner.Model != "") {
					t.Fatal("another assistant inherited selected command/model")
				}
			})
		}
	}
}
