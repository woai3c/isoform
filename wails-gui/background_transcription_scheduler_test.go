package main

import (
	"sync"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

type fakeBackgroundTranscriber struct {
	mu        sync.Mutex
	running   bool
	response  string
	requests  [][]byte
	sources   []backgroundListeningSource
	requestCh chan []byte
}

func newFakeBackgroundTranscriber(response string) *fakeBackgroundTranscriber {
	return &fakeBackgroundTranscriber{
		running:   true,
		response:  response,
		requestCh: make(chan []byte, 8),
	}
}

func (f *fakeBackgroundTranscriber) Transcribe(wavData []byte) (string, error) {
	return f.response, nil
}

func (f *fakeBackgroundTranscriber) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeBackgroundTranscriber) TranscribeBackground(wavData []byte, source string) (string, error) {
	f.mu.Lock()
	f.requests = append(f.requests, wavData)
	f.sources = append(f.sources, backgroundListeningSource(source))
	f.mu.Unlock()
	f.requestCh <- wavData
	return f.response, nil
}

func (f *fakeBackgroundTranscriber) SetBackgroundActive(active bool) {}

func (f *fakeBackgroundTranscriber) Stop() {}

func TestBackgroundTranscriptionSchedulerFlushesOnTargetDuration(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("hello from mic")
	speechCh := make(chan backgroundListeningSource, 1)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber, func(_ string, source backgroundListeningSource, _ speechCaptureContext) {
		speechCh <- source
	}, backgroundTranscriptionSchedulerConfig{
		targetBatchDuration:              10 * time.Second,
		maxAdditionalWaitAfterFirstChunk: time.Hour,
		maxQueueDurationPerSource:        30 * time.Second,
	})
	scheduler.start()
	defer scheduler.stop()

	scheduler.enqueue(testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000))
	scheduler.enqueue(testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000))

	req := waitForSchedulerRequest(t, transcriber.requestCh)
	wantBytes := wavHeaderSize + 2*16000*2
	if len(req) != wantBytes {
		t.Fatalf("merged request bytes = %d, want %d", len(req), wantBytes)
	}
	if got := waitForSpeechSource(t, speechCh); got != backgroundListeningSourceMicrophone {
		t.Fatalf("speech source = %s, want %s", got, backgroundListeningSourceMicrophone)
	}
}

func TestBackgroundTranscriptionSchedulerFlushesOnMaxWait(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("hello from system")
	speechCh := make(chan backgroundListeningSource, 1)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber, func(_ string, source backgroundListeningSource, _ speechCaptureContext) {
		speechCh <- source
	}, backgroundTranscriptionSchedulerConfig{
		targetBatchDuration:              10 * time.Second,
		maxAdditionalWaitAfterFirstChunk: 10 * time.Millisecond,
		maxQueueDurationPerSource:        30 * time.Second,
	})
	scheduler.start()
	defer scheduler.stop()

	scheduler.enqueue(testCapturedChunk(backgroundListeningSourceSystem, 5*time.Second, 16000))

	_ = waitForSchedulerRequest(t, transcriber.requestCh)
	if got := waitForSpeechSource(t, speechCh); got != backgroundListeningSourceSystem {
		t.Fatalf("speech source = %s, want %s", got, backgroundListeningSourceSystem)
	}
	transcriber.mu.Lock()
	gotSource := transcriber.sources[0]
	transcriber.mu.Unlock()
	if gotSource != backgroundListeningSourceSystem {
		t.Fatalf("transcriber source = %s, want %s", gotSource, backgroundListeningSourceSystem)
	}
}

func TestBackgroundTranscriptionSchedulerBoundsQueueDuration(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("queued")
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber, nil, backgroundTranscriptionSchedulerConfig{
		targetBatchDuration:              time.Hour,
		maxAdditionalWaitAfterFirstChunk: time.Hour,
		maxQueueDurationPerSource:        10 * time.Second,
	})

	for i := 0; i < 3; i++ {
		scheduler.enqueue(testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000))
	}

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if got := len(scheduler.queues[backgroundListeningSourceMicrophone]); got != 2 {
		t.Fatalf("queue depth = %d, want 2", got)
	}
	if got := scheduler.durations[backgroundListeningSourceMicrophone]; got != 10*time.Second {
		t.Fatalf("queue duration = %s, want 10s", got)
	}
}

func testCapturedChunk(source backgroundListeningSource, duration time.Duration, samples int) capturedAudioChunk {
	return capturedAudioChunk{
		Source:   source,
		WavData:  makeTestWAV(16000, samples),
		Duration: duration,
	}
}

