package daemon

import (
	"context"
	"strings"
	"testing"

	"yansu-agent/internal/config"
)

type routerTestAdapter struct {
	replies []string
}

func (a *routerTestAdapter) Listen(_ context.Context, _ func(Event)) error { return nil }
func (a *routerTestAdapter) Reply(_ Event, text string)                    { a.replies = append(a.replies, text) }
func (a *routerTestAdapter) PostAndStream(_ Event, _ <-chan string)        {}

func newRouterForTest(t *testing.T, entries map[string]string, adapter *routerTestAdapter) (*Router, *SessionManager) {
	t.Helper()

	registry := &ProjectRegistry{
		entries: entries,
		path:    t.TempDir() + "/projects.json",
	}
	sessions := NewSessionManager()
	t.Cleanup(sessions.Close)

	r := NewRouter(
		&config.Config{
			BaseURL: "http://127.0.0.1:1",
			Token:   "test-token",
		},
		&config.DaemonConfig{},
		registry,
		sessions,
		adapter,
	)
	return r, sessions
}

func createProjectConfig(t *testing.T, root string) {
	t.Helper()
	err := config.SaveProject(config.GetSomethingDir(root), &config.ProjectConfig{
		Name:         "main",
		Organization: "acme",
		Product:      "web",
		ProductUUID:  "prod-uuid",
	})
	if err != nil {
		t.Fatalf("save project config: %v", err)
	}
}

func TestHandleSOPCommandDispatch_FeedbackPendingNoCollision(t *testing.T) {
	adapter := &routerTestAdapter{}
	r, sessions := newRouterForTest(t, map[string]string{
		"acme/web/main": t.TempDir(),
		"acme/api/main": t.TempDir(),
	}, adapter)

	event := Event{UserID: "U1", Platform: "slack", Text: "feedback-pending"}
	r.Handle(context.Background(), event)

	if len(adapter.replies) == 0 || !strings.Contains(adapter.replies[0], "Which project?") {
		t.Fatalf("expected project selection prompt, got replies: %#v", adapter.replies)
	}

	pending := sessions.TakePending("U1:slack")
	if pending == nil {
		t.Fatal("expected pending state to be set")
	}
	if pending.Command != "feedback-pending" {
		t.Fatalf("pending command = %q, want %q", pending.Command, "feedback-pending")
	}
}

func TestHandleSOPCommandArgs_UsageAndValidation(t *testing.T) {
	adapter := &routerTestAdapter{}
	r, _ := newRouterForTest(t, map[string]string{}, adapter)

	tests := []struct {
		input string
		want  string
	}{
		{input: "trigger", want: "Usage: `trigger <stage> [project]`"},
		{input: "feedback-reply", want: "Usage: `feedback-reply <uuid> <text>`"},
		{input: "restore prd not-a-number", want: "❌ Version must be a number."},
	}

	for _, tt := range tests {
		adapter.replies = nil
		r.Handle(context.Background(), Event{UserID: "U1", Platform: "slack", Text: tt.input})
		if len(adapter.replies) == 0 {
			t.Fatalf("expected at least one reply for %q", tt.input)
		}
		if adapter.replies[0] != tt.want {
			t.Fatalf("reply for %q = %q, want %q", tt.input, adapter.replies[0], tt.want)
		}
	}
}

// TestSerialCommandsMatchHandleSwitch verifies that serialCommands stays in sync
// with the switch cases in Handle(). Every serial command must be in the map,
// and no AI-delegating command (analyze) should sneak in.
func TestSerialCommandsMatchHandleSwitch(t *testing.T) {
	// These are the exact non-default cases in Handle() that are NOT AI tasks.
	wantSerial := []string{
		"sync", "push", "pull",
		"status", "register", "help",
		"stages", "stage", "trigger",
		"feedback", "feedback-show", "feedback-pending",
		"feedback-reply", "feedback-retry", "feedback-resolve",
		"tasks", "versions", "restore",
	}
	// "analyze" delegates to runAITask — must NOT be in serialCommands.
	aiCommands := map[string]bool{"analyze": true}

	for _, cmd := range wantSerial {
		if !serialCommands[cmd] {
			t.Errorf("command %q is handled serially in Handle() but missing from serialCommands", cmd)
		}
	}
	for cmd := range serialCommands {
		if aiCommands[cmd] {
			t.Errorf("command %q is an AI command but incorrectly listed in serialCommands", cmd)
		}
	}
}

// TestHandleAI_DoesNotConsumePendingSetBySerialCommand verifies that handleAI
// skips TakePending, so pending state written by a serial command is not silently
// consumed when an AI goroutine runs concurrently.
//
// Regression for: go r.Handle(ctx, event) in daemon.go calling TakePending and
// routing the free-text AI message as a project-selection reply for the pending
// serial command, silently consuming the pending state.
func TestHandleAI_DoesNotConsumePendingSetBySerialCommand(t *testing.T) {
	projectRoot := t.TempDir()
	createProjectConfig(t, projectRoot)

	adapter := &routerTestAdapter{}
	// Single project so resolveProject returns immediately without touching SetPending.
	r, sessions := newRouterForTest(t, map[string]string{
		"acme/web/main": projectRoot,
	}, adapter)

	userKey := "U1:slack"
	// Simulate a serial command (e.g. "sync") having set pending state after the
	// AI goroutine was spawned but before it actually started executing.
	sessions.SetPending(userKey, &PendingState{Command: "sync", Args: nil})

	// handleAI must NOT call TakePending — so the "sync" pending state must survive.
	event := Event{UserID: "U1", Platform: "slack", Text: "what is the meaning of life?"}
	r.handleAI(context.Background(), event)

	// Pending state must still be intact.
	pending := sessions.TakePending(userKey)
	if pending == nil {
		t.Fatal("handleAI consumed pending state — the serial command's pending state was lost")
	}
	if pending.Command != "sync" {
		t.Fatalf("pending.Command = %q, want %q", pending.Command, "sync")
	}
}

func TestProjectSelectionCallback_RunsPendingStagesCommand(t *testing.T) {
	projectRoot := t.TempDir()
	createProjectConfig(t, projectRoot)

	adapter := &routerTestAdapter{}
	r, _ := newRouterForTest(t, map[string]string{
		"acme/web/main": projectRoot,
		"acme/api/main": t.TempDir(),
	}, adapter)

	// First message triggers selection flow.
	r.Handle(context.Background(), Event{UserID: "U1", Platform: "slack", Text: "stages"})
	if len(adapter.replies) == 0 || !strings.Contains(adapter.replies[0], "Which project?") {
		t.Fatalf("expected selection prompt, got replies: %#v", adapter.replies)
	}

	// Second message resolves pending selection and executes runStages.
	r.Handle(context.Background(), Event{UserID: "U1", Platform: "slack", Text: "acme/web/main"})

	if len(adapter.replies) < 2 {
		t.Fatalf("expected second reply from pending callback, got replies: %#v", adapter.replies)
	}
	if !strings.Contains(adapter.replies[1], "failed to send request") && !strings.HasPrefix(adapter.replies[1], "❌") {
		t.Fatalf("expected command execution error reply after callback, got %q", adapter.replies[1])
	}
}
