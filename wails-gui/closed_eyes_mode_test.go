package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type observationPauseRecorder struct {
	chunks  chan capturedAudioChunk
	stopped chan struct{}
	once    sync.Once
}

func newObservationPauseRecorder() *observationPauseRecorder {
	return &observationPauseRecorder{
		chunks:  make(chan capturedAudioChunk, 1),
		stopped: make(chan struct{}),
	}
}

func (recorder *observationPauseRecorder) Start() error { return nil }

func (recorder *observationPauseRecorder) Stop() {
	recorder.once.Do(func() {
		close(recorder.stopped)
		close(recorder.chunks)
	})
}

func (recorder *observationPauseRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{
		backgroundListeningSourceMicrophone,
		backgroundListeningSourceSystem,
	}
}

func (recorder *observationPauseRecorder) ReadChunk(_ time.Duration) (capturedAudioChunk, error) {
	chunk, ok := <-recorder.chunks
	if !ok {
		return capturedAudioChunk{}, nil
	}
	return chunk, nil
}

func TestObservationPauseStopsNativeAudioAndDiscardsPendingChunk(t *testing.T) {
	recorder := newObservationPauseRecorder()
	transcriber := &fakeTranscriber{}
	capture := &backgroundAudioCapture{
		server:   transcriber,
		stopCh:   make(chan struct{}),
		recorder: recorder,
		onSpeech: func(string, backgroundListeningSource, speechCaptureContext) {
			t.Error("observation pause emitted a transcript")
		},
		onChunk: func(capturedAudioChunk) {
			t.Error("observation pause persisted an audio chunk")
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
		audioScreenLocked: func() bool { return false },
	}
	app.closedEyes.enabled.Store(true)
	app.stopBackgroundListeningForClosedEyes()

	select {
	case <-recorder.stopped:
	default:
		t.Fatal("observation pause did not stop the native recorder")
	}
	if capture.isRunning() {
		t.Fatal("audio capture remained running while observation was paused")
	}
	if app.backgroundAudio != nil {
		t.Fatal("observation pause retained the active capture")
	}
	if transcriber.seenBlobs() != 0 {
		t.Fatalf("observation pause transcribed %d queued blobs", transcriber.seenBlobs())
	}
	status := app.GetBackgroundAudioStatus()
	if status.Running || status.SuspendedReason != backgroundAudioSuspendedPrivacyPause {
		t.Fatalf("paused background audio status=%+v", status)
	}
}

func TestObservationPauseRejectsMeetingAudioBeforeDownstreamWork(t *testing.T) {
	app := &App{audioScreenLocked: func() bool { return false }}
	app.closedEyes.enabled.Store(true)
	chunk := capturedAudioChunk{
		CapturedInMeeting:      true,
		CapturedMeetingAppName: "Zoom",
	}
	if app.shouldTranscribeBackgroundAudioChunk(chunk) {
		t.Fatal("observation pause admitted a captured meeting chunk")
	}
}

func TestObservationPauseEmitsChangeEventOnlyWhenStateChanges(t *testing.T) {
	broadcaster := NewEventBroadcaster()
	events := broadcaster.AddClient("observation-pause-test")
	defer broadcaster.RemoveClient("observation-pause-test")
	app := &App{
		eventBroadcaster:  broadcaster,
		audioScreenLocked: func() bool { return false },
	}

	nextEvent := func() (BrowserEvent, bool) {
		select {
		case payload := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			return event, true
		default:
			return BrowserEvent{}, false
		}
	}
	expectPaused := func(want bool) {
		t.Helper()
		event, ok := nextEvent()
		if !ok {
			t.Fatalf("no %s event after setting paused=%t", closedEyesModeChangedEvent, want)
		}
		if event.Event != closedEyesModeChangedEvent {
			t.Fatalf("event=%q, want %q", event.Event, closedEyesModeChangedEvent)
		}
		if len(event.Args) != 1 || event.Args[0] != want {
			t.Fatalf("event args=%v, want [%t]", event.Args, want)
		}
	}

	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	expectPaused(true)

	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatalf("pause again: %v", err)
	}
	if event, ok := nextEvent(); ok {
		t.Fatalf("pausing an already paused app emitted %+v", event)
	}

	if paused := app.ToggleClosedEyesMode(); paused {
		t.Fatal("toggle from paused did not resume")
	}
	expectPaused(false)
}
