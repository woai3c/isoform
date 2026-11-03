# browser

Cross-platform detection of Chromium-based browsers for the Wails GUI desktop app.

## Purpose

Locates a Chromium-based browser binary on the host machine that supports `--app` mode. Used by the Wails GUI to launch the web UI in an app-window (kiosk-style) browser when needed.

## API

```go
import "wails-gui/internal/platform/browser"

path := browser.FindChromium()
// Returns the absolute path to a Chromium-based browser binary,
// or "" if none is found on the current platform.
```

## Platform Implementations

The package uses Go build tags for platform-specific logic. Each file implements the same `FindChromium() string` signature.

| File | Build Tag | Strategy |
|---|---|---|
| `browser_linux.go` | `linux` | `exec.LookPath` for known binary names in `$PATH` |
| `browser_darwin.go` | `darwin` | `os.Stat` against known `.app` bundle paths under `/Applications` |
| `browser_windows.go` | `windows` | `os.Stat` against relative paths under `%ProgramFiles%`, `%ProgramFiles(x86)%`, `%LOCALAPPDATA%` |
| `browser_other.go` | `!darwin && !windows && !linux` | Returns `""` (unsupported platform) |

### Supported Browsers

- Google Chrome / Chrome Canary
- Chromium
- Microsoft Edge
- Brave Browser

## Dependencies

Standard library only — no external dependencies.

## Usage in the Larger Project

Call `FindChromium()` at startup to decide whether to open the UI in app mode. If the return value is empty, fall back to the system default browser or the embedded Wails WebView.
