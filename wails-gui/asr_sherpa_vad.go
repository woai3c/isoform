//go:build !bindings && ((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"errors"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const (
	sherpaVADForeground = iota
	sherpaVADMicrophone
	sherpaVADSystem
	sherpaVADStreamCount
)

// sherpaVAD owns a detector independently of the recognizer. The narrow
// contract lets lifecycle tests verify reuse without loading native models.
type sherpaVAD interface {
	AcceptWaveform([]float32)
	Flush()
	IsEmpty() bool
	Front() *sherpa.SpeechSegment
	Pop()
	Reset()
	Close()
}

type nativeSherpaVAD struct {
	*sherpa.VoiceActivityDetector
}

func (v *nativeSherpaVAD) Close() {
	if v.VoiceActivityDetector != nil {
		sherpa.DeleteVoiceActivityDetector(v.VoiceActivityDetector)
		v.VoiceActivityDetector = nil
	}
}

func (t *sherpaTranscriber) newVAD(cfg *sherpa.VadModelConfig, bufferSeconds float32) sherpaVAD {
	if t.newVADFunc != nil {
		return t.newVADFunc(cfg, bufferSeconds)
	}
	vad := sherpa.NewVoiceActivityDetector(cfg, bufferSeconds)
	if vad == nil {
		return nil
	}
	return &nativeSherpaVAD{VoiceActivityDetector: vad}
}

// chunkSpeechSegmentsLocked keeps one loaded detector per live channel.
// Scheduler batches can have gaps after silence/privacy filtering, and padded
// decoding uses offsets within this WAV. Reset state after each independent
// WAV so those gaps cannot join speech or carry offsets into the next batch.
// Reset preserves the loaded native model; it does not reconstruct a VAD.
// Long-form recordings own a separate detector for their continuous timeline.
// The caller holds t.mu through detection and decoding.
func (t *sherpaTranscriber) chunkSpeechSegmentsLocked(samples []float32, opts sherpaTranscriptionOptions) ([]*sherpa.SpeechSegment, error) {
	stream := sherpaVADForeground
	if opts.background {
		stream = sherpaVADMicrophone
		if opts.source == string(backgroundListeningSourceSystem) {
			stream = sherpaVADSystem
		}
	}
	cfg := t.vadConfig(opts)
	vad := t.chunkVADs[stream]
	if vad == nil {
		vad = t.newVAD(&cfg, 60)
		if vad == nil {
			return nil, errors.New("sherpa: NewVoiceActivityDetector returned nil")
		}
		t.chunkVADs[stream] = vad
	}
	defer vad.Reset()

	ws := cfg.SileroVad.WindowSize
	for i := 0; i < len(samples); i += ws {
		window := samples[i:min(i+ws, len(samples))]
		if len(window) < ws {
			tail := make([]float32, ws)
			copy(tail, window)
			window = tail
		}
		vad.AcceptWaveform(window)
	}
	vad.Flush()
	var segments []*sherpa.SpeechSegment
	for !vad.IsEmpty() {
		seg := vad.Front()
		vad.Pop()
		if seg != nil && len(seg.Samples) > 0 {
			segments = append(segments, seg)
		}
	}
	return segments, nil
}
