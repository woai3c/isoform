package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundAudioNoiseGateSkipsASRButPreservesRecording(t *testing.T) {
	for _, source := range []backgroundListeningSource{backgroundListeningSourceMicrophone, backgroundListeningSourceSystem} {
		for _, amplitude := range []int16{0, 99, 100, 1000} {
			t.Run(fmt.Sprintf("%s/%d", source, amplitude), func(t *testing.T) {
				transcriber := &fakeTranscriber{}
				scheduler := newBackgroundTranscriptionScheduler(transcriber, nil)
				scheduler.start()
				recorded := 0
				capture := &backgroundAudioCapture{
					scheduler: scheduler,
					onChunk:   func(capturedAudioChunk) { recorded++ },
				}
				tap, unsubscribe := capture.subscribeChunks(source, 1)
				defer unsubscribe()
				capture.forwardChunkDownstream(capturedAudioChunk{
					Source: source, Duration: time.Second,
					WavData: makeWAV([]int16{amplitude, -amplitude}),
				})
				scheduler.stop()
				wantCalls := 0
				if float64(amplitude) >= backgroundAudioNoiseFloor {
					wantCalls = 1
				}
				if got := transcriber.seenBlobs(); got != wantCalls {
					t.Fatalf("ASR calls = %d, want %d", got, wantCalls)
				}
				if recorded != 1 || len(tap) != 1 {
					t.Fatalf("recording/tap chunks = %d/%d, want 1/1 even for silence", recorded, len(tap))
				}
			})
		}
	}
}

func TestBackgroundAudioThirtyMinuteChunkLogsOnlySignalTransitions(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	log.Print("[background-audio] started")
	capture := &backgroundAudioCapture{
		scheduler: newBackgroundTranscriptionSchedulerWithConfig(&fakeTranscriber{}, nil, backgroundTranscriptionSchedulerConfig{
			maxQueueDurationPerSource: time.Hour,
		}),
	}
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for elapsed := time.Duration(0); elapsed < 30*time.Minute; elapsed += backgroundAudioChunkDuration {
		for _, source := range []backgroundListeningSource{backgroundListeningSourceMicrophone, backgroundListeningSourceSystem} {
			// Alternate a minute of sound and a minute of silence on both
			// sources: 720 chunks should produce only 30 sound transitions.
			amplitude := int16(0)
			if elapsed/time.Minute%2 == 0 {
				amplitude = 1000
			}
			capture.forwardChunkDownstream(capturedAudioChunk{
				Source: source, WavData: makeWAV([]int16{amplitude}),
				Duration: backgroundAudioChunkDuration, CapturedAt: start.Add(elapsed),
			})
		}
	}
	log.Print("[background-audio] stopped")
	if got := strings.Count(output.String(), "first sound after silence"); got != 30 {
		t.Fatalf("signal transition logs = %d, want 30", got)
	}
	if strings.Contains(output.String(), "chunk captured") {
		t.Fatal("per-chunk logging returned")
	}
	if !strings.Contains(output.String(), "started") || !strings.Contains(output.String(), "stopped") || output.Len() >= 32*1024 {
		t.Fatalf("30-minute capture window lost lifecycle evidence or exceeds 32 KiB with minute summaries: bytes=%d", output.Len())
	}
}

type idleTestAudioRecorder struct {
	mu         sync.Mutex
	chunks     []capturedAudioChunk
	starts     int
	stops      int
	startError func(int) error
}

func (r *idleTestAudioRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	if r.startError != nil {
		return r.startError(r.starts)
	}
	return nil
}

func (r *idleTestAudioRecorder) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
}

func (r *idleTestAudioRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{backgroundListeningSourceSystem}
}

func (r *idleTestAudioRecorder) ReadChunk(time.Duration) (capturedAudioChunk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chunks) == 0 {
		return capturedAudioChunk{}, fmt.Errorf("test recorder exhausted")
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return chunk, nil
}

func (r *idleTestAudioRecorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, r.stops
}

func newIdleTestRecorder() *idleTestAudioRecorder {
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	r := &idleTestAudioRecorder{}
	for i := 1; i <= 13; i++ {
		r.chunks = append(r.chunks, capturedAudioChunk{
			Source: backgroundListeningSourceSystem, Duration: 5 * time.Second,
			CapturedAt: start.Add(time.Duration(i) * 5 * time.Second), WavData: makeWAV([]int16{0}),
		})
	}
	r.chunks = append(r.chunks, capturedAudioChunk{
		Source: backgroundListeningSourceSystem, Duration: 5 * time.Second,
		CapturedAt: start.Add(2 * time.Minute), WavData: loudPCM(10),
	})
	return r
}

