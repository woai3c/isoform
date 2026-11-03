# ocr

Platform-specific OCR (Optical Character Recognition) package for the Wails GUI desktop app.

## Overview

This package provides a cross-platform OCR interface with native OS backends on macOS and Windows, and a no-op stub on all other platforms.

## Key Components

| File | Description |
|------|-------------|
| `ocr.go` | Shared types (`Priority`, `Result`, `TextObservation`) and the `Engine` interface |
| `gate.go` | Process-wide priority serialization gate for Vision/WinRT OCR calls (used by `ocr_darwin.go` only today) |
| `ocr_darwin.go` | macOS implementation using `Vision.framework` via CGo |
| `ocr_windows_dispatch.go` | Windows `New()` entry point — picks PaddleOCR or WinRT at runtime via `YANSU_PADDLE_OCR` |
| `ocr_windows.go` | Windows **WinRT** backend (`Windows.Media.Ocr` via a persistent PowerShell subprocess) |
| `ocr_windows.ps1` | Embedded PowerShell script that runs as the WinRT OCR subprocess |
| `ocr_windows_paddle.go` | Windows **PaddleOCR** backend (PaddleOCR-json subprocess managed directly so we can set `HideWindow + CREATE_NO_WINDOW`) |
| `ocr_stub.go` | No-op stub for non-Darwin, non-Windows platforms (`IsSupported() == false`) |
| `ocr_windows_test.go` | WinRT backend tests (constructed via `newWinRTEngine`, not `New()`) |
| `ocr_windows_paddle_test.go` | Paddle backend tests + pure unit tests for `quadToAABB` / `clamp01` |

## Architecture

The package uses Go build tags for platform dispatch:

- `ocr_darwin.go` — compiled on `darwin`
- `ocr_windows.go` — compiled on `windows`
- `ocr_stub.go` — compiled on everything else (`!darwin && !windows`)

All three files expose a single `New() Engine` constructor, so callers are fully decoupled from platform details.

### Priority

Every OCR call carries a `Priority` that controls dispatch order when multiple callers are queued:

| Constant | Value | Use case |
|----------|-------|----------|
| `PriorityHigh` | 2 | User-interactive (e.g. highlight-on-click) |
| `PriorityMid` | 1 | Realtime capture-path (e.g. PII masking) |
| `PriorityLow` | 0 | Bulk background work (e.g. post-session OCR worker) |

### Engine Interface

```go
type Engine interface {
    RecognizeJPEG(jpegData []byte, langs []string, prio Priority) (*Result, error)
    RecognizeJPEGWithBoxes(jpegData []byte, langs []string, prio Priority) ([]TextObservation, error)
    IsSupported() bool
}
```

### Types

```go
// Aggregated OCR result
type Result struct {
    Text       string  `json:"text"`
    Confidence float64 `json:"confidence"`
}

// Per-line observation with bounding box (normalized [0,1], top-left origin)
type TextObservation struct {
    Text       string  `json:"text"`
    Confidence float64 `json:"confidence"`
    X          float64 `json:"x"`      // left edge
    Y          float64 `json:"y"`      // top edge
    Width      float64 `json:"width"`
    Height     float64 `json:"height"`
}
```

### Priority Gate (`gate.go`)

A `globalGate` singleton serializes all OCR calls across the process. Vision's `VNRecognizeTextRequest` fully saturates a CPU core per in-flight call, so only one request runs at a time. The gate admits callers in priority order:

- **High** — admitted as soon as the gate is free.
- **Mid** — waits for any queued High callers to drain first.
- **Low** — waits for all High and Mid callers to drain.

Higher-priority work can starve lower-priority work by design (the background OCR worker may pause for long stretches while interactive requests fly through).

## macOS Implementation

- Uses `VNRecognizeTextRequest` via `Vision.framework`. `RecognizeJPEG` uses `VNRequestTextRecognitionLevelFast` (no language correction) for `PriorityLow` callers and `VNRequestTextRecognitionLevelAccurate` (with language correction) for all other priorities. `RecognizeJPEGWithBoxes` always uses `Accurate`.
- Accepts JPEG bytes directly; `CGImageSourceCreateWithData` decodes them without writing to disk.
- Vision framework returns bounding boxes with a **bottom-left origin**. `RecognizeJPEGWithBoxes` converts to **top-left origin** (`Y = 1.0 - visionY - height`).
- CGo memory: all `char*` strings allocated by C are freed in Go before returning.
- Requires macOS 10.15 or later.

**Required macOS frameworks (linked via CGo):** `Vision`, `Foundation`, `CoreGraphics`, `ImageIO`

## Windows Implementation

Windows has **two backends** selected at `New()` time:

```
                   YANSU_PADDLE_OCR=0?
                          │
              ┌───────────┴───────────┐
              │ default (Paddle)      │ 1/true/yes/on
              ▼                       ▼
        paddleEngine         (env says use Paddle)
              │
       (binary missing?)─────────────► winRTEngine (graceful fallback)
```

A process-wide singleton (`sync.Once`) ensures the heavy PaddleOCR subprocess is only started once, regardless of how many call sites invoke `ocr.New()`. The fallback covers the case where the bundle is missing (e.g. running `go run` without first running `scripts/download-paddleocr.sh`).

### PaddleOCR backend (default)

