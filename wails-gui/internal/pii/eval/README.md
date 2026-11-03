# PII Detection Evaluation Framework

Benchmarks PII protection across the regex and neural (GLiNER / OPF) pipelines.
Produces engineering diagnostics (per-category P/R/F1) plus the Something
Privacy Scorecard used for product decisions.

## Isolated text diagnostic

`go run ./cmd/pii-test -dataset internal/pii/eval/testdata/sample_dataset.json`
reports regex precision and recall using this evaluator's scorer, without a
model download or private snapshot. `account_boundaries.json` adds contract
prose, complete long identifiers and multilingual/fused-label true positives.
The no-argument diagnostic also uses the sample corpus when HOME has no
snapshot directory. Passing a JPEG path retains the screenshot diagnostic.
Decoder unit tests separately cover OPF subword boundaries; the text diagnostic
does not claim neural inference results. The scorer's default overlap matching
is supplemented by exact full-value redaction assertions in the PII tests.

## Quick Start

```bash
# Regex evaluation on text samples (fast, no model download)
go run ./cmd/pii-eval -dataset ./internal/pii/eval/testdata/sample_dataset.json -mode text

# OCR -> Regex on generated screenshots. The generator defaults to JPEG
# quality 50 to match production activity snapshots.
go run ./cmd/pii-gen-dataset  # generate screenshots first
go run ./cmd/pii-eval -dataset ./internal/pii/eval/testdata/screenshots/dataset.json -mode ocr-regex

# OCR -> GLiNER + Regex (requires GLiNER model download ~665 MB)
go run ./cmd/pii-eval -dataset ./internal/pii/eval/testdata/screenshots/dataset.json -mode full

# OCR -> OPF (requires OpenAI privacy-filter model download ~917 MB)
go run ./cmd/pii-eval -dataset ./internal/pii/eval/testdata/screenshots/dataset.json -mode opf

# Compare against a saved baseline
go run ./cmd/pii-eval -dataset dataset.json -mode text -compare baseline.json

# Debug per-sample errors
go run ./cmd/pii-eval -dataset dataset.json -mode text -debug
```

## Output Formats

- `table` (default) - sklearn-style classification report + Something scorecard
- `scorecard` - product decision metrics and safety gates only
- `json` - machine-readable full report
- `markdown` - GitHub-flavored markdown table

## Something Privacy Scorecard

The scorecard is designed for Something's actual product risk: protect dangerous
secrets before storage/model exposure, then preserve enough context for memory
and activity understanding. It deliberately does not use a flat harmonic mean of
recall and precision as the decision metric.

| Metric | Formula | Meaning |
|--------|---------|---------|
| Critical Leak Count | missed Critical annotations | Hard safety gate; passwords, API keys, OTPs, CVV, credentials |
| Risk-Weighted Exposure | missed risk weight / total target risk weight | Lower is better; Critical=10, High=3, Medium=0 |
| Risk-Weighted Protection | 1 - Risk-Weighted Exposure | Privacy-first protection score |
| Context Loss Rate | average unmatched prediction extent per sample | False-positive text span or screenshot mask area, capped per sample |
| Context Preservation | 1 - Context Loss Rate | How much non-PII context survives over-protection |
| Decision Score | 0.80 * Protection + 0.20 * Context Preservation | Ranking score after safety gates |
| Mean / P95 ms per sample | measured detector runtime after model init | Runtime suitability for the background masking pipeline |
| Samples/sec | sample count / total detector runtime | Throughput for candidate comparison |

Safety gates:

| Gate | Threshold |
|------|-----------|
| `critical_leak_count` | `<= 0` |
| `risk_weighted_exposure_rate` | `<= 0.02` |
| `high_risk_protection_rate` | `>= 0.95` |
| `context_preservation_score` | `>= 0.80` |

The scorecard uses **category-agnostic matching**: a prediction matches an
annotation if their text or bounding box overlaps, regardless of entity labels.
This reflects production behavior where masking a sensitive region protects the
user even if the detector label is imperfect.

## Evaluation Modes

| Mode | Pipeline | Use Case |
|------|----------|----------|
| `text` | Text -> Regex | Fast iteration on regex patterns |
| `ocr-regex` | Image -> OCR -> Regex | Regex accuracy on real screenshots |
| `full` | Image -> OCR -> GLiNER + Regex | Current production pipeline |
| `opf` | Image -> OCR -> OPF | OpenAI privacy-filter (experimental) |
| `opf-regex` | Image -> OCR -> OPF + Regex | OPF combined with regex |

## Datasets

### yansu-pii-eval-v1 (text)
103 samples, 102 annotations across 16 PII categories and 4 languages
(EN/ZH/JA/KO). Includes positive, negative, multi-entity, desktop-app, and
private-link samples.
Located at `testdata/sample_dataset.json`.

### Generated screenshots
32 synthetic screenshots with 93 ground-truth bounding boxes, generated at the
same JPEG quality used by production activity snapshots.
Generate with `go run ./cmd/pii-gen-dataset`, located at `testdata/screenshots/`.

