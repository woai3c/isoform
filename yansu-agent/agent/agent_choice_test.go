package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

func flagValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// The desktop's choice for a task reaches a Claude Code run as per-run flags;
// the agent keeps its own sign-in and settings files.
func TestAgentChoiceReachesClaudeCodeArgs(t *testing.T) {
	cfg := ExternalAgentConfig{Choice: func(input *TaskInput) AgentChoice {
		if input.Priority == PriorityBackground {
			return AgentChoice{Model: "claude-sonnet-5-5", Effort: "medium"}
		}
		return AgentChoice{Model: "claude-opus-5-5", Effort: "high"}
	}}
	for _, tc := range []struct {
		name, taskModel, priority, wantModel, wantEffort string
	}{
		{"chat with no model", "", "", "claude-opus-5-5", "high"},
		{"chat tier alias", "sonnet", "", "claude-opus-5-5", "high"},
		{"background work", "haiku", PriorityBackground, "claude-sonnet-5-5", "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := controlledClaudeArgs(&TaskInput{Model: tc.taskModel, Priority: tc.priority}, cfg, "/private/config.json", nil)
			model, _ := flagValue(args, "--model")
			effort, _ := flagValue(args, "--effort")
			if model != tc.wantModel || effort != tc.wantEffort {
				t.Fatalf("--model %q --effort %q; want %q %q", model, effort, tc.wantModel, tc.wantEffort)
			}
		})
	}
	cfg.Choice = func(*TaskInput) AgentChoice { return AgentChoice{} }
	args := controlledClaudeArgs(&TaskInput{Model: "sonnet"}, cfg, "/private/config.json", nil)
	if model, _ := flagValue(args, "--model"); model != "sonnet" || slices.Contains(args, "--effort") {
		t.Fatalf("an empty choice must keep the task model and send no effort: %v", args)
	}
	cfg.Choice = func(*TaskInput) AgentChoice { return AgentChoice{Effort: "max"} }
	args = controlledClaudeArgs(&TaskInput{Model: "sonnet"}, cfg, "/private/config.json", nil)
	if model, _ := flagValue(args, "--model"); model != "sonnet" {
		t.Fatalf("effort alone changed the model: %v", args)
	}
	if effort, _ := flagValue(args, "--effort"); effort != "max" {
		t.Fatalf("effort alone not sent: %v", args)
	}
}

// Claude Code older than the --effort flag cannot carry an effort choice.
func TestAgentChoiceNeedsTheEffortFlag(t *testing.T) {
	help := strings.Join(controlledClaudeFlags, " ")
	bin := writeFakeCLI(t, "claude", `case "$1" in
--version) echo '2.1.296 (Claude Code)' ;;
--help) echo '`+help+`' ;;
esac`)
	if err := ExternalControlProfile(context.Background(), ExternalAgentConfig{Command: bin}); err != nil {
		t.Fatalf("without a choice: %v", err)
	}
	err := ExternalControlProfile(context.Background(), ExternalAgentConfig{Command: bin, Choice: func(*TaskInput) AgentChoice { return AgentChoice{} }})
	if err == nil || !strings.Contains(err.Error(), "--effort") {
		t.Fatalf("a choice and no --effort flag: %v", err)
	}
}

func TestChooseRouteNativeOnly(t *testing.T) {
	ready := AssistantState{SignedIn: true, Available: true, ControlProfile: true}
	policy := RoutingPolicy{ManagedOrder: []string{"claude-code", "codex"}, NativeOnly: true,
		Assistants: map[string]AssistantState{"claude-code": ready, "codex": ready}}
	available := map[string]bool{"claude-code": true, "codex": true}
	route := chooseRoute(&TaskInput{BackendHint: BackendHintChat}, policy, available, authority.ExecutionModeNormal)
	if len(route) != 1 || route[0].Backend != BackendNative || route[0].Reason != "the user chose Isoform to answer" {
		t.Fatalf("route=%+v", route)
	}
	forced := chooseRoute(&TaskInput{ForceBackend: "claude-code"}, policy, available, authority.ExecutionModeNormal)
	if fmt.Sprint(forced[0].Backend) != "claude-code" {
		t.Fatalf("an explicit per-run backend still wins: %+v", forced)
	}
}
