# pii

PII (Personally Identifiable Information) detection and masking for activity screenshots captured by the Yansu desktop app.

## Purpose

This package detects and pixelates sensitive data in JPEG screenshots before they are stored or processed. It uses two complementary approaches:

1. **GLiNER ONNX model** — a zero-shot named entity recognition model that detects PII spans from OCR text observations
2. **Regex fallback** — pattern-based detection for structured PII (credit cards, API keys, passwords, OTPs) that the model may miss

When a sensitive application (e.g. a password manager) is the frontmost window, the entire screenshot is pixelated instead of relying on per-field detection.

## Key Components

| File | Description |
|---|---|
| `types.go` | `DetectionResult` struct and `Detector` interface |
| `detector.go` | `glinerDetector` — GLiNER ONNX model inference, span decoding, greedy NMS |
| `regex.go` | `DetectWithRegex`, `MergeDetections` — regex patterns, line merging, continuation detection |
| `mask.go` | `MaskRegions`, `BlurRegion`, `BlurRegionsBatch`, `BlurEntireImage` — pixelation applied to JPEG images |
| `sensitive_apps.go` | Built-in and user-configured sensitive app bundle ID registry |
| `tokenizer.go` | SentencePiece Unigram tokenizer (Viterbi) compatible with DeBERTa-v3 / GLiNER |
| `download.go` | Downloads GLiNER ONNX model and ONNX Runtime shared library on first use |
| `regex_test.go` | Unit tests for regex detection and the Luhn credit card validator |

## Architecture

```
OCR observations ([]ocr.TextObservation)
        │
        ├─▶ glinerDetector.Detect()      ← GLiNER ONNX model (primary)
        │        tokenize → infer → decodeSpans → map back to bounding boxes
        │
        └─▶ DetectWithRegex()            ← regex patterns (fallback)
                 per-observation scan
                 + continuation detection (wrapped lines)
                 + merged-line scan (OCR splits)
                 └─▶ deduplicateDetections()

MergeDetections(gliner, regex)           ← union, GLiNER takes priority on overlap
        │
        ▼
MaskRegions(jpegData, detections, quality)  ← pixelate bounding boxes in image
```

Separately, `IsSensitiveApp(bundleID)` is checked before running OCR — if the frontmost app is a password manager or similar, `BlurEntireImage` is called directly.

## Guardrails

Two defensive checks bound how much of an image a single per-region detection can mask. They exist because GLiNER spans can map to observations that are spatially scattered (OCR reading-order artifact, common on chat / tiled UIs); without these, the union rectangle would pixelate most of the window.

| Constant | Where | Effect |
|---|---|---|
| `maxSpanWidthFrac` / `maxSpanHeightFrac` (`detector.go`) | After a GLiNER span's outer bbox is computed | If the bbox exceeds these fractions (0.6 wide / 0.08 tall), the span is masked line by line (`spanRegions`): each OCR observation it covers gets its own box. Real PII fits on 1–3 text lines. Regex Pass 2 does the same for a match in a merged row: only the observations the match covers are masked, not the whole row. |
| `maxMaskAreaFraction` (`mask.go`) | Inside `MaskRegions`, before pixelating | If any single detection's pixel area exceeds 25% of the image, `MaskRegions` returns `ErrMaskRegionTooLarge` and the activity mask drops the frame: a frame is better lost than written with that region unmasked. Acts as a backstop for detector regressions and any other source (regex, cache, manual) of an oversized box. Path-A whole-window blurs bypass this cap. |

Look for `[pii] GLiNER span covers scattered observations` and `[pii] oversized mask` in the application log to confirm either guardrail is firing.

## Detected Entity Types

| Category | Detection method |
|---|---|
| `credit_card` | GLiNER + regex (Luhn-validated) |
| `cvv` | GLiNER + regex (near CVV/CVC labels) |
| `password` | GLiNER + regex (after `password:` / `passwd=`) |
| `one_time_password` | GLiNER + regex (near OTP/2FA labels) |
| `street_address` | GLiNER + regex (US-format with ZIP) |
| `api_key` | GLiNER + regex (`sk-`, `ghp_`, `AKIA…`, long hex, `api_key=…`) |

## Dependencies

