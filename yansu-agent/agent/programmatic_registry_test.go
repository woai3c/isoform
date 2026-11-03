package agent

import (
	"context"
	"strings"
	"testing"

	"yansu-agent/agent/tools"
)

func TestRegistryForSession_RegistersProgrammaticByDefault(t *testing.T) {
	r := NewNativeAgentRunner(NativeRunnerConfig{})
	scoped := r.registryForSession(tools.NewPlanModeState(false), tools.NewTodoList(), nil)
	if scoped.Get("Programmatic") == nil {
		t.Fatal("Programmatic should be registered for unrestricted chat sessions")
	}
}

func TestRegistryForSession_ProgrammaticRespectsAllowedTools(t *testing.T) {
	r := NewNativeAgentRunner(NativeRunnerConfig{})
	plan := tools.NewPlanModeState(false)
	todos := tools.NewTodoList()

	readOnly := r.registryForSession(plan, todos, []string{"Read", "Grep", "Glob"})
	if readOnly.Get("Programmatic") != nil {
		t.Fatal("Programmatic should not be registered unless explicitly allowed in restricted sessions")
	}

	scoped := r.registryForSession(plan, todos, []string{"Programmatic", "Bash"})
	res, err := scoped.Execute(context.Background(), "Programmatic", tools.ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = bash("printf ok", description="Print ok").content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, `"result": "ok"`) {
		t.Fatalf("expected Bash helper to be allowed, got:\n%s", res.Content)
	}

	res, err = scoped.Execute(context.Background(), "Programmatic", tools.ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = read("/tmp/nope").content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "undefined: read") {
		t.Fatalf("expected Read helper to be unavailable, got:\n%s", res.Content)
	}
}

func TestRegistryForSession_ProgrammaticExposesBundledHelpersWhenAllowed(t *testing.T) {
	r := NewNativeAgentRunner(NativeRunnerConfig{})
	plan := tools.NewPlanModeState(false)
	todos := tools.NewTodoList()

	scoped := r.registryForSession(plan, todos, []string{"Programmatic", "WebFetch"})
	res, err := scoped.Execute(context.Background(), "Programmatic", tools.ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = "ok" if web_fetch else "missing"`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, `"result": "ok"`) {
		t.Fatalf("expected WebFetch helper to be available, got:\n%s", res.Content)
	}

	scoped = r.registryForSession(plan, todos, []string{"Programmatic", "Bash"})
	res, err = scoped.Execute(context.Background(), "Programmatic", tools.ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = "ok" if opencli else "missing"`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, `"result": "ok"`) {
		t.Fatalf("expected OpenCLI helper to be available when Bash is allowed, got:\n%s", res.Content)
	}
}
