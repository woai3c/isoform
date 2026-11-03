# OCR Evaluation Tool

CLI tool for evaluating OCR recognition quality against annotated ground-truth datasets. Part of the [Yansu Desktop](../../) (wails-gui) project.

## Overview

This tool runs the platform's native OCR engine against a dataset of test images and computes accuracy metrics (CER, WER) by comparing recognized text against known ground truth. It supports both evaluating existing datasets and generating new ones from synthetic or native-rendered text images.

The tool is used to:

- Benchmark OCR accuracy across languages (English, Chinese, mixed), font sizes, and themes (light/dark).
- Compare OCR results between engine versions or configuration changes.
- Simulate real-world degradation via JPEG re-encoding at configurable quality levels.
- Generate test images that mimic actual app screenshots (chat, IDE, browser, terminal).

## Architecture

```
ocraeural      ocr-eval (this tool)
   |
   |-- Dataset Generation
   |     |-- Synthetic: Go font rasterizer (gen.go)
   |     '-- Native:  macOS WebKit + CoreText (gen_native.go)
   |
   |-- OCR Engine
   |     '-- Platform OCR (Vision on macOS, Windows OCR on Windows)
   |
   '-- Evaluation & Reporting
         '-- internal/ocr/eval (metrics, scoring, formatting)
```

### Key Design Patterns

- **Dataset-driven evaluation**: Images, metadata, and ground truth are stored in a JSON dataset file. This makes results reproducible and shareable.
- **Pluggable generation**: Two generation modes (synthetic, native) produce images with different rendering characteristics to stress-test the OCR engine.
- **CJK-aware tokenization**: WER treats each CJK character as a word, matching how Chinese/Japanese text is segmented.

## Files

| File | Purpose |
|------|---------|
| `main.go` | CLI entry point, flag parsing, orchestration (load/generate dataset, run OCR, score, report). |
| `gen.go` | Synthetic dataset generation using `golang.org/x/image/font/opentype`. Renders text blocks onto RGBA images with configurable fonts, sizes, colors, and JPEG quality. |
| `gen_native.go` | Native dataset generation using macOS WebKit (via Swift script). Renders HTML with system fonts and subpixel antialiasing to match real screenshots. |
| `normalize.go` | Thin wrapper around `ocr.NormalizeFullwidth` for fullwidth-to-halfwidth ASCII normalization. |
| `reencode.go` | JPEG re-encoding helper to simulate compression artifacts at a given quality level. |

## Dependencies

### Go Modules

- `wails-gui/internal/ocr/eval` -- Dataset types, CER/WER metrics, and report formatting.
- `wails-gui/internal/platform/ocr` -- Platform OCR engine interface and implementations.
- `golang.org/x/image/font/opentype` -- Font parsing and rasterization (synthetic mode only).

### System Dependencies

- **macOS** (required for native generation mode): `swift`, `sips`, WebKit framework.
- **macOS** (OCR evaluation): Vision framework (macOS 10.15+).
- **Windows** (OCR evaluation): Windows.Media.Ocr (Windows 10+).

## Usage

### Evaluate an Existing Dataset

```bash
cd wails-gui
go run ./cmd/ocr-eval -dataset /path/to/dataset.json
```

### Generate and Evaluate (Synthetic)

```bash
cd wails-gui
go run ./cmd/ocr-eval \
  -gen-mode synthetic \
  -font /path/to/NotoSansCJK.ttc \
  -output report.json
```

### Generate and Evaluate (Native -- macOS Only)

```bash
cd wails-gui
go run ./cmd/ocr-eval \
  -gen-mode native \
  -output report.json
```

### Compare Against a Baseline

```bash
cd wails-gui
go run ./cmd/ocr-eval \
  -dataset dataset.json \
  -compare baseline.json
```

### Simulate JPEG Compression

```bash
cd wails-gui
go run ./cmd/ocr-eval \
  -dataset dataset.json \
  -jpeg-quality 50
```

### Generate Dataset Only (No OCR)

