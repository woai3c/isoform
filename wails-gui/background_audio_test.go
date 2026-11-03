package main

import (
	"encoding/binary"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// makeWAV constructs a minimal WAV blob (header + PCM samples) for tests.
// Each int16 sample is little-endian; the header itself is not parsed by
// wavChunkRMS so we can leave it zero-filled.
func makeWAV(samples []int16) []byte {
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(s))
	}
	return append(make([]byte, wavHeaderSize), pcm...)
}

func TestWavChunkRMS_EmptyOrTooSmall(t *testing.T) {
	if got := wavChunkRMS(nil); got != 0 {
		t.Fatalf("nil buffer rms = %v, want 0", got)
	}
	if got := wavChunkRMS(make([]byte, wavHeaderSize)); got != 0 {
		t.Fatalf("header-only buffer rms = %v, want 0", got)
	}
	if got := wavChunkRMS(make([]byte, wavHeaderSize-1)); got != 0 {
		t.Fatalf("truncated buffer rms = %v, want 0", got)
	}
}

func TestWavChunkRMS_SilencePCMIsZero(t *testing.T) {
	silent := makeWAV(make([]int16, 1000)) // all zeros
	if got := wavChunkRMS(silent); got != 0 {
		t.Fatalf("silent pcm rms = %v, want 0", got)
	}
}

func TestWavChunkRMS_ConstantSignalMatchesAmplitude(t *testing.T) {
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = 1000
	}
	got := wavChunkRMS(makeWAV(samples))
	// RMS of a constant signal equals the constant.
	if got < 999 || got > 1001 {
		t.Fatalf("constant 1000 rms = %v, want ~1000", got)
	}
}

// loudPCM returns a WAV blob with a constant amplitude above
// systemAudioAudibleRMSThreshold so captureLoop treats it as audible.
func loudPCM(samples int) []byte {
	s := make([]int16, samples)
	for i := range s {
		s[i] = 2000
	}
	return makeWAV(s)
}

// TestCaptureLoop_MicFirstThenAudibleSystemRetroactivelyMarksEcho
// reproduces the race that reviewers flagged: mic chunk for window W is
// consumed from the recorder channel BEFORE the system_audio chunk for
// the same window. Before the fix, the mic chunk's LikelyEcho stayed
// false because lastSystemAudibleAtUnixNano hadn't been updated yet.
// The pending-mic hold mechanism should let the later-arriving system
// chunk retroactively mark the held mic chunk.
func TestCaptureLoop_MicFirstThenAudibleSystemRetroactivelyMarksEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &stoppableFakeRecorder{chunks: make(chan capturedAudioChunk, 4)}
		captured := make(chan capturedAudioChunk, 4)

		b := &backgroundAudioCapture{
			stopCh:          make(chan struct{}),
			captureLoopDone: make(chan struct{}),
			recorder:        recorder,
			onChunk:         func(c capturedAudioChunk) { captured <- c },
			running:         true,
		}
		defer func() {
			close(b.stopCh)
			recorder.Stop()
			<-b.captureLoopDone
		}()
		go b.captureLoop()

		captureMoment := time.Date(2026, 5, 15, 22, 0, 0, 0, time.UTC)
		// Echo classification must not depend on the live frontmost app or
		// trigger a browser URL lookup while synchronizing the capture loop.
		capturedApp := activitymon.AppInfo{BundleID: "test.capture", AppName: "Capture test"}
		// 1) The mic arrives first. Wait until captureLoop has consumed it
		// and is blocked on the next read, independently of machine load.
		recorder.chunks <- capturedAudioChunk{
			Source:      backgroundListeningSourceMicrophone,
			WavData:     makeWAV(make([]int16, 1000)),
			CapturedAt:  captureMoment,
			CapturedApp: capturedApp,
			Duration:    5 * time.Second,
		}
		synctest.Wait()
		if got := len(captured); got != 0 {
			t.Fatalf("forwarded %d chunks before the sibling system chunk arrived, want the mic held", got)
		}

		// 2) The later system chunk has a capture timestamp 50ms after the
		// mic, within the same window. It must mark the held mic as echo.
		recorder.chunks <- capturedAudioChunk{
			Source:      backgroundListeningSourceSystem,
			WavData:     loudPCM(1000),
			CapturedAt:  captureMoment.Add(50 * time.Millisecond),
			CapturedApp: capturedApp,
			Duration:    5 * time.Second,
		}
		synctest.Wait()
		if got := len(captured); got != 2 {
			t.Fatalf("collected %d chunks, want 2", got)
		}
		collected := []capturedAudioChunk{<-captured, <-captured}

		// Expected order out of captureLoop with the pending-mic hold fix:
		//   [0] first mic (held until sibling system_audio arrives, then
		//       flushed retroactively marked as echo)
		//   [1] the system chunk itself (forwarded right after the held mic)
		if collected[0].Source != backgroundListeningSourceMicrophone {
			t.Fatalf("collected[0].Source = %s, want microphone", collected[0].Source)
		}
		if !collected[0].LikelyEcho {
			t.Fatalf("first-window mic chunk LikelyEcho = false, want true — the sibling system_audio chunk arrived later but within the suppression window. This is the race the pending-mic hold is supposed to close.")
		}
		if collected[1].Source != backgroundListeningSourceSystem {
			t.Fatalf("collected[1].Source = %s, want system_audio", collected[1].Source)
		}
	})
}

