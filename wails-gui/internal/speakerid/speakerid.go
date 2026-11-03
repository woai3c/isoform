// Package speakerid wraps sherpa-onnx's SpeakerEmbeddingExtractor for use as
// the second layer of identity attribution on captured audio: ASR tells us
// WHAT was said, this tells us WHO said it.
//
// The standard flow is:
//
//  1. Enrollment — the user records a short reference clip (~10s) on a known
//     handle (Self, or a labeled Contact). ExtractFromPCM produces a fixed-
//     dimension float32 embedding that gets persisted alongside the principal.
//  2. Verification — when a fresh microphone chunk arrives during a session,
//     ExtractFromPCM is run again and the resulting embedding is compared to
//     enrolled embeddings via CosineSimilarity. A high score (~> 0.7) means
//     "same person as enrollment"; a low score means "someone else".
//
// The package is intentionally engine-only — it doesn't decide thresholds,
// load WAV files, or persist anything. That lives in the wiring layer.
package speakerid

import (
	"errors"
	"fmt"
	"math"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Extractor wraps the sherpa-onnx C struct. Safe for concurrent use across
// goroutines because the Compute() path uses its own per-call OnlineStream
// guarded by a mutex on the shared extractor handle.
type Extractor struct {
	mu  sync.Mutex
	ext *sherpa.SpeakerEmbeddingExtractor
	dim int
}

// Config is the user-facing configuration. Only Model is required.
type Config struct {
	// Model is the absolute path to a sherpa-compatible speaker embedding
	// ONNX file (e.g. 3D-Speaker CAM++, WeSpeaker, NeMo TitaNet).
	Model string
	// NumThreads is the per-extractor thread count. 1 is sufficient for the
	// in-process verification flow (one extraction per speech chunk).
	NumThreads int
	// Debug enables verbose sherpa logging when nonzero. Off by default.
	Debug bool
}

// New loads the speaker model and returns a ready extractor. The caller must
// invoke Close() to release the underlying C resources.
func New(cfg Config) (*Extractor, error) {
	if cfg.Model == "" {
		return nil, errors.New("speakerid: empty model path")
	}
	numThreads := cfg.NumThreads
	if numThreads <= 0 {
		numThreads = 1
	}
	debug := 0
	if cfg.Debug {
		debug = 1
	}
	ext := sherpa.NewSpeakerEmbeddingExtractor(&sherpa.SpeakerEmbeddingExtractorConfig{
		Model:      cfg.Model,
		NumThreads: numThreads,
		Debug:      debug,
		Provider:   "cpu",
	})
	if ext == nil {
		return nil, fmt.Errorf("speakerid: failed to load model %q", cfg.Model)
	}
	return &Extractor{ext: ext, dim: ext.Dim()}, nil
}

// Close releases the underlying sherpa resources. Safe to call multiple times.
func (e *Extractor) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ext != nil {
		sherpa.DeleteSpeakerEmbeddingExtractor(e.ext)
		e.ext = nil
	}
}

// Dim returns the embedding dimension of the loaded model (typically 192,
// 256, or 512). Useful for callers that need to pre-size storage.
func (e *Extractor) Dim() int { return e.dim }

// ExtractFromPCM runs the embedding extractor on a single utterance worth
// of mono 16-bit-equivalent float32 PCM samples (range -1..1) at the given
// sample rate (typically 16000 Hz; the model's training rate). Returns a
// dense float32 vector of length Dim().
//
// The caller is responsible for pre-segmentation — pass speech audio only
// (i.e. after VAD), not raw chunks with leading/trailing silence. A clip
// shorter than ~1.5s tends to produce noisy embeddings; ~3-10s is the
// sweet spot for reliable verification.
func (e *Extractor) ExtractFromPCM(samples []float32, sampleRate int) ([]float32, error) {
	if len(samples) == 0 {
		return nil, errors.New("speakerid: empty samples")
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("speakerid: invalid sample rate %d", sampleRate)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ext == nil {
		return nil, errors.New("speakerid: extractor closed")
	}
	stream := e.ext.CreateStream()
	defer sherpa.DeleteOnlineStream(stream)
	stream.AcceptWaveform(sampleRate, samples)
	stream.InputFinished()
	if !e.ext.IsReady(stream) {
		return nil, errors.New("speakerid: extractor reported not ready after InputFinished")
	}
	return e.ext.Compute(stream), nil
}

// CosineSimilarity returns the cosine of the angle between two embeddings.
// Range [-1, 1]; for speaker verification, values:
//
//	~> 0.7-0.8   strong match (same speaker)
//	~  0.5-0.7   weak match (probably same, or related voice)
//	~< 0.5       different speakers
//
// Returns NaN if the lengths differ (unrecoverable model mismatch) or if
// either vector is all-zero (degenerate input).
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return math.NaN()
	}
	var dot, normA, normB float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return math.NaN()
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// AverageEmbeddings returns the L2-normalized mean of n embeddings — used
// to combine multiple enrollment clips into a single canonical reference
// per principal (more robust to clip-level noise than picking one). Empty
// input returns nil; mismatched dimensions returns an error.
func AverageEmbeddings(embeddings [][]float32) ([]float32, error) {
	if len(embeddings) == 0 {
		return nil, nil
	}
	dim := len(embeddings[0])
	if dim == 0 {
		return nil, errors.New("speakerid: empty embedding")
	}
	sum := make([]float64, dim)
	for _, emb := range embeddings {
		if len(emb) != dim {
			return nil, fmt.Errorf("speakerid: embedding dim mismatch: %d vs %d", len(emb), dim)
		}
		for i, v := range emb {
			sum[i] += float64(v)
		}
	}
	out := make([]float32, dim)
	var norm float64
	for i, v := range sum {
		mean := v / float64(len(embeddings))
		out[i] = float32(mean)
		norm += mean * mean
	}
	if norm == 0 {
		return out, nil
	}
	inv := float32(1.0 / math.Sqrt(norm))
	for i := range out {
		out[i] *= inv
	}
	return out, nil
}
