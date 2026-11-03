//go:build !bindings && ((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wails-gui/internal/asr"
	"wails-gui/models"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const (
	sherpaSampleRate = 16000

	envSherpaAllowedLanguages = "YANSU_SHERPA_ALLOWED_LANGS"
	envSherpaLanguageHint     = "YANSU_SHERPA_LANGUAGE"

	sherpaVADMinSilenceDuration = 0.5
	sherpaVADWindowSize         = 512
	sherpaVADMaxSpeechDuration  = 30
	sherpaVADSpeechPad          = 750 * time.Millisecond

	sherpaForegroundVADThreshold       = 0.5
	sherpaForegroundVADMinSpeech       = 0.25
	sherpaBackgroundMicVADThreshold    = 0.5
	sherpaBackgroundMicVADMinSpeech    = 0.25
	sherpaBackgroundSystemVADThreshold = 0.60
	sherpaBackgroundSystemVADMinSpeech = 0.35
	sherpaBackgroundMinSegmentDuration = 850 * time.Millisecond

	// sherpaLongFormMinSegmentDuration is lower than the chunk path's floor:
	// across a whole call, short answers ("Yes.", "OK.") are speech, and the
	// chunk path's floor mostly existed to drop the word fragments that
	// fixed 5-second boundaries cut off.
	sherpaLongFormMinSegmentDuration = 300 * time.Millisecond
	// sherpaLongFormEdgePad is decoded around every long-form segment:
	// Silero marks speech a little late, which clips first consonants
	// ("erfect" for "Perfect"). Half the detector's minimum silence, so two
	// neighboring segments never share audio.
	sherpaLongFormEdgePad = 200 * time.Millisecond
	// sherpaLongFormHistory is how much recent audio the long-form reader
	// keeps behind the detector for padded re-decodes (segments are at most
	// sherpaVADMaxSpeechDuration long).
	sherpaLongFormHistory = 40 * time.Second
)

// sherpaPaths describes where the runtime models live on disk.
type sherpaPaths struct {
	root         string // ~/.yansu-agent/sherpa
	senseVoice   string // <root>/sense-voice/model.int8.onnx
	tokens       string // <root>/sense-voice/tokens.txt
	silero       string // <root>/silero_vad.onnx
	speakerEmbd  string // <root>/speaker/3dspeaker_campplus.onnx
	segmentation string
}

func defaultSherpaPaths() sherpaPaths {
	dataDir := configDirOrFallback()
	root := filepath.Join(dataDir, "models", "sherpa")
	return sherpaPaths{
		root:         root,
		senseVoice:   models.Path(dataDir, "sherpa/sense-voice/model.int8.onnx"),
		tokens:       models.Path(dataDir, "sherpa/sense-voice/tokens.txt"),
		silero:       models.Path(dataDir, "sherpa/silero_vad.onnx"),
		speakerEmbd:  models.Path(dataDir, "sherpa/speaker/3dspeaker_campplus.onnx"),
		segmentation: models.Path(dataDir, "sherpa/speaker/pyannote-segmentation-3.0.int8.onnx"),
	}
}

// sherpaTranscriber implements asr.Transcriber on top of sherpa-onnx
// (SenseVoice int8 + Silero VAD), running in-process via cgo.
type sherpaTranscriber struct {
	mu       sync.Mutex
	recog    *sherpa.OfflineRecognizer
	vadCfg   *sherpa.VadModelConfig
	langs    map[string]struct{}
	langHint string
	paths    sherpaPaths
	closed   atomic.Bool
	bgActive atomic.Bool
	// Independent WAV batches reset detector state, but keep each channel's
	// loaded model until Stop. Access is serialized by mu with recognition.
	chunkVADs  [sherpaVADStreamCount]sherpaVAD
	newVADFunc func(*sherpa.VadModelConfig, float32) sherpaVAD
	// maxDecodeSamples is the longest stretch the model takes in one decode
	// (0: no limit). Longer long-form segments are cut at their quietest
	// point first; Qwen3-ASR, for one, drops what does not fit.
	maxDecodeSamples int
}

