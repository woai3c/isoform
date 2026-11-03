package main

import (
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestBackgroundListeningLockStateTransitions(t *testing.T) {
	state := backgroundListeningLockState{}
	steps := []struct {
		locked bool
		want   backgroundListeningLockAction
	}{
		{locked: false, want: backgroundListeningLockNoAction},
		{locked: false, want: backgroundListeningLockNoAction},
		{locked: true, want: backgroundListeningLockSuspend},
		{locked: true, want: backgroundListeningLockNoAction},
		{locked: false, want: backgroundListeningLockResume},
		{locked: false, want: backgroundListeningLockNoAction},
	}
	for index, step := range steps {
		if got := state.observe(step.locked); got != step.want {
			t.Fatalf("step %d action=%d, want %d", index, got, step.want)
		}
	}

	initiallyLocked := backgroundListeningLockState{}
	if got := initiallyLocked.observe(true); got != backgroundListeningLockSuspend {
		t.Fatalf("initial locked action=%d, want suspend", got)
	}
}

func TestBackgroundAudioStatusExplainsDesktopLock(t *testing.T) {
	app := &App{audioScreenLocked: func() bool { return true }}
	status := app.GetBackgroundAudioStatus()
	if status.Running || !status.ScreenLocked ||
		status.SuspendedReason != backgroundAudioSuspendedDesktopLocked {
		t.Fatalf("locked background audio status=%+v", status)
	}
}

func TestBackgroundListeningProtectedAppDetection(t *testing.T) {
	cases := []struct {
		name string
		app  activitymon.AppInfo
		want bool
	}{
		{name: "login app name", app: activitymon.AppInfo{AppName: " LoginWindow "}, want: true},
		{name: "login bundle", app: activitymon.AppInfo{BundleID: "com.apple.loginwindow"}, want: true},
		{name: "login bundle suffix", app: activitymon.AppInfo{BundleID: "test.loginwindow"}, want: true},
		{name: "ordinary app", app: activitymon.AppInfo{AppName: "Safari", BundleID: "com.apple.Safari"}, want: false},
		{name: "name only contains login", app: activitymon.AppInfo{AppName: "LoginWindow Notes"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backgroundListeningAppIsProtected(tc.app); got != tc.want {
				t.Fatalf("protected=%v, want %v for %+v", got, tc.want, tc.app)
			}
		})
	}
}

func TestStartBackgroundListeningFailsClosedWhileDesktopLocked(t *testing.T) {
	app := &App{audioScreenLocked: func() bool { return true }}
	app.startBackgroundListeningIfReady()
	if app.backgroundAudio != nil {
		t.Fatal("locked desktop started native audio capture")
	}
}

func TestDesktopLockRejectsMeetingAudioBeforeDownstreamWork(t *testing.T) {
	app := &App{audioScreenLocked: func() bool { return true }}
	chunk := capturedAudioChunk{
		CapturedInMeeting:      true,
		CapturedMeetingAppName: "Zoom",
	}
	if app.shouldTranscribeBackgroundAudioChunk(chunk) {
		t.Fatal("desktop lock admitted a captured meeting chunk")
	}
}

func TestScreenLockStopsNativeCaptureAndDiscardsPendingAudio(t *testing.T) {
	recorder := &stoppableFakeRecorder{chunks: make(chan capturedAudioChunk, 1)}
	transcriber := &fakeTranscriber{}
	capture := &backgroundAudioCapture{
		server:   transcriber,
		stopCh:   make(chan struct{}),
		recorder: recorder,
		onSpeech: func(string, backgroundListeningSource, speechCaptureContext) {
			t.Error("screen-lock suspension emitted a transcript")
		},
	}
	if err := capture.start(); err != nil {
		t.Fatalf("start capture: %v", err)
	}
	recorder.chunks <- capturedAudioChunk{
		Source:     backgroundListeningSourceMicrophone,
		WavData:    makeWAV(make([]int16, 16000)),
		Duration:   time.Second,
		CapturedAt: time.Now(),
	}

	app := &App{
		backgroundAudio:   capture,
		audioScreenLocked: func() bool { return true },
	}
	app.suspendBackgroundListeningForScreenLock()
	if capture.isRunning() {
		t.Fatal("native capture remained running after screen lock")
	}
	if app.backgroundAudio != nil {
		t.Fatal("screen-lock suspension retained the active capture")
	}
	if transcriber.seenBlobs() != 0 {
		t.Fatalf("screen-lock suspension transcribed %d queued blobs", transcriber.seenBlobs())
	}
}