- [`github.com/yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go) — Go bindings for ONNX Runtime
- ONNX Runtime shared library (`libonnxruntime.dylib` / `.so` / `.dll`) — downloaded automatically on first use
- GLiNER ONNX model ([`knowledgator/gliner-pii-base-v1.0`](https://huggingface.co/knowledgator/gliner-pii-base-v1.0)) — downloaded from the Yansu R2 mirror first, with Hugging Face fallback. The detector uses the fp32 export (`model.onnx`, ~665 MB); the fp16 export has failed session creation across ORT builds with a Cast-node type mismatch in the embedding layer.
- `wails-gui/internal/platform/ocr` — provides `TextObservation` (text + normalized bounding box)

## Usage

### Initialize the detector

```go
detector, err := pii.NewDetector(configDir, func(name string, downloaded, total int64, bps float64) {
    log.Printf("downloading %s: %d/%d bytes", name, downloaded, total)
})
if err != nil {
    // model or runtime download failed
}
defer detector.Destroy()
```

`configDir` is the base config directory (e.g. `~/.yansu-agent`). Model files are cached under `configDir/models/gliner-pii-base/` and the ONNX Runtime library under `configDir/onnxruntime/`.

### Detect and mask PII in a screenshot

```go
// observations come from the OCR layer
glinerResults := detector.Detect(observations)
regexResults  := pii.DetectWithRegex(observations)
detections    := pii.MergeDetections(glinerResults, regexResults)

maskedJPEG, err := pii.MaskRegions(jpegData, detections, 85)
```

### Blur multiple window regions in one pass

When multiple sensitive windows are on screen, `BlurRegionsBatch` decodes and re-encodes the JPEG only once:

```go
regions := []pii.WindowRegion{
    {X: 0.1, Y: 0.2, W: 0.4, H: 0.3},
    {X: 0.6, Y: 0.5, W: 0.2, H: 0.2},
}
maskedJPEG, err := pii.BlurRegionsBatch(jpegData, regions, 85)
```

### Sensitive application check

```go
if category, sensitive := pii.IsSensitiveApp(bundleID); sensitive {
    maskedJPEG, err = pii.BlurEntireImage(jpegData, 85)
}
```

### User-configurable sensitive apps

```go
// Add a single entry
pii.AddUserSensitiveApp("com.example.vault", "password_manager")

// Remove a single entry
pii.RemoveUserSensitiveApp("com.example.vault")

// Replace the entire user list atomically (e.g. on settings load)
pii.SetUserSensitiveApps(map[string]string{
    "com.example.vault": "password_manager",
})

// Get a copy of the user-configured map
userApps := pii.GetUserSensitiveApps() // map[string]string

// List all (defaults + user), sorted
entries := pii.GetAllSensitiveApps() // []pii.SensitiveAppEntry

// Get all bundle prefixes (defaults + user) for window scanning
prefixes := pii.AllBundlePrefixes() // []string
```

## Configuration

No environment variables or config files are needed. All runtime assets are downloaded automatically on first `NewDetector` call:

| Asset | Location | Size |
|---|---|---|
| GLiNER model (`model.onnx`) | `<configDir>/models/gliner-pii-base/` | ~665 MB |
| Tokenizer (`tokenizer.json`) | `<configDir>/models/gliner-pii-base/` | small |
| ONNX Runtime lib | `<configDir>/onnxruntime/` | ~10–20 MB |

ONNX Runtime version: **1.24.4** (macOS x86_64 uses 1.23.0 — last build that supports it).

Optional download overrides:

- `YANSU_PII_MODEL_URL`
- `YANSU_PII_TOKENIZER_URL`

## Operational Pitfalls

These constraints come from production/debugging incidents. Treat them as part
of the PII contract, not incidental implementation details.

### Model precision

Use the fp32 GLiNER export (`model.onnx`) on all platforms.

The fp16 export is roughly half the file size, but it has failed ONNX Runtime
session creation with a Cast output type mismatch in the embedding layer. When
that happens the app falls back to regex-only masking, which leaves names,
addresses, and other model-detected entities exposed. Do not switch back to
`model_fp16.onnx` unless the exact packaged ORT version has a cross-platform
smoke test proving it can create a session and run inference.

The downloader intentionally deletes stale `model_fp16.onnx` and
`model_quint8.onnx` files so an upgraded user does not keep running an old
incompatible asset.

### CPU limits

ONNX Runtime defaults can use all physical cores for a single inference. On a
20-core Windows desktop this made GLiNER saturate CPU and pushed PII p95 into
tens of seconds. Keep all of these safeguards together:

- GLiNER session options in `detector.go`: `intra_threads=2`,
  `inter_threads=1`, sequential execution, CPU memory arena disabled, and
  memory pattern disabled.
- Activity PII semaphore capacity of 1 in `activity_monitor.go`.
- Cached detections for byte-identical OCR observations.
- Async detector startup with a bounded in-memory screenshot queue so the first
  fp32 model download/load does not block the app and does not write unmasked
  JPEGs before GLiNER is ready.

If a future change touches these, compare a Windows Perf Snapshot before/after.
Healthy signs are `PII busy drops 0`, no growing `in-flight`, and PII p95 in
low seconds rather than tens of seconds under heavy activity.

### Runtime loading

PII and memory embeddings share the process-wide ONNX Runtime environment
through `internal/onnxdl`. Packaged builds should reuse the runtime shipped
beside the app (`Contents/Frameworks` on macOS, next to `Yansu.exe` on
Windows). Loading a second runtime from `~/.yansu-agent/onnxruntime` into the
same process has caused long-running native crash suspicion and should be
avoided. See `internal/onnxdl/README.md`.

### Download mirrors

The primary model asset location is the Yansu R2 mirror:

- `https://model-assets.yansu.app/gliner-pii/base-v1.0/onnx/model.onnx`
- `https://model-assets.yansu.app/gliner-pii/base-v1.0/tokenizer.json`

Hugging Face remains a fallback. Keep SHA-256 verification enabled for the
canonical URLs; if a user manually replaced a local model, a mismatch should
delete and re-download rather than silently running an unknown file.

The fp32 model is large (~665 MB). Upload it to R2 with a multipart-capable S3
client; browser/Wrangler-style upload paths may fail on large objects. Never
commit R2 credentials or API tokens.

The download timeout is intentionally 30 minutes. A 10-minute timeout was too
tight for 665 MB on slower networks and caused avoidable fallback to regex-only
masking. Perf Snapshot reports the current PII mode (`gliner`, `initializing`,
`regex_only_retry`, `regex_only`, or `unavailable`) plus queued screenshot count
so support can tell whether the model is still downloading/loading.

### Activity masking paths

There are two related but different privacy paths:

- Screenshot JPEGs: sensitive-app window blur first, then async OCR +
  GLiNER + regex. The unmasked JPEG must not be written to disk; on OCR/mask
  errors the frame is dropped. While GLiNER is downloading/loading, screenshots
  are held only in a bounded RAM queue (`pendingPIIQueueMaxFrames`,
  `pendingPIIQueueMaxBytes`) and session extraction is deferred until queued
  frames are processed. If detector init fails, queued frames are dropped and
  future screenshots fall back to the best-effort regex path after the retry
  delay.
- Text fields (`ax_text`, parser output, memory/activity text): regex
  redaction via `RedactText`. GLiNER is not currently run on these text stores.
  Product/security copy must not imply full named-entity PII coverage for these
  text stores until a separate text-PII pipeline exists.

This means a screenshot can be correctly masked while a text-only excerpt still
needs regex coverage. When investigating a report, check whether the user is
looking at the image, OCR text, parser/AX text, memory, or activity summary.

### Rejected replacements

- **OpenAI Privacy Filter / OPF**: prior local integration attempts were
  abandoned because download/init reliability and memory spikes were not
  acceptable for a desktop background pipeline.
- **Microsoft Presidio**: useful as a library, but it requires a Python/NLP
  runtime. It may work on a developer macOS machine with Python already
  installed, but a standalone Windows app cannot depend on users installing and
  managing that environment. Do not reintroduce Presidio without an explicit
  packaging plan for Python, models, updates, startup, and resource limits.

### Verification checklist

Useful log lines:

```text
[activity] PII detector init started in background
[pii] GLiNER detector ready (entities=6, intra_threads=2, inter_threads=1)
[activity] PII detector ready for activity masking
[pii-mask] detected N PII regions in <file>.jpg (gliner=X, regex=Y)
```

Absence of `detected` lines after the detector is ready usually means the
sampled frames did not contain model/regex-detected PII. It does not by itself
mean PII is disabled; confirm OCR breadcrumb lines are still running.

## Testing

```bash
cd wails-gui
go test ./internal/pii/...
```

`regex_test.go` covers:
- Continuation detection (API key wrapping across OCR lines)
- Natural text stopping continuation
- Credit card group-of-4 format detection
- Luhn algorithm validation
