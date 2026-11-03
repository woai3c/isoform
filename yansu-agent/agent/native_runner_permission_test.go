package agent

import (
	"testing"

	"yansu-agent/agent/native"
)

func TestNativePermissionEventPreservesToolUseIdentity(t *testing.T) {
	runner := &NativeAgentRunner{}
	event := runner.loopEventToStream(native.LoopEvent{Type: "permission_request", ToolName: "Write", ToolUseID: "save-report", Content: "May I save?"}, "run", "session")
	if event.ToolUseID != "save-report" || event.SessionID != "session" || event.ToolName != "Write" {
		t.Fatalf("permission event = %+v", event)
	}
}
