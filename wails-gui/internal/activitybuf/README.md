# activitybuf

Thread-safe circular buffer for storing and querying `ActivityEvent` records in the Wails GUI desktop app.

## Purpose

Provides a bounded, rolling window of user activity events (keyboard input, clicks, etc.) for consumption by the AI context layer. Events are automatically evicted when the buffer exceeds its capacity or when events fall outside the configured time window. Consecutive keyboard events from the same application are merged into a single event to reduce noise.

## Key Components

| File | Description |
|------|-------------|
| `buffer.go` | Core `RingBuffer` implementation |
| `buffer_test.go` | Unit tests covering eviction, wrap-around, merging, and snapshot isolation |

## Architecture

`RingBuffer` is a fixed-size circular buffer backed by a pre-allocated slice. It enforces two independent eviction policies on every `Push`:

1. **Capacity eviction** — when the buffer is full, the oldest event is overwritten.
2. **Time-window eviction** — events older than the configured `window` are dropped.

Keyboard event merging reduces the number of stored events: when a new `EventKeyboard` event arrives from the same `AppBundleID` within 2 seconds of the previous keyboard event, the character is appended to the existing event rather than stored separately.

All public methods are protected by a `sync.Mutex`.

## Defaults

| Constant | Value |
|----------|-------|
| `defaultCapacity` | 1000 events |
| `defaultWindow` | 30 seconds |
| `mergeKeyboardThreshold` | 2000 ms |

## Usage

```go
import (
    "time"
    "wails-gui/internal/activitybuf"
    "wails-gui/internal/platform/activitymon"
)

buf := activitybuf.NewRingBuffer(1000, 30*time.Second)

// Add an event
buf.Push(activitymon.ActivityEvent{
    Timestamp:   time.Now().UnixMilli(),
    Kind:        activitymon.EventKeyboard,
    KeyChars:    []string{"h"},
    AppName:     "Terminal",
    AppBundleID: "com.apple.Terminal",
})

// Get a snapshot (oldest → newest, safe copy)
events := buf.Snapshot()

// Inspect size
n := buf.Len()

// Reset
buf.Clear()
```

Pass `0` for either argument to use the defaults:

```go
buf := activitybuf.NewRingBuffer(0, 0) // uses defaultCapacity and defaultWindow
```

## API

### `NewRingBuffer(capacity int, window time.Duration) *RingBuffer`

Creates a new buffer. Non-positive values fall back to `defaultCapacity` / `defaultWindow`.

### `Push(evt activitymon.ActivityEvent)`

Adds an event. Merges with the previous keyboard event if conditions are met; otherwise appends. Evicts expired entries after each push.

### `Snapshot() []activitymon.ActivityEvent`

Returns a copy of all live events ordered oldest to newest. Returns `nil` for an empty buffer. Evicts expired entries before copying.

### `Len() int`

Returns the current number of events in the buffer.

### `Clear()`

Removes all events.

## Dependencies

- `wails-gui/internal/platform/activitymon` — defines `ActivityEvent` and `EventKind`
- Standard library: `sync`, `time`

## Testing

```bash
cd wails-gui
go test ./internal/activitybuf/...
```

Tests cover: initialization defaults, push/len, capacity eviction, time-window eviction, snapshot ordering, snapshot isolation (mutations don't affect the buffer), `Clear`, empty snapshot, and ring wrap-around.
