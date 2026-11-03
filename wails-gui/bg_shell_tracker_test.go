package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

type bgShellResumeCall struct {
	info   bgShellSessionInfo
	prompt string
}

// newBgShellTestApp is an App with a store holding chat "chat-1", a tracker
// and a bridge client that sees every event. Resumes are recorded instead of
// run.
func newBgShellTestApp(t *testing.T) (*App, chan []byte, *[]bgShellResumeCall) {
	t.Helper()
	return newBgShellTestAppAt(t, filepath.Join(t.TempDir(), "state.db"))
}

// newBgShellTestAppAt is newBgShellTestApp with its database at dbPath.
func newBgShellTestAppAt(t *testing.T, dbPath string) (*App, chan []byte, *[]bgShellResumeCall) {
	t.Helper()
	st, err := storage.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.SaveProject(ctx, storage.ProjectRow{ID: "p1", Name: "P", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSession(ctx, storage.SessionRow{LocalID: "chat-1", ProjectID: "p1", Label: "Chat", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	app := &App{
		ctx:              ctx,
		store:            st,
		bgShells:         newBgShellTracker(),
		activeTags:       map[string]bool{},
		eventBroadcaster: NewEventBroadcaster(),
	}
	client := app.eventBroadcaster.AddClient("bg-shell-test")
	t.Cleanup(func() { app.eventBroadcaster.RemoveClient("bg-shell-test") })

	var calls []bgShellResumeCall
	prev := bgShellResumeRun
	bgShellResumeRun = func(_ *App, info bgShellSessionInfo, prompt string) error {
		calls = append(calls, bgShellResumeCall{info: info, prompt: prompt})
		return nil
	}
	t.Cleanup(func() { bgShellResumeRun = prev })
	return app, client, &calls
}

func bgShellEventNames(client chan []byte) []string {
	var names []string
	for {
		select {
		case payload := <-client:
			var msg BrowserEvent
			if json.Unmarshal(payload, &msg) == nil {
				names = append(names, msg.Event)
			}
		case <-time.After(50 * time.Millisecond):
			return names
		}
	}
}

func finishBgShell(app *App, shellID string, exit int) {
	app.bgShells.Register(shellID, bgShellSessionInfo{Tag: "chat-1", SessionID: "s", WorkDir: "/tmp/w", Model: "sonnet", Command: "make test"})
	app.onBgShellDone(shellID, exit, "completed", "make test")
}

// Regression: the renderer used to store the finished line and send the
// follow-up prompt on bg:shell:auto-resume, once per open frontend (the app
// window and a browser preview), so one finished script started two turns.
// The backend now does both, once, and only tells the windows to show the row.
func TestBgShellDoneResumesOnceFromTheBackend(t *testing.T) {
	app, client, calls := newBgShellTestApp(t)

	finishBgShell(app, "bg_1", 0)

	if len(*calls) != 1 {
		t.Fatalf("resume ran %d times, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.info.Tag != "chat-1" || got.info.WorkDir != "/tmp/w" || got.info.Model != "sonnet" || got.info.SessionID != "s" {
		t.Fatalf("resume ran with %+v", got.info)
	}
	if !strings.Contains(got.prompt, "bg_1") || !strings.Contains(got.prompt, "exit=0") {
		t.Fatalf("resume prompt %q", got.prompt)
	}

	rows, err := app.store.LoadStreamItems(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Kind != "system" || rows[1].Kind != "user" || rows[1].Content != "Background script finished." {
		t.Fatalf("rows = %+v", rows)
	}

	names := bgShellEventNames(client)
	appended := 0
	for _, name := range names {
		if name == "bg:shell:auto-resume" {
			t.Fatalf("the renderer was still asked to resume: %v", names)
		}
		if name == mainChatItemsAppendedEvent {
			appended++
		}
	}
	if appended != 1 {
		t.Fatalf("events %v, want one %s", names, mainChatItemsAppendedEvent)
	}
}

// Every finish leaves its own rows: the system line used to carry an empty id,
// so only the first one ever stored survived.
func TestBgShellDoneStoresEveryFinish(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	if err := app.store.SetUIState(context.Background(), uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatal(err)
	}

	finishBgShell(app, "bg_1", 0)
	finishBgShell(app, "bg_2", 2)

	if len(*calls) != 2 {
		t.Fatalf("resume ran %d times, want 2", len(*calls))
	}
	rows, err := app.store.LoadStreamItems(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	systems := 0
	for _, row := range rows {
		switch row.Kind {
		case "user":
			users = append(users, row.Content)
		case "system":
			systems++
		}
	}
	if systems != 2 {
		t.Fatalf("stored %d system lines, want 2: %+v", systems, rows)
	}
	want := []string{"后台脚本已完成。", "后台脚本已完成，但出现错误（exit=2）。"}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user lines = %q, want %q", users, want)
	}
}

func bgShellRows(t *testing.T, app *App) (users []string, systems int) {
	t.Helper()
	rows, err := app.store.LoadStreamItems(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		switch row.Kind {
		case "user":
			users = append(users, row.Content)
		case "system":
			systems++
		}
	}
	return users, systems
}

// A script that finishes during a run waits for the run: the system line is
// stored at once, the follow-up line and turn come when the run ends, and
// scripts that finished meanwhile share one turn.
func TestBgShellDoneDuringARunResumesWhenTheRunEnds(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	app.activeTags["chat-1"] = true

	finishBgShell(app, "bg_1", 0)
	finishBgShell(app, "bg_2", 1)

	if len(*calls) != 0 {
		t.Fatalf("resume ran during a run: %+v", *calls)
	}
	if users, systems := bgShellRows(t, app); len(users) != 0 || systems != 2 {
		t.Fatalf("during the run: users=%q systems=%d", users, systems)
	}

	delete(app.activeTags, "chat-1")
	app.afterRunBgShells("chat-1")

	if len(*calls) != 1 {
		t.Fatalf("resume ran %d times after the run, want 1", len(*calls))
	}
	if p := (*calls)[0].prompt; !strings.Contains(p, "bg_1 (exit=0") || !strings.Contains(p, "bg_2 (exit=1") {
		t.Fatalf("resume prompt %q", p)
	}
	if users, _ := bgShellRows(t, app); len(users) != 2 {
		t.Fatalf("user lines after the run: %q", users)
	}
	if app.activeTags["chat-1"] {
		t.Fatal("the resume left its claim on the chat")
	}
}

// A script whose chat was deleted while it ran neither records nor runs
// anything in the chat's folder.
func TestBgShellDoneAfterTheChatIsDeletedDoesNothing(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	app.bgShells.Register("bg_kept", bgShellSessionInfo{Tag: "chat-1", WorkDir: "/tmp/w"})
	app.bgShells.Register("bg_gone", bgShellSessionInfo{Tag: "chat-gone", WorkDir: "/tmp/w"})

	if err := app.DeleteSessionData("chat-1"); err != nil {
		t.Fatal(err)
	}
	if len(app.bgShells.ActiveForSession("chat-1")) != 0 {
		t.Fatal("deleting the chat kept its background scripts")
	}
	app.onBgShellDone("bg_kept", 0, "completed", "make test")
	app.onBgShellDone("bg_gone", 0, "completed", "make test")

	if len(*calls) != 0 {
		t.Fatalf("a deleted chat resumed: %+v", *calls)
	}
}

// A message that gets in between the stored line and the follow-up turn keeps
// the finish queued; the message's run resumes it without a second line. If
// that run already ended, the finish resumes at once instead of waiting for
// a run that will not come.
func TestBgShellResumeRefusedWaitsForTheRun(t *testing.T) {
	for _, runStillGoing := range []bool{true, false} {
		app, _, _ := newBgShellTestApp(t)
		resumed := make(chan string, 2)
		refuse := true
		bgShellResumeRun = func(a *App, _ bgShellSessionInfo, prompt string) error {
			if refuse {
				refuse = false
				if runStillGoing {
					a.mu.Lock()
					a.activeTags["chat-1"] = true
					a.mu.Unlock()
				}
				return errPromptAlreadyRunning
			}
			resumed <- prompt
			return nil
		}

		finishBgShell(app, "bg_1", 0)
		if runStillGoing {
			select {
			case p := <-resumed:
				t.Fatalf("resumed during the run: %q", p)
			case <-time.After(100 * time.Millisecond):
			}
			app.mu.Lock()
			delete(app.activeTags, "chat-1")
			app.mu.Unlock()
			app.afterRunBgShells("chat-1")
		}
		select {
		case <-resumed:
		case <-time.After(2 * time.Second):
			t.Fatalf("runStillGoing=%v: the refused finish never resumed", runStillGoing)
		}
		if users, _ := bgShellRows(t, app); len(users) != 1 {
			t.Fatalf("runStillGoing=%v: user lines %q", runStillGoing, users)
		}
	}
}

// Once a chat is deleted no turn starts there, whatever asks for one.
func TestExecutePromptRefusesADeletedChat(t *testing.T) {
	app, _, _ := newBgShellTestApp(t)
	if err := app.DeleteSessionData("chat-1"); err != nil {
		t.Fatal(err)
	}
	err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac)
	if !errors.Is(err, errChatDeleted) {
		t.Fatalf("err = %v, want errChatDeleted", err)
	}
}

// No follow-up turn without its line in the chat.
func TestBgShellResumeNeedsItsLineStored(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	app.activeTags["chat-1"] = true
	finishBgShell(app, "bg_1", 0)
	delete(app.activeTags, "chat-1")
	app.store.Close()

	app.afterRunBgShells("chat-1")

	if len(*calls) != 0 {
		t.Fatalf("resume ran without its line: %+v", *calls)
	}
	if len(app.bgShells.TakePending("chat-1")) != 1 {
		t.Fatal("the finish was not kept for the next try")
	}
}

// bgShellBlockingRunner holds each run until the test releases it or the run
// is cancelled.
type bgShellBlockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *bgShellBlockingRunner) Init(context.Context) error { return nil }
func (r *bgShellBlockingRunner) IsReady() bool              { return true }
func (r *bgShellBlockingRunner) ExecuteTask(ctx context.Context, _ *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return "bg-run", nil
}
func (r *bgShellBlockingRunner) CancelRun(string) error                   { return nil }
func (r *bgShellBlockingRunner) RespondToPermission(string, string) error { return nil }
func (r *bgShellBlockingRunner) CancelPermission(string)                  {}
func (r *bgShellBlockingRunner) Shutdown() error                          { return nil }

// The run's end decides: a run that finishes resumes the scripts that
// finished during it, a stopped run does not.
func TestBgShellDoneDuringARunFollowsTheRunsEnd(t *testing.T) {
	for _, stop := range []bool{false, true} {
		app, _, _ := newBgShellTestApp(t)
		resumed := make(chan string, 2)
		bgShellResumeRun = func(_ *App, info bgShellSessionInfo, prompt string) error {
			resumed <- prompt
			return nil
		}
		runner := &bgShellBlockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
		app.runner = runner
		app.tagCancels = map[string]context.CancelFunc{}
		app.sessionTempDirs = map[string]string{}
		app.streams = map[string]context.CancelFunc{}
		app.runIDByTag = map[string]string{}
		app.pendingTraceIDs = map[string]string{}

		if err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac); err != nil {
			t.Fatal(err)
		}
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the run did not start")
		}
		finishBgShell(app, "bg_1", 0)
		if stop {
			app.CancelByTag("chat-1")
		} else {
			close(runner.release)
		}
		waitForTagIdle(t, app, "chat-1")

		select {
		case prompt := <-resumed:
			if stop {
				t.Fatalf("a stopped run resumed: %q", prompt)
			}
		case <-time.After(500 * time.Millisecond):
			if !stop {
				t.Fatal("the finished run did not resume the script")
			}
		}
	}
}

// A delete that fails leaves a chat that still works: it is not refused.
func TestFailedChatDeleteDoesNotRefuseTheChat(t *testing.T) {
	app, _, _ := newBgShellTestApp(t)
	app.store.Close()
	if err := app.DeleteSessionData("chat-1"); err == nil {
		t.Fatal("delete on a closed store succeeded")
	}
	app.mu.Lock()
	refused := app.chatDeleteGuardedLocked("chat-1")
	app.mu.Unlock()
	if refused {
		t.Fatal("a chat whose delete failed refuses turns")
	}
}

// A chat takes no turn from the moment its delete starts: a send from another
// window, or a script finishing during the cleanup, cannot store rows after
// the delete. The delete is held at its first write by another connection's
// write lock.
func TestChatDeleteRefusesTurnsWhileItRuns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	app, _, _ := newBgShellTestAppAt(t, dbPath)

	lockDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lockDB.Close()
	ctx := context.Background()
	lock, err := lockDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}

	deleted := make(chan error, 1)
	go func() { deleted <- app.DeleteSessionData("chat-1") }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		app.mu.Lock()
		marked := app.chatDeleteGuardedLocked("chat-1")
		app.mu.Unlock()
		if marked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the delete never marked the chat")
		}
		time.Sleep(5 * time.Millisecond)
	}
	err = app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac)
	if !errors.Is(err, errChatDeleted) {
		t.Fatalf("turn during the delete: err = %v, want errChatDeleted", err)
	}

	if _, err := lock.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the delete never finished")
	}
}