// TestBackgroundTranscriptionScheduler_PropagatesEchoFlag asserts the
// any-chunk-echo semantics in processBatch: if any chunk in a batch was
// tagged LikelyEcho at capture time, the resulting onSpeech callback
// must receive LikelyEcho=true. This guards against silently dropping
// the echo signal during batch merging.
func TestBackgroundTranscriptionScheduler_PropagatesEchoFlag(t *testing.T) {
	tests := []struct {
		name       string
		echoFlags  []bool
		wantEcho   bool
		wantCalled bool
	}{
		{name: "all_clean", echoFlags: []bool{false, false}, wantEcho: false, wantCalled: true},
		{name: "first_echo", echoFlags: []bool{true, false}, wantEcho: true, wantCalled: true},
		{name: "last_echo", echoFlags: []bool{false, true}, wantEcho: true, wantCalled: true},
		{name: "all_echo", echoFlags: []bool{true, true}, wantEcho: true, wantCalled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transcriber := newFakeBackgroundTranscriber("video commentary")
			ctxCh := make(chan speechCaptureContext, 1)
			scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
				func(_ string, _ backgroundListeningSource, ctx speechCaptureContext) {
					ctxCh <- ctx
				}, backgroundTranscriptionSchedulerConfig{
					targetBatchDuration:              10 * time.Second,
					maxAdditionalWaitAfterFirstChunk: time.Hour,
					maxQueueDurationPerSource:        30 * time.Second,
				})
			scheduler.start()
			defer scheduler.stop()

			for _, echo := range tt.echoFlags {
				chunk := testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000)
				chunk.LikelyEcho = echo
				scheduler.enqueue(chunk)
			}

			select {
			case ctx := <-ctxCh:
				if ctx.LikelyEcho != tt.wantEcho {
					t.Fatalf("LikelyEcho = %v, want %v", ctx.LikelyEcho, tt.wantEcho)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for onSpeech callback")
			}
		})
	}
}

// TestBackgroundTranscriptionScheduler_PropagatesCapturedApp asserts the
// captured app from the first chunk in a batch reaches onSpeech. This
// guards Bug B: attribution must reflect where the audio was recorded,
// not where focus is when transcription finishes.
func TestBackgroundTranscriptionScheduler_PropagatesCapturedApp(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("hello there")
	ctxCh := make(chan speechCaptureContext, 1)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
		func(_ string, _ backgroundListeningSource, ctx speechCaptureContext) {
			ctxCh <- ctx
		}, backgroundTranscriptionSchedulerConfig{
			targetBatchDuration:              10 * time.Second,
			maxAdditionalWaitAfterFirstChunk: time.Hour,
			maxQueueDurationPerSource:        30 * time.Second,
		})
	scheduler.start()
	defer scheduler.stop()

	captureMoment := time.Date(2026, 5, 15, 21, 30, 0, 0, time.UTC)
	first := testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000)
	first.CapturedAt = captureMoment
	first.CapturedApp = activitymon.AppInfo{BundleID: "com.tencent.xinWeChat", AppName: "微信", WindowTitle: "图片和视频"}
	second := testCapturedChunk(backgroundListeningSourceMicrophone, 5*time.Second, 16000)
	second.CapturedAt = captureMoment.Add(5 * time.Second)
	second.CapturedApp = activitymon.AppInfo{BundleID: "com.cmuxterm.app", AppName: "cmux", WindowTitle: "3 Hallucination"}
	scheduler.enqueue(first)
	scheduler.enqueue(second)

	select {
	case ctx := <-ctxCh:
		if ctx.App.AppName != "微信" {
			t.Fatalf("App.AppName = %q, want 微信 (first chunk's app)", ctx.App.AppName)
		}
		if !ctx.CapturedAt.Equal(captureMoment) {
			t.Fatalf("CapturedAt = %v, want %v (first chunk's moment)", ctx.CapturedAt, captureMoment)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onSpeech callback")
	}
}

