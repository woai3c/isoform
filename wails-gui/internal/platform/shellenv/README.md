# shellenv

Cross-platform shell environment utilities for the Wails GUI desktop app.

## Purpose

When a GUI app is launched from a desktop environment (Finder on macOS, a Windows shortcut, etc.) it often inherits a stripped-down `PATH` that omits tools installed by package managers like Homebrew, nvm, or bun. This package solves that problem at startup and provides helpers for locating and invoking CLI tools reliably on every supported platform.

## Key Components

| File | Platform | Description |
|---|---|---|
| `shellenv.go` | all | Shared, platform-agnostic helpers: `ResolveCommandPath`, `ExpandUserPath`, `ParseMarkedValue` |
| `shellenv_unix.go` | `!windows` | Unix/macOS implementations: login-shell PATH resolution, `LoginShell`, `ShellQuote`, `IsExecutableFile` |
| `shellenv_windows.go` | `windows` | Windows implementations of the same interface; uses `COMSPEC`/PowerShell and Windows executable extensions |
| `startup_darwin.go` | `darwin` | `ResolveStartupPATH` — replaces the process PATH with the full login-shell PATH on macOS app bundles |
| `startup_other.go` | `!darwin` | `ResolveStartupPATH` — no-op PATH replacement; only appends missing tool directories |
| `setup_cmd_unix.go` | `!windows` | `BuildSetupCommand` — wraps a shell command in a login shell (`-l -c`) |
| `setup_cmd_windows.go` | `windows` | `BuildSetupCommand` — wraps a shell command in `cmd.exe /C` |

## Public API

### Startup

```go
// Call once at app startup to enrich the process PATH.
shellenv.ResolveStartupPATH()
```

On **macOS**, this queries the user's login shell (zsh, bash, etc.) to obtain the full `PATH` and replaces the process `PATH` with it. It then also appends the directories containing `claude`, `codex`, and `node` if they are not already present.

On **Linux/Windows**, the process already inherits the full `PATH`, so this only appends any missing tool directories.

### Command resolution

```go
// Find the absolute path of a CLI tool using multiple strategies.
path := shellenv.ResolveCommandPath("claude")
// e.g. "/opt/homebrew/bin/claude"

// Check that a path is an executable file (platform-aware).
ok := shellenv.IsExecutableFile("/usr/local/bin/node")

// Expand ~ in a path string.
expanded := shellenv.ExpandUserPath("~/.bun/bin")
```

`ResolveCommandPath` tries three strategies in order:
1. `exec.LookPath` against the current `PATH`.
2. A login-shell invocation (`whence -p`, `type -P`, `command -v`) to pick up tools in the user's full environment.
3. A hard-coded list of common tool directories (`.bun/bin`, `.local/bin`, Homebrew, etc.).

### Running setup scripts

```go
// Build an exec.Cmd that runs `command` through the platform-native shell.
cmd := shellenv.BuildSetupCommand(ctx, "npm install -g @anthropic-ai/claude-code")
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
_ = cmd.Run()
```

On Unix this uses the login shell (`-l -c`); on Windows it uses `cmd.exe /C`.

### Environment variable resolution

```go
// Resolve a single env var from the login shell environment.
value := shellenv.ResolveEnvVar("ANTHROPIC_API_KEY")

// Resolve and apply multiple env vars into the current process environment.
// Returns the names of vars that were successfully set.
applied := shellenv.ApplyEnvVars("ANTHROPIC_API_KEY", "OPENAI_API_KEY")
```

`ResolveEnvVar` queries the user's login shell for a variable that may not be present in the GUI app's inherited environment. Only names matching `[A-Z][A-Z0-9_]*` are accepted (validated by `isSafeEnvVarName`). `ApplyEnvVars` is a batch convenience wrapper that calls `os.Setenv` for each resolved variable.

### Shell quoting

```go
// Safely embed a value in a shell script.
quoted := shellenv.ShellQuote(userSuppliedValue)
// Unix:    'value' (single-quote escape)
// Windows: "value" (double-quote escape)
```

### Parsing marked output

```go
// Extract a value embedded between sentinel markers in shell output.
value := shellenv.ParseMarkedValue(output, "BEGIN=", "=END")
```

Used internally to safely extract values from login-shell output without relying on a fixed line format.

## Design Notes

- **Login-shell invocation**: On Unix the package runs the login shell twice (`-lc` and `-lic`) and picks the `PATH` candidate with the highest *score*. Score is computed as the number of PATH entries, with a +1000 bonus for each of `claude`, `codex`, and `node` being present. This ensures the richest environment wins when the two invocations produce different results.
- **Build tags**: All platform-specific files use `//go:build` constraints so the correct implementation is selected automatically by the Go toolchain.
- **No global state**: Functions are stateless; `ResolveStartupPATH` mutates `os.Environ` once at startup as an explicit side effect, which is the expected usage pattern.

## Dependencies

- Standard library only (`os`, `os/exec`, `path/filepath`, `strings`, `log`).
- `setup_cmd_windows.go` depends on `wails-gui/internal/platform/cmdutil` for `CommandContext`.

## Usage in the Larger Project

`ResolveStartupPATH()` is called once during Wails app initialisation so that subsequent `exec.Command` calls throughout the application can find `claude`, `codex`, `node`, and other CLI tools regardless of how the app was launched. `BuildSetupCommand` is used wherever the app needs to run a shell script (e.g. dependency installation) through the native shell.
