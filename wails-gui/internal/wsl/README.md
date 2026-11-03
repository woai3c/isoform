# wsl

Windows Subsystem for Linux (WSL) integration package for the Wails GUI desktop app.

## Overview

This package provides utilities for detecting, querying, and interacting with WSL distributions on Windows. It uses build tags to provide a no-op stub on non-Windows platforms, keeping cross-platform builds clean.

## Key Components

| File | Description |
|------|-------------|
| `wsl.go` | Windows implementation (`//go:build windows`) — all WSL functionality |
| `wsl_stub.go` | Non-Windows stub (`//go:build !windows`) — no-op implementations |
| `wsl_test.go` | Unit tests for path conversion and distro list parsing |

## API

### Types

```go
type Distro struct {
    Name      string `json:"name"`
    IsDefault bool   `json:"is_default"`
    State     string `json:"state"`   // "Running" or "Stopped"
    Version   int    `json:"version"` // 1 or 2
}
```

### Functions

```go
// Detect WSL availability
Available() bool

// List and query distros
ListDistros() ([]Distro, error)
DefaultDistro() (string, error)

// Path conversion
ToLinuxPath(windowsPath string) string           // "D:\project" -> "/mnt/d/project"
ToWindowsPath(distro, linuxPath string) string   // "/home/user" -> "\\wsl$\Ubuntu\home\user"
IsWSLPath(path string) bool

// Shell integration
ShellCommand(distro, workDir string) (shell string, args []string)
ResolveJSRuntime(distro string) (string, error)  // probes for "node" or "bun"
```

## Path Conversion Examples

```go
// Windows drive path -> WSL mount
ToLinuxPath(`D:\project\src`)    // -> "/mnt/d/project/src"
ToLinuxPath(`C:\Users\bin`)      // -> "/mnt/c/Users/bin"

// UNC WSL paths are stripped to Linux-native paths
ToLinuxPath(`\\wsl$\Ubuntu\home\user`)      // -> "/home/user"
ToLinuxPath(`\\wsl.localhost\Ubuntu\home`)  // -> "/home"

// Already Linux — returned as-is
ToLinuxPath("/home/user/project")  // -> "/home/user/project"

// Linux path -> Windows UNC
ToWindowsPath("Ubuntu", "/home/user")  // -> "\\wsl$\Ubuntu\home\user"
```

## Architecture

The package uses Go build constraints to split implementations:

- **`wsl.go`** — invokes `wsl.exe` via `cmdutil.Command` and parses its UTF-16LE output.
- **`wsl_stub.go`** — satisfies the same interface on Linux/macOS so callers compile unchanged.

`wsl.exe --list --verbose` output is UTF-16LE encoded; `decodeUTF16` handles BOM stripping and decoding before `parseListVerbose` extracts distro records.

## Dependencies

- `wails-gui/internal/platform/cmdutil` — subprocess execution abstraction
- Standard library: `encoding/binary`, `unicode/utf16`, `os/exec`, `path/filepath`

## Testing

Tests run on all platforms (the stub's path functions are pass-through on non-Windows, so `TestToLinuxPath` and `TestToWindowsPath` cover the Windows logic directly):

```bash
cd wails-gui && go test ./internal/wsl/...
```

Tests cover:
- `ToLinuxPath`: drive letters, UNC paths, already-Linux paths, paths with spaces
- `ToWindowsPath`: home dirs, root, default distro fallback
- `IsWSLPath`: UNC detection vs. native Windows/Linux paths
- `parseListVerbose`: header skipping, default marker (`*`), state/version parsing