func drainIdleTestSilence(t *testing.T, r *idleSystemAudioRecorder, recorder *idleTestAudioRecorder) {
	t.Helper()
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 13; i++ {
		if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
			t.Fatal(err)
		}
		_, stops := recorder.counts()
		if i <= 12 && stops != 0 {
			t.Fatalf("tap stopped before more than a minute of silence: chunk=%d", i)
		}
	}
	if starts, stops := recorder.counts(); starts != 1 || stops != 1 {
		t.Fatalf("paused tap start/stop counts = %d/%d, want 1/1", starts, stops)
	}
	if len(r.ActiveSources()) != 0 {
		t.Fatal("paused tap still reported active")
	}
}

func TestIdleSystemAudioCapturePausesAndResumesOnOutput(t *testing.T) {
	recorder := newIdleTestRecorder()
	var outputActive atomic.Bool
	polled := make(chan struct{}, 8)
	r := &idleSystemAudioRecorder{
		recorder: recorder, pollInterval: time.Millisecond,
		outputActive: func() (bool, bool) {
			select {
			case polled <- struct{}{}:
			default:
			}
			return outputActive.Load(), true
		},
	}
	defer r.Stop()
	drainIdleTestSilence(t, r, recorder)
	result := make(chan capturedAudioChunk, 1)
	errors := make(chan error, 1)
	go func() {
		chunk, err := r.ReadChunk(backgroundAudioChunkDuration)
		result <- chunk
		errors <- err
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-polled:
		case <-time.After(time.Second):
			t.Fatal("idle capture did not check output activity")
		}
	}
	if starts, _ := recorder.counts(); starts != 1 {
		t.Fatal("silent output restarted the native tap")
	}
	outputActive.Store(true)
	select {
	case chunk := <-result:
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if wavChunkRMS(chunk.WavData) < backgroundAudioNoiseFloor {
			t.Fatal("resumed sound was not forwarded")
		}
	case <-time.After(time.Second):
		t.Fatal("output activity did not wake capture")
	}
	if starts, stops := recorder.counts(); starts != 2 || stops != 1 {
		t.Fatalf("resumed tap start/stop counts = %d/%d, want 2/1", starts, stops)
	}
}

func TestIdleSystemAudioCaptureStopCancelsWake(t *testing.T) {
	recorder := newIdleTestRecorder()
	r := &idleSystemAudioRecorder{
		recorder: recorder, pollInterval: time.Hour,
		outputActive: func() (bool, bool) { return false, true },
	}
	drainIdleTestSilence(t, r, recorder)
	done := make(chan struct{})
	go func() {
		_, _ = r.ReadChunk(backgroundAudioChunkDuration)
		close(done)
	}()
	r.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the idle wake wait")
	}
	if starts, stops := recorder.counts(); starts != 1 || stops != 1 {
		t.Fatalf("stop restarted or stopped an already sleeping tap: %d/%d", starts, stops)
	}
}

func TestIdleSystemAudioCaptureUnknownActivityResumes(t *testing.T) {
	recorder := newIdleTestRecorder()
	known := true
	r := &idleSystemAudioRecorder{
		recorder: recorder, pollInterval: time.Millisecond,
		outputActive: func() (bool, bool) { return false, known },
	}
	defer r.Stop()
	drainIdleTestSilence(t, r, recorder)
	// Uncertainty while already capturing defers the pause. Uncertainty
	// first seen after sleeping must instead wake it conservatively.
	known = false
	if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
		t.Fatal(err)
	}
	if starts, _ := recorder.counts(); starts != 2 {
		t.Fatal("unknown activity stranded capture asleep")
	}
}

func TestIdleSystemAudioCaptureSoundResetsSilenceWindow(t *testing.T) {
	recorder := newIdleTestRecorder()
	// Sound at 60 seconds breaks the initial quiet interval. The silent
	// 65-second chunk must begin a new interval instead of stopping capture.
	recorder.chunks[11].WavData = loudPCM(10)
	r := &idleSystemAudioRecorder{
		recorder: recorder,
		outputActive: func() (bool, bool) {
			t.Fatal("active capture should not poll for a wake signal")
			return false, true
		},
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	for i := 0; i < 13; i++ {
		if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
			t.Fatal(err)
		}
	}
	if starts, stops := recorder.counts(); starts != 1 || stops != 0 {
		t.Fatalf("sound did not reset the silence window: starts=%d stops=%d", starts, stops)
	}
}

func TestIdleSystemAudioKeepsDetectedCallActive(t *testing.T) {
	native := newIdleTestRecorder()
	r := &idleSystemAudioRecorder{recorder: native, outputActive: func() (bool, bool) { return false, true }, keepActive: func() bool { return true }}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	// A quiet call must not tear down the tap even if the OS output query
	// briefly reports no active player.
	for i := 0; i < 13; i++ {
		if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
			t.Fatal(err)
		}
	}
	if r.pauses != 0 {
		t.Fatal("a detected call put its system tap to sleep")
	}
}