- Talks to **PaddleOCR-json** ([hiroi-sora/PaddleOCR-json](https://github.com/hiroi-sora/PaddleOCR-json)) as a long-lived stdin/stdout subprocess. The engine code in `ocr_windows_paddle.go` manages the subprocess directly (rather than going through a third-party wrapper) so it can set `syscall.SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}` — without that, a black console window pops up every time `Yansu.exe` (a Wails GUI app) starts the child.
- Pipeline: DBNet text detection → angle classification → CRNN recognition. PP-OCRv3 mobile models.
- Uses `models/config_chinese.txt` — the Chinese dictionary covers ASCII, so mixed Chinese/English screenshots work on a single engine instance.
- `-ensure_ascii=0` flag emits UTF-8 directly instead of `\uXXXX` escapes, roughly halving the stdout line length for CJK-heavy pages.
- Subprocess is restarted every 20 minutes (`paddleRestartInterval`) under the engine's own mutex to mitigate a known upstream memory leak — invisible to callers because the mutex blocks new calls until restart finishes.
- Process crashes / pipe errors trigger an inline restart with a single retry; if that fails the call returns an error and the next caller will start from scratch.
- ~5× slower per call than WinRT (~2s vs ~400 ms on typical 1080p captures) but dramatically more accurate on Chinese — see the PR that introduced this backend for the 200-image comparison report.
- Bundle is **windows/amd64 only**, ~250 MB extracted (compressed to ~88 MB in the installer). Downloaded by `scripts/download-paddleocr.sh` and copied next to `Yansu.exe` as `paddleocr/` by `scripts/build-wails.sh`.
- Bounding boxes returned by Paddle are 4-point quadrilaterals (potentially rotated). `quadToAABB` collapses them to the axis-aligned rect the `TextObservation` API promises; rotation information is dropped.

### WinRT backend (fallback)

- Uses the `Windows.Media.Ocr` WinRT API, available on Windows 10 and later.
- Runs as a **persistent PowerShell subprocess** to avoid the per-call startup cost of WinRT initialization.
- The embedded `ocr_windows.ps1` script is extracted to a temp file on first use and the process is kept alive across calls.
- JPEG data is written to a temporary file and the path is passed to PowerShell (avoids JSON/base64 size limits).
- Communication protocol over stdin/stdout:
  - Startup: PowerShell prints `READY` when initialized.
  - Request (JSON line): `{"mode":"text|boxes","path":"C:\\...\\image.jpg","langs":["en-US"]}`
  - Response (text): `{"text":"...","confidence":1.0}`
  - Response (boxes): `{"observations":[{"text":"...","confidence":1.0,"x":0.1,"y":0.2,"width":0.5,"height":0.05}]}`
  - Error: `{"error":"..."}`
  - Shutdown: send empty line or `EXIT`
- Supports CJK language engines (`zh-Hans`, `zh-Hant`, `ja`, `ko`) with fallback to the user profile language.
- WinRT confidence is hard-coded to 1.0 — the API doesn't expose per-line scores.

### Environment variables

| Variable | Effect |
|----------|--------|
| `YANSU_PADDLE_OCR` | Set to `0`/`false`/`no`/`off` to force the WinRT backend. Anything else (including unset) selects Paddle. |
| `YANSU_PADDLE_OCR_EXE` | Override the path to `PaddleOCR-json.exe`. Used by tests and for local dev when the bundle lives outside `<exe-dir>/paddleocr/`. |

### Resolution order for `PaddleOCR-json.exe`

1. `$YANSU_PADDLE_OCR_EXE` (if set and exists).
2. `<dirname(os.Executable())>/paddleocr/PaddleOCR-json.exe` — production bundle layout populated by `scripts/build-wails.sh`.
3. `~/.yansu-agent/paddleocr/PaddleOCR-json.exe` — dev convenience: matches where a standalone `scripts/download-paddleocr.sh` invocation could be re-targeted.

If none resolve, `newPaddleEngine` returns an error and `initSharedEngine` falls back to WinRT.

## Usage

```go
import "github.com/org/repo/wails-gui/internal/platform/ocr"

engine := ocr.New()

if !engine.IsSupported() {
    // running on unsupported platform (e.g. Linux)
}

// Simple text extraction (user-interactive priority)
result, err := engine.RecognizeJPEG(jpegBytes, []string{"en-US"}, ocr.PriorityHigh)
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Text, result.Confidence)

// Text with bounding boxes (realtime capture-path priority)
observations, err := engine.RecognizeJPEGWithBoxes(jpegBytes, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)
for _, obs := range observations {
    fmt.Printf("%s (%.2f) @ [%.3f, %.3f, %.3f x %.3f]\n",
        obs.Text, obs.Confidence, obs.X, obs.Y, obs.Width, obs.Height)
}
```

## Platform Support

| Platform | Supported | Default Backend | Fallback | Minimum Version |
|----------|-----------|-----------------|----------|-----------------|
| macOS    | Yes       | `Vision.framework` via CGo | — | macOS 10.15 |
| Windows  | Yes       | PaddleOCR-json subprocess | WinRT (`Windows.Media.Ocr`) | Windows 10 |
| Linux    | No        | stub (returns error) | — | — |

## Testing

Tests live in two files (both `//go:build windows`):

- `ocr_windows_test.go` — WinRT backend smoke tests (blank JPEG → empty result, nil JPEG → error). Always runs on Windows; constructs the engine via `newWinRTEngine` directly so it doesn't depend on the env-driven dispatch.
- `ocr_windows_paddle_test.go` — Paddle backend smoke tests plus pure unit tests for `quadToAABB` and `clamp01`. The integration tests skip themselves unless `YANSU_PADDLE_OCR_EXE` points at a real binary; the unit tests always run.

Run on Windows:

```sh
cd wails-gui && go test ./internal/platform/ocr/...

# With Paddle integration tests enabled:
YANSU_PADDLE_OCR_EXE='C:/path/to/PaddleOCR-json.exe' \
  go test ./internal/platform/ocr/... -v
```

Tests are skipped automatically on platforms where `IsSupported()` returns false.
