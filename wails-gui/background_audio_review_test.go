package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// quietSpeechBurst models a brief utterance surrounded by silence in a
// five-second capture. Alternating samples avoid relying on a DC offset.
func quietSpeechBurst(amplitude int16, burst, offset time.Duration) []byte {
	const sampleRate = 16000
	samples := make([]int16, 5*sampleRate)
	start := int(offset * sampleRate / time.Second)
	end := start + int(burst*sampleRate/time.Second)
	for i := start; i < end && i < len(samples); i++ {
		samples[i] = amplitude
		if i%2 != 0 {
			samples[i] = -amplitude
		}
	}
	return makeWAV(samples)
}

func TestBackgroundAudioNoiseGatePreservesQuietSpeechBursts(t *testing.T) {
	for _, example := range []struct {
		amplitude int16
		duration  time.Duration
	}{
		{200, time.Second},
		{120, 2 * time.Second},
		{120, 100 * time.Millisecond},
	} {
		for _, source := range []backgroundListeningSource{backgroundListeningSourceMicrophone, backgroundListeningSourceSystem} {
			t.Run(fmt.Sprintf("%s/%d/%s", source, example.amplitude, example.duration), func(t *testing.T) {
				wav := quietSpeechBurst(example.amplitude, example.duration, 13*time.Millisecond)
				if rms := wavChunkRMS(wav); rms >= backgroundAudioNoiseFloor {
					t.Fatalf("fixture must reproduce dilution below the old whole-chunk threshold: rms=%f", rms)
				}
				transcriber := &fakeTranscriber{}
				scheduler := newBackgroundTranscriptionScheduler(transcriber, nil)
				scheduler.start()
				capture := &backgroundAudioCapture{scheduler: scheduler}
				capture.forwardChunkDownstream(capturedAudioChunk{Source: source, WavData: wav, Duration: backgroundAudioChunkDuration})
				scheduler.stop()
				if calls := transcriber.seenBlobs(); calls != 1 {
					t.Fatalf("quiet utterance reached ASR %d times, want 1", calls)
				}
			})
		}
	}
}

func TestIdleSystemAudioBriefSpeechPreventsSilencePause(t *testing.T) {
	recorder := newIdleTestRecorder()
	for i := 0; i < 13; i++ {
		recorder.chunks[i].WavData = quietSpeechBurst(120, 100*time.Millisecond, 13*time.Millisecond)
	}
	r := &idleSystemAudioRecorder{recorder: recorder, outputActive: func() (bool, bool) { return false, true }}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	for i := 0; i < 13; i++ {
		if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
			t.Fatal(err)
		}
	}
	if _, stops := recorder.counts(); stops != 0 {
		t.Fatalf("tap stopped %d times despite a quiet utterance in each chunk", stops)
	}
}

func TestIdleSystemAudioKeepsOpenSilentOutputStream(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprintf("activity_known_%t", known), func(t *testing.T) {
			recorder := newIdleTestRecorder()
			r := &idleSystemAudioRecorder{recorder: recorder, outputActive: func() (bool, bool) { return known, known }}
			if err := r.Start(); err != nil {
				t.Fatal(err)
			}
			defer r.Stop()
			for i := 0; i < 13; i++ {
				if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
					t.Fatal(err)
				}
			}
			if _, stops := recorder.counts(); stops != 0 {
				t.Fatalf("tap stopped %d times with active or unknown output; the next onset would be lost", stops)
			}
		})
	}
}

func TestBackgroundAudioSilenceSummaryCountsSkippedChunks(t *testing.T) {
	var output bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	capture := &backgroundAudioCapture{}
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 13; i++ {
		capture.forwardChunkDownstream(capturedAudioChunk{
			Source: backgroundListeningSourceMicrophone, WavData: makeWAV([]int16{0}),
			Duration: backgroundAudioChunkDuration, CapturedAt: start.Add(time.Duration(i) * backgroundAudioChunkDuration),
		})
	}
	logs := output.String()
	if !strings.Contains(logs, "capture summary: source=microphone chunks=13 asr_eligible=0 silence_skipped=13") {
		t.Fatalf("missing silence counters: %s", logs)
	}
	if strings.Count(logs, "capture summary:") != 1 {
		t.Fatalf("silent chunks should produce only a minute summary: %s", logs)
	}
}

// The retry tests use Go's virtual clock so native timer code runs unchanged,
// while assertions depend on elapsed simulated time rather than machine load.
func TestIdleSystemAudioResumeRetriesWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		oldWriter := log.Writer()
		log.SetOutput(&output)
		t.Cleanup(func() { log.SetOutput(oldWriter) })
		recorder := newIdleTestRecorder()
		var outputActive atomic.Bool
		var attempts []time.Time
		recorder.startError = func(attempt int) error {
			if attempt == 1 {
				return nil
			}
			attempts = append(attempts, time.Now())
			if attempt <= 3 {
				return fmt.Errorf("resume unavailable")
			}
			return nil
		}
		r := &idleSystemAudioRecorder{
			recorder:     recorder,
			outputActive: func() (bool, bool) { return outputActive.Load(), true },
		}
		defer r.Stop()
		drainIdleTestSilence(t, r, recorder)
		outputActive.Store(true)
		started := time.Now()
		if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
			t.Fatalf("resume failure escaped to the per-chunk error loop instead of retrying with backoff: %v", err)
		}
		if len(attempts) != 3 {
			t.Fatalf("resume attempts = %d, want 3", len(attempts))
		}
		if delay := attempts[0].Sub(started); delay != systemAudioWakePoll {
			t.Fatalf("initial poll delay=%s, want %s", delay, systemAudioWakePoll)
		}
		for i, want := range []time.Duration{time.Second, 2 * time.Second} {
			if delay := attempts[i+1].Sub(attempts[i]); delay != want {
				t.Fatalf("retry %d delay=%s, want %s", i+1, delay, want)
			}
		}
		logs := output.String()
		if strings.Count(logs, "system capture resume failed:") != 1 || !strings.Contains(logs, "failures=2 suppressed=1") {
			t.Fatalf("resume errors should be suppressed and counted on recovery: %s", logs)
		}
	})
}

