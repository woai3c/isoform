package tools

import (
	"context"
	"fmt"
)

// BashOutputTool polls a background shell's stdout/stderr.
type BashOutputTool struct {
	bash *BashTool
}

// NewBashOutputTool pairs with a BashTool so shell_ids line up.
func NewBashOutputTool(bash *BashTool) *BashOutputTool {
	return &BashOutputTool{bash: bash}
}

func (*BashOutputTool) Name() string { return "BashOutput" }

func (*BashOutputTool) Description() string {
	return "Return the incremental stdout+stderr of a background shell started via Bash with run_in_background=true. " +
		"Returns the bytes produced since the last call."
}

func (*BashOutputTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"shell_id"},
		"properties": map[string]any{
			"shell_id": map[string]any{"type": "string"},
		},
	}
}

func (*BashOutputTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *BashOutputTool) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	id, _ := call.Input["shell_id"].(string)
	if id == "" {
		return ToolResult{}, fmt.Errorf("shell_id is required")
	}
	status, err := t.bash.Output(id)
	if err != nil {
		return ToolResult{}, err
	}
	content := status.Output
	if status.Truncated {
		content = "[earlier output dropped]\n" + content
	}
	content += fmt.Sprintf("\n(status=%s, exit=%d)", status.Status, status.Exit)
	return ToolResult{
		Content: content,
		Metadata: map[string]any{
			"status":    status.Status,
			"exit":      status.Exit,
			"truncated": status.Truncated,
		},
	}, nil
}

// KillShellTool terminates a background shell started via Bash.
type KillShellTool struct {
	bash *BashTool
}

// NewKillShellTool constructs a KillShell tool bound to the given Bash.
func NewKillShellTool(bash *BashTool) *KillShellTool {
	return &KillShellTool{bash: bash}
}

func (*KillShellTool) Name() string { return "KillShell" }

func (*KillShellTool) Description() string {
	return "Kill a background shell started via Bash (run_in_background=true). " +
		"Sends SIGTERM, then SIGKILL after a short grace period."
}

func (*KillShellTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"shell_id"},
		"properties": map[string]any{
			"shell_id": map[string]any{"type": "string"},
		},
	}
}

func (*KillShellTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *KillShellTool) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	id, _ := call.Input["shell_id"].(string)
	if id == "" {
		return ToolResult{}, fmt.Errorf("shell_id is required")
	}
	if err := t.bash.Kill(id); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: fmt.Sprintf("Killed shell_id=%s", id)}, nil
}
