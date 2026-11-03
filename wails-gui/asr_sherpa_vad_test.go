//go:build !bindings && ((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"bytes"
	"io"
	"log"
	"strings"
	"testing"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type testSherpaVAD struct {
	samples []float32
	queued  bool
	resets  int
	closes  int
}

func (v *testSherpaVAD) AcceptWaveform(samples []float32) {
	v.samples = append(v.samples, samples...)
}
func (v *testSherpaVAD) Flush()        { v.queued = len(v.samples) > 0 }
func (v *testSherpaVAD) IsEmpty() bool { return !v.queued }
func (v *testSherpaVAD) Front() *sherpa.SpeechSegment {
	return &sherpa.SpeechSegment{Start: 0, Samples: append([]float32(nil), v.samples...)}
}
func (v *testSherpaVAD) Pop() { v.queued = false }
func (v *testSherpaVAD) Reset() {
	v.samples = nil
	v.queued = false
	v.resets++
}
func (v *testSherpaVAD) Close() { v.closes++ }

func newTestSherpaVADTranscriber() *sherpaTranscriber {
	cfg := &sherpa.VadModelConfig{}
	cfg.SileroVad.WindowSize = sherpaVADWindowSize
	cfg.SileroVad.Threshold = sherpaForegroundVADThreshold
	cfg.SileroVad.MinSpeechDuration = sherpaForegroundVADMinSpeech
	return &sherpaTranscriber{vadCfg: cfg}
}

func TestSherpaVADReusesOneDetectorPerStream(t *testing.T) {
	tr := newTestSherpaVADTranscriber()
	var detectors []*testSherpaVAD
	var thresholds []float32
	tr.newVADFunc = func(cfg *sherpa.VadModelConfig, _ float32) sherpaVAD {
		vad := &testSherpaVAD{}
		detectors = append(detectors, vad)
		thresholds = append(thresholds, cfg.SileroVad.Threshold)
		return vad
	}
	opts := []sherpaTranscriptionOptions{
		sherpaBackgroundOptions(string(backgroundListeningSourceMicrophone)),
		sherpaBackgroundOptions(string(backgroundListeningSourceSystem)),
		{},
	}
	// Thirty minutes of five-second chunks per stream loads three models,
	// with independent settings and no retained samples between WAV batches.
	const chunks = 30 * 60 / 5
	for chunk := 0; chunk < chunks; chunk++ {
		for _, opt := range opts {
			samples := make([]float32, 2*sherpaVADWindowSize)
			samples[0] = float32(chunk + 1)
			segments, err := tr.chunkSpeechSegmentsLocked(samples, opt)
			if err != nil {
				t.Fatal(err)
			}
			if len(segments) != 1 || segments[0].Start != 0 || len(segments[0].Samples) != len(samples) || segments[0].Samples[0] != samples[0] {
				t.Fatalf("chunk %d leaked samples or offsets from another WAV: %+v", chunk, segments)
			}
		}
	}
	if len(detectors) != len(opts) {
		t.Fatalf("created %d detectors, want one per stream (%d)", len(detectors), len(opts))
	}
	for i, want := range []float32{sherpaBackgroundMicVADThreshold, sherpaBackgroundSystemVADThreshold, sherpaForegroundVADThreshold} {
		if thresholds[i] != want {
			t.Fatalf("stream %d threshold = %v, want %v", i, thresholds[i], want)
		}
		if detectors[i].resets != chunks || len(detectors[i].samples) != 0 {
			t.Fatalf("stream %d resets = %d, retained samples = %d", i, detectors[i].resets, len(detectors[i].samples))
		}
		if detectors[i].closes != 0 {
			t.Fatalf("stream %d closed its model between chunks", i)
		}
	}
	tr.Stop()
	tr.Stop()
	for i, detector := range detectors {
		if detector.closes != 1 {
			t.Fatalf("stream %d closed %d times, want once", i, detector.closes)
		}
	}
	for _, detector := range tr.chunkVADs {
		if detector != nil {
			t.Fatal("Stop retained a closed detector")
		}
	}
}

func TestSherpaVADRetriesFailedCreation(t *testing.T) {
	tr := newTestSherpaVADTranscriber()
	created := 0
	tr.newVADFunc = func(_ *sherpa.VadModelConfig, _ float32) sherpaVAD {
		created++
		if created == 1 {
			return nil
		}
		return &testSherpaVAD{}
	}
	samples := make([]float32, sherpaVADWindowSize)
	opts := sherpaBackgroundOptions(string(backgroundListeningSourceMicrophone))
	if _, err := tr.chunkSpeechSegmentsLocked(samples, opts); err == nil {
		t.Fatal("failed VAD creation returned no error")
	}
	if _, err := tr.chunkSpeechSegmentsLocked(samples, opts); err != nil {
		t.Fatalf("VAD creation did not recover: %v", err)
	}
	if created != 2 {
		t.Fatalf("VAD creation attempts = %d, want 2", created)
	}
	tr.Stop()
}