func TestIdleSystemAudioStopCancelsResumeBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := newIdleTestRecorder()
		recorder.startError = func(attempt int) error {
			if attempt > 1 {
				return fmt.Errorf("resume unavailable")
			}
			return nil
		}
		var outputActive atomic.Bool
		r := &idleSystemAudioRecorder{
			recorder:     recorder,
			outputActive: func() (bool, bool) { return outputActive.Load(), true },
		}
		defer r.Stop()
		drainIdleTestSilence(t, r, recorder)
		outputActive.Store(true)
		result := make(chan error, 1)
		go func() { _, err := r.ReadChunk(backgroundAudioChunkDuration); result <- err }()
		synctest.Wait()
		time.Sleep(systemAudioWakePoll)
		synctest.Wait()
		if starts, _ := recorder.counts(); starts != 2 {
			t.Fatalf("initial resume attempts = %d, want 1", starts-1)
		}
		select {
		case err := <-result:
			t.Fatalf("ReadChunk returned before stop instead of staying in backoff: %v", err)
		default:
		}
		r.Stop()
		synctest.Wait()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("stop should cancel retry cleanly: %v", err)
			}
		default:
			t.Fatal("stop did not cancel resume retry")
		}
		if starts, _ := recorder.counts(); starts != 2 {
			t.Fatalf("stopped recorder retried again: starts=%d", starts)
		}
	})
}

func TestIdleSystemAudioResumeBackoffResetsAfterRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := newIdleTestRecorder()
		recorder.startError = func(attempt int) error {
			if attempt == 2 || attempt == 4 {
				return fmt.Errorf("transient resume failure")
			}
			return nil
		}
		var outputActive atomic.Bool
		r := &idleSystemAudioRecorder{
			recorder:     recorder,
			outputActive: func() (bool, bool) { return outputActive.Load(), true },
		}
		defer r.Stop()
		drainIdleTestSilence(t, r, recorder)
		for cycle := 0; cycle < 2; cycle++ {
			if cycle > 0 {
				outputActive.Store(false)
				recorder.mu.Lock()
				recorder.chunks = append(recorder.chunks, newIdleTestRecorder().chunks...)
				recorder.mu.Unlock()
				for i := 0; i < 13; i++ {
					if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
						t.Fatal(err)
					}
				}
			}
			outputActive.Store(true)
			started := time.Now()
			if _, err := r.ReadChunk(backgroundAudioChunkDuration); err != nil {
				t.Fatalf("resume cycle %d: %v", cycle, err)
			}
			if elapsed, want := time.Since(started), systemAudioWakePoll+time.Second; elapsed != want {
				t.Fatalf("resume cycle %d elapsed=%s, want %s after resetting backoff", cycle, elapsed, want)
			}
		}
		if starts, stops := recorder.counts(); starts != 5 || stops != 2 {
			t.Fatalf("two resume cycles start/stop counts=%d/%d, want 5/2", starts, stops)
		}
	})
}

func TestIdleSystemAudioResumeFailureVolumeOverThirtyMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		oldWriter := log.Writer()
		log.SetOutput(&output)
		t.Cleanup(func() { log.SetOutput(oldWriter) })
		recorder := newIdleTestRecorder()
		recorder.startError = func(attempt int) error {
			if attempt > 1 {
				return fmt.Errorf("resume unavailable")
			}
			return nil
		}
		var outputActive atomic.Bool
		r := &idleSystemAudioRecorder{
			recorder:     recorder,
			outputActive: func() (bool, bool) { return outputActive.Load(), true },
		}
		defer r.Stop()
		drainIdleTestSilence(t, r, recorder)
		outputActive.Store(true)
		result := make(chan error, 1)
		go func() { _, err := r.ReadChunk(backgroundAudioChunkDuration); result <- err }()
		synctest.Wait()
		time.Sleep(30 * time.Minute)
		synctest.Wait()
		// Attempts happen at 0.25, 1.25, 3.25, 7.25, 15.25, 31.25 seconds,
		// then every 30 seconds. The initial successful Start adds one.
		if starts, _ := recorder.counts(); starts != 65 {
			t.Fatalf("30-minute start count=%d, want 65 with capped backoff", starts)
		}
		if summaries := strings.Count(output.String(), "system capture resume failed:"); summaries != 30 {
			t.Fatalf("30-minute error summaries=%d, want 30", summaries)
		}
		r.Stop()
		synctest.Wait()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("stop should cancel persistent resume retry: %v", err)
			}
		default:
			t.Fatal("stop did not release persistent resume retry")
		}
	})
}

func TestIdleSystemAudioResumeRetryDelayCapped(t *testing.T) {
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		if got := systemAudioResumeRetryDelay(uint64(i + 1)); got != want {
			t.Errorf("failure %d delay=%s, want %s", i+1, got, want)
		}
	}
	if got := systemAudioResumeRetryDelay(^uint64(0)); got != 30*time.Second {
		t.Fatalf("long-running failure streak escaped cap: %s", got)
	}
}
