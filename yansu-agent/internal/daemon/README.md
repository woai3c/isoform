# daemon

The `daemon` package implements the yansu relay daemon — a long-running background process that bridges chat platforms (Slack) with local development projects via the yansu backend.

## Purpose

The daemon listens for incoming messages from chat platforms, routes them to the correct command handler or AI task, executes operations against registered local project directories, and streams results back to the user. It runs in **relay mode**: instead of connecting directly to Slack, it receives events from the centralized yansu backend over a persistent WebSocket connection.

## Key Components

| File | Responsibility |
|------|---------------|
| `daemon.go` | Entry point — `Run()` bootstraps and starts the daemon loop |
| `router.go` | Parses incoming events and dispatches to serial command handlers or AI tasks |
| `adapter.go` | `Adapter` interface that chat platform implementations must satisfy |
| `relay.go` | `RelayAdapter` — WebSocket connection to the backend relay (`/api/daemon/ws`) |
| `event.go` | Platform-agnostic `Event` type for all incoming messages |
| `registry.go` | `ProjectRegistry` — maps project refs to local directory paths |
| `session.go` | Per-user `Session` queues and `SessionManager` |
| `service.go` | OS-level service management (launchd on macOS, systemd on Linux) |

## Architecture

```
Chat Platform (Slack)
        │
        ▼
  yansu Backend (DaemonHub)
        │  WebSocket /api/daemon/ws
        ▼
  RelayAdapter.Listen()
        │
        ▼
  daemon.Run() event loop
        │
        ├─ serial commands → Session.Enqueue() → Router.Handle()
        │                    (one at a time per user)
        │
        └─ AI / free-text  → go Router.handleAI()
                             (up to 3 concurrent per user)
```

### Execution Paths

The router separates two execution strategies based on the command:

- **Serial path** — known management commands (`sync`, `push`, `pull`, `status`, `register`, `stages`, `stage`, `trigger`, `feedback-*`, `tasks`, `versions`, `restore`, `help`) are enqueued in a per-user channel (capacity 5) and executed one at a time. This prevents race conditions on the local filesystem.

- **AI path** — free-text messages and `analyze` bypass the serial queue and run concurrently in goroutines. A semaphore limits each user to 3 simultaneous AI tasks.

### Project Selection

When a command is issued but multiple projects are registered, the router stores a `PendingState` for the user and prompts with *"Which project?"*. The next message from that user is interpreted as a project selection, which resumes the original command.

### Relay Connection

`RelayAdapter` maintains a persistent WebSocket to the backend:

- Authenticates with a Bearer token.
- Sends a `register` message with `team_id` and `slack_user_id` on connect.
- Sends a `heartbeat` every 30 seconds to keep the route key alive.
- Reconnects with exponential backoff (2 s → 5 min cap) on disconnect.
- Enters a 30-second dormant backoff when a `replaced` message is received (another daemon took over the identity).

## Dependencies

- [`github.com/gorilla/websocket`](https://github.com/gorilla/websocket) — WebSocket client
- `yansu-agent/internal/client` — backend REST/WS API client
- `yansu-agent/internal/config` — configuration, PID file, project config helpers
- `yansu-agent/internal/sync` — sync primitives shared across packages

## Usage

The daemon is started via the CLI:

```bash
yansu daemon start          # run in foreground (relay mode)
```

To install as a persistent OS service:

```bash
yansu service install       # macOS: launchd | Linux: systemd
yansu service status
yansu service restart
yansu service uninstall
```

### Project Registration

Projects must be registered before commands targeting them will work:

```bash
cd /path/to/my-project
yansu daemon register       # stores org/product/project → /path/to/my-project
```

The registry is persisted at `~/.yansu-agent/projects.json`.

## Configuration

| Item | Location | Purpose |
|------|----------|---------|
| Registry | `~/.yansu-agent/projects.json` | Maps project refs to local paths |
| PID file | `~/.yansu-agent/daemon.pid` | Used by service management commands |
| Daemon logs | `~/.yansu-agent/logs/daemon.log` | stdout/stderr when running as a service |
| Auth token | `~/.yansu-agent/config.json` (via `config.Config`) | Login token for backend API |

Service installation persists the resolved `YANSU_AGENT_HOME` and `YANSU_URL`
in both launchd and systemd configuration. The published folder retains
`ai.yansu.daemon` / `yansu-daemon.service`; `~/.isoform-agent` uses
`ai.isoform.daemon` / `isoform-daemon.service`. Other absolute data folders get a
stable service-name suffix, so installing or managing one folder does not
replace another folder's daemon. Use the same selected home for install, status,
stop, restart and uninstall. A CLI extracted to `~/.isoform-agent/bin/yansu`
infers its folder even when invoked without the desktop's environment.
Relative and unexpanded `YANSU_AGENT_HOME` values are rejected.

## Testing

Unit tests are co-located in the package (`package daemon`):

```bash
cd yansu-agent && go test ./internal/daemon/...
```

Key test files:

- **`router_test.go`** — verifies command dispatch, project selection flow, AI/serial path isolation, and that `serialCommands` stays in sync with `Handle()` switch cases.
- **`slack_context_test.go`** — verifies `buildPromptWithContext` prompt construction, size limits, and context truncation behaviour.

## Interfaces

### `Adapter`

Any chat platform integration must implement:

```go
type Adapter interface {
    Listen(ctx context.Context, handler func(Event)) error
    Reply(event Event, text string)
    PostAndStream(event Event, ch <-chan string)
}
```

### `ContextProvider` (optional)

Adapters that can supply conversation history implement:

```go
type ContextProvider interface {
    FetchContext(ctx context.Context, event Event) ([]ContextMessage, error)
}
```

`RelayAdapter` implements both interfaces.

### `Event`

```go
type Event struct {
    UserID    string
    ChannelID string
    ThreadTS  string      // non-empty for thread replies
    Text      string
    EventTS   string
    TeamID    string
    Platform  string      // "slack"
    RelayID   string      // backend-assigned UUID for relay correlation
    Files     []EventFile // attached files (images, documents, etc.)
}

type EventFile struct {
    ID       string
    Name     string
    MimeType string
    URL      string // private download URL
    Size     int    // file size in bytes
    Thumb    string // thumbnail URL for images
    Content  []byte // downloaded file content
}
```
