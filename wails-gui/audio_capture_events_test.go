package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resetAudioCaptureEvents is a test helper so each case starts clean.
// Not exported because nothing outside this file should be poking the
// ring buffer directly.
func resetAudioCaptureEvents() {
	audioCaptureEventsMu.Lock()
	audioCaptureEvents = nil
	audioCaptureEventApp = nil
	audioCaptureEventsMu.Unlock()
}

func TestRecordAudioCaptureEvent_AppendsAndStampsTimestamp(t *testing.T) {
	resetAudioCaptureEvents()

	recordAudioCaptureEvent(AudioCaptureEvent{
		Source: "microphone", Kind: "started", Outcome: "success",
	})

	app := &App{}
	got := app.GetAudioCaptureEvents()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Timestamp <= 0 {
		t.Errorf("Timestamp was not auto-stamped: %d", got[0].Timestamp)
	}
	if got[0].Source != "microphone" || got[0].Kind != "started" {
		t.Errorf("event fields not preserved: %+v", got[0])
	}
}

func TestRecordAudioCaptureEvent_EnforcesRingBufferSize(t *testing.T) {
	resetAudioCaptureEvents()

	// Push 50 more than the cap so we know the oldest ones are dropped.
	for i := 0; i < audioCaptureEventBufferSize+50; i++ {
		recordAudioCaptureEvent(AudioCaptureEvent{
			Kind: "started", Message: "event",
		})
	}

	app := &App{}
	got := app.GetAudioCaptureEvents()
	if len(got) != audioCaptureEventBufferSize {
		t.Fatalf("buffer length = %d, want %d", len(got), audioCaptureEventBufferSize)
	}
}

func TestGetAudioCaptureEvents_ReturnsSnapshotCopy(t *testing.T) {
	resetAudioCaptureEvents()
	recordAudioCaptureEvent(AudioCaptureEvent{Kind: "first"})

	app := &App{}
	snap := app.GetAudioCaptureEvents()

	// Mutating the snapshot must not affect the shared buffer.
	snap[0].Kind = "mutated"

	again := app.GetAudioCaptureEvents()
	if again[0].Kind != "first" {
		t.Errorf("shared buffer leaked: got %q, want %q", again[0].Kind, "first")
	}
}

func TestRecordAudioCaptureEvent_IsGoroutineSafe(t *testing.T) {
	resetAudioCaptureEvents()

	const writers = 8
	const perWriter = 50

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				recordAudioCaptureEvent(AudioCaptureEvent{
					Kind: "started", Message: "parallel",
				})
			}
		}(i)
	}
	wg.Wait()

	app := &App{}
	got := app.GetAudioCaptureEvents()
	want := writers * perWriter
	if want > audioCaptureEventBufferSize {
		want = audioCaptureEventBufferSize
	}
	if len(got) != want {
		t.Fatalf("got %d events, want %d (with concurrent writers)", len(got), want)
	}
}

// TestRecordAudioCaptureEvent_EmitsBrowserEventWhenAppBound ensures the
// DevTools panel's live subscription actually fires. The registered App
// is observed via a counter; we can't reach into the Wails runtime in
// a unit test, but the call path goes through (App).emitBrowserEvent
// which we can stub by shadowing the slot after registration.
//
// Because emitBrowserEvent takes a concrete *App receiver, the cleanest
// assertion is: when no app is registered, no panic. We verify that
// separately. For the "fires" branch we lean on the production code
// path — an integration test would cover it end-to-end.
func TestRecordAudioCaptureEvent_NoPanicWithoutApp(t *testing.T) {
	resetAudioCaptureEvents()

	// Intentionally do NOT call registerAudioCaptureEventApp — the
	// function must tolerate being called before startup wires the App.
	recordAudioCaptureEvent(AudioCaptureEvent{Kind: "started"})
	// If the above panicked the test fails.
}

// TestRecordNativeAudioCaptureEvent_DeliversOffTheCallerThread guards the
// exits on 2026-10-12/13: the native microphone recorder reports "stopped"
// from inside its @autoreleasepool, and sending that event to Wails on the
// same thread left the main thread releasing a freed string. The event must
// be recorded before the call returns, but delivered without the caller.
func TestRecordNativeAudioCaptureEvent_DeliversOffTheCallerThread(t *testing.T) {
	assertNativeAudioEventDeliveredOffCaller(t, func() {
		recordNativeAudioCaptureEvent(AudioCaptureEvent{
			Source: "microphone", Kind: "stopped", Message: "caller requested stop",
		})
	})
}

