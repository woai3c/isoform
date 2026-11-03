# OCR Evaluation (`eval`)

Package `eval` provides metrics computation and reporting for Optical Character Recognition (OCR) quality evaluation. It measures recognition accuracy by comparing OCR output against known ground-truth text.

## Purpose

This package is the core evaluation engine used by the [`ocr-eval`](../../cmd/ocr-eval/) CLI tool. Given a dataset of images with annotated ground-truth text and corresponding OCR results, it computes standard OCR quality metrics (CER and WER), aggregates them by language and category, and formats the results for human or machine consumption.

## Key Components

| File | Description |
|------|-------------|
| `types.go` | Domain types: `Sample`, `Dataset`, `OCRResult`, `SampleMetrics`, `GroupMetrics`, `Report` |
| `metrics.go` | Core metric algorithms: `CER`, `WER`, `Score`, plus Levenshtein distance and CJK-aware tokenization |
| `reporter.go` | Output formatters (`FormatTable`, `FormatJSON`, `FormatErrors`, `CompareReports`) and file I/O |
| `metrics_test.go` | Unit tests covering exact matches, edge cases, CJK text, and mixed-language scenarios |

## Architecture

The package follows a simple three-stage pipeline:

```
Dataset + OCR Results  →  Score()  →  Report  →  Formatter
      (types.go)       (metrics.go) (types.go) (reporter.go)
```

1. **Input**: A `Dataset` (ground-truth samples) and a map of `OCRResult` (predictions keyed by sample ID).
2. **Scoring**: `Score()` computes per-sample CER and WER, then aggregates them into overall, per-language, and per-category statistics.
3. **Output**: `FormatTable`, `FormatJSON`, or `CompareReports` renders the `Report`.

### Design Patterns

- **Pure functions**: `CER`, `WER`, and `Score` are stateless and operate only on their arguments.
- **Unicode-aware text processing**: All text is processed as `[]rune` so CJK and mixed-language strings are measured correctly.
- **Zero external dependencies**: The package uses only the Go standard library.

## Metrics

### Character Error Rate (CER)

CER is the normalized Levenshtein edit distance between the ground truth and the prediction, computed at the rune level:

```go
cer := eval.CER("你好世界", "你好世") // 0.25 (1 substitution / 4 characters)
```

- Returns `0` for exact matches.
- Returns `1.0` when ground truth is empty but prediction is not.
- Whitespace is trimmed before comparison.

### Word Error Rate (WER)

WER is the normalized edit distance between tokenized word sequences. For CJK scripts, each character is treated as an individual word:

```go
wer := eval.WER("hello world", "hello earth") // 0.5 (1 of 2 words wrong)
wer := eval.WER("你好世界", "你好事界")       // 0.25 (1 of 4 characters wrong)
```

CJK runes are identified by Unicode ranges including Unified Ideographs, Extensions A–D, Compatibility Ideographs, CJK Symbols, and Halfwidth/Fullwidth Forms.

### Aggregation

`Score` produces a `Report` with:

- **Overall** statistics across all samples
- **By Language** breakdown (e.g., `en`, `zh`, `zh-en`)
- **By Category** breakdown (e.g., `ide`, `chat`, `browser`, `terminal`)
- **Per-sample** detail including CER, WER, confidence, and text lengths

Each group reports mean CER, median CER, mean WER, and mean confidence.

## Usage

### As a Library

```go
import "wails-gui/internal/ocr/eval"

// Load a dataset
ds, err := eval.LoadDataset("dataset.json")

// Run OCR and collect results (using your OCR engine)
results := map[string]eval.OCRResult{
    "s1": {Text: "hello world", Confidence: 0.95},
    "s2": {Text: "你好世界",    Confidence: 0.90},
}

// Score
report := eval.Score(ds.Samples, results)

// Print table to stdout
eval.FormatTable(os.Stdout, report)

// Save JSON report
err = eval.SaveReport("report.json", report)
```

### Via the CLI Tool

See [`cmd/ocr-eval`](../../cmd/ocr-eval/) for the command-line wrapper. Typical workflow:

```bash
# Evaluate an existing dataset
cd wails-gui
go run ./cmd/ocr-eval -dataset dataset.json

# Generate a synthetic dataset and evaluate
go run ./cmd/ocr-eval -gen-mode synthetic -font /path/to/font.ttf

# Generate native macOS-rendered images and evaluate
go run ./cmd/ocr-eval -gen-mode native

# Compare against a baseline report
go run ./cmd/ocr-eval -dataset dataset.json -compare baseline.json
```

## Data Types

### Sample

```go
type Sample struct {
    ID          string   `json:"id"`
    ImagePath   string   `json:"image_path"`
    Language    string   `json:"language"`    // e.g., "en", "zh", "zh-en"
    Category    string   `json:"category"`    // e.g., "ide", "chat", "browser"
    GroundTruth string   `json:"ground_truth"`
    Lines       []string `json:"lines,omitempty"` // optional line-level ground truth
}
```

### Report

```go
type Report struct {
    TotalSamples int             `json:"total_samples"`
    Overall      GroupMetrics    `json:"overall"`
    ByLanguage   []GroupMetrics  `json:"by_language"`
    ByCategory   []GroupMetrics  `json:"by_category"`
    Samples      []SampleMetrics `json:"samples"`
}
```

### GroupMetrics

```go
type GroupMetrics struct {
    Group     string  `json:"group"`
    Samples   int     `json:"samples"`
    MeanCER   float64 `json:"mean_cer"`
    MedianCER float64 `json:"median_cer"`
    MeanWER   float64 `json:"mean_wer"`
    MeanConf  float64 `json:"mean_confidence"`
}
```

## File I/O

| Function | Purpose |
|----------|---------|
| `LoadDataset(path)` | Load a `Dataset` from JSON |
| `SaveDataset(path, ds)` | Persist a `Dataset` to JSON |
| `LoadReport(path)` | Load a previously saved `Report` |
| `SaveReport(path, report)` | Persist a `Report` to JSON |

## Output Formats

### Table (`FormatTable`)

Human-readable aligned table showing groups, sample counts, mean/median CER, mean WER, and mean confidence.

### JSON (`FormatJSON`)

Pretty-printed JSON suitable for machine parsing, CI artifacts, or trend tracking.

### Error Analysis (`FormatErrors`)

Lists all samples with CER above a configurable threshold for debugging poor OCR performance.

### Comparison (`CompareReports`)

Side-by-side delta between a baseline and a candidate report, useful for regression testing when tuning OCR parameters.

## Testing

Run the package tests:

```bash
cd wails-gui
go test ./internal/ocr/eval/...
```

Tests cover:
- Exact match and empty-string edge cases
- Single-character edits (substitution, insertion, deletion)
- Chinese text (exact match, one-character errors)
- Mixed Chinese-English text
- Whitespace trimming behavior
- WER tokenization for CJK and Latin scripts
- End-to-end `Score` aggregation with multiple languages and categories

## Dependencies

None. This package uses only the Go standard library (`sort`, `strings`, `unicode/utf8`, `encoding/json`, `fmt`, `io`, `os`).

## Integration

This package is consumed by:

- [`cmd/ocr-eval`](../../cmd/ocr-eval/) — CLI tool for running evaluations against the platform OCR engine (`internal/platform/ocr`)
- The platform OCR engine itself lives in `internal/platform/ocr` and implements the `Engine` interface used by the evaluator
