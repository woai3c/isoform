# ocr-helper

macOS OCR helper subprocess for the Isoform desktop app.

## Overview

`yansu-ocr-helper` is a small, standalone command-line tool that wraps macOS `Vision.framework` OCR calls in a separate process. The main Wails GUI app spawns this helper when available so that the heavy CGO + Objective-C runtime dependency stays isolated from the primary application process.

The helper is **macOS-only**. On other platforms the same source tree compiles to a no-op binary that exits with an error.

## Purpose

- Offload `Vision.framework` OCR work from the main `Isoform` app process.
- Provide a simple JSON-over-stdio interface so the main app can request OCR without linking Vision directly.
- Allow the main app to fall back gracefully to an in-process Vision implementation if the helper is missing.

## Key Components

| File | Build Tag | Description |
|------|-----------|-------------|
| `main.go` | `darwin` | Helper implementation: reads a JSON request from stdin, runs OCR via `internal/platform/ocr`, writes a JSON response to stdout |
| `main_other.go` | `!darwin` | Stub implementation that prints an error and exits with status 1 on non-macOS platforms |

## Architecture

```
┌─────────────────────┐      JSON/stdin-stdout      ┌──────────────────────┐
│   Isoform (Wails GUI) │  ──────────────────────────▶  │   yansu-ocr-helper   │
│                     │                              │  (this command)      │
│  internal/platform  │                              │  internal/platform   │
│  /ocr/helper_darwin │◀──────────────────────────   │  /ocr (darwinEngine) │
│  .go                │                              │                      │
└─────────────────────┘                              └──────────────────────┘
                                                            │
                                                            ▼
                                                    Vision.framework
                                                    (VNRecognizeTextRequest)
```

The parent process (`internal/platform/ocr/helper_darwin.go`) discovers the helper binary next to the app bundle, retains one helper process and exchanges newline-delimited JSON messages over stdin/stdout. Launch prewarming runs accurate Vision recognition on a small synthetic image; it never captures the screen.

## Request / Response Protocol

The helper reads newline-delimited JSON request objects from stdin and writes one newline-delimited JSON response per request. The process and engine stay alive between requests. Every response echoes the request `id`.

### Request

```json
{
  "id": 1,
  "mode": "text",
  "jpegData": "<base64-encoded JPEG bytes>",
  "langs": ["en-US", "zh-Hans"],
  "priority": 2
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `uint64` | Request ID echoed in the response; prevents stale replies being attributed to later frames |
| `mode` | `string` | `"text"` for aggregated text, `"boxes"` for per-line observations with bounding boxes |
| `jpegData` | `[]byte` (base64 in JSON) | JPEG-encoded image data |
| `langs` | `[]string` | Optional recognition language tags (e.g. `en-US`, `zh-Hans`) |
| `priority` | `int` | Priority hint (`0` = low, `1` = mid, `2` = high). Currently passed through to the engine but the helper processes one request at a time |

### Response

Text mode:

```json
{
  "id": 1,
  "text": "recognized text",
  "confidence": 0.97
}
```

Boxes mode:

```json
{
  "id": 1,
  "observations": [
    {
      "text": "line one",
      "confidence": 0.98,
      "x": 0.1,
      "y": 0.2,
      "width": 0.5,
      "height": 0.05
    }
  ]
}
```

Error response:

```json
{
  "id": 1,
  "error": "OCR not supported"
}
```

Bounding box coordinates are normalized `[0, 1]` with the origin at the **top-left** of the image.

## Dependencies

- Go 1.25+
- macOS SDK with `Vision.framework`, `Foundation`, `CoreGraphics`, and `ImageIO`
- `CGO_ENABLED=1` (required for the Objective-C bridge)
- Same Go module as `wails-gui` (`module wails-gui`)

No third-party Go dependencies beyond the project's own `internal/platform/ocr` package.

## Usage

### Build

The helper is built automatically by `scripts/build-wails.sh` as part of the desktop release pipeline:

```sh
./wails-gui/scripts/build-wails.sh
```

For local development on macOS you can build it directly:

```sh
cd wails-gui
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
  go build -o build/bin/yansu-ocr-helper ./cmd/ocr-helper
```

### Run manually

```sh
# Set the child marker to prevent recursive helper spawning
export YANSU_OCR_HELPER_CHILD=1

echo '{"mode":"text","jpegData":"...","langs":["en-US"],"priority":2}' \
  | ./build/bin/yansu-ocr-helper
```

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `YANSU_OCR_HELPER_CHILD` | Set by the helper itself to prevent recursive spawning; also respected by the parent as a disable flag |
| `YANSU_OCR_HELPER_DISABLE` | Set on the parent process to skip helper usage and fall back to in-process Vision |
| `YANSU_OCR_HELPER_PATH` | Override the path to the helper binary |

## Configuration

There are no standalone configuration files for this command. All behavior is controlled via:

1. The JSON request payload (mode, languages, priority).
2. Environment variables read by the parent process for helper discovery.

## Testing

Protocol unit tests cover persistent requests and response IDs. The `internal/platform/ocr` subprocess tests cover first/second timeout handling, process reuse, late replies, dropped-frame counters, prewarming and shutdown.

To verify the helper works after building:

```sh
cd wails-gui
./build/bin/yansu-ocr-helper <<'EOF'
{"id":1,"mode":"text","jpegData":"","langs":["en-US"],"priority":0}
EOF
```

An empty JPEG payload will produce an OCR error response, which confirms the binary started and parsed the request correctly.

## Platform Support

| Platform | Supported | Notes |
|----------|-----------|-------|
| macOS (darwin/amd64, darwin/arm64) | Yes | Native Vision.framework backend |
| Windows | No | Use `internal/platform/ocr` directly; Windows uses PaddleOCR/WinRT |
| Linux | No | Stub exits with status 1 |

## Integration Notes

- Rebuild the bundled helper together with the desktop through `scripts/build-wails.sh` when this protocol changes. Hot-reloading only the Go app leaves an old helper that cannot echo request IDs.
- The parent process in `internal/platform/ocr/helper_darwin.go` searches for the helper binary in standard bundle locations (`../Resources/yansu-ocr-helper`, `<exe-dir>/yansu-ocr-helper`, and local `build/bin/` paths).
- If the helper cannot be found or is disabled, the parent falls back to in-process `Vision.framework` OCR via `ocr_darwin.go`. Helper request errors propagate to the caller.
- Requests are capped at **64 MiB** to avoid unbounded memory consumption from malformed input.
- Requests have a **20-second** warm budget and a **3-minute** cold-start budget for Vision model compilation. The first timeout drops that frame but leaves the helper running. A second consecutive timeout stops/reaps it; the next request starts a fresh helper. A successful request resets the timeout count.
- Only one request is in flight. After a timeout the parent drains and discards that request's late reply before sending another image. Response IDs are checked as an additional safeguard.
- Session summaries report request, completion, dropped-frame, timeout and restart counts. They contain no OCR text. Prewarm requests are excluded from frame counters.
- Normal desktop shutdown closes the helper. A parent-PID watchdog also exits the helper if the desktop crashes during native recognition.
