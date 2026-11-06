package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// The privacy monitor checks the screen on the OS's announcements, not on a
// one-second timer, wherever the platform makes announcements.
func TestBackgroundListeningPrivacyMonitorChecksOnScreenEventsNotATimer(t *testing.T) {
	if !activitymon.SystemStateEventsSupported {
		t.Skip("this platform announces no screen changes; the monitor polls")
	}
	pings := make(chan struct{}, 1)
	previous := subscribeScreenState
	subscribeScreenState = func() (<-chan struct{}, func()) { return pings, func() {} }
	defer func() { subscribeScreenState = previous }()

	var checks atomic.Int32
	app := &App{audioScreenLocked: func() bool { checks.Add(1); return false }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { app.runBackgroundListeningPrivacyMonitor(ctx); close(done) }()

	waitForChecks(t, &checks, 1)
	// The backstop on macOS is 30 seconds; a missed event costs at most that.
	if activitymon.SystemStateEventsSupported && backgroundListeningBackstopPollInterval != 30*time.Second {
		t.Fatalf("backstop = %v, want 30s", backgroundListeningBackstopPollInterval)
	}
	time.Sleep(1500 * time.Millisecond)
	if got := checks.Load(); got != 1 {
		t.Fatalf("checks without an event = %d, want 1 (the start check only)", got)
	}
	pings <- struct{}{}
	waitForChecks(t, &checks, 2)
	cancel()
	<-done
}

func waitForChecks(t *testing.T, checks *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for checks.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("checks = %d, want %d", checks.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
