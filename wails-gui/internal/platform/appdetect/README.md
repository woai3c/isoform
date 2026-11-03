# appdetect

Cross-platform package for detecting installed developer applications and opening project directories in them.

## Purpose

Provides two core operations for the Wails GUI desktop app:

1. **Detection** – scan the local machine for known editors, terminals, and file managers
2. **Launch** – open a given project directory in a detected application

The package uses Go build tags to compile the correct platform-specific implementation (`darwin`, `linux`, `windows`) with no runtime switching required.

## Key Components

| File | Build Tag | Description |
|---|---|---|
| `appdetect.go` | all platforms | Shared `InstalledApp` type and `validateProjectPath` helper |
| `detect_darwin.go` | `darwin` | macOS detection via `/Applications` filesystem scan; launch via `open -a` |
| `detect_linux.go` | `linux` | Linux detection via `exec.LookPath`; launch via direct command execution |
| `detect_windows.go` | `windows` | Windows detection via `LookPath` + `ProgramFiles`/`LOCALAPPDATA` scan; launch via direct command execution |

## API

```go
// InstalledApp represents a detected application.
type InstalledApp struct {
    Name      string `json:"name"`
    AppBundle string `json:"app_bundle"` // e.g. "Visual Studio Code.app", "code", "Code.exe"
    Category  string `json:"category"`   // "editor" | "terminal" | "file_manager"
}

// DetectInstalledApps returns all known apps currently installed on this machine.
func DetectInstalledApps() []InstalledApp

// OpenInApp opens projectPath in the application identified by appBundle.
func OpenInApp(appBundle string, projectPath string) error
```

## Detected Applications

### Editors
| App | macOS | Linux | Windows |
|---|---|---|---|
| VS Code | `Visual Studio Code.app` | `code` | `Microsoft VS Code/Code.exe` |
| Cursor | `Cursor.app` | `cursor` | `Cursor/Cursor.exe` |
| Windsurf | `Windsurf.app` | `windsurf` | `Windsurf/Windsurf.exe` |
| Zed | `Zed.app` | `zed` | – |
| Sublime Text | `Sublime Text.app` | `subl` | `Sublime Text/sublime_text.exe` |

### Terminals (macOS / Windows)
| App | macOS | Windows |
|---|---|---|
| Terminal | `Utilities/Terminal.app` | – |
| Ghostty | `Ghostty.app` | – |
| Warp | `Warp.app` | – |
| iTerm2 | `iTerm.app` | – |
| Alacritty | `Alacritty.app` | – |
| Windows Terminal | – | `wt.exe` |

### File Managers (always present)
| Platform | App | AppBundle |
|---|---|---|
| macOS | Finder | `Finder.app` |
| Linux | Files | `xdg-open` |
| Windows | Explorer | `explorer.exe` |

## Detection Strategy

- **macOS**: checks for `.app` bundles in `/Applications` and `/System/Applications`
- **Linux**: uses `exec.LookPath` to check if executables are on `$PATH`
- **Windows**: first tries `exec.LookPath`, then checks `%ProgramFiles%` and `%LOCALAPPDATA%\Programs`

## Dependencies

- [`wails-gui/internal/platform/cmdutil`](../cmdutil) – thin wrapper around `exec.Cmd` used to start processes

## Usage Example

```go
import "wails-gui/internal/platform/appdetect"

// List all detected apps
apps := appdetect.DetectInstalledApps()
for _, app := range apps {
    fmt.Printf("[%s] %s (%s)\n", app.Category, app.Name, app.AppBundle)
}

// Open a project in VS Code (macOS)
err := appdetect.OpenInApp("Visual Studio Code.app", "/path/to/project")
if err != nil {
    log.Fatal(err)
}
```

`OpenInApp` validates that `projectPath` is a non-empty, accessible directory before launching; it returns an error if the path is invalid.