// fakeTranscriber is a minimal asr.Transcriber that records every WAV
// blob TranscribeBackground saw and always returns a fixed string. Used
// to assert that scheduled chunks actually reached the transcription
// pipeline during stop()-time flushing.
//
// inFlightCh, when set, lets a test observe the start of a
// transcription call (one element pushed per call). blockCh, when set,
// causes TranscribeBackground to wait until the test closes it — used
// to simulate a Whisper call that's still running when stop() fires.
type fakeTranscriber struct {
	mu         sync.Mutex
	wavBlobs   [][]byte
	inFlightCh chan struct{}
	blockCh    chan struct{}
}

func (f *fakeTranscriber) Transcribe(wavData []byte) (string, error) {
	return "", nil
}
func (f *fakeTranscriber) TranscribeBackground(wavData []byte, source string) (string, error) {
	f.mu.Lock()
	f.wavBlobs = append(f.wavBlobs, append([]byte(nil), wavData...))
	inFlight := f.inFlightCh
	block := f.blockCh
	f.mu.Unlock()
	if inFlight != nil {
		inFlight <- struct{}{}
	}
	if block != nil {
		<-block
	}
	return "user speech", nil
}
func (f *fakeTranscriber) IsRunning() bool          { return true }
func (f *fakeTranscriber) SetBackgroundActive(bool) {}
func (f *fakeTranscriber) Stop()                    {}
func (f *fakeTranscriber) seenBlobs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.wavBlobs)
}

// stoppableFakeRecorder implements the audioRecorder interface and
// supports a clean Stop that unblocks any in-flight ReadChunk by
// closing its source channel.
type stoppableFakeRecorder struct {
	chunks chan capturedAudioChunk
	once   sync.Once
}

func (f *stoppableFakeRecorder) Start() error { return nil }
func (f *stoppableFakeRecorder) Stop() {
	f.once.Do(func() { close(f.chunks) })
}
func (f *stoppableFakeRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{
		backgroundListeningSourceMicrophone,
		backgroundListeningSourceSystem,
	}
}
func (f *stoppableFakeRecorder) ReadChunk(_ time.Duration) (capturedAudioChunk, error) {
	chunk, ok := <-f.chunks
	if !ok {
		// Mirror darwinCombinedAudioRecorder behavior on stop: return
		// an empty chunk rather than an error so the captureLoop empty-
		// chunk-then-stop branch is exercised.
		return capturedAudioChunk{}, nil
	}
	return chunk, nil
}