type testSherpaSampleReader struct {
	samples []float32
}

func (r *testSherpaSampleReader) ReadSamples() ([]float32, error) {
	samples := r.samples
	r.samples = nil
	return samples, io.EOF
}

func TestSherpaVADLongFormOwnsIndependentDetector(t *testing.T) {
	tr := newTestSherpaVADTranscriber()
	var detectors []*testSherpaVAD
	tr.newVADFunc = func(_ *sherpa.VadModelConfig, _ float32) sherpaVAD {
		vad := &testSherpaVAD{}
		detectors = append(detectors, vad)
		return vad
	}
	samples := make([]float32, sherpaVADWindowSize)
	opts := sherpaBackgroundOptions(string(backgroundListeningSourceMicrophone))
	if _, err := tr.chunkSpeechSegmentsLocked(samples, opts); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := tr.TranscribeLongForm(&testSherpaSampleReader{samples: samples}, opts.source); err != nil {
			t.Fatal(err)
		}
	}
	if len(detectors) != 3 {
		t.Fatalf("created %d detectors, want one live and two separate recording detectors", len(detectors))
	}
	if detectors[0].closes != 0 || detectors[1].closes != 1 || detectors[2].closes != 1 {
		t.Fatal("long-form ownership closed the live detector or leaked a recording detector")
	}
	tr.Stop()
	for _, detector := range detectors {
		if detector.closes != 1 {
			t.Fatalf("detector closed %d times, want once", detector.closes)
		}
	}
}

func TestSherpaDroppedSegmentsKeepTranscriptOutOfLogs(t *testing.T) {
	t.Setenv("YANSU_DEBUG_TRANSCRIPTS", "")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	const privateText = "Private appointment with Alice at 10 tomorrow"
	opts := sherpaBackgroundOptions(string(backgroundListeningSourceMicrophone))
	allowed := sherpaLanguageSet([]string{"en"})
	if sherpaDecodedSegmentAccepted("BGM", "en", privateText, allowed, opts) {
		t.Fatal("non-speech segment accepted")
	}
	if sherpaDecodedSegmentAccepted("Speech", "zh", privateText, allowed, opts) {
		t.Fatal("disallowed language accepted")
	}
	const repetitiveText = "Alice Alice Alice Alice"
	if sherpaDecodedSegmentAccepted("Speech", "en", repetitiveText, allowed, opts) {
		t.Fatal("repetitive background transcript accepted")
	}
	if strings.Contains(output.String(), privateText) || strings.Contains(output.String(), repetitiveText) || strings.Contains(output.String(), "text=") {
		t.Fatalf("dropped segment log contains transcript: %s", output.String())
	}
	if strings.Count(output.String(), "bytes=") != 3 {
		t.Fatalf("dropped segment logs missing size metadata: %s", output.String())
	}
}

func TestSherpaVADFeedsPartialFinalWindow(t *testing.T) {
	tr := newTestSherpaVADTranscriber()
	detector := &testSherpaVAD{}
	tr.newVADFunc = func(*sherpa.VadModelConfig, float32) sherpaVAD { return detector }
	samples := make([]float32, sherpaVADWindowSize+17)
	samples[len(samples)-1] = 0.5
	segments, err := tr.chunkSpeechSegmentsLocked(samples, sherpaBackgroundOptions("microphone"))
	if err != nil || len(segments) != 1 {
		t.Fatalf("segments=%v err=%v", segments, err)
	}
	if len(segments[0].Samples) != 2*sherpaVADWindowSize || segments[0].Samples[len(samples)-1] != 0.5 {
		t.Fatal("the final partial VAD window was lost")
	}
}

type sherpaEOFRestartReader struct{ transcriber *sherpaTranscriber }

func (r sherpaEOFRestartReader) ReadSamples() ([]float32, error) {
	r.transcriber.Stop()
	return make([]float32, sherpaVADWindowSize), io.EOF
}
func TestSherpaLongFormRestartAtEOFKeepsTheRecordingPending(t *testing.T) {
	tr := newTestSherpaVADTranscriber()
	tr.newVADFunc = func(*sherpa.VadModelConfig, float32) sherpaVAD { return &testSherpaVAD{} }
	if _, err := tr.TranscribeLongForm(sherpaEOFRestartReader{transcriber: tr}, "microphone"); err == nil {
		t.Fatal("ASR closed at EOF was acknowledged as a complete transcript")
	}
}
