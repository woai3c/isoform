package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainChatWaitingDeadlineKeepsStateAndRetries(t *testing.T) {
	fixture, events := newWaitingStateFixture(t)
	appendWaitingStateTestCard(t, fixture, "deadline-first", time.Now().Add(time.Hour))
	nextMainChatWaitingEvent(t, events)
	before := fixture.app.GetMainChatWaiting()
	var attempts atomic.Int32
	fixture.app.mainChatWaiting.mu.Lock()
	fixture.app.mainChatWaiting.retryDelay = 100 * time.Millisecond
	fixture.app.mainChatWaiting.compute = func() (MainChatWaitingState, time.Time, error) {
		if attempts.Add(1) == 1 {
			return MainChatWaitingState{}, time.Time{}, context.DeadlineExceeded
		}
		return fixture.app.computeMainChatWaiting()
	}
	fixture.app.mainChatWaiting.mu.Unlock()
	fixture.app.markMainChatWaitingDirty("deadline-test")
	waitForMainChatWaitingWorkerIdle(t, fixture.app)
	if got := fixture.app.GetMainChatWaiting(); got.Count != before.Count || got.Revision != before.Revision {
		t.Fatalf("deadline discarded last good state: before=%+v after=%+v", before, got)
	}
	state := waitForMainChatWaiting(t, fixture.app, func(state MainChatWaitingState) bool { return state.Revision > before.Revision })
	if state.Count != before.Count || attempts.Load() < 2 {
		t.Fatalf("retry failed to publish: state=%+v attempts=%d", state, attempts.Load())
	}
}

func TestMainChatWaitingStateKeepsLastGoodStateOnComputeFailure(t *testing.T) {
	fixture, events := newWaitingStateFixture(t)
	expiresAt := time.Now().Add(time.Hour)
	if id := appendWaitingStateTestCard(t, fixture, "compute-failure", expiresAt); id == "" {
		t.Fatal("waiting card was not appended")
	}
	created, _ := nextMainChatWaitingEvent(t, events)
	before := fixture.app.GetMainChatWaiting()
	if before.Count != 1 {
		t.Fatalf("pre-failure state=%+v, want count 1", before)
	}
	fixture.app.mainChatWaiting.mu.Lock()
	expiryTimer := fixture.app.mainChatWaiting.timer
	fixture.app.mainChatWaiting.retryDelay = 25 * time.Millisecond
	var attempts atomic.Int32
	fixture.app.mainChatWaiting.beforeCompute = func() { attempts.Add(1) }
	fixture.app.mainChatWaiting.mu.Unlock()
	if expiryTimer == nil {
		t.Fatal("waiting card did not arm an expiry timer")
	}

	if err := fixture.app.store.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.app.markMainChatWaitingDirty("test-resync")
	waitForMainChatWaitingWorkerIdle(t, fixture.app)

	after := fixture.app.GetMainChatWaiting()
	if after.Count != created.Count || after.Revision != before.Revision {
		t.Errorf("failed compute changed last good state: before=%+v after=%+v", before, after)
	}
	fixture.app.mainChatWaiting.mu.Lock()
	timerUnchanged := fixture.app.mainChatWaiting.timer == expiryTimer
	fixture.app.mainChatWaiting.mu.Unlock()
	if !timerUnchanged {
		t.Error("failed compute changed the expiry timer")
	}
	fixture.app.mainChatWaiting.mu.Lock()
	retryScheduled := fixture.app.mainChatWaiting.retryTimer != nil
	fixture.app.mainChatWaiting.mu.Unlock()
	if !retryScheduled {
		t.Error("failed compute did not schedule a retry")
	}
	for draining := true; draining; {
		select {
		case raw := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event == mainChatWaitingChangedEvent {
				t.Errorf("failed compute emitted waiting event: %s", raw)
			}
		default:
			draining = false
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() < 2 {
		t.Fatalf("retry did not run, compute attempts=%d", attempts.Load())
	}
	waitForMainChatWaitingWorkerIdle(t, fixture.app)
	fixture.app.mainChatWaiting.mu.Lock()
	if fixture.app.mainChatWaiting.retryTimer != nil {
		fixture.app.mainChatWaiting.retryTimer.Stop()
		fixture.app.mainChatWaiting.retryTimer = nil
	}
	fixture.app.mainChatWaiting.mu.Unlock()

	fixture.app.markMainChatWaitingDirty("privacy-withdrawal")
	waitForMainChatWaitingWorkerIdle(t, fixture.app)
	if got := fixture.app.GetMainChatWaiting(); got.Count != 0 {
		t.Errorf("failed privacy resync restored withdrawn count: %+v", got)
	}
	fixture.app.stopMainChatWaitingLifecycle()
	fixture.app.mainChatWaiting.mu.Lock()
	retryStopped := fixture.app.mainChatWaiting.retryTimer == nil
	fixture.app.mainChatWaiting.mu.Unlock()
	if !retryStopped {
		t.Error("lifecycle stop did not clear the retry timer")
	}
}

func waitForMainChatWaitingWorkerIdle(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		app.mainChatWaiting.mu.Lock()
		running := app.mainChatWaiting.running
		app.mainChatWaiting.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("main-chat waiting worker did not become idle")
}
