//go:build windows

package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// isBackgroundListeningSourceSupported reports which sources have a
// working capture implementation. On Windows both mic and system audio
// run on WASAPI, and the combined path simply fans them in via
// combinedAudioRecorder — so everything is supported.
func isBackgroundListeningSourceSupported(source backgroundListeningSource) bool {
	switch source {
	case backgroundListeningSourceBoth,
		backgroundListeningSourceMicrophone,
		backgroundListeningSourceSystem:
		return true
	default:
		return false
	}
}

func newPlatformAudioRecorder(source backgroundListeningSource) audioRecorder {
	switch source {
	case backgroundListeningSourceBoth:
		return &combinedAudioRecorder{newRecorders: newWindowsCombinedRecorders}
	case backgroundListeningSourceSystem:
		return &windowsLoopbackRecorder{}
	default:
		return &windowsMicRecorder{}
	}
}

// newWindowsCombinedRecorders returns the candidate recorders for the
// combined source on Windows. Order matches macOS for consistency: system
// audio first so the loopback session is established before the mic
// recorder begins feeding chunks. combinedAudioRecorder will drop either
// if it fails to start, so the user always gets at least the working one.
func newWindowsCombinedRecorders() []audioRecorder {
	return []audioRecorder{
		&windowsLoopbackRecorder{},
		&windowsMicRecorder{},
	}
}

// windowsMicRecorder captures default-endpoint microphone input via WASAPI
// shared-mode. The heavy lifting (COM init, format conversion, ring
// buffer) lives in wasapiSession.
type windowsMicRecorder struct {
	mu      sync.Mutex
	session *wasapiSession
	readWG  sync.WaitGroup
}

func (r *windowsMicRecorder) Start() error {
	session := newWASAPISession(backgroundListeningSourceMicrophone, false /* loopback */)
	if err := session.Start(); err != nil {
		log.Printf("[background-audio] WASAPI mic Start failed: %v", err)
		recordAudioCaptureEvent(AudioCaptureEvent{
			Source: string(backgroundListeningSourceMicrophone),
			Kind:   "started", Outcome: "failed",
			Message: err.Error(),
		})
		return fmt.Errorf("start WASAPI microphone capture: %w", err)
	}
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()
	return nil
}

func (r *windowsMicRecorder) ReadChunk(d time.Duration) (capturedAudioChunk, error) {
	r.mu.Lock()
	session := r.session
	if session == nil {
		r.mu.Unlock()
		return capturedAudioChunk{}, fmt.Errorf("microphone recorder not initialized")
	}
	r.readWG.Add(1)
	r.mu.Unlock()
	defer r.readWG.Done()

	seconds := int(d.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	wav, err := session.ReadChunk(seconds)
	if err != nil {
		return capturedAudioChunk{Source: backgroundListeningSourceMicrophone}, err
	}
	if wav == nil {
		// Session stopped or buffer empty; mirror the macOS recorder's
		// "empty chunk, no error" semantics so captureLoop's empty-chunk
		// branch fires.
		return capturedAudioChunk{Source: backgroundListeningSourceMicrophone}, nil
	}
	return capturedAudioChunk{
		CaptureID:   nextAudioCaptureIdentity(),
		Source:      backgroundListeningSourceMicrophone,
		WavData:     wav,
		Duration:    time.Duration(len(wav)-wavHeaderSize) * time.Second / meetingAudioBytesPerSecond,
		CapturedAt:  time.Now(),
		CapturedApp: activitymon.FrontmostAppInfo(),
	}, nil
}

func (r *windowsMicRecorder) Stop() {
	r.mu.Lock()
	session := r.session
	r.session = nil
	r.mu.Unlock()

	if session != nil {
		session.Stop()
	}
	r.readWG.Wait()
	recordAudioCaptureEvent(AudioCaptureEvent{
		Source:  string(backgroundListeningSourceMicrophone),
		Kind:    "stopped",
		Message: "caller requested stop",
	})
}

func (r *windowsMicRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{backgroundListeningSourceMicrophone}
}

// windowsLoopbackRecorder captures everything the system is currently
// rendering (default output endpoint) via WASAPI's AUDCLNT_STREAMFLAGS_LOOPBACK.
// This is the Windows equivalent of the macOS per-process tap; it's
// coarser (everything playing, not just one app) but it's the standard
// Windows pattern (OBS, Audacity, NAudio-based apps).
type windowsLoopbackRecorder struct {
	mu      sync.Mutex
	session *wasapiSession
	readWG  sync.WaitGroup
}

func (r *windowsLoopbackRecorder) Start() error {
	session := newWASAPISession(backgroundListeningSourceSystem, true /* loopback */)
	if err := session.Start(); err != nil {
		log.Printf("[background-audio] WASAPI loopback Start failed: %v", err)
		recordAudioCaptureEvent(AudioCaptureEvent{
			Source: string(backgroundListeningSourceSystem),
			Kind:   "started", Outcome: "failed",
			Message: err.Error(),
		})
		return fmt.Errorf("start WASAPI loopback capture: %w", err)
	}
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()
	return nil
}

func (r *windowsLoopbackRecorder) ReadChunk(d time.Duration) (capturedAudioChunk, error) {
	r.mu.Lock()
	session := r.session
	if session == nil {
		r.mu.Unlock()
		return capturedAudioChunk{}, fmt.Errorf("loopback recorder not initialized")
	}
	r.readWG.Add(1)
	r.mu.Unlock()
	defer r.readWG.Done()

	seconds := int(d.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	wav, err := session.ReadChunk(seconds)
	if err != nil {
		return capturedAudioChunk{Source: backgroundListeningSourceSystem}, err
	}
	if wav == nil {
		return capturedAudioChunk{Source: backgroundListeningSourceSystem}, nil
	}
	return capturedAudioChunk{
		CaptureID:   nextAudioCaptureIdentity(),
		Source:      backgroundListeningSourceSystem,
		WavData:     wav,
		Duration:    time.Duration(len(wav)-wavHeaderSize) * time.Second / meetingAudioBytesPerSecond,
		CapturedAt:  time.Now(),
		CapturedApp: activitymon.FrontmostAppInfo(),
	}, nil
}

func (r *windowsLoopbackRecorder) Stop() {
	r.mu.Lock()
	session := r.session
	r.session = nil
	r.mu.Unlock()

	if session != nil {
		session.Stop()
	}
	r.readWG.Wait()
	recordAudioCaptureEvent(AudioCaptureEvent{
		Source:  string(backgroundListeningSourceSystem),
		Kind:    "stopped",
		Message: "caller requested stop",
	})
}

func (r *windowsLoopbackRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{backgroundListeningSourceSystem}
}