func TestBackgroundTranscriptionScheduler_PropagatesCapturedMeetingContext(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("late transcript from meeting")
	ctxCh := make(chan speechCaptureContext, 1)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
		func(_ string, _ backgroundListeningSource, ctx speechCaptureContext) {
			ctxCh <- ctx
		}, backgroundTranscriptionSchedulerConfig{
			targetBatchDuration:              10 * time.Second,
			maxAdditionalWaitAfterFirstChunk: time.Hour,
			maxQueueDurationPerSource:        30 * time.Second,
		})
	scheduler.start()
	defer scheduler.stop()

	first := testCapturedChunk(backgroundListeningSourceSystem, 5*time.Second, 16000)
	first.CapturedApp = activitymon.AppInfo{BundleID: "com.googlecode.iterm2", AppName: "iTerm2", WindowTitle: "notes"}
	second := testCapturedChunk(backgroundListeningSourceSystem, 5*time.Second, 16000)
	second.CapturedInMeeting = true
	second.CapturedMeetingAppName = "Microsoft Teams"
	second.CapturedMeetingBundleID = "com.microsoft.teams2"
	scheduler.enqueue(first)
	scheduler.enqueue(second)

	select {
	case ctx := <-ctxCh:
		if !ctx.InMeeting {
			t.Fatal("expected captured meeting context to propagate through ASR scheduler")
		}
		if ctx.MeetingAppName != "Microsoft Teams" {
			t.Fatalf("MeetingAppName = %q, want Microsoft Teams", ctx.MeetingAppName)
		}
		if ctx.MeetingBundleID != "com.microsoft.teams2" {
			t.Fatalf("MeetingBundleID = %q, want com.microsoft.teams2", ctx.MeetingBundleID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onSpeech callback")
	}
}

func TestBackgroundTranscriptionScheduler_PropagatesCandidateMeetingContext(t *testing.T) {
	transcriber := newFakeBackgroundTranscriber("candidate transcript")
	ctxCh := make(chan speechCaptureContext, 1)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
		func(_ string, _ backgroundListeningSource, ctx speechCaptureContext) {
			ctxCh <- ctx
		}, backgroundTranscriptionSchedulerConfig{
			targetBatchDuration:              10 * time.Second,
			maxAdditionalWaitAfterFirstChunk: time.Hour,
			maxQueueDurationPerSource:        30 * time.Second,
		})
	scheduler.start()
	defer scheduler.stop()

	chunk := testCapturedChunk(backgroundListeningSourceSystem, 10*time.Second, 16000)
	chunk.CapturedMeetingCandidate = true
	chunk.CandidateMeetingAppName = "Zoom"
	chunk.CandidateMeetingReason = "candidate_continuation_audio"
	scheduler.enqueue(chunk)

	select {
	case ctx := <-ctxCh:
		if !ctx.MeetingCandidate {
			t.Fatal("expected candidate meeting context to propagate through scheduler")
		}
		if ctx.CandidateMeetingAppName != "Zoom" {
			t.Fatalf("CandidateMeetingAppName = %q, want Zoom", ctx.CandidateMeetingAppName)
		}
		if ctx.CandidateMeetingReason != "candidate_continuation_audio" {
			t.Fatalf("CandidateMeetingReason = %q, want candidate_continuation_audio", ctx.CandidateMeetingReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onSpeech callback")
	}
}

func TestBackgroundTranscriptionScheduler_DoesNotBatchAcrossRecordingEpochs(t *testing.T) {
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(nil, nil, backgroundTranscriptionSchedulerConfig{
		targetBatchDuration:              10 * time.Second,
		maxAdditionalWaitAfterFirstChunk: time.Hour,
		maxQueueDurationPerSource:        time.Minute,
	})
	first := testCapturedChunk(backgroundListeningSourceSystem, 5*time.Second, 16000)
	first.MeetingRecordingEpoch = 3
	second := testCapturedChunk(backgroundListeningSourceSystem, 5*time.Second, 16000)
	second.MeetingRecordingEpoch = 5
	if !scheduler.enqueue(first) || !scheduler.enqueue(second) {
		t.Fatal("failed to enqueue epoch-boundary test chunks")
	}

	scheduler.mu.Lock()
	firstBatch, _ := scheduler.popBatchLocked(backgroundListeningSourceSystem)
	secondBatch, _ := scheduler.popBatchLocked(backgroundListeningSourceSystem)
	scheduler.mu.Unlock()

	if len(firstBatch) != 1 || firstBatch[0].recordingEpoch != 3 {
		t.Fatalf("first batch = %#v, want one epoch-3 chunk", firstBatch)
	}
	if len(secondBatch) != 1 || secondBatch[0].recordingEpoch != 5 {
		t.Fatalf("second batch = %#v, want one epoch-5 chunk", secondBatch)
	}
}

func waitForSchedulerRequest(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transcription request")
		return nil
	}
}

func waitForSpeechSource(t *testing.T, ch <-chan backgroundListeningSource) backgroundListeningSource {
	t.Helper()
	select {
	case source := <-ch:
		return source
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for speech callback")
		return ""
	}
}