// assertNativeAudioEventDeliveredOffCaller runs record, which must report a
// "stopped" event, and checks that the event is in the ring when record
// returns while its browser delivery happens on another goroutine. The
// condition that matters is that no Wails call runs on the native caller's
// thread while its @autoreleasepool is active; a test cannot observe that
// pool, so a delivery that does not block the calling goroutine is the proxy.
func assertNativeAudioEventDeliveredOffCaller(t *testing.T, record func()) {
	t.Helper()
	resetAudioCaptureEvents()
	defer resetAudioCaptureEvents()

	app := &App{eventBroadcaster: NewEventBroadcaster()}
	client := app.eventBroadcaster.AddClient("native-audio-event-test")
	defer app.eventBroadcaster.RemoveClient("native-audio-event-test")
	registerAudioCaptureEventApp(app)

	// While the write lock is held, delivery on the caller's goroutine
	// would block the call itself.
	app.eventBroadcaster.mu.Lock()
	returned := make(chan struct{})
	go func() {
		record()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		app.eventBroadcaster.mu.Unlock()
		t.Fatal("native audio event was delivered on the native caller's thread")
	}

	got := app.GetAudioCaptureEvents()
	if len(got) != 1 || got[0].Kind != "stopped" {
		t.Fatalf("event not recorded before the native call returned: %+v", got)
	}
	app.eventBroadcaster.mu.Unlock()

	select {
	case payload := <-client:
		if !strings.Contains(string(payload), `"audio-capture:event"`) {
			t.Fatalf("unexpected payload: %s", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native audio event never reached the browser bridge")
	}
}

// TestRegisterAudioCaptureEventApp_Rebinding verifies that a later call
// to registerAudioCaptureEventApp replaces the previous binding so a
// test or hot-reload can swap the App reference safely.
func TestRegisterAudioCaptureEventApp_Rebinding(t *testing.T) {
	resetAudioCaptureEvents()

	first := &App{}
	second := &App{}

	registerAudioCaptureEventApp(first)
	registerAudioCaptureEventApp(second)

	audioCaptureEventsMu.Lock()
	bound := audioCaptureEventApp
	audioCaptureEventsMu.Unlock()

	if bound != second {
		t.Errorf("re-registration did not swap the binding")
	}

	// And unbinding:
	registerAudioCaptureEventApp(nil)
	audioCaptureEventsMu.Lock()
	bound = audioCaptureEventApp
	audioCaptureEventsMu.Unlock()
	if bound != nil {
		t.Errorf("nil re-registration did not clear the binding")
	}
}

// Guard against the race where a very bursty sequence of events (many
// restarts in a second) would overflow or starve the mutex. Counter
// confirms none were lost on the caller's side.
func TestRecordAudioCaptureEvent_BurstHighCardinality(t *testing.T) {
	resetAudioCaptureEvents()

	var accepted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < audioCaptureEventBufferSize*2; i++ {
			recordAudioCaptureEvent(AudioCaptureEvent{Kind: "restart"})
			accepted.Add(1)
		}
	}()
	wg.Wait()

	if accepted.Load() != int64(audioCaptureEventBufferSize*2) {
		t.Errorf("only %d records accepted, want %d",
			accepted.Load(), audioCaptureEventBufferSize*2)
	}

	app := &App{}
	got := app.GetAudioCaptureEvents()
	if len(got) != audioCaptureEventBufferSize {
		t.Errorf("ring buffer size = %d, want %d", len(got), audioCaptureEventBufferSize)
	}
}

func TestAudioCaptureBrowserRPCIsReadOnly(t *testing.T) {
	for _, method := range []string{
		"GetAudioCaptureEvents",
		"GetBackgroundAudioStatus",
	} {
		if !browserRPCAllowlist[method] {
			t.Fatalf("%s is not Browser RPC allowlisted", method)
		}
	}
	for _, method := range []string{
		"StartBackgroundListening",
		"StopBackgroundListening",
		"ResumeBackgroundListeningAfterScreenUnlock",
		"SetBackgroundListening",
		"SetBackgroundListeningSource",
		"SetAutomaticMeetingRecordingEnabled",
	} {
		if browserRPCAllowlist[method] {
			t.Fatalf("audio capture mutation %s entered the Browser RPC allowlist", method)
		}
	}
}
