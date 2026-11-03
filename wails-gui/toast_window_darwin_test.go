//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"
)

func TestDesktopToastRejectsEmptyContent(t *testing.T) {
	original := toastState
	toastState = &toastQueueState{}
	t.Cleanup(func() { toastState = original })

	for _, body := range []string{"", " ", "\t\r\n", "\u3000"} {
		showDesktopToastWithOptions(body, body, "", "", toastOptions{
			OnShown: func() { t.Error("empty toast reported shown") },
		})
	}
	if toastState.counter != 0 || len(toastState.visible) != 0 || len(toastState.pending) != 0 {
		t.Fatalf("empty content entered the native toast queue: %+v", toastState)
	}
}

func TestDesktopToastDeliversLockedEngagementCopy(t *testing.T) {
	text := engagementText{PushBody: "Work is waiting on you.", Title: "The private acquisition needs an answer."}
	title, body := engagementToastCopy(text, true)
	if title != text.PushBody || body != "" {
		t.Fatal("locked caller copy must contain only the generic title")
	}

	// Keep the test at the Go queue boundary: saturate the visible slots so
	// enqueue never creates a native panel, and seed the notification cache.
	original := toastState
	toastState = &toastQueueState{visible: make([]*toastInstance, toastMaxVisible)}
	notifDeniedMu.Lock()
	oldDenied, oldAt := notifDeniedCached, notifDeniedAt
	notifDeniedCached, notifDeniedAt = false, time.Now()
	notifDeniedMu.Unlock()
	var inst *toastInstance
	t.Cleanup(func() {
		if inst != nil && inst.timer != nil {
			inst.timer.Stop()
		}
		toastState = original
		notifDeniedMu.Lock()
		notifDeniedCached, notifDeniedAt = oldDenied, oldAt
		notifDeniedMu.Unlock()
	})
	shown := make(chan struct{}, 1)
	showDesktopToastWithOptions(title, body, "", "", toastOptions{OnShown: func() { shown <- struct{}{} }})
	if len(toastState.pending) != 1 {
		t.Fatal("title-only locked notification was dropped at enqueue")
	}
	req := toastState.pending[0]
	if req.title != text.PushBody || req.body != "" || strings.Contains(renderToastHTML(req), text.Title) {
		t.Fatal("locked notification payload exposed the private body")
	}
	inst = &toastInstance{toastRequest: req}
	toastState.visible, toastState.pending = []*toastInstance{inst}, nil
	// Like the readiness test below, reveal finds no native panel. Verify
	// that the real caller's payload can reach the delivery receipt boundary.
	handleToastReady(inst.id)
	if !inst.ready || inst.timer == nil {
		t.Fatal("title-only locked notification was rejected at readiness")
	}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("title-only locked notification did not report shown")
	}
}

func TestToastReadyRejectsEmptyOrClosedContent(t *testing.T) {
	original := toastState
	inst := &toastInstance{toastRequest: &toastRequest{id: "empty", body: " \n"}}
	toastState = &toastQueueState{visible: []*toastInstance{inst}}
	t.Cleanup(func() { toastState = original })

	handleToastReady("empty")
	handleToastReady("already-closed")
	if inst.ready || inst.timer != nil {
		t.Fatal("empty or closed content was allowed to reveal a panel")
	}
}

func TestToastReadyStopsLoadWatchdog(t *testing.T) {
	original := toastState
	shown := make(chan struct{}, 1)
	inst := &toastInstance{toastRequest: &toastRequest{
		id: "ready-content", body: "A populated notification",
		options: toastOptions{OnShown: func() { shown <- struct{}{} }},
	}}
	watchdog := time.AfterFunc(time.Hour, func() { t.Error("load watchdog was not stopped") })
	inst.timer = watchdog
	toastState = &toastQueueState{visible: []*toastInstance{inst}}
	t.Cleanup(func() {
		watchdog.Stop()
		if inst.timer != nil {
			inst.timer.Stop()
		}
		toastState = original
	})

	// No native window is created here. The reveal dispatch finds no panel;
	// this checks the Go lifecycle and delivery receipt at the boundary.
	handleToastReady(inst.id)
	if !inst.ready || inst.timer == watchdog || inst.remaining != toastDuration {
		t.Fatal("ready content did not replace the load watchdog with its display timer")
	}
	if watchdog.Stop() {
		t.Fatal("load watchdog remained armed after content became ready")
	}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("ready content did not report shown")
	}
	timer := inst.timer
	handleToastReady(inst.id)
	if inst.timer != timer {
		t.Fatal("duplicate readiness restarted the display timer")
	}
}

func TestToastFailedContentClosesWithoutUserDismissal(t *testing.T) {
	original := toastState
	inst := &toastInstance{toastRequest: &toastRequest{
		id: "failed-content", body: "Content that disappeared",
		options: toastOptions{
			OnShown:     func() { t.Error("failed content reported shown") },
			OnDismissed: func() { t.Error("content failure reported as user dismissal") },
		},
	}, paused: true}
	inst.timer = time.AfterFunc(time.Hour, func() { t.Error("failed toast timer remained armed") })
	toastState = &toastQueueState{visible: []*toastInstance{inst}}
	t.Cleanup(func() {
		inst.timer.Stop()
		toastState = original
	})

	handleToastEvent(inst.id, "failed")
	if len(toastState.visible) != 0 || inst.timer.Stop() {
		t.Fatal("content failure did not close the paused toast and stop its timer")
	}
}

func TestToastLoadTimeoutOnlyClosesUnreadyContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
	}{{"unready", false}, {"ready", true}} {
		t.Run(tc.name, func(t *testing.T) {
			original := toastState
			inst := &toastInstance{toastRequest: &toastRequest{id: "load-timeout", body: "Content"}, ready: tc.ready}
			inst.timer = time.AfterFunc(time.Hour, func() { t.Error("load timeout timer leaked") })
			toastState = &toastQueueState{visible: []*toastInstance{inst}}
			t.Cleanup(func() {
				inst.timer.Stop()
				toastState = original
			})

			handleToastLoadTimeout(inst.id)
			if tc.ready {
				if len(toastState.visible) != 1 || !inst.timer.Stop() {
					t.Fatal("load watchdog closed ready content or stopped its display timer")
				}
			} else if len(toastState.visible) != 0 || inst.timer.Stop() {
				t.Fatal("load watchdog retained unready content or its timer")
			}
		})
	}
}
