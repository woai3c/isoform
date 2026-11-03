package main

import (
	"sync"
	"time"
)

// AudioCaptureEvent is a single line in the audio-capture diagnostics log.
// Every start attempt, config-change, restart, and stop by either the mic
// or system-audio recorder pushes one of these onto a shared ring buffer
// so the DevTools "Audio capture" panel can render what actually happened.
//
// This exists because native audio capture failures are otherwise invisible:
// no UI indicator, and Go's stderr usually goes to a terminal nobody reads.
// The event log surfaces microphone/system-audio start, stop, and error
// transitions in the DevTools panel.
type AudioCaptureEvent struct {
	Timestamp int64  `json:"timestamp"` // unix ms
	Source    string `json:"source"`    // "microphone" | "system_audio" | ""
	Kind      string `json:"kind"`      // start, stop, suspend, resume, restart, config, or error lifecycle event
	Outcome   string `json:"outcome"`   // "success" | "failed" | ""
	Message   string `json:"message"`
}

const audioCaptureEventBufferSize = 200

var (
	audioCaptureEventsMu sync.Mutex
	audioCaptureEvents   []AudioCaptureEvent
	audioCaptureEventApp *App // set once at startup; used to emit browser events
)

// registerAudioCaptureEventApp binds the App instance that will receive
// browser event emissions whenever a new capture event is recorded.
// Called once from App.startup. Passing nil clears the binding (tests).
func registerAudioCaptureEventApp(a *App) {
	audioCaptureEventsMu.Lock()
	audioCaptureEventApp = a
	audioCaptureEventsMu.Unlock()
}

// recordAudioCaptureEvent appends an event to the ring buffer and emits
// a `audio-capture:event` Wails event so the DevTools panel can render
// it live. Safe to call from any goroutine. Native recorder callbacks use
// recordNativeAudioCaptureEvent instead.
func recordAudioCaptureEvent(event AudioCaptureEvent) {
	event, app := appendAudioCaptureEvent(event)
	if app != nil {
		app.emitBrowserEvent("audio-capture:event", event)
	}
}

// recordNativeAudioCaptureEvent records an event reported from inside a
// native recorder call (the goAudioCaptureEvent cgo export). The ring
// buffer is updated before it returns, so snapshots keep the native order,
// but the live event is sent from a new goroutine so no Wails call runs on
// the native caller's thread. See goAudioCaptureEvent for why.
func recordNativeAudioCaptureEvent(event AudioCaptureEvent) {
	event, app := appendAudioCaptureEvent(event)
	if app != nil {
		go app.emitBrowserEvent("audio-capture:event", event)
	}
}

func appendAudioCaptureEvent(event AudioCaptureEvent) (AudioCaptureEvent, *App) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	audioCaptureEventsMu.Lock()
	audioCaptureEvents = append(audioCaptureEvents, event)
	if len(audioCaptureEvents) > audioCaptureEventBufferSize {
		audioCaptureEvents = audioCaptureEvents[len(audioCaptureEvents)-audioCaptureEventBufferSize:]
	}
	app := audioCaptureEventApp
	audioCaptureEventsMu.Unlock()
	return event, app
}

// GetAudioCaptureEvents returns a snapshot copy of the event ring buffer
// (newest last). Bound to the frontend by Wails for the DevTools panel
// to render on first open, after which it switches to live events.
func (a *App) GetAudioCaptureEvents() []AudioCaptureEvent {
	audioCaptureEventsMu.Lock()
	defer audioCaptureEventsMu.Unlock()
	out := make([]AudioCaptureEvent, len(audioCaptureEvents))
	copy(out, audioCaptureEvents)
	return out
}

// BackgroundAudioStatus is the "what is actually running right now?"
// snapshot displayed above the event log. The frontend polls it on
// panel open; the event log carries the deltas after that.
type BackgroundAudioStatus struct {
	Running         bool     `json:"running"`
	ActiveSources   []string `json:"activeSources"`
	ScreenLocked    bool     `json:"screenLocked"`
	SuspendedReason string   `json:"suspendedReason,omitempty"`
	// MicrophonePaused is the user's Microphone switch turned off: system
	// audio may still run while the microphone does not.
	MicrophonePaused bool `json:"microphonePaused,omitempty"`
}

// GetBackgroundAudioStatus reports which capture sources are currently
// feeding the pipeline. Used by the DevTools panel so the user sees
// "mic: off" even before any new event arrives.
func (a *App) GetBackgroundAudioStatus() BackgroundAudioStatus {
	locked := a.isBackgroundListeningScreenLocked()
	a.backgroundAudioMu.Lock()
	defer a.backgroundAudioMu.Unlock()
	return a.backgroundAudioStatusLocked(locked)
}

// The caller holds backgroundAudioMu through the recorder snapshot.
func (a *App) backgroundAudioStatusLocked(locked bool) BackgroundAudioStatus {
	a.mu.Lock()
	capture := a.backgroundAudio
	a.mu.Unlock()
	status := BackgroundAudioStatus{ScreenLocked: locked, MicrophonePaused: microphoneSwitchIsSource() && !backgroundMicrophoneEnabled()}
	if capture == nil {
		status.SuspendedReason = a.backgroundAudioSuspendedReason(locked, false)
		return status
	}
	sources := capture.activeSources()
	strs := make([]string, 0, len(sources))
	for _, s := range sources {
		strs = append(strs, string(s))
	}
	status.Running = capture.isRunning()
	status.ActiveSources = strs
	status.SuspendedReason = a.backgroundAudioSuspendedReason(locked, status.Running)
	return status
}
