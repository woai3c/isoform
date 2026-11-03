//go:build darwin

package main

import (
	"context"
	"testing"
	"time"
)

// A grant made from the first screen must not be mistaken for a grant that
// existed at launch just because optional services have not started yet.
func TestLaunchRecordsScreenRecordingBeforeFirstScreen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	previousRecorded := screenRecordingLaunchRecorded.Load()
	previousGrant := screenRecordingAtLaunch.Load()
	t.Cleanup(func() {
		screenRecordingLaunchRecorded.Store(previousRecorded)
		screenRecordingAtLaunch.Store(previousGrant)
	})
	screenRecordingLaunchRecorded.Store(false)
	app := NewApp()
	t.Cleanup(func() { app.launch.stop(); app.telemetry.Close() })
	// Exercise the real startup callback without creating a native window or
	// publishing a store: the opener exits at the existing stopped fence.
	app.storageStartup.stop()
	app.startup(context.Background())
	awaitStorageStartup(t, app.storageStartup.done)
	if !screenRecordingLaunchRecorded.Load() {
		t.Fatal("launch grant observation waited for deferred services")
	}
	select {
	case <-app.launch.painted:
		t.Fatal("fixture unexpectedly acknowledged first-screen paint")
	default:
	}
}

func TestLaunchUnbundledAppleEventsStatusDoesNotWait(t *testing.T) {
	if appleEventsHasApplicationIdentity() {
		t.Skip("requires an unbundled test process")
	}
	done := make(chan struct{})
	var messages textAutomationState
	var mail mailAutomationState
	go func() {
		messages = textMessagesAutomation(false)
		mail = mailAutomationStatus(false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unbundled status read waited on Apple Events")
	}
	if messages != textAutomationUnknown || mail != mailAutomationUnknown {
		t.Fatalf("unbundled helper invented a permission: Messages=%q Mail=%q", messages, mail)
	}
}
