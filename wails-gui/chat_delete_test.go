package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

// workDirRemoveGates turns the sessionWorkDirRemoveAll test seam into a
// deterministic pause inside DeleteSessionData's cleanup: every call
// announces itself through entered and then blocks until the test closes the
// gate it handed out.
type workDirRemoveGates struct {
	entered chan chan struct{}
}

func newWorkDirRemoveGates() *workDirRemoveGates {
	return &workDirRemoveGates{entered: make(chan chan struct{}, 4)}
}

func (g *workDirRemoveGates) removeAll(string) error {
	gate := make(chan struct{})
	g.entered <- gate
	<-gate
	return nil
}

// next waits for the next cleanup to reach the directory removal and returns
// the gate that releases it.
func (g *workDirRemoveGates) next(t *testing.T) chan struct{} {
	t.Helper()
	select {
	case gate := <-g.entered:
		return gate
	case <-time.After(5 * time.Second):
		t.Fatal("no delete reached the work dir removal")
		return nil
	}
}

// chatDeleteGuardState reports the delete-guard state of a chat: whether a
// turn is refused and how many deletes are still running.
func chatDeleteGuardState(app *App, tag string) (bool, int) {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.chatDeleteGuardedLocked(tag), app.deletingChats[tag]
}

func waitForDeletingCount(t *testing.T, app *App, tag string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, n := chatDeleteGuardState(app, tag); n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("deletes in flight for %s never reached %d", tag, want)
}

// Two deletes of the same chat race and both fail. The first one to fail must
// not reopen the chat while the second delete is still cleaning up: the guard
// releases only when the last delete ends.
func TestRacingChatDeletesHoldTheGuardUntilTheLastOneFails(t *testing.T) {
	shortGuardTestHome(t)
	app, _, _ := newBgShellTestApp(t)
	gates := newWorkDirRemoveGates()
	app.sessionWorkDirRemoveAll = gates.removeAll
	// With the store closed every delete fails at store.DeleteSession; the
	// cleanup steps before it log and continue.
	app.store.Close()

	results := make(chan error, 2)
	go func() { results <- app.DeleteSessionData("chat-1") }()
	gateA := gates.next(t)
	go func() { results <- app.DeleteSessionData("chat-1") }()
	waitForDeletingCount(t, app, "chat-1", 2)

	// Let the first delete fail. The second is still cleaning up — it holds
	// the chat's work dir lock — so the chat must keep refusing turns.
	close(gateA)
	if err := <-results; err == nil {
		t.Fatal("the first delete succeeded on a closed store")
	}
	if guarded, inFlight := chatDeleteGuardState(app, "chat-1"); !guarded || inFlight != 1 {
		t.Fatalf("after the first delete failed: guarded=%v inFlight=%d, want true, 1", guarded, inFlight)
	}

	// The last delete failing releases the guard: the chat is still there.
	close(gates.next(t))
	if err := <-results; err == nil {
		t.Fatal("the second delete succeeded on a closed store")
	}
	if guarded, inFlight := chatDeleteGuardState(app, "chat-1"); guarded || inFlight != 0 {
		t.Fatalf("after the last delete failed: guarded=%v inFlight=%d, want false, 0", guarded, inFlight)
	}
	app.mu.Lock()
	leaked := len(app.deletingChats)
	app.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("deletingChats leaked %d entries", leaked)
	}
}

// Two deletes of the same chat race and both succeed: the chat stays deleted
// for good, the in-flight count drains, and no turn ever starts there again.
func TestRacingChatDeletesStayDeletedWhenBothSucceed(t *testing.T) {
	shortGuardTestHome(t)
	app, _, _ := newBgShellTestApp(t)
	gates := newWorkDirRemoveGates()
	app.sessionWorkDirRemoveAll = gates.removeAll

	results := make(chan error, 2)
	go func() { results <- app.DeleteSessionData("chat-1") }()
	gateA := gates.next(t)
	go func() { results <- app.DeleteSessionData("chat-1") }()
	waitForDeletingCount(t, app, "chat-1", 2)

	close(gateA)
	if err := <-results; err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if guarded, inFlight := chatDeleteGuardState(app, "chat-1"); !guarded || inFlight != 1 {
		t.Fatalf("between the two deletes: guarded=%v inFlight=%d, want true, 1", guarded, inFlight)
	}

	close(gates.next(t))
	if err := <-results; err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if guarded, inFlight := chatDeleteGuardState(app, "chat-1"); !guarded || inFlight != 0 {
		t.Fatalf("after both deletes: guarded=%v inFlight=%d, want true, 0", guarded, inFlight)
	}
	err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac)
	if !errors.Is(err, errChatDeleted) {
		t.Fatalf("turn after the deletes: err = %v, want errChatDeleted", err)
	}
}