Performance is reported separately from the privacy gates. A detector can pass
privacy gates but still be unsuitable as the default runtime path if latency
causes screenshot masking to queue or drop frames.

## Current Scores (2026-06-07)

### Something Privacy Scorecard

PII categories are grouped by exposure severity. This reflects our design
philosophy: prioritize blocking the PII that causes the most damage if leaked.

| Risk Tier | What it includes | Leak consequence |
|-----------|-----------------|------------------|
| **Critical** | Passwords, API keys, credentials, OTP, CVV | Direct financial loss or security breach |
| **High** | Emails, phones, addresses, account IDs, signed URLs | Social engineering or targeted harassment |
| **Medium** | Person names, dates, org names | Low standalone risk (not redacted by design) |

### AX/parser text path

Dataset: `yansu-pii-eval-v1` (103 text samples / 102 annotations). This
corresponds to structured desktop text captured through AX/parser output before
storage. The default for this path must be local, deterministic, and fast enough
to run before any parsed activity payload is persisted.

| Detector | Critical leaks | High leaks | Risk-weighted protection | Context preservation | Decision score | Mean / P95 ms | Recommendation |
|----------|----------------|------------|--------------------------|----------------------|----------------|---------------|----------------|
| Regex | **0** | 1 | **0.995** | **0.956** | **0.988** | **0.1 / 0.2** | Default: passes all gates; fastest; no model dependency |
| GLiNER + Regex | 3 | 2 | 0.945 | 0.941 | 0.944 | 31.8 / 57.8 | Reject for text path: fails critical/risk gates and is slower |
| Gemini 3 Flash | **0** | 4 | 0.982 | **0.992** | 0.984 | 4388.7 / 9998.0 | Remote baseline only; fails high-risk gate and sends raw text remotely |

### Screenshot masking path

Dataset: `yansu-pii-eval-screenshots-v1` (32 screenshot samples / 93 bounding-box
annotations). This corresponds to production activity screenshots after JPEG
encoding, OCR, and bbox-based masking.

| Detector | Critical leaks | High leaks | Risk-weighted protection | Context preservation | Decision score | Mean / P95 ms | Recommendation |
|----------|----------------|------------|--------------------------|----------------------|----------------|---------------|----------------|
| OCR+Regex | 2 | 1 | 0.960 | 0.984 | 0.965 | 365.7 / 425.8 | Fallback only |
| GLiNER + Regex | **0** | **0** | **1.000** | 0.984 | **0.997** | 479.0 / 606.6 | Default screenshot path |

Decision notes:

- Structured text currently passes the Something scorecard gates with regex-only
  detection. This matches the production AX/parser path, where regex redaction
  is the primary pre-storage protection layer.
- GLiNER + regex is not adopted for AX/parser text because it fails the
  critical-leak and risk-exposure gates on the text dataset while adding model
  latency and packaging complexity.
- Gemini 3 Flash was run as a remote reference baseline for AX/parser text. It
  has zero category-agnostic critical leaks, but misses high-risk annotations,
  is orders of magnitude slower than regex, and would send raw parsed desktop
  text to a remote API before redaction.
- GLiNER + regex passes all screenshot safety gates and is the best default
  screenshot candidate: it has zero critical and high-risk leaks while staying
  around 0.5-0.6s/sample on local eval runs.
- OCR + regex is faster than GLiNER + regex and now passes the high-risk
  protection gate, but it still misses critical screenshot annotations and
  remains a fallback when the model is not ready.
- GLiNER2-PII, OPF, and Gemini 3 Flash are benchmark-only candidates from
  prior exploratory runs. Re-run them on the expanded screenshot dataset before
  using them for current score comparisons.
- Context preservation is high for screenshots because most false-positive masks
  are small screen regions. This is why prediction precision alone is
  misleading: many small FPs are less harmful than one missed credential.

The screenshot dataset is intentionally stricter than the text dataset: it uses
production JPEG quality, low-contrast UI, browser/autofill forms, banking and
medical portals, email/support-ticket threads, AI chat secret review, browser
developer console output, OCR-fragmented sensitive text, Slack-style incident
messages, GitHub Actions logs, CRM notes, spreadsheet exports, signed links,
and clean negative screens. This makes it useful for catching OCR misses and
over-redaction regressions.

## Architecture Decisions

1. **Person names not redacted**: Redacting names would break the memory/knowledge
   pipeline, which needs to distinguish between different people's actions.
   Pseudonymization is a future option but adds significant complexity.

2. **Dates not redacted**: Dates are ubiquitous in screen content (timestamps,
   file dates, git logs). Blanket redaction would severely impact utility.

3. **Regex is the primary text layer**: The regex detector passes the Something
   scorecard gates on the text dataset and is fast enough for pre-storage
   AX/parser redaction.

4. **GLiNER + regex is the primary screenshot layer**: The combined detector
   passes all screenshot safety gates. Regex covers structured secrets and OCR
   variants; GLiNER adds semantic sensitivity for less regular text.

5. **OPF (OpenAI privacy-filter) evaluated but not adopted**: Prior runs passed
   the screenshot gates after scorecard/masking fixes, but OPF remained too slow
   for the high-frequency activity monitoring pipeline.