// TestCaptureLoop_StopFlushesPendingMicIntoScheduler reproduces the
// stop-time race that reviewers flagged: a mic chunk is held in
// pendingMic, stop() is invoked, and the held chunk must still reach
// the transcription scheduler before scheduler teardown. Pre-fix this
// failed because stop() nil-ed b.scheduler before captureLoop's defer
// could enqueue.
func TestCaptureLoop_StopFlushesPendingMicIntoScheduler(t *testing.T) {
	recorder := &stoppableFakeRecorder{chunks: make(chan capturedAudioChunk, 4)}
	transcriber := &fakeTranscriber{}

	// speechEvents captures onSpeech invocations so the test can assert
	// the last chunk reaches the activity-event pipeline, not just the
	// transcriber. Buffered to avoid blocking processBatch.
	speechEvents := make(chan string, 4)
	capture := &backgroundAudioCapture{
		server:   transcriber,
		stopCh:   make(chan struct{}),
		recorder: recorder,
		// onChunk records that the meeting-recorder path also saw the
		// chunk — orthogonal to the scheduler/onSpeech assertion below.
		onChunk: func(c capturedAudioChunk) {},
		onSpeech: func(text string, _ backgroundListeningSource, _ speechCaptureContext) {
			speechEvents <- text
		},
	}
	if err := capture.start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Enqueue a single mic chunk so it ends up in pendingMic awaiting
	// a sibling system_audio chunk that never arrives. The chunk must
	// have a non-trivial wav payload so the scheduler will actually
	// transcribe it (scheduler skips zero-byte chunks).
	recorder.chunks <- capturedAudioChunk{
		Source:     backgroundListeningSourceMicrophone,
		WavData:    loudPCM(16000),
		Duration:   1 * time.Second,
		CapturedAt: time.Now(),
	}

	// Give captureLoop a moment to pick up the chunk and stash it in
	// pendingMic, then trigger stop(). The fix must guarantee that
	// stop() waits for captureLoop's defer to flush pendingMic through
	// forwardChunkDownstream before tearing the scheduler down.
	time.Sleep(100 * time.Millisecond)
	capture.stop()

	// Two assertions, both required for the user-visible fix:
	//   1) The chunk reaches Whisper (fakeTranscriber sees a blob).
	//   2) The resulting transcript fires onSpeech (activity-event path).
	// Pre-fix, processBatch had an unconditional stopCh gate after
	// transcription that short-circuited onSpeech during drain — so
	// the transcriber saw the blob but the speech event was lost.
	if transcriber.seenBlobs() == 0 {
		t.Fatalf("transcriber saw 0 blobs after stop; the held pendingMic chunk did not reach Whisper")
	}
	select {
	case text := <-speechEvents:
		if text == "" {
			t.Fatalf("onSpeech fired with empty text; expected the fake transcriber's response")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onSpeech was never invoked for the drained chunk; the activity-event pipeline missed the final transcript")
	}
}

// TestScheduler_InFlightBatchEmitsOnSpeechAfterStop covers a subtler
// stop-time race that exists in addition to the pendingMic / drain
// scenarios already tested above: a batch was pop-ed by the normal
// nextBatch path and is currently inside TranscribeBackground when
// stop() is invoked. Pre-fix, processBatch had an unconditional stopCh
// gate after transcription that fired here too — the batch was already
// off the queues so the drain path never saw it, and the gate cancelled
// onSpeech. The fix is to drop that gate entirely; scheduler.stop()
// blocks on doneCh, so the in-flight processBatch is allowed to finish
// onSpeech before downstream teardown begins.
func TestScheduler_InFlightBatchEmitsOnSpeechAfterStop(t *testing.T) {
	transcriber := &fakeTranscriber{
		inFlightCh: make(chan struct{}, 1),
		blockCh:    make(chan struct{}),
	}
	speechEvents := make(chan string, 4)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
		func(text string, _ backgroundListeningSource, _ speechCaptureContext) {
			speechEvents <- text
		}, backgroundTranscriptionSchedulerConfig{
			// Trigger batching immediately for whatever's enqueued —
			// the test only cares about the in-flight-during-stop window.
			targetBatchDuration:              0,
			maxAdditionalWaitAfterFirstChunk: 0,
			maxQueueDurationPerSource:        time.Minute,
		})
	scheduler.start()

	// Enqueue a chunk; the run-loop will pop it and enter
	// TranscribeBackground, which then blocks until we close blockCh.
	scheduler.enqueue(testCapturedChunk(backgroundListeningSourceMicrophone, 1*time.Second, 16000))

	select {
	case <-transcriber.inFlightCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Whisper was never invoked; cannot reproduce in-flight stop race")
	}

	// At this point processBatch is mid-TranscribeBackground. Trigger
	// stop() in a separate goroutine because scheduler.stop() blocks
	// on doneCh.
	stopDone := make(chan struct{})
	go func() {
		scheduler.stop()
		close(stopDone)
	}()

	// Give stop() time to set s.stopped and close stopCh. If the bug
	// were present, the still-blocked TranscribeBackground would return
	// after this point and processBatch would observe a closed stopCh.
	time.Sleep(50 * time.Millisecond)

	// Let the Whisper call return; the in-flight processBatch should
	// proceed to fire onSpeech despite stopCh being closed.
	close(transcriber.blockCh)

	select {
	case text := <-speechEvents:
		if text == "" {
			t.Fatalf("onSpeech fired with empty text after stop; expected the transcriber's response")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onSpeech was NOT invoked for the in-flight batch after stop — the post-transcription stop gate is dropping the user's last spoken words")
	}

	// Sanity: stop() should return cleanly after the in-flight batch
	// completes and run-loop sees no more queued chunks.
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler.stop() did not return after the in-flight batch finished")
	}
}

