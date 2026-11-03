# singleinstance

Enforces single-instance semantics for the Yansu Desktop (wails-gui) application — only one desktop app per user session. Uses OS-level locking plus local IPC so a second launch can signal the running instance (e.g. "bring your window to the front") instead of silently exiting or spawning a duplicate.

This follows the same pattern used by Electron's `app.requestSingleInstanceLock()`, Chromium, Slack, Discord, and VS Code.

## Architecture

Two primitives work together across all supported platforms:

| Primitive | Unix (Linux / macOS) | Windows |
|-----------|---------------------|---------|
| **OS lock** | `flock(LOCK_EX \| LOCK_NB)` on a file under `~/.yansu-agent/` | `CreateMutexW` in the `Local\` per-session namespace |
| **IPC channel** | Unix domain socket under `~/.yansu-agent/` | Named pipe (`\\.\pipe\<appID>`) via `go-winio` |

The lock is auto-released on process exit (normal or crash), so a dead instance never permanently blocks subsequent launches. The IPC listener is started only after the lock is acquired, guaranteeing no race between probing and connecting.

## Files

| File | Purpose |
|------|---------|
| `singleinstance.go` | Public API: `Acquire`, `Send`, `Instance.Release`, `Instance.Messages()`, and the IPC accept loop |
| `lock_unix.go` | `flock`-based single-instance lock for Linux and macOS |
| `lock_windows.go` | `CreateMutexW`-based single-instance lock for Windows |
| `ipc_unix.go` | Unix domain socket listener/dialer for IPC |
| `ipc_windows.go` | Windows named-pipe listener/dialer using `go-winio` |
| `singleinstance_test.go` | Integration tests for lock exclusion, IPC delivery, and no-listener error paths |

## API

```go
// ErrAlreadyRunning is returned by Acquire when another process already holds
// the lock for the given app identifier.
var ErrAlreadyRunning = errors.New("another instance is already running")

// Acquire takes the single-instance lock for appID and starts an IPC listener.
// On success the lock is held and the listener is accepting messages.
func Acquire(appID string) (*Instance, error)

// Send delivers a newline-terminated message to the running instance for appID.
// Returns an error if no instance is listening or the IPC channel is unreachable.
func Send(appID, msg string) error

// Instance represents an acquired single-instance handle.
type Instance struct { /* ... */ }

// Release closes the IPC listener and releases the OS lock. Idempotent.
func (i *Instance) Release() error

// Messages returns a receive-only channel of messages sent by second launches.
// Buffered up to 16 messages; overflow is dropped (a single "focus" is enough).
func (i *Instance) Messages() <-chan string
```

## Usage

In `wails-gui/main.go`, the package is used at application startup:

```go
const (
    singleInstanceID = "yansu-desktop"
    msgFocus         = "focus"
)

// Attempt to acquire the single-instance lock and start IPC listener.
instance, lockErr := singleinstance.Acquire(singleInstanceID)
if lockErr != nil {
    if errors.Is(lockErr, singleinstance.ErrAlreadyRunning) {
        // Another instance is already running — send it a "focus" message
        // so it brings its window to the front, then exit.
        if err := singleinstance.Send(singleInstanceID, msgFocus); err != nil {
            log.Printf("[main] could not signal running instance: %v", err)
        } else {
            log.Println("[main] another instance is running; sent focus and exiting")
        }
        os.Exit(0)
    }
    log.Fatalf("[main] single-instance lock error: %v", lockErr)
}
defer instance.Release()

// The running instance listens for incoming messages from second launches.
go func() {
    for msg := range instance.Messages() {
        switch msg {
        case "focus":
            // Bring the main window to the foreground.
        }
    }
}()
```

## Dependencies

- **go-winio** (Windows only) — `github.com/Microsoft/go-winio` for named-pipe IPC
- Standard library only on Unix platforms

## Testing

Run from the `wails-gui` directory:

```bash
go test ./internal/singleinstance/...
```

The tests exercise real OS primitives (no mocking):

| Test | What it verifies |
|------|-----------------|
| `TestAcquireRejectsSecondCaller` | Second `Acquire` with same `appID` returns `ErrAlreadyRunning`; after `Release`, a third `Acquire` succeeds |
| `TestSendDeliversMessage` | IPC round-trip: `Send` delivers `"focus"` to the running instance's `Messages()` channel |
| `TestSendWithoutListener` | `Send` to a non-existent listener returns an error cleanly |
