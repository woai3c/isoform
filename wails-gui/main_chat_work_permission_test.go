package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"
	"yansu-agent/agent/native"
)

func TestWorkNotificationOpensMainChat(t *testing.T) {
	for _, kind := range []engagement.Kind{engagement.KindWorkFinished, engagement.KindWorkBlocked, engagement.KindDecisionDue} {
		if got := engagementClickData(engagement.Moment{ID: string(kind), Kind: kind, SubjectID: "work"}); got != "main-chat-item:"+string(kind) {
			t.Fatalf("%s opens %q", kind, got)
		}
	}
}

// Exercise the actual permission rendezvous without starting a model or tool.
type workPermissionTestRunner struct {
	agent.AgentRunner
	permissions *native.PermissionManager
	calls       atomic.Int32
	db          *sql.DB
}

func (r *workPermissionTestRunner) RespondToPermission(sessionID, optionID string) error {
	r.calls.Add(1)
	deadline := time.Now().Add(time.Second)
	for {
		err := r.permissions.Respond(sessionID, optionID)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		// Await registers on another goroutine; retry only that startup race.
		time.Sleep(time.Millisecond)
	}
}

func newMainChatPermissionTestApp(t *testing.T) (*App, *workPermissionTestRunner) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.db")
	chatStore, err := storage.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chatStore.Close() })
	app := &App{store: chatStore, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	if _, err := store.Save(handoff.Entry{ID: "permission-work", Title: "Prepare a report", Status: handoff.StatusInProgress, ChatSessionID: "work-chat"}); err != nil {
		t.Fatal(err)
	}
	runner := &workPermissionTestRunner{permissions: native.NewPermissionManager()}
	runner.db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.db.Close() })
	app.runner = runner
	return app, runner
}

func awaitWorkPermission(t *testing.T, runner *workPermissionTestRunner, sessionID string) <-chan native.PermissionResult {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan native.PermissionResult, 1)
	go func() {
		response, _ := runner.permissions.Await(ctx, sessionID, 5*time.Second)
		result <- response
	}()
	return result
}

