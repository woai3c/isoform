# OCR Evaluation (`internal/ocr`)

OCR accuracy evaluation library for the Yansu Desktop project. Provides standard metrics (CER, WER), dataset types, and report formatting for benchmarking optical character recognition quality.

## Overview

This package implements the evaluation layer used by [`cmd/ocr-eval`](../../cmd/ocr-eval/) to benchmark the platform's native OCR engine. It is a pure Go library with no external dependencies.

The evaluation workflow is:

1. Load a **Dataset** -- JSON file with image paths and ground-truth text.
2. Run OCR on each image and collect **OCRResult** values.
3. Call `eval.Score()` to compute per-sample and aggregated metrics.
4. Format the **Report** as a table, JSON, or side-by-side comparison.

## Package Structure

```
internal/ocr/
└── eval/
    ├── types.go       # Dataset, OCRResult, SampleMetrics, GroupMetrics, Report
    ├── metrics.go     # CER, WER, Score, aggregateGroup
    ├── reporter.go    # FormatTable, FormatJSON, CompareReports, SaveReport, LoadReport
    └── metrics_test.go # Unit tests
```

## Key Components

### `types.go` -- Data Types

| Type | Description |
|------|-------------|
| `Sample` | One test image with ground-truth text, language, and category tags. |
| `Dataset` | A named collection of `Sample` values. |
| `OCRResult` | Raw OCR output: recognized text and confidence score. |
| `SampleMetrics` | Per-sample CER, WER, confidence, and length stats. |
| `GroupMetrics` | Aggregated metrics for a language or category group. |
| `Report` | Full evaluation output: overall, by-language, by-category, and per-sample breakdown. |

### `metrics.go` -- Core Metrics

**CER** (Character Error Rate)

```go
cer := eval.CER("hello world", "hallo world") // 0.0909 (1 substitution / 11 chars)
```

Computes Levenshtein edit distance at the rune (Unicode code point) level, divided by ground-truth length. Handles empty inputs, CJK characters, and trims surrounding whitespace. Capped at 1.0.

**WER** (Word Error Rate)

```go
wer := eval.WER("hello world", "hallo world") // 0.5 (1 word wrong / 2 words)
```

Tokenizes text into words, then computes edit distance on the word slice. CJK characters are treated as individual tokens to match Chinese/Japanese segmentation conventions.

**Score** (Full Evaluation)

```go
report := eval.Score(dataset.Samples, results)
```

Computes CER/WER for every sample, aggregates by language and category, and returns a complete `Report` with mean and median statistics.

### `reporter.go` -- Output Formatting

| Function | Purpose |
|----------|---------|
| `FormatTable(w, report)` | Human-readable aligned table to `io.Writer`. |
| `FormatJSON(w, report)` | Pretty-printed JSON report. |
| `FormatErrors(w, report, threshold)` | Lists samples with CER above a threshold. |
| `CompareReports(w, baseline, candidate, ...)` | Side-by-side CER comparison of two reports. |
| `SaveReport(path, report)` / `LoadReport(path)` | Persist and reload JSON reports. |
| `LoadDataset(path)` / `SaveDataset(path, ds)` | Persist and reload JSON datasets. |

## Architecture

```
Dataset (JSON)
  |
  +-- Sample[]  {id, image_path, language, category, ground_truth}
  |
  v
OCR Engine  -->  OCRResult {text, confidence}
  |
  v
Score(samples, results)  -->  Report
  |
  +-- Overall {mean_cer, median_cer, mean_wer, mean_confidence}
  +-- ByLanguage[]
  +-- ByCategory[]
  +-- Samples[]
```

The scoring pipeline is stateless and purely functional: given the same inputs, it always produces the same `Report`. This makes it safe to run in parallel and easy to cache or compare across runs.

## Dependencies

Only the Go standard library:

- `encoding/json` -- dataset/report serialization
- `fmt`, `io`, `os` -- reporting and I/O
- `sort`, `strings`, `unicode/utf8` -- metrics and CJK-aware tokenization

## Usage

### Basic Evaluation

```go
package main

import (
    "fmt"
    "os"
    "wails-gui/internal/ocr/eval"
)

func main() {
    ds, _ := eval.LoadDataset("dataset.json")

    results := map[string]eval.OCRResult{
        "s1": {Text: "recognized text", Confidence: 0.95},
    }

    report := eval.Score(ds.Samples, results)
    eval.FormatTable(os.Stdout, report)
}
```

### Compare Two Engine Versions

```go
baseline, _ := eval.LoadReport("baseline.json")
candidate, _ := eval.LoadReport("candidate.json")

eval.CompareReports(os.Stdout, *baseline, *candidate, "v1.0", "v1.1")
```

### Custom Threshold Error Analysis

```go
eval.FormatErrors(os.Stdout, report, 0.05) // show samples with CER > 5%
```

## Testing

```bash
cd wails-gui
go test ./internal/ocr/eval/...
```

Tests cover:

- Exact match, substitution, insertion, deletion cases for CER
- Empty ground-truth and prediction edge cases
- Chinese (CJK) exact match and single-character errors
- Mixed Chinese-English text
- WER with English words and Chinese characters-as-words
- CJK tokenization (fullwidth forms, punctuation)
- End-to-end `Score()` aggregation across languages and categories
- Levenshtein distance correctness

## Dataset Format

A dataset is a JSON file with image paths and ground-truth annotations:

```json
{
  "name": "ocr-eval-native",
  "version": "1.0",
  "samples": [
    {
      "id": "zh_chat_light",
      "image_path": "images/zh_chat_light.jpg",
      "language": "zh",
      "category": "chat",
      "ground_truth": "Hello, how are you?"
    }
  ]
}
```

## Report Format

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

## Integration

This package is consumed by [`cmd/ocr-eval`](../../cmd/ocr-eval/) and the project's OCR benchmarking pipeline. It is intentionally separate from the OCR engine (`internal/platform/ocr`) so evaluation logic can be tested and reused independently.