// chatDeleteBlockingRunner holds its run until the run's context is
// cancelled, then stays inside ExecuteTask until the test releases it, so a
// test can watch a delete wait for the run to unwind.
type chatDeleteBlockingRunner struct {
	started        chan struct{}
	cancelled      chan struct{}
	release        chan struct{}
	emitRunCreated bool
}

func (r *chatDeleteBlockingRunner) Init(context.Context) error { return nil }
func (r *chatDeleteBlockingRunner) IsReady() bool              { return true }
func (r *chatDeleteBlockingRunner) ExecuteTask(ctx context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	if r.emitRunCreated {
		_ = emit(&agent.StreamEvent{Type: "run_created", RunID: "run-1"})
	}
	close(r.started)
	<-ctx.Done()
	close(r.cancelled)
	<-r.release
	return "stopped", nil
}
func (r *chatDeleteBlockingRunner) CancelRun(string) error                   { return nil }
func (r *chatDeleteBlockingRunner) RespondToPermission(string, string) error { return nil }
func (r *chatDeleteBlockingRunner) CancelPermission(string)                  {}
func (r *chatDeleteBlockingRunner) Shutdown() error                          { return nil }

// A run that is active when its chat's delete starts is cancelled the way the
// Stop button cancels it — by run id once run_created arrived, by tag before
// that — and the delete waits for the run to unwind before it removes the chat.
func TestChatDeleteCancelsTheActiveRunAndWaitsForIt(t *testing.T) {
	for _, runCreated := range []bool{false, true} {
		t.Run(fmt.Sprintf("runCreated=%v", runCreated), func(t *testing.T) {
			shortGuardTestHome(t)
			app, _, _ := newBgShellTestApp(t)
			runner := &chatDeleteBlockingRunner{
				started:        make(chan struct{}),
				cancelled:      make(chan struct{}),
				release:        make(chan struct{}),
				emitRunCreated: runCreated,
			}
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
			if runCreated {
				deadline := time.Now().Add(5 * time.Second)
				tracked := false
				for time.Now().Before(deadline) {
					app.mu.Lock()
					tracked = app.runIDByTag["chat-1"] == "run-1"
					app.mu.Unlock()
					if tracked {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if !tracked {
					t.Fatal("the run id never reached the tracker")
				}
			}

			deleted := make(chan error, 1)
			go func() { deleted <- app.DeleteSessionData("chat-1") }()

			// The delete cancels the run: nothing else cancelled it.
			select {
			case <-runner.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("the delete did not cancel the chat's run")
			}
			// ... and then waits for the run to unwind instead of deleting around it.
			select {
			case err := <-deleted:
				t.Fatalf("the delete finished while the run was still unwinding: %v", err)
			case <-time.After(100 * time.Millisecond):
			}

			close(runner.release)
			select {
			case err := <-deleted:
				if err != nil {
					t.Fatalf("delete: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the delete never finished")
			}

			app.mu.Lock()
			active := app.activeTags["chat-1"]
			guarded := app.chatDeleteGuardedLocked("chat-1")
			streamsLeft := len(app.streams)
			app.mu.Unlock()
			if active {
				t.Fatal("the delete returned while the run still held the chat")
			}
			if !guarded {
				t.Fatal("a deleted chat takes turns again")
			}
			if streamsLeft != 0 {
				t.Fatalf("the cancelled run left %d stream cancel entries", streamsLeft)
			}
			if _, err := app.store.LoadSession(context.Background(), "chat-1"); err == nil {
				t.Fatal("the chat row survived its delete")
			}
			err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac)
			if !errors.Is(err, errChatDeleted) {
				t.Fatalf("turn after the delete: err = %v, want errChatDeleted", err)
			}
		})
	}
}

// A panic during the cleanup still releases the delete guard: the delete
// reports an error, the panic is logged, and the chat keeps taking turns.
func TestChatDeletePanicInCleanupReleasesTheGuard(t *testing.T) {
	shortGuardTestHome(t)
	app, _, _ := newBgShellTestApp(t)
	app.sessionWorkDirRemoveAll = func(string) error { panic("boom") }

	var logs bytes.Buffer
	prevLog := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevLog) })

	err := app.DeleteSessionData("chat-1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the panic as an error", err)
	}
	if out := logs.String(); !strings.Contains(out, "[delete-session]") || !strings.Contains(out, "boom") {
		t.Fatalf("panic was not logged: %q", out)
	}
	if guarded, inFlight := chatDeleteGuardState(app, "chat-1"); guarded || inFlight != 0 {
		t.Fatalf("after the panic: guarded=%v inFlight=%d, want false, 0", guarded, inFlight)
	}
	if _, err := app.store.LoadSession(context.Background(), "chat-1"); err != nil {
		t.Fatalf("the chat row is gone after a panicked cleanup: %v", err)
	}
}