func workPermissionEvent(toolID string) AgentEvent {
	return AgentEvent{Type: "permission_request", Tag: "work-chat", SessionID: "work-agent", ToolUseID: toolID,
		Content: "Save the report to the project folder?", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"/tmp/report.md"}`),
		PermissionOptions: []agent.PermissionOption{{ID: "allow_once", Name: "Allow once"}, {ID: "allow_always", Name: "Always allow"}, {ID: "reject", Name: "Reject"}}}
}

func TestMainChatWorkPermissionRoundTripAndReplay(t *testing.T) {
	for _, action := range []string{"approve", "decline"} {
		t.Run(action, func(t *testing.T) {
			app, runner := newMainChatPermissionTestApp(t)
			response := awaitWorkPermission(t, runner, "work-agent")
			var resolved atomic.Int32
			app.onAgentEvent(func(evt AgentEvent) {
				if evt.Type == "permission_resolved" && evt.Tag == "work-chat" {
					resolved.Add(1)
				}
			})
			evt := workPermissionEvent("save-report")
			app.emitAgentEvent(evt)
			app.emitAgentEvent(evt)
			cards := mainChatProactiveItems(t, app)
			if len(cards) != 1 || cards[0].Kind != "decision_due" || cards[0].Permission.ApproveOptionID != "allow_once" {
				t.Fatalf("permission cards: %+v", cards)
			}
			card := cards[0]
			for i := 0; i < 2; i++ {
				state, err := app.ResolveMainChatProactiveAction(card.MomentID, action)
				if err != nil || state != "acted" {
					t.Fatalf("resolve = %q, %v", state, err)
				}
			}
			select {
			case result := <-response:
				if result.Granted != (action == "approve") || result.Always || result.SessionAlways {
					t.Fatalf("response = %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("runner did not receive the answer")
			}
			if runner.calls.Load() != 1 || resolved.Load() != 1 || mainChatProactiveItems(t, app)[0].State != "acted" {
				t.Fatal("replay answered twice or failed to synchronize the original card")
			}
			var reopenedRequests atomic.Int32
			app.onAgentEvent(func(evt AgentEvent) {
				if evt.Type == "permission_request" {
					reopenedRequests.Add(1)
				}
			})
			app.emitAgentEvent(evt)
			if _, err := app.handoffStore.MutateIfStatus("permission-work", []handoff.Status{handoff.StatusInProgress}, func(entry *handoff.Entry) { entry.Status = handoff.StatusCompleted }); err != nil {
				t.Fatal(err)
			}
			app.emitAgentEvent(evt)
			if reopenedRequests.Load() != 0 {
				t.Fatal("a replay reopened the original permission card")
			}
			// Durable card replay after restart never reaches another runner.
			reopened := &App{store: app.store, runner: runner}
			if state, err := reopened.ResolveMainChatProactiveAction(card.MomentID, action); err != nil || state != "acted" || runner.calls.Load() != 1 {
				t.Fatalf("restart replay = %q, %v", state, err)
			}
		})
	}
}

func TestMainChatWorkPermissionOriginalCardResolvesMainChat(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	response := awaitWorkPermission(t, runner, "work-agent")
	app.emitAgentEvent(workPermissionEvent("save-report"))
	if err := app.RespondToPermission("work-agent", "reject"); err != nil {
		t.Fatal(err)
	}
	<-response
	card := mainChatProactiveItems(t, app)[0]
	if card.State != "acted" {
		t.Fatalf("main chat state = %q", card.State)
	}
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "approve"); err != nil || runner.calls.Load() != 1 {
		t.Fatal("main-chat replay changed the original answer")
	}
	question := workPermissionEvent("question")
	question.PermissionOptions = []agent.PermissionOption{{ID: "blue", Name: "Blue"}}
	app.emitAgentEvent(question)
	if mainChatProactiveItems(t, app)[0].State != "acted" {
		t.Fatal("a later free-form question changed an accepted permission's history")
	}
}

func TestMainChatWorkPermissionConcurrentSurfacesAnswerOnce(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	response := awaitWorkPermission(t, runner, "work-agent")
	app.emitAgentEvent(workPermissionEvent("save"))
	card := mainChatProactiveItems(t, app)[0]
	var group sync.WaitGroup
	errors := make(chan error, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		_, err := app.ResolveMainChatProactiveAction(card.MomentID, "approve")
		errors <- err
	}()
	go func() {
		defer group.Done()
		errors <- app.RespondToPermission("work-agent", "reject")
	}()
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	<-response
	if runner.calls.Load() != 1 || mainChatProactiveItems(t, app)[0].State != "acted" {
		t.Fatal("concurrent surfaces answered more than once")
	}
}

func TestMainChatWorkPermissionStaleCardCannotAnswerNextRequest(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	app.emitAgentEvent(workPermissionEvent("first"))
	first := mainChatProactiveItems(t, app)[0]
	app.emitAgentEvent(workPermissionEvent("second"))
	app.emitAgentEvent(workPermissionEvent("first"))
	app.workPermissions.mu.Lock()
	current := app.workPermissions.bySession["work-agent"]
	app.workPermissions.mu.Unlock()
	if current.RequestID != workPermissionRequestID(workPermissionEvent("second")) || current.state != "pending" {
		t.Fatal("a delayed replay replaced the live request")
	}
	if state, err := app.ResolveMainChatProactiveAction(first.MomentID, "approve"); err != nil || state != "expired" || runner.calls.Load() != 0 {
		t.Fatalf("old request = %q, %v", state, err)
	}
	app.emitAgentEvent(AgentEvent{Type: "close", Tag: "work-chat", SessionID: "work-agent"})
	for _, card := range mainChatProactiveItems(t, app) {
		if card.State != "expired" {
			t.Fatalf("closed request = %+v", card)
		}
	}
}

func TestMainChatWorkPermissionPendingRestartAndUnadvertisedActionFailClosed(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	app.emitAgentEvent(workPermissionEvent("first"))
	card := mainChatProactiveItems(t, app)[0]
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "allow_always"); err == nil {
		t.Fatal("accepted an unadvertised main-chat action")
	}
	if err := app.RespondToPermission("work-agent", "invented"); err == nil {
		t.Fatal("accepted an unadvertised runner option")
	}
	app.emitAgentEvent(workPermissionEvent("second"))
	if err := app.RespondToPermission(card.MomentID, "allow_once"); err == nil || runner.calls.Load() != 0 {
		t.Fatal("a stale desktop work token answered a later request")
	}
	reopened := &App{store: app.store, runner: runner}
	pending := mainChatProactiveItems(t, app)[1]
	if _, err := reopened.ResolveMainChatProactiveAction(pending.MomentID, "approve"); err == nil || runner.calls.Load() != 0 {
		t.Fatal("a pending historical card answered a live request")
	}
	main, _ := app.ensureMainChat(context.Background())
	prompt := app.injectMainChatReports(main.LocalID)
	if strings.Contains(prompt, "project folder") || strings.Contains(prompt, "report.md") {
		t.Fatal("permission details leaked into prompt context")
	}
}

func TestMainChatWorkPermissionPersistenceRetryDoesNotAnswerTwice(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	response := awaitWorkPermission(t, runner, "work-agent")
	app.emitAgentEvent(workPermissionEvent("save"))
	card := mainChatProactiveItems(t, app)[0]
	// A real SQLite trigger refuses the presentation update after the runner
	// accepts the answer. The retry repairs the card without another response.
	if _, err := runner.db.Exec(`CREATE TRIGGER refuse_permission_update BEFORE UPDATE OF tool_input ON stream_items BEGIN SELECT RAISE(FAIL, 'retry'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "approve"); err == nil {
		t.Fatal("expected refused presentation update")
	}
	<-response
	app.emitAgentEvent(AgentEvent{Type: "close", Tag: "work-chat", SessionID: "work-agent"})
	if _, err := runner.db.Exec(`DROP TRIGGER refuse_permission_update`); err != nil {
		t.Fatal(err)
	}
	if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "approve"); err != nil || state != "acted" || runner.calls.Load() != 1 {
		t.Fatalf("retry = %q, %v, calls=%d", state, err, runner.calls.Load())
	}
}

func TestMainChatWorkPermissionExpiryWriteRepairsOnRead(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	app.emitAgentEvent(workPermissionEvent("save"))
	if _, err := runner.db.Exec(`CREATE TRIGGER refuse_permission_expiry BEFORE UPDATE OF tool_input ON stream_items BEGIN SELECT RAISE(FAIL, 'retry'); END`); err != nil {
		t.Fatal(err)
	}
	app.emitAgentEvent(AgentEvent{Type: "close", Tag: "work-chat", SessionID: "work-agent"})
	if mainChatProactiveItems(t, app)[0].State != "pending" {
		t.Fatal("trigger did not refuse expiry")
	}
	if _, err := runner.db.Exec(`DROP TRIGGER refuse_permission_expiry`); err != nil {
		t.Fatal(err)
	}
	main, _ := app.ensureMainChat(context.Background())
	if _, err := app.LoadStreamItemsPage(main.LocalID, 0, 50); err != nil {
		t.Fatal(err)
	}
	if mainChatProactiveItems(t, app)[0].State != "expired" || runner.calls.Load() != 0 {
		t.Fatal("reading the main chat did not repair the expired presentation")
	}
}