// Stop drops the scripts that finished during the run: the system line stays
// as the record, and no line or turn follows. Driven through executePrompt,
// whose run ends with a cancelled context.
func TestBgShellDoneDuringAStoppedRunDoesNotResume(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	runner := &bgShellBlockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	app.runner = runner
	app.tagCancels = map[string]context.CancelFunc{}
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}

	if err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not start")
	}
	finishBgShell(app, "bg_1", 0)
	app.CancelByTag("chat-1")
	waitForTagIdle(t, app, "chat-1")
	time.Sleep(200 * time.Millisecond)

	if len(*calls) != 0 {
		t.Fatalf("a stopped run resumed: %+v", *calls)
	}
	if users, systems := bgShellRows(t, app); len(users) != 0 || systems != 1 {
		t.Fatalf("users=%q systems=%d", users, systems)
	}
	if pending := app.bgShells.TakePending("chat-1"); len(pending) != 0 {
		t.Fatalf("the stopped run kept %d finished script(s)", len(pending))
	}
}

// bgShellBashRunner runs one Bash call, in the background or not, whose result
// mentions a shell id.
type bgShellBashRunner struct {
	background bool
}

func (r *bgShellBashRunner) Init(context.Context) error { return nil }
func (r *bgShellBashRunner) IsReady() bool              { return true }
func (r *bgShellBashRunner) ExecuteTask(_ context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	input := map[string]any{"command": "echo shell_id=fake"}
	if r.background {
		input["run_in_background"] = true
	}
	_ = emit(&agent.StreamEvent{Type: "tool_use", ToolName: "Bash", ToolUseID: "t1", ToolInput: input})
	_ = emit(&agent.StreamEvent{Type: "tool_result", ToolName: "Bash", ToolUseID: "t1", Content: "started shell_id=bg_9"})
	return "bash-run", nil
}
func (r *bgShellBashRunner) CancelRun(string) error                   { return nil }
func (r *bgShellBashRunner) RespondToPermission(string, string) error { return nil }
func (r *bgShellBashRunner) CancelPermission(string)                  {}
func (r *bgShellBashRunner) Shutdown() error                          { return nil }

// Only a run_in_background call is tracked as a background script; a
// foreground command whose output says "shell_id=" is not.
func TestOnlyBackgroundBashCallsAreTracked(t *testing.T) {
	for _, background := range []bool{false, true} {
		app, _, _ := newBgShellTestApp(t)
		app.runner = &bgShellBashRunner{background: background}
		app.tagCancels = map[string]context.CancelFunc{}
		app.sessionTempDirs = map[string]string{}
		app.streams = map[string]context.CancelFunc{}
		app.runIDByTag = map[string]string{}
		app.pendingTraceIDs = map[string]string{}

		if err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac); err != nil {
			t.Fatal(err)
		}
		waitForTagIdle(t, app, "chat-1")
		tracked := len(app.bgShells.ActiveForSession("chat-1"))
		if background && tracked != 1 || !background && tracked != 0 {
			t.Fatalf("background=%v: %d shells tracked", background, tracked)
		}
	}
}
