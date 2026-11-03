package toolserver

import "yansu-agent/agent/native"

func emitUse(cfg *native.LoopConfig, name, id string, input map[string]any) {
	cfg.Emit(native.LoopEvent{Type: "tool_use", ToolName: name, ToolUseID: id, ToolInput: input})
}
