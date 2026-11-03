# speakerid

Speaker identity verification via speaker embedding extraction. This package wraps
[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)'s `SpeakerEmbeddingExtractor`
to answer the **WHO** behind captured audio — complementing ASR (which answers **WHAT**
was said).

## Purpose

The package provides the second layer of identity attribution in the desktop app's
audio pipeline:

1. **Enrollment** — Record a short reference clip (~3-10s) for a known speaker.
   `ExtractFromPCM` produces a fixed-dimension `float32` embedding vector that
   the caller persists alongside the speaker's principal record.
2. **Verification** — When a fresh microphone chunk arrives during a session,
   run `ExtractFromPCM` again and compare the new embedding to enrolled embeddings
   via `CosineSimilarity`. A high score (~> 0.7) means "same speaker";
   a low score means "someone else".

The package is intentionally **engine-only** — it does not decide thresholds, load
WAV files, or persist embeddings. Those concerns live in the wiring layer
(`voice_enrollment.go`).

## Architecture

```
┌─────────────────┐     ┌─────────────┐     ┌─────────────────────┐
│  PCM samples    │────▶│  Extractor  │────▶│  float32 embedding  │
│  (mono, 16 kHz) │     │ (sherpa-onnx│     │  (dim = 192/256/512)│
└─────────────────┘     └─────────────┘     └─────────────────────┘
                                                               │
                    ┌──────────────────────────────────────────┘
                    ▼
        ┌─────────────────────┐
        │  CosineSimilarity   │────▶ score ∈ [-1, 1]
        │  (embedding vs ref) │
        └─────────────────────┘
```

The `Extractor` type wraps the sherpa-onnx C struct and is safe for concurrent use
across goroutines. Each `ExtractFromPCM` call creates its own `OnlineStream`;
access to the shared extractor handle is guarded by a mutex.

## Key Components

| Symbol | Type | Description |
|--------|------|-------------|
| `Extractor` | struct | Thread-safe wrapper around sherpa-onnx `SpeakerEmbeddingExtractor`. |
| `Config` | struct | User-facing configuration. Only `Model` is required. |
| `New` | func | Loads the speaker model and returns a ready extractor. |
| `Close` | method | Releases underlying C resources. Safe to call multiple times. |
| `Dim` | method | Returns the embedding dimension of the loaded model. |
| `ExtractFromPCM` | method | Runs extraction on mono 16-bit-equivalent float32 PCM. |
| `CosineSimilarity` | func | Computes cosine similarity between two embeddings. |
| `AverageEmbeddings` | func | L2-normalized mean of multiple embeddings (robust enrollment). |

## Dependencies

- `github.com/k2-fsa/sherpa-onnx-go v1.13.0` — Go bindings for sherpa-onnx speaker embedding extraction
- Platform-specific indirect deps: `sherpa-onnx-go-linux`, `sherpa-onnx-go-macos`, `sherpa-onnx-go-windows`

Requires a compatible ONNX speaker embedding model at runtime (e.g. 3D-Speaker CAM++,
WeSpeaker, or NeMo TitaNet). The model path is passed via `Config.Model`.

## Usage

### Create an extractor

```go
ext, err := speakerid.New(speakerid.Config{
    Model:      "/path/to/3dspeaker_campplus.onnx",
    NumThreads: 1,   // sufficient for single-chunk verification
    Debug:      false,
})
if err != nil {
    log.Fatal(err)
}
defer ext.Close()

fmt.Printf("model loaded; embedding dim = %d\n", ext.Dim())
```

### Extract an embedding

```go
// samples: mono float32 PCM in range [-1, 1] at the model's training rate
// (typically 16000 Hz). Pass speech audio only (after VAD), not raw chunks.
embedding, err := ext.ExtractFromPCM(samples, 16000)
if err != nil {
    log.Fatal(err)
}
// embedding is a []float32 of length ext.Dim()
```

### Compare two embeddings

```go
score := speakerid.CosineSimilarity(embA, embB)
// score > 0.7-0.8  → strong match (same speaker)
// score 0.5-0.7    → weak match
// score < 0.5      → different speakers
```

### Average multiple enrollment clips

```go
// Combine several enrollment clips into one robust reference embedding.
canonical, err := speakerid.AverageEmbeddings([][]float32{emb1, emb2, emb3})
```

## Configuration

No environment variables or config files are read by this package. All
configuration is passed explicitly via the `Config` struct:

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `Model` | yes | — | Absolute path to a sherpa-compatible speaker embedding ONNX file |
| `NumThreads` | no | `1` | Per-extractor thread count |
| `Debug` | no | `false` | Enables verbose sherpa logging when true |

## Testing

Run unit tests from this directory:

```bash
cd wails-gui/internal/speakerid
go test -v ./...
```

Tests cover:

- `CosineSimilarity` — same vector, orthogonal vectors, opposite vectors, scaled vectors, 45-degree angle
- `CosineSimilarity` degenerate inputs — length mismatch, empty vectors, all-zero vectors (all return `NaN`)
- `AverageEmbeddings` — empty input, dimension mismatch, single-input normalization, two aligned inputs, two orthogonal inputs, opposing inputs (zero mean)

Note: Tests exercise only the pure-Go math utilities. The sherpa-onnx C path
(`New`, `ExtractFromPCM`) requires the ONNX model file at runtime and is tested
via the `cmd/speakerid-poc` tool and integration tests in `voice_enrollment.go`.

## API / Interfaces

### `Config`

```go
type Config struct {
    Model      string // path to ONNX speaker embedding model
    NumThreads int    // thread count (default 1)
    Debug      bool   // verbose logging
}
```

### `Extractor`

```go
// Create
func New(cfg Config) (*Extractor, error)

// Lifecycle
func (e *Extractor) Close()
func (e *Extractor) Dim() int

// Extraction
func (e *Extractor) ExtractFromPCM(samples []float32, sampleRate int) ([]float32, error)
```

### Utility Functions

```go
// CosineSimilarity returns the cosine of the angle between two embeddings.
// Range [-1, 1]. Returns NaN on length mismatch or all-zero vectors.
func CosineSimilarity(a, b []float32) float64

// AverageEmbeddings returns the L2-normalized mean of n embeddings.
// Empty input returns nil; mismatched dimensions return an error.
func AverageEmbeddings(embeddings [][]float32) ([]float32, error)
```

## Integration

This package is consumed by:

- **`wails-gui/voice_enrollment.go`** — Wails app methods for enrollment,
  verification, speaker clustering, and the Settings UI voice-match test.
- **`wails-gui/cmd/speakerid-poc/main.go`** — Standalone CLI tool for sanity-checking
  a model against two WAV files before integration.
