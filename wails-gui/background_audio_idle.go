package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	systemAudioIdleAfter       = time.Minute
	systemAudioWakePoll        = 250 * time.Millisecond
	systemAudioResumeRetryBase = time.Second
	systemAudioResumeRetryMax  = 30 * time.Second
)

// withSystemAudioIdlePause wraps only the system source, leaving microphone
// capture and its subscriptions running while the output tap sleeps.
func withSystemAudioIdlePause(recorder audioRecorder, keepActive ...func() bool) audioRecorder {
	if combined, ok := recorder.(*combinedAudioRecorder); ok {
		newRecorders := combined.newRecorders
		combined.newRecorders = func() []audioRecorder {
			recorders := newRecorders()
			for i, candidate := range recorders {
				recorders[i] = withSystemAudioIdlePause(candidate, keepActive...)
			}
			return recorders
		}
		return combined
	}
	if sources := recorder.ActiveSources(); len(sources) != 1 || sources[0] != backgroundListeningSourceSystem {
		return recorder
	}
	wake := platformSystemAudioWakeCheck()
	if wake == nil {
		return recorder
	}
	r := &idleSystemAudioRecorder{recorder: recorder, outputActive: wake, pollInterval: systemAudioWakePoll}
	if len(keepActive) > 0 {
		r.keepActive = keepActive[0]
	}
	return r
}

// idleSystemAudioRecorder releases the native tap after sustained silence.
// Output-process activity, rather than microphone sound, wakes the tap so
// headphones and a paused microphone do not prevent recovery. A process can
// hold its output open while sending silence; in that case we keep the tap
// active to preserve its next onset. The pre-ASR noise gate still saves work.
type idleSystemAudioRecorder struct {
	mu                     sync.Mutex // serializes native Start/Stop and state transitions
	recorder               audioRecorder
	outputActive           func() (active, known bool)
	keepActive             func() bool
	pollInterval           time.Duration
	running                bool
	active                 bool
	stopCh                 chan struct{}
	silentSince            time.Time // owned by the single ReadChunk caller
	pauseDeferred          bool
	pauses                 uint64
	pauseDeferrals         uint64
	resumeFailures         uint64
	suppressedResumeErrors uint64
}

func (r *idleSystemAudioRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return nil
	}
	if err := r.recorder.Start(); err != nil {
		return err
	}
	r.running, r.active = true, true
	r.stopCh = make(chan struct{})
	r.silentSince = time.Time{}
	r.pauseDeferred = false
	r.pauses, r.pauseDeferrals, r.resumeFailures, r.suppressedResumeErrors = 0, 0, 0, 0
	return nil
}

func (r *idleSystemAudioRecorder) ReadChunk(duration time.Duration) (capturedAudioChunk, error) {
	r.mu.Lock()
	running, stopCh := r.running, r.stopCh
	r.mu.Unlock()
	if !running {
		return capturedAudioChunk{}, fmt.Errorf("system audio recorder not initialized")
	}
	if err := r.resumeWhenOutputActive(stopCh); err != nil {
		return capturedAudioChunk{}, err
	}
	select {
	case <-stopCh:
		return capturedAudioChunk{}, nil
	default:
	}
	chunk, err := r.recorder.ReadChunk(duration)
	if err != nil {
		return chunk, err
	}
	if wavChunkMaxWindowRMS(chunk.WavData) >= backgroundAudioNoiseFloor {
		r.silentSince = time.Time{}
		r.mu.Lock()
		r.pauseDeferred = false
		r.mu.Unlock()
		return chunk, nil
	}
	capturedAt := chunk.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	if r.silentSince.IsZero() {
		r.silentSince = capturedAt.Add(-chunk.Duration)
	}
	if capturedAt.Sub(r.silentSince) > systemAudioIdleAfter {
		// A quiet but open output stream may start speaking at any moment.
		// Stopping it just to wake immediately creates churn and loses onset.
		output, known := r.outputActive()
		r.mu.Lock()
		if r.running && r.active {
			if output || !known || (r.keepActive != nil && r.keepActive()) {
				r.pauseDeferrals++
				if !r.pauseDeferred {
					log.Printf("[background-audio] system capture pause deferred: output_active=%t activity_known=%t", output, known)
					r.pauseDeferred = true
				}
			} else {
				r.recorder.Stop()
				r.active = false
				r.pauseDeferred = false
				r.pauses++
				log.Printf("[background-audio] system capture paused after silence: duration=%s", capturedAt.Sub(r.silentSince))
			}
		}
		r.mu.Unlock()
	}
	return chunk, nil
}

func (r *idleSystemAudioRecorder) resumeWhenOutputActive(stopCh <-chan struct{}) error {
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active {
		return nil
	}
	interval := r.pollInterval
	if interval <= 0 {
		interval = systemAudioWakePoll
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	var failures, suppressed uint64
	var lastErrorLog time.Time
	for {
		select {
		case <-stopCh:
			return nil
		case <-timer.C:
			output, known := r.outputActive()
			if known && !output && (r.keepActive == nil || !r.keepActive()) {
				timer.Reset(interval)
				continue
			}
			// If the native activity query fails, fail open for capture:
			// a missing wake signal must never strand the tap asleep.
			r.mu.Lock()
			if !r.running {
				r.mu.Unlock()
				return nil
			}
			if err := r.recorder.Start(); err != nil {
				failures++
				r.resumeFailures++
				delay := systemAudioResumeRetryDelay(failures)
				if lastErrorLog.IsZero() || time.Since(lastErrorLog) >= time.Minute {
					log.Printf("[background-audio] system capture resume failed: failures=%d retry_in=%s suppressed=%d err=%v", failures, delay, suppressed, err)
					lastErrorLog = time.Now()
				} else {
					suppressed++
					r.suppressedResumeErrors++
				}
				r.mu.Unlock()
				// Retry here so the outer capture loop cannot turn each
				// failure into a fresh native start and another log line.
				timer.Reset(delay)
				continue
			}
			r.active = true
			r.silentSince = time.Time{}
			r.mu.Unlock()
			log.Printf("[background-audio] system capture resumed: output_active=%t activity_known=%t failures=%d suppressed=%d", output, known, failures, suppressed)
			return nil
		}
	}
}

func systemAudioResumeRetryDelay(failures uint64) time.Duration {
	delay := systemAudioResumeRetryBase
	for failures > 1 && delay < systemAudioResumeRetryMax {
		delay = min(delay*2, systemAudioResumeRetryMax)
		failures--
	}
	return delay
}

func (r *idleSystemAudioRecorder) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	r.running = false
	close(r.stopCh)
	if r.active {
		r.recorder.Stop()
		r.active = false
	}
	if r.pauses > 0 || r.pauseDeferrals > 0 || r.resumeFailures > 0 {
		log.Printf("[background-audio] system capture idle summary: pauses=%d pause_deferrals=%d resume_failures=%d suppressed=%d", r.pauses, r.pauseDeferrals, r.resumeFailures, r.suppressedResumeErrors)
	}
}

func (r *idleSystemAudioRecorder) ActiveSources() []backgroundListeningSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active && r.stopCh != nil {
		return nil
	}
	return r.recorder.ActiveSources()
}
