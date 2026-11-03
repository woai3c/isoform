# pii-helper

Standalone child-process binary for PII (Personally Identifiable Information) detection in the Yansu desktop app.

## Overview

`yansu-pii-helper` is a small Go binary that runs the GLiNER ONNX model in an isolated subprocess. The main Yansu application spawns it to detect sensitive text (credit cards, passwords, API keys, addresses, etc.) in OCR observations, without risking a native crash in the parent process.

## Purpose

ONNX Runtime inference can segfault when the model and runtime are incompatible (e.g., exported ops missing from the local build). By running GLiNER in a separate process, a crash kills only the helper; the main app detects the failure, logs it, and continues without PII masking. This isolation trade-off accepts a small IPC overhead in exchange for process stability.

The helper is **macOS-only** (`darwin`). On other platforms the build produces a stub that exits immediately.

## Key Components

| File | Build Tag | Description |
|------|-----------|-------------|
| `main.go` | `//go:build darwin` | Real helper: initializes a `pii.Detector`, reads JSON requests from stdin, writes JSON responses to stdout |
| `main_other.go` | `//go:build !darwin` | Stub that prints an error and exits with code 1 on non-macOS platforms |

## Architecture

The helper communicates with the parent process over a simple **line-delimited JSON protocol** on stdin/stdout:

### Startup

1. Parent spawns: `yansu-pii-helper --config-dir <path>`
2. Helper initializes the GLiNER detector (downloads model if missing)
3. Helper writes a single ready response line to stdout

### Request / Response Loop

```
Parent ──stdin──►  Helper
       ◄─stdout──
```

Each line is a JSON object.

**Request** (parent → helper):

```json
{
  "observations": [
    {
      "text": "Card: 4111 1111 1111 1111",
      "confidence": 0.98,
      "x": 0.1,
      "y": 0.2,
      "width": 0.3,
      "height": 0.05
    }
  ]
}
```

**Response** (helper → parent):

```json
{
  "detections": [
    {
      "Text": "4111 1111 1111 1111",
      "Category": "credit_card",
      "X": 0.15,
      "Y": 0.2,
      "Width": 0.2,
      "Height": 0.05,
      "Confidence": 0.92
    }
  ]
}
```

Error responses include an `"error"` field instead of `"detections"`.

## Parent-Process Integration

The `internal/pii` package in the main app manages the helper lifecycle:

- `helper_darwin.go` — spawns the binary, sends requests, and parses responses
- `helper_other.go` — no-op stub on non-Darwin platforms
- If the helper is unavailable or crashes, `NewDetector()` falls back to an in-process GLiNER detector (`newLocalDetector`)

Environment variables controlling behavior (read by the parent):

| Variable | Effect |
|----------|--------|
| `YANSU_PII_HELPER_PATH` | Override the helper binary path |
| `YANSU_PII_HELPER_DISABLE` | Skip the helper and use in-process detection |
| `YANSU_PII_HELPER_CHILD` | Set internally to prevent recursive spawning |

## Dependencies

- `wails-gui/internal/pii` — `NewDetector`, `DetectionResult`, model loading
- `wails-gui/internal/platform/ocr` — `TextObservation` type

These are internal packages; the helper is compiled from the same module (`wails-gui`).

## Build

The helper is built automatically by the Wails release script. Do **not** run raw `go build` for production.

```bash
# Development (produces build/bin/yansu-pii-helper)
cd wails-gui
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -o build/bin/yansu-pii-helper ./cmd/pii-helper

# Production — always use the release script
./wails-gui/scripts/build-wails.sh
```

The production build:

1. Compiles the helper with `CGO_ENABLED=1` (required by ONNX Runtime)
2. Signs it with the `pii-helper.entitlements.plist` (disables library validation so the bundled ONNX runtime dylibs load)
3. Copies it into `Yansu.app/Contents/Resources/yansu-pii-helper`

## Usage

Manual invocation is mainly for debugging:

```bash
./build/bin/yansu-pii-helper --config-dir ~/.yansu-agent
```

The helper will emit a ready line and then wait for JSON requests on stdin:

```bash
$ echo '{"observations":[]}' | ./build/bin/yansu-pii-helper --config-dir ~/.yansu-agent
{"ready":true}
{"detections":[]}
```

## Configuration

| Flag | Required | Description |
|------|----------|-------------|
| `--config-dir` | Yes | Base config directory where the GLiNER model and tokenizer are stored (e.g., `~/.yansu-agent`) |

The helper uses `pii.NewDetector(configDir, nil)` internally, which downloads the ONNX model and tokenizer on first run if they are not present.
