//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"yansu-agent/internal/config"
)

type analyzeDispatchEvent struct {
	reply         string
	streamStarted bool
}

type analyzeDispatchAdapter struct {
	events   chan analyzeDispatchEvent
	terminal chan struct{}
	once     sync.Once
}

func (*analyzeDispatchAdapter) Listen(context.Context, func(Event)) error { return nil }
func (a *analyzeDispatchAdapter) Reply(_ Event, message string) {
	a.events <- analyzeDispatchEvent{reply: message}
	a.once.Do(func() { close(a.terminal) })
}
func (a *analyzeDispatchAdapter) PostAndStream(_ Event, output <-chan string) {
	a.events <- analyzeDispatchEvent{streamStarted: true}
	for range output {
	}
	a.once.Do(func() { close(a.terminal) })
}

// One generous deadline bounds each lifecycle, rather than requiring every
// scheduler hop to finish within a short independent timing window. Leave
// enough of the test runner's remaining deadline for cancellation cleanup.
func analyzeDispatchWaitContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	timeout := 30 * time.Second
	if deadline, ok := t.Deadline(); ok && time.Until(deadline)/2 < timeout {
		timeout = time.Until(deadline) / 2
	}
	return context.WithTimeout(context.Background(), timeout)
}

func TestAnalyzeHandlerReturnsWhileSnapshotBlockedAndCancels(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	createAnalyzeGitFixture(t, home, project)
	gitConfig := filepath.Join(project, ".git", "config")
	if err := os.Remove(gitConfig); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(gitConfig, 0600); err != nil {
		t.Fatal(err)
	}

	adapter := &analyzeDispatchAdapter{events: make(chan analyzeDispatchEvent, 4), terminal: make(chan struct{})}
	sessions := NewSessionManager()
	t.Cleanup(sessions.Close)
	registry := &ProjectRegistry{entries: map[string]string{"fixture/project": project}}
	router := &Router{cfg: &config.Config{}, daemonCfg: &config.DaemonConfig{}, registry: registry, sessions: sessions, adapter: adapter}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitCtx, stopWaiting := analyzeDispatchWaitContext(t)
	defer stopWaiting()
	event := Event{UserID: "owner", Platform: "test", Text: "fixture/project"}
	// Project-selection replies use the serial session queue even for analyze.
	// The next queued task must run while the real Git collector is blocked.
	sessions.SetPending("owner:test", &PendingState{Command: "analyze"})
	session := sessions.GetOrCreate("owner:test")
	handled, nextTask := make(chan struct{}), make(chan struct{})
	session.Enqueue(func() {
		router.Handle(ctx, event)
		close(handled)
	})
	session.Enqueue(func() { close(nextTask) })
	t.Cleanup(func() {
		cancel()
		cleanupCtx, stopCleanup := analyzeDispatchWaitContext(t)
		defer stopCleanup()
		for _, completed := range []<-chan struct{}{nextTask, adapter.terminal} {
			select {
			case <-completed:
			case <-cleanupCtx.Done():
				t.Error("analysis task did not finish cancellation cleanup")
				return
			}
		}
	})
	waitForDispatch := func(completed <-chan struct{}, description string) {
		t.Helper()
		select {
		case <-completed:
		case event := <-adapter.events:
			t.Fatalf("analysis ended before %s while its snapshot should be blocked: %+v", description, event)
		case <-waitCtx.Done():
			t.Fatalf("timed out waiting for %s while the snapshot was blocked", description)
		}
	}

	// O_NONBLOCK makes fixture setup itself bounded. A successful writer open
	// proves Git has opened the FIFO for reading; keeping it open without data
	// then deterministically holds the actual snapshot until cancellation.
	var writer *os.File
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for writer == nil {
		var err error
		writer, err = os.OpenFile(gitConfig, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatalf("open Git configuration FIFO: %v", err)
		}
		select {
		case event := <-adapter.events:
			t.Fatalf("analysis ended before its snapshot blocked: %+v", event)
		case <-waitCtx.Done():
			t.Fatal("Git snapshot did not reach the blocking configuration fixture")
		case <-poll.C:
		}
	}
	defer writer.Close()
	waitForDispatch(handled, "Router.Handle to return")
	waitForDispatch(nextTask, "the next serial session task to run")
	// Dispatch is useful only while collection is still blocked. The live
	// reader and reserved slot prevent the collector's own timeout from
	// making a synchronous handler appear to have returned independently.
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatalf("Git collector stopped before cancellation: %v", err)
	}
	if len(session.aiTaskSlots) != 1 {
		t.Fatal("blocked snapshot did not reserve an AI task slot")
	}

	cancel()
	select {
	case event := <-adapter.events:
		if event.streamStarted {
			t.Fatal("cancelled snapshot launched the model stream")
		}
		if !strings.Contains(event.reply, "Git evidence") {
			t.Fatalf("unexpected cancelled snapshot reply: %s", event.reply)
		}
	case <-waitCtx.Done():
		t.Fatal("cancelled snapshot did not finish")
	}
	// Reply is the terminal preparation-failure notification, emitted only
	// after route cleanup and slot release; no timed absence is a success.
	waitForDispatch(adapter.terminal, "the cancellation reply to finish")
	if len(session.aiTaskSlots) != 0 {
		t.Error("cancelled snapshot kept its AI task slot")
	}
	for {
		_, err := writer.Write([]byte("\n"))
		if errors.Is(err, syscall.EPIPE) {
			break
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) {
			t.Fatalf("check cancelled Git reader: %v", err)
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("cancelled Git collector kept its configuration reader open")
		case <-poll.C:
		}
	}
}
