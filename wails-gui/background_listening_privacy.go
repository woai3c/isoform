package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const backgroundListeningPrivacyPollInterval = time.Second

const (
	backgroundAudioSuspendedDesktopLocked = "desktop_locked"
	backgroundAudioSuspendedPrivacyPause  = "privacy_pause"
	backgroundAudioSuspendedDisabled      = "disabled"
	backgroundAudioSuspendedNotRunning    = "not_running"
	// backgroundAudioSuspendedMicrophoneOff is listening on with the
	// microphone switched off and no system audio on this Mac.
	backgroundAudioSuspendedMicrophoneOff = "microphone_paused"
)

type backgroundListeningLockAction uint8

const (
	backgroundListeningLockNoAction backgroundListeningLockAction = iota
	backgroundListeningLockSuspend
	backgroundListeningLockResume
)

type backgroundListeningLockState struct {
	initialized bool
	locked      bool
}

func (state *backgroundListeningLockState) observe(locked bool) backgroundListeningLockAction {
	if !state.initialized {
		state.initialized = true
		state.locked = locked
		if locked {
			return backgroundListeningLockSuspend
		}
		return backgroundListeningLockNoAction
	}
	if state.locked == locked {
		return backgroundListeningLockNoAction
	}
	state.locked = locked
	if locked {
		return backgroundListeningLockSuspend
	}
	return backgroundListeningLockResume
}

func (a *App) startBackgroundListeningPrivacyMonitor(ctx context.Context) {
	if a == nil || ctx == nil {
		return
	}
	a.mu.Lock()
	if a.audioScreenLocked == nil {
		a.audioScreenLocked = backgroundListeningDesktopProtected
	}
	a.mu.Unlock()
	go a.runBackgroundListeningPrivacyMonitor(ctx)
}

func (a *App) runBackgroundListeningPrivacyMonitor(ctx context.Context) {
	ticker := time.NewTicker(backgroundListeningPrivacyPollInterval)
	defer ticker.Stop()

	state := backgroundListeningLockState{}
	reconcile := func() {
		switch state.observe(a.isBackgroundListeningScreenLocked()) {
		case backgroundListeningLockSuspend:
			a.suspendBackgroundListeningForScreenLock()
		case backgroundListeningLockResume:
			a.resumeBackgroundListeningAfterScreenUnlock()
		}
	}

	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func (a *App) isBackgroundListeningScreenLocked() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	detector := a.audioScreenLocked
	a.mu.Unlock()
	return detector != nil && detector()
}

func backgroundListeningDesktopProtected() bool {
	if activitymon.IsScreenLocked() {
		return true
	}
	return backgroundListeningAppIsProtected(activitymon.FrontmostAppInfo())
}

func backgroundListeningAppIsProtected(frontmost activitymon.AppInfo) bool {
	appName := strings.ToLower(strings.TrimSpace(frontmost.AppName))
	bundleID := strings.ToLower(strings.TrimSpace(frontmost.BundleID))
	return appName == "loginwindow" ||
		bundleID == "com.apple.loginwindow" ||
		strings.HasSuffix(bundleID, ".loginwindow")
}

func (a *App) suspendBackgroundListeningForScreenLock() {
	if a == nil {
		return
	}
	a.stopBackgroundListeningWithDrain(false)
	recordAudioCaptureEvent(AudioCaptureEvent{
		Kind:    "suspended",
		Outcome: "success",
		Message: "Paused audio because the desktop is locked.",
	})
	log.Printf("[background-listening] suspended at source while desktop is locked")
}

func (a *App) resumeBackgroundListeningAfterScreenUnlock() {
	if a == nil || a.IsClosedEyesModeEnabled() || !a.GetBackgroundListening() {
		return
	}
	recordAudioCaptureEvent(AudioCaptureEvent{
		Kind:    "resume_attempt",
		Message: "Desktop unlocked; checking whether background audio may resume.",
	})
	if err := a.ensureWhisperServerRunning(); err != nil {
		recordAudioCaptureEvent(AudioCaptureEvent{
			Kind:    "error",
			Outcome: "failed",
			Message: fmt.Sprintf("Background audio could not resume after unlock: %v", err),
		})
		log.Printf("[background-listening] unlock resume failed: %v", err)
		return
	}
	a.startBackgroundListeningIfReady()
	if a.isBackgroundListeningRunning() {
		recordAudioCaptureEvent(AudioCaptureEvent{
			Kind:    "resumed",
			Outcome: "success",
			Message: "Native audio capture resumed after the desktop was unlocked.",
		})
	}
}

func (a *App) backgroundAudioSuspendedReason(locked, running bool) string {
	switch {
	case locked:
		return backgroundAudioSuspendedDesktopLocked
	case a != nil && a.IsClosedEyesModeEnabled():
		return backgroundAudioSuspendedPrivacyPause
	case a != nil && !a.GetBackgroundListening():
		return backgroundAudioSuspendedDisabled
	case !running && resolveBackgroundListeningSource(loadConfig()) == backgroundListeningSourceNone:
		return backgroundAudioSuspendedMicrophoneOff
	case !running:
		return backgroundAudioSuspendedNotRunning
	default:
		return ""
	}
}