// newSherpaTranscriber loads the SenseVoice recognizer and stores VAD config.
// Each live audio channel lazily loads one detector and reuses its model.
// Caller is responsible for ensuring model files exist at the given paths.
func newSherpaTranscriber(paths sherpaPaths, allowedLanguages []string, languageHint string) (*sherpaTranscriber, error) {
	for _, p := range []string{paths.senseVoice, paths.tokens, paths.silero} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("sherpa model file missing: %s", p)
		}
	}

	cfg := &sherpa.OfflineRecognizerConfig{}
	cfg.FeatConfig.SampleRate = 16000
	cfg.FeatConfig.FeatureDim = 80
	cfg.ModelConfig.SenseVoice.Model = paths.senseVoice
	cfg.ModelConfig.SenseVoice.Language = sherpaLanguageHint(languageHint)
	cfg.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
	cfg.ModelConfig.Tokens = paths.tokens
	cfg.ModelConfig.NumThreads = 2
	// SenseVoice's C++ impl rejects anything other than greedy_search
	// (offline-recognizer-sense-voice-impl.h:81). For beam search we'd
	// need to switch architectures (Paraformer / Transducer / Whisper).
	cfg.DecodingMethod = "greedy_search"

	return newSherpaTranscriberWithRecognizer(cfg, paths, allowedLanguages)
}

// newSherpaTranscriberWithRecognizer loads any sherpa offline recognizer
// behind the same Silero VAD and acceptance filters.
func newSherpaTranscriberWithRecognizer(cfg *sherpa.OfflineRecognizerConfig, paths sherpaPaths, allowedLanguages []string) (*sherpaTranscriber, error) {
	recog := sherpa.NewOfflineRecognizer(cfg)
	if recog == nil {
		return nil, errors.New("sherpa: NewOfflineRecognizer returned nil")
	}

	vadCfg := &sherpa.VadModelConfig{}
	vadCfg.SileroVad.Model = paths.silero
	vadCfg.SileroVad.Threshold = sherpaForegroundVADThreshold
	vadCfg.SileroVad.MinSilenceDuration = sherpaVADMinSilenceDuration
	vadCfg.SileroVad.MinSpeechDuration = sherpaForegroundVADMinSpeech
	vadCfg.SileroVad.WindowSize = sherpaVADWindowSize
	vadCfg.SileroVad.MaxSpeechDuration = sherpaVADMaxSpeechDuration
	vadCfg.SampleRate = sherpaSampleRate
	vadCfg.NumThreads = 1

	return &sherpaTranscriber{
		recog:    recog,
		vadCfg:   vadCfg,
		langs:    sherpaLanguageSet(allowedLanguages),
		langHint: cfg.ModelConfig.SenseVoice.Language,
		paths:    paths,
	}, nil
}

// Compile-time check that we satisfy the contract.
var _ asr.Transcriber = (*sherpaTranscriber)(nil)

// Transcribe is the foreground (push-to-talk) entry point. Browser captures
// arrive at arbitrary sample rates (typically 48 kHz). We resample to 16 kHz,
// run Silero VAD, and only decode the speech segments — without VAD the
// recognizer hallucinates short phrases ("Thank you", "嗯") on silence.
func (t *sherpaTranscriber) Transcribe(wav []byte) (string, error) {
	return t.transcribeWAV(wav, sherpaTranscriptionOptions{})
}

// TranscribeBackground decodes a background-capture chunk. Background audio
// always arrives at 16 kHz mono from the platform recorder.
func (t *sherpaTranscriber) TranscribeBackground(wav []byte, source string) (string, error) {
	return t.transcribeWAV(wav, sherpaBackgroundOptions(source))
}

