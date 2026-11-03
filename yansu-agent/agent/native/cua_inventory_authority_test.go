package native

import (
	"context"
	"testing"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

// fakeCuaTool answers list_apps with window titles and counts its calls.
type fakeCuaTool struct{ listCalls int }

func (*fakeCuaTool) Name() string               { return "cua_driver" }
func (*fakeCuaTool) Description() string        { return "fake" }
func (*fakeCuaTool) JSONSchema() map[string]any { return map[string]any{"type": "object"} }
func (*fakeCuaTool) NeedsPermission(map[string]any) (bool, tools.PermissionPrompt) {
	return false, tools.PermissionPrompt{}
}
func (f *fakeCuaTool) Execute(_ context.Context, call tools.ToolCall) (tools.ToolResult, error) {
	if call.Input["name"] == "list_apps" {
		f.listCalls++
		return tools.ToolResult{Content: "Mail: Re: salary review"}, nil
	}
	return tools.ToolResult{Content: "ok"}, nil
}
func (*fakeCuaTool) DescribeAuthority(input map[string]any) authority.Action {
	name, _ := input["name"].(string)
	return authority.Action{Capability: "computer." + name, ToolName: "cua_driver", Operation: name, Effect: authority.EffectRead}
}

func TestInventoryOnlyThroughTheRunsAuthority(t *testing.T) {
	cua := &fakeCuaTool{}
	registry := tools.NewRegistry()
	registry.Register(cua)
	unattended := &LoopConfig{Registry: registry, AutoApproveTools: true, AuthorityEvaluator: authority.NewPolicy(nil)}
	if got := fetchCuaInventory(context.Background(), unattended); got != "" || cua.listCalls != 0 {
		t.Fatalf("a run that may not list apps got %q (%d calls)", got, cua.listCalls)
	}
	granted := &LoopConfig{Registry: registry, AutoApproveTools: true,
		AuthorityEvaluator: authority.NewPolicy([]authority.Grant{{ID: "g", Capability: "computer.list_apps"}})}
	if got := fetchCuaInventory(context.Background(), granted); got != "Mail: Re: salary review" {
		t.Fatalf("a granted run got %q", got)
	}
	attended := &LoopConfig{Registry: registry}
	if got := fetchCuaInventory(context.Background(), attended); got == "" {
		t.Fatal("an attended run keeps its inventory")
	}
}
