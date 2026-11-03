# notify

Cross-platform desktop notification package for the Wails GUI application.

## Overview

Package `notify` exposes a `Send` function, a `SetClickHandler` registration function, and an `IsAppForeground` helper that together enable native desktop notifications on Linux and Windows. macOS notifications are handled by a separate in-app NSWindow toast mechanism and do not go through this package's `Send` function. Platform-specific implementations are selected at compile time via Go build tags.

## API

### Send

```go
func Send(title, body string, opts *Options) error
```

Displays a desktop notification with the given title and body. Pass `nil` for `opts` to use defaults. Returns an error if the underlying system command or framework call fails.

**Platform availability**: Linux and Windows only. macOS uses in-app NSWindow toast.

### IsAppForeground

```go
func IsAppForeground() bool
```

Reports whether the application window is currently in the foreground. Call sites use this to decide between a native OS notification (app in background) and an in-app toast (app already visible).

| Platform | Mechanism | Notes |
|----------|-----------|-------|
| macOS | CGo → `[NSApplication sharedApplication].isActive` | Accurate native detection |
| Windows | `user32.dll` `GetForegroundWindow` + `GetWindowThreadProcessId` | Accurate: detects when window is behind others, not just minimized |
| Linux (and others) | Returns `true` (conservative fallback) | Causes callers to prefer in-app toast |

### SetClickHandler

```go
func SetClickHandler(handler ClickHandler)
```

Registers a callback invoked when the user clicks a delivered desktop notification. Only one handler can be active at a time; calling again replaces the previous handler. The handler is called in a new goroutine.

```go
type ClickHandler func(clickData string)
```

### Options

```go
type Options struct {
    // IconPath is the absolute path to a .png or .ico file shown beside the
    // notification. Leave empty to use the OS default icon.
    IconPath string

    // ClickData is an opaque string attached to the notification and returned
    // via the registered ClickHandler when the user clicks the notification.
    // Leave empty if no click handling is needed.
    ClickData string
}
```

## Platform Implementations

| File | Build Tag | Mechanism |
|------|-----------|-----------|
| `notify_linux.go` | `linux` | `notify-send <title> <body>` — `IconPath` passed via `-i` flag |
| `notify_windows.go` | `windows` | PowerShell WinRT `ToastNotificationManager` with custom AUMID |
| `focus_darwin.go` | `darwin` | CGo → `NSApplication.isActive` |
| `focus_windows.go` | `windows` | `user32.dll` `GetForegroundWindow` |
| `focus_other.go` | `!windows && !darwin` | Conservative fallback — always returns `true` |

### Linux detail

Wraps `notify-send` from `libnotify`. If `opts.IconPath` is set, it is passed via the `-i` flag. `ClickData` is not supported on Linux (no click-back mechanism from `notify-send`).

### Windows detail

Spawns a PowerShell process that uses the WinRT `Windows.UI.Notifications.ToastNotificationManager` API. It:

1. Registers a custom `AppUserModelID` (`Yansu.Desktop`) under `HKCU\Software\Classes\AppUserModelId` so the notification displays "Yansu" rather than "Windows PowerShell".
2. Optionally sets an `IconUri` registry value when a `.ico` path is provided via `opts.IconPath`.
3. Looks for a sibling `appicon.png` next to the `.ico` file and uses it as the `appLogoOverride` in the toast XML.
4. Constructs a `ToastGeneric` XML payload and shows it via `ToastNotificationManager::CreateToastNotifier`.

The PowerShell process exits promptly after calling `Show()`. `Send` blocks until the process exits, but callers need not wrap it in a goroutine for non-blocking behavior in practice.

## Dependencies

- [`wails-gui/internal/platform/cmdutil`](../cmdutil) — thin wrapper around `exec.Command` used by the Linux and Windows implementations.
- **Linux**: requires `libnotify` / `notify-send` installed on the host (`apt install libnotify-bin` or equivalent).
- **macOS**: requires CGo and the `AppKit` framework (for `IsAppForeground`); no `Send` implementation.
- **Windows**: requires PowerShell and `user32.dll` — available on all modern Windows versions.

## Usage

```go
import "wails-gui/internal/platform/notify"

// Register a click handler (call once at startup)
notify.SetClickHandler(func(clickData string) {
    log.Printf("notification clicked: %s", clickData)
})

// Basic notification
if err := notify.Send("Build finished", "Your project compiled successfully.", nil); err != nil {
    log.Printf("notification failed: %v", err)
}

// With a custom icon and click data
if err := notify.Send("Alert", "Something happened.", &notify.Options{
    IconPath:  "/path/to/icon.ico",
    ClickData: "action:open-project:123",
}); err != nil {
    log.Printf("notification failed: %v", err)
}

// Only notify via OS when the app is not in the foreground
if !notify.IsAppForeground() {
    notify.Send("New message", "You have a new message.", nil)
} else {
    // show in-app toast instead
}
```

Errors from `Send` are non-fatal in most call sites — a notification failure should not interrupt the main application flow.

## Testing

No automated tests are present. Manual verification requires a desktop session on each target OS with the relevant notification daemon or framework available.
