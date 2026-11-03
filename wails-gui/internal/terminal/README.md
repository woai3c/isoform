# terminal

This package implements an embedded PTY (pseudo-terminal) service for the Wails GUI application. It manages multiple concurrent terminal sessions, bridges each session to a local WebSocket server, and handles terminal capability queries transparently so that TUI applications (such as `nvim`) work correctly inside the xterm.js frontend renderer.

## Purpose

When a user opens a terminal panel in the Wails GUI, this package:

1. Spawns a login shell in a PTY.
2. Starts a shared local WebSocket server (bound to `127.0.0.1`, random port) to expose each shell session.
3. Intercepts terminal capability queries emitted by shell programs, replies to them directly on the PTY, and strips them from the data forwarded to the frontend — preventing xterm.js from generating duplicate, delayed responses that would corrupt TUI applications.

## Key Components

| File | Description |
|---|---|
| `manager.go` | `Manager` — lifecycle management for all terminal sessions and the shared WebSocket server. |
| `responder.go` | `Responder` — stateful escape-sequence parser that intercepts and responds to terminal queries. |
| `responder_test.go` | Unit tests for `Responder` covering all supported query types. |
| `shell_resolve.go` | `resolveShellCommand` — maps a `RemoteEnv` (nil/local, WSL, SSH) to the correct shell binary and arguments. |
| `shell_unix.go` | Unix/macOS shell detection — respects `$SHELL`, falls back to `/bin/zsh` (macOS) or `/bin/bash`. |
| `shell_windows.go` | Windows shell detection — prefers PowerShell 7 (`pwsh.exe`) → PowerShell 5 → `cmd.exe`. |
| `shutdown_unix.go` | Graceful Unix process termination: `SIGTERM` → 3-second timeout → `SIGKILL`. |
| `shutdown_windows.go` | Windows process termination: direct `Kill()` (no SIGTERM on Windows). |

## Architecture

```
Frontend (xterm.js)
       │  WebSocket ws://127.0.0.1:<port>/terminal/<id>
       ▼
 Manager.handleTerminalWS
       │                    │
  PTY → WS goroutine    WS → PTY goroutine
  reads PTY output      writes WS input
  passes through            to PTY
  Responder.Filter
       │
  ┌────┴──────────────────────────────────────┐
  │              Responder                    │
  │  Intercepts queries, writes responses     │
  │  directly back to PTY master, strips      │
  │  query bytes from forwarded data.         │
  └───────────────────────────────────────────┘
```

**WebSocket lifecycle**: The HTTP server starts on the first `CreateTerminal` call and stops automatically when the last terminal is closed.

**Security**: All WebSocket upgrade requests are validated — the remote address must be loopback (`127.0.0.1` / `::1` / `localhost`) and the `Origin` header must be one of `localhost`, `127.0.0.1`, `::1`, `wails.localhost`, or `wails` (desktop webviews may omit `Origin` entirely, which is also allowed).

**Shell shutdown**: `CloseTerminal` sends `SIGTERM` first, gives the process 3 seconds to clean up, then sends `SIGKILL` if it hasn't exited.

## Responder — Supported Terminal Queries

The `Responder` handles the following escape sequences by replying directly on the PTY and stripping them from the xterm.js data stream:

| Query | Sequence | Response |
|---|---|---|
| DA1 (Primary Device Attributes) | `ESC [ c` / `ESC [ 0 c` | `ESC [ ? 62 ; 22 c` |
| DA2 (Secondary Device Attributes) | `ESC [ > c` / `ESC [ > 0 c` | `ESC [ > 0 ; 0 ; 0 c` |
| OSC 10 (foreground color query) | `ESC ] 10 ; ? BEL/ST` | X11 rgb spec of configured fg color |
| OSC 11 (background color query) | `ESC ] 11 ; ? BEL/ST` | X11 rgb spec of configured bg color |
| DECRQM (mode query) | `ESC [ ? <n> $ p` | `ESC [ ? <n> ; 0 $ y` (not recognised) |
| Kitty keyboard protocol query | `ESC [ ? u` | `ESC [ ? 0 u` (flags = 0) |
| CPR (Cursor Position Request) | `ESC [ 6 n` | `ESC [ 1 ; 1 R` |
| XTVERSION | `ESC [ > q` | `ESC P > | ESC \` (empty DCS) |
| DCS sequences | `ESC P … ESC \` | Stripped entirely (no response) |

All other escape sequences (cursor movement, color codes, alternate-screen switching, etc.) are passed through unmodified.

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/aymanbagabas/go-pty` | PTY creation, window resize, and cross-platform shell command execution |
| `github.com/gorilla/websocket` | WebSocket server and connection handling |
| `github.com/google/uuid` | Unique terminal session IDs |

## Usage

### Creating a terminal

```go
import terminalsvc "wails-gui/internal/terminal"

// Provide a callback that fires when a shell exits.
mgr := terminalsvc.NewManager(func(ctx context.Context, id string) {
    runtime.EventsEmit(ctx, "terminal:exit", id)
})

// Start a shell in the given directory with optional theme colors (local).
info, err := mgr.CreateTerminal(ctx, "/path/to/workdir", &terminalsvc.ThemeColors{
    Foreground: "#c0caf5",
    Background: "#0f0f14",
}, nil) // nil env = local shell
// info.ID     — opaque string used to identify this session
// info.WSPort — connect xterm.js to ws://127.0.0.1:<WSPort>/terminal/<ID>

// Start a shell inside a WSL distribution.
info, err := mgr.CreateTerminal(ctx, "C:\\Users\\me\\project", colors, &terminalsvc.RemoteEnv{
    Type:   "wsl",
    Distro: "Ubuntu-22.04", // empty string = WSL default distro
})
```

### Resizing

```go
err := mgr.ResizeTerminal(info.ID, 24, 80) // rows, cols
```

### Closing

```go
mgr.CloseTerminal(info.ID) // graceful SIGTERM → SIGKILL
mgr.CloseAll()             // shut down all sessions and the WS server
```

### Listing active terminals

```go
list := mgr.ListTerminals() // []terminalsvc.Info
```

## Configuration

| Environment variable | Effect |
|---|---|
| `SHELL` | Shell binary to launch (falls back to `/bin/zsh` on macOS, `/bin/bash` elsewhere) |

Each shell process inherits `TERM=xterm-256color`, `TERM_PROGRAM=xterm.js`, and (when theme colors are provided) `COLORFGBG` so that vim/nvim can infer the background brightness without issuing OSC queries.

## Testing

Run the unit tests from the module root:

```bash
cd wails-gui
go test ./internal/terminal/...
```

The tests cover all supported query types, mixed data (queries interleaved with normal output), sequences split across multiple `Filter` calls, non-query sequences that must pass through unchanged, and helper utilities (`hexToXColorSpec`, `isDarkHex`).