// transcribeWAV is the unified VAD-then-decode path. Both foreground and
// background go through it so they stay symmetric: silence in → empty out.
func (t *sherpaTranscriber) transcribeWAV(wav []byte, opts sherpaTranscriptionOptions) (string, error) {
	if t == nil || t.closed.Load() {
		return "", errors.New("sherpa transcriber is closed")
	}
	samples, rate, err := decodeWAVMonoFloat(wav)
	if err != nil {
		return "", fmt.Errorf("decode wav: %w", err)
	}
	if len(samples) == 0 {
		return "", nil
	}
	if rate != sherpaSampleRate {
		samples = resampleLinearTo16k(samples, rate)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.recog == nil {
		return "", errors.New("sherpa transcriber is closed")
	}

	segments, err := t.chunkSpeechSegmentsLocked(samples, opts)
	if err != nil {
		return "", err
	}

	var parts []string
	for i, seg := range segments {
		ownedFrom, ownedTo := 0, len(samples)
		if i > 0 {
			prev := segments[i-1]
			ownedFrom = (prev.Start + len(prev.Samples) + seg.Start) / 2
		}
		if i+1 < len(segments) {
			ownedTo = (seg.Start + len(seg.Samples) + segments[i+1].Start) / 2
		}
		segDuration := sherpaSamplesDuration(len(seg.Samples))
		if opts.minSegmentDuration > 0 && segDuration > 0 && segDuration < opts.minSegmentDuration {
			log.Printf("[asr] dropped short sherpa segment: source=%s duration=%s min=%s", opts.source, segDuration, opts.minSegmentDuration)
			continue
		}

		res, txt := t.decodeSherpaSamples(seg.Samples)
		if res == nil || txt == "" {
			continue
		}
		if shouldReDecodeSherpaSegmentWithPadding(res.Lang, opts) {
			segmentSamples := ownedDecodeWindow(samples, 0, seg.Start, len(seg.Samples), int(opts.speechPad*sherpaSampleRate/time.Second), ownedFrom, ownedTo)
			if len(segmentSamples) > len(seg.Samples) {
				paddedRes, paddedTxt := t.decodeSherpaSamples(segmentSamples)
				if paddedRes != nil && paddedTxt != "" && sherpaDecodedSegmentAcceptable(paddedRes.Event, paddedRes.Lang, paddedTxt, t.langs, opts) {
					res = paddedRes
					txt = paddedTxt
				}
			}
		}
		if !sherpaDecodedSegmentAccepted(res.Event, res.Lang, txt, t.langs, opts) {
			continue
		}
		parts = append(parts, txt)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, " "), nil
}

var _ asr.LongFormTranscriber = (*sherpaTranscriber)(nil)

// TranscribeLongForm runs Silero VAD over a whole recording read block by
// block, so memory stays bounded for hour-long calls, and decodes every
// speech segment with its offset. The recognizer lock is held per segment,
// not for the whole recording, so live transcription keeps running beside
// it.
func (t *sherpaTranscriber) TranscribeLongForm(r asr.SampleReader, source string) ([]asr.Segment, error) {
	if t == nil || t.closed.Load() || r == nil {
		return nil, errors.New("sherpa transcriber is closed")
	}
	opts := sherpaBackgroundOptions(source)
	opts.background = false // a recorded call keeps real short replies and farewells
	opts.minSegmentDuration = sherpaLongFormMinSegmentDuration
	// The system channel's stricter detector, meant to keep 5-second chunks
	// of background media out, misses short replies ("Good.") in a call.
	opts.vadThreshold = sherpaBackgroundMicVADThreshold
	opts.vadMinSpeech = sherpaBackgroundMicVADMinSpeech
	vadCfg := t.vadConfig(opts)
	vad := t.newVAD(&vadCfg, 60)
	if vad == nil {
		return nil, errors.New("sherpa: NewVoiceActivityDetector returned nil")
	}
	defer vad.Close()

	ws := vadCfg.SileroVad.WindowSize
	keep := int(sherpaLongFormHistory/time.Second) * sherpaSampleRate
	var (
		pending      []float32 // read but not yet fed (less than one window)
		history      []float32 // recent samples, for padded re-decodes
		historyStart int       // absolute index of history[0]
		segments     []asr.Segment
	)
	// Hold one detected segment until the next one's start is known. Its
	// context then stops halfway through the intervening pause; a forced VAD
	// boundary has no shared padding at all.
	var pendingSegment *sherpa.SpeechSegment
	ownedFrom := 0
	decode := func(seg *sherpa.SpeechSegment, from, to int) {
		pieces := t.splitLongSegment(seg.Samples)
		for i, piece := range pieces {
			lo, hi := from, to
			if i > 0 {
				lo = seg.Start + piece[0]
			}
			if i+1 < len(pieces) {
				hi = seg.Start + piece[1]
			}
			part := &sherpa.SpeechSegment{Start: seg.Start + piece[0], Samples: seg.Samples[piece[0]:piece[1]]}
			if s, ok := t.decodeLongFormSegment(part, history, historyStart, opts, len(pieces) > 1, lo, hi); ok {
				segments = append(segments, s)
			}
		}
	}
	drain := func() {
		for !vad.IsEmpty() {
			seg := vad.Front()
			vad.Pop()
			if seg == nil || len(seg.Samples) == 0 {
				continue
			}
			if pendingSegment != nil {
				boundary := (pendingSegment.Start + len(pendingSegment.Samples) + seg.Start) / 2
				decode(pendingSegment, ownedFrom, boundary)
				ownedFrom = boundary
			}
			pendingSegment = seg
		}
	}

	for {
		if t.closed.Load() {
			return segments, errors.New("sherpa transcriber is closed")
		}
		block, err := r.ReadSamples()
		if len(block) > 0 {
			pending = append(pending, block...)
			history = append(history, block...)
			n := len(pending) / ws * ws
			for i := 0; i < n; i += ws {
				vad.AcceptWaveform(pending[i : i+ws])
			}
			pending = append([]float32(nil), pending[n:]...)
			drain()
			if pendingSegment != nil && historyStart+len(history)-pendingSegment.Start-len(pendingSegment.Samples) > (sherpaVADMaxSpeechDuration+1)*sherpaSampleRate {
				boundary := pendingSegment.Start + len(pendingSegment.Samples) + int(opts.speechPad*sherpaSampleRate/time.Second)
				decode(pendingSegment, ownedFrom, boundary)
				ownedFrom = boundary
				pendingSegment = nil
			}
			if len(history) > 2*keep {
				drop := len(history) - keep
				if pendingSegment != nil {
					drop = min(drop, max(0, pendingSegment.Start-historyStart-int(opts.speechPad*sherpaSampleRate/time.Second)))
				}
				history = append([]float32(nil), history[drop:]...)
				historyStart += drop
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return segments, err
		}
	}
	if len(pending) > 0 {
		tail := make([]float32, ws)
		copy(tail, pending)
		vad.AcceptWaveform(tail)
	}
	vad.Flush()
	drain()
	if pendingSegment != nil {
		decode(pendingSegment, ownedFrom, historyStart+len(history))
	}
	// Stop can race EOF and the final decode. Never acknowledge a partial
	// pass as complete just because there is no next ReadSamples iteration.
	if t.closed.Load() {
		return nil, errors.New("sherpa transcriber closed during recording decode")
	}
	return segments, nil
}

// sherpaSplitMinTail: a last piece shorter than this joins the piece before
// it when both fit the model.
const sherpaSplitMinTail = time.Second

// splitLongSegment cuts a segment the model cannot take whole into pieces
// that fit with their edge padding, cutting in the middle of the pauses a
// finer voice-activity pass finds; only a stretch with no pause at all is
// cut at its quietest point.
func (t *sherpaTranscriber) splitLongSegment(samples []float32) [][2]int {
	if t.maxDecodeSamples <= 0 || len(samples) <= t.maxDecodeSamples {
		return [][2]int{{0, len(samples)}}
	}
	limit := t.maxDecodeSamples - 2*int(sherpaLongFormEdgePad*sherpaSampleRate/time.Second)
	return packSpeechSpans(samples, t.speechSpans(samples), limit, int(sherpaSplitMinTail*sherpaSampleRate/time.Second), t.maxDecodeSamples)
}

// speechSpans finds the speech inside a segment with a finer detector
// (short pauses count), as [from, to) sample ranges.
func (t *sherpaTranscriber) speechSpans(samples []float32) [][2]int {
	cfg := *t.vadCfg
	cfg.SileroVad = t.vadCfg.SileroVad
	cfg.SileroVad.Threshold = sherpaBackgroundMicVADThreshold
	cfg.SileroVad.MinSilenceDuration = 0.12
	cfg.SileroVad.MinSpeechDuration = 0.1
	cfg.SileroVad.MaxSpeechDuration = 120
	seconds := float32(len(samples))/sherpaSampleRate + 2
	vad := sherpa.NewVoiceActivityDetector(&cfg, seconds)
	if vad == nil {
		return nil
	}
	defer sherpa.DeleteVoiceActivityDetector(vad)
	ws := cfg.SileroVad.WindowSize
	for i := 0; i+ws <= len(samples); i += ws {
		vad.AcceptWaveform(samples[i : i+ws])
	}
	vad.Flush()
	var spans [][2]int
	for !vad.IsEmpty() {
		seg := vad.Front()
		vad.Pop()
		if seg != nil && len(seg.Samples) > 0 {
			spans = append(spans, [2]int{seg.Start, seg.Start + len(seg.Samples)})
		}
	}
	return spans
}

// packSpeechSpans cuts samples into [from, to) pieces of at most limit
// samples, in the middle of the pauses between the speech spans, each piece
// as long as it can be. A stretch with no pause short enough is cut at its
// quietest 20 ms. A last piece shorter than minTail joins the one before it
// when the two stay within hardMax.
func packSpeechSpans(samples []float32, spans [][2]int, limit, minTail, hardMax int) [][2]int {
	n := len(samples)
	if limit <= 0 || n <= limit {
		return [][2]int{{0, n}}
	}
	var pauses []int
	for i := 1; i < len(spans); i++ {
		if gapStart, gapEnd := spans[i-1][1], spans[i][0]; gapEnd > gapStart {
			pauses = append(pauses, (gapStart+gapEnd)/2)
		}
	}
	var out [][2]int
	start := 0
	for n-start > limit {
		cut := -1
		for _, p := range pauses {
			if p > start && p <= start+limit {
				cut = p
			}
		}
		if cut < 0 {
			cut = quietestCut(samples, start+limit*2/3, start+limit)
		}
		out = append(out, [2]int{start, cut})
		start = cut
	}
	out = append(out, [2]int{start, n})
	if last := len(out) - 1; last > 0 && out[last][1]-out[last][0] < minTail && out[last][1]-out[last-1][0] <= hardMax {
		out[last-1][1] = out[last][1]
		out = out[:last]
	}
	return out
}

// quietestCut returns the middle of the quietest 20 ms in [from, to).
func quietestCut(samples []float32, from, to int) int {
	const frame = sherpaSampleRate / 50
	cut := to
	quietest := math.MaxFloat64
	for f := from; f+frame <= to && f+frame <= len(samples); f += frame {
		var energy float64
		for _, v := range samples[f : f+frame] {
			energy += float64(v) * float64(v)
		}
		if energy < quietest {
			quietest = energy
			cut = f + frame/2
		}
	}
	return cut
}

// decodeLongFormSegment decodes one detected segment the way transcribeWAV
// does, with the same acceptance filters. A piece of a split segment is
// never too short: the segment as a whole passed that check.
func (t *sherpaTranscriber) decodeLongFormSegment(seg *sherpa.SpeechSegment, history []float32, historyStart int, opts sherpaTranscriptionOptions, piece bool, ownedFrom, ownedTo int) (asr.Segment, bool) {
	if seg == nil || len(seg.Samples) == 0 {
		return asr.Segment{}, false
	}
	duration := sherpaSamplesDuration(len(seg.Samples))
	if !piece && opts.minSegmentDuration > 0 && duration < opts.minSegmentDuration {
		return asr.Segment{}, false
	}

	samples := seg.Samples
	if rel := seg.Start - historyStart; rel >= 0 {
		if padded := ownedDecodeWindow(history, historyStart, seg.Start, len(seg.Samples), int(t.fittingPad(sherpaLongFormEdgePad, len(seg.Samples))*sherpaSampleRate/time.Second), ownedFrom, ownedTo); len(padded) > len(samples) {
			samples = padded
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.recog == nil {
		return asr.Segment{}, false
	}
	res, txt := t.decodeSherpaSamples(samples)
	if res == nil || txt == "" {
		return asr.Segment{}, false
	}
	if shouldReDecodeSherpaSegmentWithPadding(res.Lang, opts) {
		if rel := seg.Start - historyStart; rel >= 0 {
			segmentSamples := ownedDecodeWindow(history, historyStart, seg.Start, len(seg.Samples), int(t.fittingPad(opts.speechPad, len(seg.Samples))*sherpaSampleRate/time.Second), ownedFrom, ownedTo)
			if len(segmentSamples) > len(seg.Samples) {
				paddedRes, paddedTxt := t.decodeSherpaSamples(segmentSamples)
				if paddedRes != nil && paddedTxt != "" && sherpaDecodedSegmentAcceptable(paddedRes.Event, paddedRes.Lang, paddedTxt, t.langs, opts) {
					res = paddedRes
					txt = paddedTxt
				}
			}
		}
	}
	// Qwen3-ASR answers "language <name><asr_text>..."; a bare "language"
	// is a decode that produced no text.
	if strings.EqualFold(txt, "language") {
		return asr.Segment{}, false
	}
	if !sherpaDecodedSegmentAccepted(res.Event, res.Lang, txt, t.langs, opts) {
		return asr.Segment{}, false
	}
	start := sherpaSamplesDuration(seg.Start)
	return asr.Segment{Start: start, End: start + duration, Text: txt}, true
}

func (t *sherpaTranscriber) decodeSherpaSamples(samples []float32) (*sherpa.OfflineRecognizerResult, string) {
	if len(samples) == 0 {
		return nil, ""
	}
	stream := sherpa.NewOfflineStream(t.recog)
	if stream == nil {
		return nil, ""
	}
	stream.AcceptWaveform(sherpaSampleRate, samples)
	t.recog.Decode(stream)
	res := stream.GetResult()
	sherpa.DeleteOfflineStream(stream)
	if res == nil {
		return nil, ""
	}
	return res, strings.TrimSpace(stripSenseVoiceTags(res.Text))
}

func (t *sherpaTranscriber) IsRunning() bool {
	return t != nil && !t.closed.Load()
}

func (t *sherpaTranscriber) SetBackgroundActive(active bool) {
	if t == nil {
		return
	}
	t.bgActive.Store(active)
}

func (t *sherpaTranscriber) Stop() {
	if t == nil || !t.closed.CompareAndSwap(false, true) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, vad := range t.chunkVADs {
		if vad != nil {
			vad.Close()
			t.chunkVADs[i] = nil
		}
	}
	if t.recog != nil {
		sherpa.DeleteOfflineRecognizer(t.recog)
		t.recog = nil
	}
}

type sherpaTranscriptionOptions struct {
	background         bool
	source             string
	speechPad          time.Duration
	minSegmentDuration time.Duration
	vadThreshold       float32
	vadMinSpeech       float32
}

func sherpaBackgroundOptions(source string) sherpaTranscriptionOptions {
	opts := sherpaTranscriptionOptions{
		background:         true,
		source:             strings.TrimSpace(source),
		speechPad:          sherpaVADSpeechPad,
		minSegmentDuration: sherpaBackgroundMinSegmentDuration,
		vadThreshold:       sherpaBackgroundMicVADThreshold,
		vadMinSpeech:       sherpaBackgroundMicVADMinSpeech,
	}
	if opts.source == "" {
		opts.source = string(backgroundListeningSourceMicrophone)
	}
	if opts.source == string(backgroundListeningSourceSystem) {
		opts.vadThreshold = sherpaBackgroundSystemVADThreshold
		opts.vadMinSpeech = sherpaBackgroundSystemVADMinSpeech
	}
	return opts
}

func (t *sherpaTranscriber) vadConfig(opts sherpaTranscriptionOptions) sherpa.VadModelConfig {
	cfg := *t.vadCfg
	cfg.SileroVad = t.vadCfg.SileroVad
	if opts.vadThreshold > 0 {
		cfg.SileroVad.Threshold = opts.vadThreshold
	}
	if opts.vadMinSpeech > 0 {
		cfg.SileroVad.MinSpeechDuration = opts.vadMinSpeech
	}
	return cfg
}

func sherpaSamplesDuration(sampleCount int) time.Duration {
	if sampleCount <= 0 {
		return 0
	}
	return time.Duration(sampleCount) * time.Second / sherpaSampleRate
}

func paddedSherpaSegmentSamples(samples []float32, start, length int, pad time.Duration) []float32 {
	if len(samples) == 0 || length <= 0 {
		return nil
	}
	padSamples := int(pad * sherpaSampleRate / time.Second)
	if padSamples < 0 {
		padSamples = 0
	}
	from := start - padSamples
	if from < 0 {
		from = 0
	}
	to := start + length + padSamples
	if to > len(samples) {
		to = len(samples)
	}
	if from >= to {
		return nil
	}
	return samples[from:to]
}

func sherpaDecodedSegmentAcceptable(event, lang, text string, allowed map[string]struct{}, opts sherpaTranscriptionOptions) bool {
	return isSherpaSpeechEvent(event) &&
		sherpaLanguageAllowed(allowed, lang) &&
		(!opts.background || !shouldDropBackgroundTranscript(text))
}

func shouldReDecodeSherpaSegmentWithPadding(lang string, opts sherpaTranscriptionOptions) bool {
	if opts.speechPad <= 0 {
		return false
	}
	// SenseVoice tags Chinese "zh"/"yue"; Qwen3-ASR names the language.
	switch normalizeSenseVoiceTag(lang) {
	case "zh", "yue", "chinese", "mandarin", "cantonese":
		return true
	default:
		return false
	}
}

// fittingPad shortens a decode padding so the padded segment still fits the
// model's window.
func (t *sherpaTranscriber) fittingPad(pad time.Duration, length int) time.Duration {
	if t.maxDecodeSamples <= 0 {
		return pad
	}
	room := (t.maxDecodeSamples - length) / 2
	if room <= 0 {
		return 0
	}
	if fit := sherpaSamplesDuration(room); fit < pad {
		return fit
	}
	return pad
}

func sherpaDecodedSegmentAccepted(event, lang, text string, allowed map[string]struct{}, opts sherpaTranscriptionOptions) bool {
	if !isSherpaSpeechEvent(event) {
		log.Printf("[asr] dropped non-speech sherpa segment: source=%s event=%s lang=%s bytes=%d%s", opts.source, event, lang, len(text), backgroundTranscriptDebugLogSuffix(text))
		return false
	}
	if !sherpaLanguageAllowed(allowed, lang) {
		log.Printf("[asr] dropped disallowed-language sherpa segment: source=%s event=%s lang=%s allowed=%s bytes=%d%s", opts.source, event, lang, formatSherpaLanguages(allowed), len(text), backgroundTranscriptDebugLogSuffix(text))
		return false
	}
	if opts.background && shouldDropBackgroundTranscript(text) {
		log.Printf("[asr] dropped likely non-speech sherpa segment: source=%s event=%s lang=%s bytes=%d%s", opts.source, event, lang, len(text), backgroundTranscriptDebugLogSuffix(text))
		return false
	}
	return true
}

func normalizeSenseVoiceTag(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "<|")
	s = strings.TrimSuffix(s, "|>")
	return s
}

func isSherpaSpeechEvent(event string) bool {
	normalized := normalizeSenseVoiceTag(event)
	return normalized == "" || normalized == "speech"
}

func sherpaLanguageAllowed(allowed map[string]struct{}, lang string) bool {
	if allowed == nil {
		return true
	}
	normalized := normalizeSenseVoiceTag(lang)
	if normalized == "" {
		return true
	}
	_, ok := allowed[normalized]
	return ok
}

func sherpaLanguageSet(languages []string) map[string]struct{} {
	if len(languages) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		normalized := normalizeSenseVoiceTag(lang)
		if normalized == "" {
			continue
		}
		if normalized == "all" || normalized == "*" {
			return nil
		}
		set[normalized] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func sherpaAllowedLanguagesForUILanguage(uiLanguage string) []string {
	if env := strings.TrimSpace(os.Getenv(envSherpaAllowedLanguages)); env != "" {
		return splitSherpaLanguageList(env)
	}
	switch normalizeUILanguage(uiLanguage) {
	case "zh-CN":
		return []string{"zh", "yue", "en"}
	default:
		return []string{"en", "zh", "yue"}
	}
}

func sherpaLanguageHintForUILanguage(uiLanguage string) string {
	if env := strings.TrimSpace(os.Getenv(envSherpaLanguageHint)); env != "" {
		return sherpaLanguageHint(env)
	}
	// Default to "auto" — let the SenseVoice model pick per-segment instead
	// of hard-biasing toward one language. UI language is a poor proxy for
	// audio language: a Chinese-speaking user with an English UI (common
	// among bilingual developers) would otherwise get "en" hint, which
	// pushes the model to romanize Chinese proper nouns ("天津" → "Tianjin").
	// "auto" eliminates that bias at the cost of a small quality dip on
	// genuinely monolingual audio — empirically negligible for sense-voice.
	// Users with a strong preference can pin via YANSU_SHERPA_LANGUAGE.
	_ = uiLanguage
	return "auto"
}

func sherpaLanguageHint(language string) string {
	normalized := normalizeSenseVoiceTag(language)
	switch normalized {
	case "zh", "en", "ja", "ko", "yue", "auto":
		return normalized
	default:
		return "auto"
	}
}

func splitSherpaLanguageList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
}

func formatSherpaLanguages(languages map[string]struct{}) string {
	if languages == nil {
		return "all"
	}
	keys := make([]string, 0, len(languages))
	for lang := range languages {
		keys = append(keys, lang)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// resampleLinearTo16k linearly interpolates float32 samples from srcRate to
// 16 kHz. Naive (no anti-alias filter) but adequate for speech: signal energy
// in human voice sits well below 8 kHz Nyquist, so aliasing artifacts have
// negligible impact on Silero VAD or SenseVoice output. Used to align both
// foreground (browser, typically 48 kHz) and any non-16 kHz background
// chunks to the rate Silero VAD requires.
func resampleLinearTo16k(samples []float32, srcRate int) []float32 {
	if srcRate == 16000 || len(samples) == 0 {
		return samples
	}
	ratio := 16000.0 / float64(srcRate)
	n := int(float64(len(samples)) * ratio)
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		srcPos := float64(i) / ratio
		idx := int(srcPos)
		if idx >= len(samples)-1 {
			out[i] = samples[len(samples)-1]
			continue
		}
		frac := float32(srcPos - float64(idx))
		out[i] = samples[idx]*(1-frac) + samples[idx+1]*frac
	}
	return out
}

// stripSenseVoiceTags removes SenseVoice control tags such as <|zh|>,
// <|NEUTRAL|>, <|Speech|> from the recognizer text.
func stripSenseVoiceTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inside := false
	for i := 0; i < len(s); i++ {
		switch {
		case !inside && i+1 < len(s) && s[i] == '<' && s[i+1] == '|':
			inside = true
			i++
		case inside && i+1 < len(s) && s[i] == '|' && s[i+1] == '>':
			inside = false
			i++
		case !inside:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(b.String())
}

// decodeWAVMonoFloat parses a minimal 16-bit PCM WAV (mono or stereo) blob
// and returns float32 samples in [-1, 1] plus the sample rate. Stereo input
// is downmixed to mono by averaging channels.
func decodeWAVMonoFloat(wav []byte) (samples []float32, sampleRate int, err error) {
	if len(wav) < 44 {
		return nil, 0, fmt.Errorf("wav too short: %d bytes", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a RIFF/WAVE file")
	}

	var (
		fmtFound  bool
		dataChunk []byte
		channels  uint16
		bps       uint16
		audioFmt  uint16
	)
	off := 12
	for off+8 <= len(wav) {
		id := string(wav[off : off+4])
		size := int(binary.LittleEndian.Uint32(wav[off+4 : off+8]))
		off += 8
		if size < 0 || off+size > len(wav) {
			return nil, 0, fmt.Errorf("invalid chunk %q size=%d", id, size)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, fmt.Errorf("fmt chunk too small: %d", size)
			}
			audioFmt = binary.LittleEndian.Uint16(wav[off : off+2])
			channels = binary.LittleEndian.Uint16(wav[off+2 : off+4])
			sampleRate = int(binary.LittleEndian.Uint32(wav[off+4 : off+8]))
			bps = binary.LittleEndian.Uint16(wav[off+14 : off+16])
			fmtFound = true
		case "data":
			dataChunk = wav[off : off+size]
		}
		// Pad to even size per RIFF spec.
		off += size
		if size%2 == 1 {
			off++
		}
	}
	if !fmtFound {
		return nil, 0, errors.New("missing fmt chunk")
	}
	if dataChunk == nil {
		return nil, 0, errors.New("missing data chunk")
	}
	if audioFmt != 1 {
		return nil, 0, fmt.Errorf("unsupported audio format: %d (only PCM=1 supported)", audioFmt)
	}
	if bps != 16 {
		return nil, 0, fmt.Errorf("unsupported bit depth: %d (only 16-bit supported)", bps)
	}
	if channels != 1 && channels != 2 {
		return nil, 0, fmt.Errorf("unsupported channel count: %d", channels)
	}

	totalFrames := len(dataChunk) / int(channels) / 2
	samples = make([]float32, totalFrames)
	const scale = float32(1.0 / 32768.0)
	switch channels {
	case 1:
		for i := 0; i < totalFrames; i++ {
			s := int16(binary.LittleEndian.Uint16(dataChunk[i*2 : i*2+2]))
			samples[i] = float32(s) * scale
		}
	case 2:
		for i := 0; i < totalFrames; i++ {
			l := int16(binary.LittleEndian.Uint16(dataChunk[i*4 : i*4+2]))
			r := int16(binary.LittleEndian.Uint16(dataChunk[i*4+2 : i*4+4]))
			samples[i] = (float32(l) + float32(r)) * 0.5 * scale
		}
	}
	return samples, sampleRate, nil
}

// initSherpaTranscriber loads sherpa with model files from defaultSherpaPaths()
// and logs readiness. Used by InitVoiceInput.
func initSherpaTranscriber(uiLanguage string) (asr.Transcriber, error) {
	paths := defaultSherpaPaths()
	allowedLanguages := sherpaAllowedLanguagesForUILanguage(uiLanguage)
	languageHint := sherpaLanguageHintForUILanguage(uiLanguage)
	start := time.Now()
	t, err := newSherpaTranscriber(paths, allowedLanguages, languageHint)
	if err != nil {
		return nil, err
	}
	log.Printf("[asr] sherpa-onnx ready (model=%s vad=%s lang_hint=%s langs=%s load=%s)",
		paths.senseVoice, paths.silero, t.langHint, formatSherpaLanguages(t.langs), time.Since(start).Round(time.Millisecond))
	return t, nil
}