// TestScheduler_StopTimesOutWhenTranscriptionHangs covers the deadlock
// risk reviewers flagged: asr.Transcriber.TranscribeBackground has no
// cancel/context, so a stuck Whisper call would otherwise make
// scheduler.stop() — and any caller awaiting it (capture.stop(),
// StopBackgroundListening, app shutdown) — block forever. The fix
// caps the doneCh wait with cfg.stopDrainTimeout; this test drives a
// permanently-blocked TranscribeBackground and asserts stop() returns
// promptly even though the run-loop goroutine is still stuck inside
// processBatch.
func TestScheduler_StopTimesOutWhenTranscriptionHangs(t *testing.T) {
	transcriber := &fakeTranscriber{
		inFlightCh: make(chan struct{}, 1),
		blockCh:    make(chan struct{}), // never closed during stop → hang
	}
	// onSpeechCalls observes late callbacks. After stop() times out,
	// any onSpeech invocation here represents a leaked transcript
	// reaching the activity pipeline — the bug reviewers flagged.
	onSpeechCalls := make(chan string, 4)
	scheduler := newBackgroundTranscriptionSchedulerWithConfig(transcriber,
		func(text string, _ backgroundListeningSource, _ speechCaptureContext) {
			onSpeechCalls <- text
		},
		backgroundTranscriptionSchedulerConfig{
			targetBatchDuration:              0,
			maxAdditionalWaitAfterFirstChunk: 0,
			maxQueueDurationPerSource:        time.Minute,
			stopDrainTimeout:                 200 * time.Millisecond,
		})
	scheduler.start()

	scheduler.enqueue(testCapturedChunk(backgroundListeningSourceMicrophone, 1*time.Second, 16000))

	select {
	case <-transcriber.inFlightCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Whisper was never invoked; cannot reproduce hung-transcribe scenario")
	}

	// Call stop() and assert it returns within a window comfortably
	// larger than stopDrainTimeout but well below "hung forever". The
	// hung transcriber is still blocked on blockCh; pre-fix this would
	// block scheduler.stop() until blockCh is closed (i.e. forever in
	// production).
	stopReturned := make(chan struct{})
	go func() {
		scheduler.stop()
		close(stopReturned)
	}()

	select {
	case <-stopReturned:
		// Good — stop respected the drain timeout despite the hang.
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler.stop() blocked past the drain timeout — a stuck Whisper call would freeze app shutdown")
	}

	// Now release the blocked transcriber. Pre-fix, processBatch would
	// continue past the (now-completed) Whisper call and invoke
	// onSpeech, leaking a late transcript into the activity pipeline
	// after the caller already considered the scheduler torn down.
	// The abandoned flag should suppress that.
	close(transcriber.blockCh)

	select {
	case text := <-onSpeechCalls:
		t.Fatalf("onSpeech fired with %q AFTER scheduler.stop() timed out — late transcript leaked into the activity pipeline despite the abandoned flag", text)
	case <-time.After(500 * time.Millisecond):
		// Good — the late transcript was dropped, not delivered.
	}
}

func TestWavChunkRMS_AboveAndBelowSystemAudioThreshold(t *testing.T) {
	// Below threshold (300): a quiet noise floor should not flag.
	quiet := make([]int16, 1000)
	for i := range quiet {
		quiet[i] = 100
	}
	if rms := wavChunkRMS(makeWAV(quiet)); rms >= systemAudioAudibleRMSThreshold {
		t.Fatalf("quiet rms=%v incorrectly above audible threshold %v", rms, systemAudioAudibleRMSThreshold)
	}
	// Above threshold: typical playback amplitude should flag.
	loud := make([]int16, 1000)
	for i := range loud {
		loud[i] = 2000
	}
	if rms := wavChunkRMS(makeWAV(loud)); rms < systemAudioAudibleRMSThreshold {
		t.Fatalf("loud rms=%v below audible threshold %v", rms, systemAudioAudibleRMSThreshold)
	}
}

func TestShouldDropBackgroundTranscript_DropsShortLatinHallucination(t *testing.T) {
	if !shouldDropBackgroundTranscript("Thank you") {
		t.Fatal("expected Thank you to be dropped")
	}
	if !shouldDropBackgroundTranscript("as a male.") {
		t.Fatal("expected as a male. to be dropped")
	}
}

func TestShouldDropBackgroundTranscript_DropsRepetitiveTokens(t *testing.T) {
	if !shouldDropBackgroundTranscript("Mama, mama, mama, mama.") {
		t.Fatal("expected repetitive transcript to be dropped")
	}
}

func TestShouldDropBackgroundTranscript_KeepsChineseSpeech(t *testing.T) {
	if shouldDropBackgroundTranscript("这个产品真的这么神奇") {
		t.Fatal("expected Chinese transcript to be kept")
	}
}

func TestShouldDropBackgroundTranscript_KeepsLongEnglishSentence(t *testing.T) {
	if shouldDropBackgroundTranscript("please open the design document after lunch and review it carefully") {
		t.Fatal("expected long English sentence to be kept")
	}
}

func TestShouldDropBackgroundTranscript_KeepsShortEnglishSpeechFragments(t *testing.T) {
	if shouldDropBackgroundTranscript("and we've") {
		t.Fatal("expected short English speech fragment to be kept")
	}
	if shouldDropBackgroundTranscript("coaches but also") {
		t.Fatal("expected spoken English clause to be kept")
	}
}
