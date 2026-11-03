// Package asr defines the speech-to-text engine interface used by wails-gui.
//
// The wails-gui main package owns engine selection (whisper.cpp via HTTP, or
// sherpa-onnx in-process via cgo). The Transcriber interface lets call sites
// (foreground push-to-talk, background listening capture loop, transcription
// scheduler) talk to whichever engine is wired up without caring which.
package asr

import "time"

// Transcriber is the engine-agnostic speech-to-text contract.
//
// Implementations:
//   - whisperASR (wails-gui/asr_whisper_adapter.go) wraps the whisper.cpp
//     HTTP server subprocess.
//   - sherpaTranscriber (wails-gui/asr_sherpa.go) uses sherpa-onnx in-process.
type Transcriber interface {
	// Transcribe decodes a complete WAV blob and returns plain text.
	// Used by foreground (push-to-talk) and one-shot transcription endpoints.
	Transcribe(wav []byte) (string, error)

	// TranscribeBackground decodes a WAV blob captured by the background
	// listening loop. The source is "microphone" or "system_audio" and
	// engines may apply source-specific tuning (VAD thresholds, hallucination
	// filters). Returns empty string when the engine determines the chunk
	// contains no reliable speech.
	TranscribeBackground(wav []byte, source string) (string, error)

	// IsRunning reports whether the engine is initialized and ready.
	IsRunning() bool

	// SetBackgroundActive flags whether background listening is currently
	// consuming the engine. Engines may use this to keep a subprocess warm
	// or to skip idle-shutdown.
	SetBackgroundActive(active bool)

	// Stop releases engine resources. Safe to call more than once.
	Stop()
}

// Segment is one stretch of speech found in a long recording.
type Segment struct {
	// Start and End are offsets from the start of the recording.
	Start time.Duration
	End   time.Duration
	Text  string
}

// SampleReader hands out a recording's 16 kHz mono samples in order, a
// block at a time, and returns io.EOF once they are used up.
type SampleReader interface {
	ReadSamples() ([]float32, error)
}

// LongFormTranscriber is implemented by engines that can run voice activity
// detection across a whole recording, so speech is cut at its own pauses
// instead of at fixed chunk boundaries, and that report where each segment
// sits. The source is "microphone" or "system_audio", as for
// TranscribeBackground.
type LongFormTranscriber interface {
	TranscribeLongForm(r SampleReader, source string) ([]Segment, error)
}