```bash
cd wails-gui
go run ./cmd/ocr-eval \
  -gen-mode native \
  -gen \
  -gen-dir ./my-dataset
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-dataset` | (none) | Path to an existing evaluation dataset JSON. |
| `-output` | (none) | Path to save the evaluation report JSON. |
| `-compare` | (none) | Path to a baseline report JSON for side-by-side comparison. |
| `-format` | `table` | Output format: `table` or `json`. |
| `-gen-mode` | (none) | Dataset generation mode: `synthetic` or `native`. |
| `-gen-dir` | (temp dir) | Directory for generated images and dataset JSON. |
| `-font` | (none) | Path to a `.ttf`/`.otf` font file (required for synthetic mode). |
| `-langs` | `en-US,zh-Hans` | Comma-separated OCR recognition languages. |
| `-jpeg-quality` | `0` | Re-encode input at this JPEG quality before OCR (`0` = original). |
| `-normalize` | `false` | Apply fullwidth-to-halfwidth normalization on OCR output. |
| `-error-threshold` | `0.05` | CER threshold for error analysis (used with `-debug`). |
| `-gen` | `false` | Generate dataset only, skip OCR evaluation. |
| `-debug` | `false` | Show per-sample error details above threshold. |
| `-dump` | `false` | Dump ground truth vs OCR output for each sample. |

## Dataset Format

A dataset is a JSON file with the following structure:

```json
{
  "name": "ocr-eval-native",
  "description": "Native macOS-rendered test images",
  "version": "1.0",
  "samples": [
    {
      "id": "zh_chat_light",
      "image_path": "images/zh_chat_light.jpg",
      "language": "zh",
      "category": "chat",
      "ground_truth": "Project update\nPlease review the design doc"
    }
  ]
}
```

Fields:

- `id` -- Unique sample identifier.
- `image_path` -- Path to the JPEG image (relative or absolute).
- `language` -- Primary language tag (`zh`, `en`, `zh-en`, etc).
- `category` -- UI category (`chat`, `ide`, `browser`, `terminal`).
- `ground_truth` -- Exact text expected to be recognized from the image.
- `lines` (optional) -- Line-level ground truth for per-line evaluation.

## Report Format

The evaluation report contains per-sample and aggregated metrics:

```json
{
  "total_samples": 10,
  "overall": {
    "group": "overall",
    "samples": 10,
    "mean_cer": 0.0123,
    "median_cer": 0.0080,
    "mean_wer": 0.0345,
    "mean_confidence": 0.97
  },
  "by_language": [...],
  "by_category": [...],
  "samples": [...]
}
```

### Metrics

- **CER** (Character Error Rate) -- `edit_distance / len(ground_truth)`. Measures character-level accuracy. Range: 0 (perfect) to 1.0 (completely wrong).
- **WER** (Word Error Rate) -- `edit_distance(words) / len(words)`. Measures word-level accuracy. For CJK text, each character is treated as a word.
- **Confidence** -- Average confidence score returned by the OCR engine per sample.

## Testing

Unit tests for the evaluation metrics are in the `internal/ocr/eval` package:

```bash
cd wails-gui
go test ./internal/ocr/eval/...
```

Tests cover CER/WER computation, CJK tokenization, Levenshtein distance, and report scoring.

## Generated Samples

### Synthetic Mode (`gen.go`)

Renders text using a user-supplied OpenType font at 144 DPI. Covers:

- Chinese: large (32px), medium (20px), small (14px), light/dark themes, low quality (Q50).
- English: large (32px), small (14px), light/dark themes.
- Mixed Chinese-English: code comments with Chinese text and English code.

### Native Mode (`gen_native.go`) -- macOS Only

Renders HTML via macOS WebKit with system fonts and subpixel antialiasing. Covers:

- Chat app messages (Feishu/Lark style bubbles).
- IDE code with syntax highlighting (VS Code dark theme).
- Browser article content.
- Terminal output with colored error messages.
- Mixed Chinese-English conversations.

## Integration with the Project

This tool consumes the same `internal/platform/ocr` engine used by the Yansu Desktop app for screen-capture OCR and PII detection. Evaluating against generated datasets ensures regressions in OCR quality are caught before they affect end-user features.
