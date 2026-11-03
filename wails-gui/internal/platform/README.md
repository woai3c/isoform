# platform

Cross-platform abstraction layer for the Wails GUI desktop application. Each sub-package exposes a single, platform-neutral API implemented via Go build tags (`//go:build`) for macOS, Linux, Windows, and a no-op fallback.

## Packages

### `activitymon`

Captures user activity events (clicks, keystrokes, app switches, focus changes, speech) and screen content from the operating system.

**Interfaces**

```go
// Monitor captures OS-level user activity events.
type Monitor interface {
    Start(ctx context.Context, callback func(ActivityEvent)) error
    Stop()
    IsRunning() bool
}

// ScreenCapture captures the screen as a JPEG snapshot.
type ScreenCapture interface {
    CaptureRaw() (rgbaPixels []byte, width, height int, err error)
    EncodeJPEG(rgbaPixels []byte, width, height, quality int) ([]byte, error)
}
```

Created with `activitymon.New()` and `activitymon.NewScreenCapture()` respectively.

**ActivityEvent fields**

| Field | Description |
|---|---|
| `Timestamp` | Unix milliseconds |
| `Kind` | `"click"`, `"keyboard"`, `"app_switch"`, `"focus"`, or `"speech"` |
| `AppBundleID` | Bundle identifier of the frontmost app |
| `AppName` | Display name of the frontmost app |
| `WindowTitle` | Title of the focused window |
| `ClickX/Y` | Screen coordinates for click events |
| `ClickRole/Label` | Accessibility role and label of the clicked element |
| `KeyCode/Modifiers` | Key code and modifier flags for keyboard events |
| `KeyChars` | Typed characters as a slice (one element per keystroke; empty in sensitive fields) |
| `KeyChar` | Legacy single-character field; use `GetKeyChars()` to read either field transparently |
| `FocusRole/Label` | Accessibility role and label of the focused element |
| `SpeechText` | Transcribed text from background audio (speech events only) |
| `SpeechSource` | Audio source for speech events: `"microphone"` (local user) or `"system_audio"` (remote speakers) |
| `SpeechLikelyEcho` | True when a mic transcript was captured while speakers were playing (likely acoustic echo, not user speech) |
| `SpeechSpeakerVerified` | Voice-fingerprint match result for mic transcripts: `nil` (not run), `true` (matches enrolled Self), `false` (does not match) |
| `SpeechSpeakerEmbedding` | Speaker fingerprint vector (~192-512 dims) for post-hoc diarization clustering |
| `MeetingCandidate` | Set when speech was captured in the conservative fallback meeting-detection path |
| `MeetingCandidateApp` | App bundle ID that triggered the meeting candidate |
| `MeetingCandidateReason` | Human-readable reason for the meeting candidate classification |

**WindowBounds**

```go
// WindowBounds holds normalized (0-1) window coordinates on screen.
type WindowBounds struct {
    X, Y, W, H float64
}
```

**Helper method**

```go
// GetKeyChars returns KeyChars if populated, or falls back to the legacy KeyChar string.
chars := event.GetKeyChars() // []string
```

**Platform behaviour**

| Platform | Implementation |
|---|---|
| macOS | CGEventTap (keyboard/mouse), NSWorkspace observer (app switch), AXObserver (focus), CGWindowListCreateImage (screen capture); requires Accessibility permission |
| Windows | `SetWindowsHookExW` (keyboard/mouse low-level hooks), `SetWinEventHook` (foreground window changes) via `user32.dll`; no special permissions required |
| Other | No-op stub (all methods return immediately) |

**Monitor stats**

```go
// GetStats returns a snapshot of the event pipeline health counters.
// All values are cumulative since process start except RingPeakDepth (high-water mark).
stats := activitymon.GetStats() // Stats{EventsDrained, Wakeups, RingPeakDepth, RingDrops}
```

**Capture buffer pool**

```go
// Reusable buffer pool for screen capture to reduce GC pressure.
buf := activitymon.AcquireCaptureBuffer(size)
defer activitymon.ReleaseCaptureBuffer(buf)
```

**Screen capture functions**

```go
// Direct screenshot (cross-platform)
pixels, w, h, err := activitymon.CaptureScreen(targetWidth, targetHeight)

// Screen dimensions (cross-platform)
width, height := activitymon.ScreenDimensions()

// Check if screen is locked (cross-platform)
locked := activitymon.IsScreenLocked()
```

**Utility functions (macOS only, no-ops elsewhere)**

```go
bundleID := activitymon.FrontmostAppBundleID()           // e.g. "com.apple.Safari"
appName  := activitymon.FrontmostAppName()               // e.g. "Safari"
info     := activitymon.FrontmostAppInfo()               // AppInfo{BundleID, AppName, WindowTitle}
bounds   := activitymon.FrontmostWindowBounds()          // WindowBounds of frontmost window
sens     := activitymon.FindVisibleSensitiveWindows([]string{"com.1password"}) // []WindowBounds
```

**Computer-use API (macOS only)**

Programmatic control of other applications via the Accessibility APIs.

```go
// Check availability.
if activitymon.ComputerUseSupported() && activitymon.ComputerUsePermitted() {
    // Enumerate UI elements of a running app.
    elements := activitymon.CollectAppElements("Safari") // []UIElement

    // List all running apps / windows.
    apps    := activitymon.ListRunningApps()             // []RunningApp
    windows := activitymon.ListAppWindows("Finder")      // []AppWindow

    // Input synthesis.
    activitymon.ClickAt(100, 200)
    activitymon.TypeText("hello")
    activitymon.PressKey("Return")
    activitymon.Hotkey("cmd+c")
    activitymon.Scroll(-3, 0)
    activitymon.Drag(10, 10, 200, 200)
    activitymon.PasteText("clipboard text")

    // Open a URL in the default browser.
    activitymon.OpenURL("https://example.com")
}
```

`UIElement` exposes `ID`, `Role`, `Title`, `Label`, `Value`, `Description`, `RoleDescription`, `Help`, `Identifier`, `IsActionable`, and bounding-box coordinates (`X`, `Y`, `Width`, `Height`). On non-macOS platforms all functions are no-ops and `ComputerUseSupported()` returns `false`.

**Private browsing detection**

```go
// Returns true if the window title indicates a private/incognito session.
isPrivate := activitymon.IsPrivateBrowsing(event.WindowTitle)
```

Matches patterns for Safari "Private Browsing", Chrome/Edge "Incognito", Firefox "Private Browsing", etc.

**Event coalescing**

`coalesceStateEvents` deduplicates consecutive `app_switch`/`focus` events within a configurable time window, keeping only the latest state per window while preserving all input events (keyboard, click, speech).

---

### `activitymon/appparser`

Extracts structured content (conversations, documents, terminal output) from accessibility trees. Each parser registers itself via `init()` and declares which bundle IDs it handles via `CanParse`.

```go
// Structured parse — prefers AX tree, falls back to flat text.
// Pass the active browser URL (empty for native apps) so URL-aware
// parsers can dispatch.
content := appparser.ParseTree(axTree, flatText, bundleID, windowTitle, browserURL)

// Flat-text parse (legacy).
content := appparser.Parse(axText, bundleID, windowTitle, browserURL)
```

**ParsedContent fields**

| Field | Description |
|---|---|
| `ContentType` | `"conversation"`, `"document"`, `"terminal"`, `"web_page"`, `"task_list"`, `"events"` |
| `Title` | Window or page title |
| `Body` | Full text body |
| `URL` | Page URL (web parsers) |
| `Channel` | Channel/thread name (chat apps) |
| `Messages` | `[]ConversationMessage{Sender, Content, Timestamp, ThreadParent}` |
| `DedupOfHash` | When set, this snapshot was elided because the previous snapshot from the same bundle had identical structured content. Consumers should resolve the sibling payload indicated by the hash. |
| `Metadata` | Arbitrary key-value pairs |

**Supported apps**

| App | Match | Content type |
|---|---|---|
| Chrome, Safari, Firefox, Brave, Edge, Opera, Arc, Vivaldi | bundle ID | `web_page` |
| ChatGPT native (`com.openai.chat`) | bundle ID | `conversation` |
| ChatGPT web (`chatgpt.com`) | URL | `conversation` |
| Claude web (`claude.ai`) | URL | `conversation` |
| Slack (`com.tinyspeck.slackmacgap`) | bundle ID | `conversation` |
| Discord | bundle ID / URL | `conversation` |
| WhatsApp | bundle ID | `conversation` |
| Feishu | bundle ID | `conversation` |
| Tencent Chat | bundle ID | `conversation` |
| iMessage (`com.apple.MobileSMS`) | bundle ID | `conversation` |
| Apple Mail (`com.apple.mail`) | bundle ID | `conversation` |
| Gmail (`mail.google.com`) | URL | `conversation` / `document` |
| Apple Notes (`com.apple.Notes`) | bundle ID | `document` |
| Obsidian (`md.obsidian`) | bundle ID | `document` |
| Notion (`notion.so`, `*.notion.site`, `notion.id`) | URL / bundle ID | `document` |
| GitHub (`github.com`) | URL | `document` / `conversation` |
| Linear (`linear.app`, `com.linear*`) | URL + bundle ID | `document` / `task_list` |
| Asana | bundle ID | `document` / `task_list` |
| Apple Calendar (`com.apple.iCal`) | bundle ID | `events` |
| Google Calendar (`calendar.google.com`) | URL | `events` |
| Apple Reminders (`com.apple.reminders`) | bundle ID | `task_list` |
| Terminal emulators | bundle ID | `terminal` |
| VS Code / Cursor and other code editors | bundle ID | `document` |
| Finder (`com.apple.finder`) | bundle ID | `document` |
| App Store (`com.apple.AppStore`) | bundle ID | `document` |
| Microsoft Office (Word, Excel, PowerPoint) | bundle ID | `document` |

**Parser interfaces**

```go
// All parsers implement CanParse.
type Parser interface {
    CanParse(bundleID string) bool
}

// URLMatcher is optional — for web versions of native apps.
type URLMatcher interface {
    CanParseURL(url string) bool
}

// TextParser operates on flat AX text (fallback path).
type TextParser interface {
    Parser
    Parse(axText, bundleID, windowTitle, url string) *ParsedContent
}

// TreeParser operates on the structured AXNode tree (preferred path).
type TreeParser interface {
    Parser
    ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent
}
```

Dispatch priority: URL match first (TreeParser, then TextParser), then bundle-ID match (TreeParser, then TextParser).

**AX tree utilities:** `FindFirst`, `FindAll`, `Traverse`, `ChildrenByRole`, `HasClass`, `HasClassContaining`, `Text`, `CollectStaticTexts`, `IsSelected`.

---

### `appdetect`

Detects installed editors, terminals, and file managers on the current machine and opens project directories in them.

**API**
```go
// Detect all supported installed apps for the current platform.
apps := appdetect.DetectInstalledApps() // []InstalledApp

// Open a project directory in the named application.
err := appdetect.OpenInApp(app.AppBundle, "/path/to/project")
```

**InstalledApp fields**
| Field | Description |
|---|---|
| `Name` | Display name (e.g. `"VS Code"`) |
| `AppBundle` | Platform identifier (`.app` bundle, `.exe` path, or binary name) |
| `Category` | `"editor"`, `"terminal"`, or `"file_manager"` |

**Platform behaviour**
| Platform | Detection strategy | File manager |
|---|---|---|
| macOS | Scans `/Applications` and `/System/Applications` | Finder (always present) |
| Linux | `exec.LookPath` for binaries in PATH | `xdg-open` (always added) |
| Windows | PATH + `%ProgramFiles%` + `%LOCALAPPDATA%\Programs` | Explorer (always present) |

---

### `browser`

Finds the path to a Chromium-based browser binary that supports `--app` mode (used for popup windows).

```go
path := browser.FindChromium() // "" if none found
```

Searches for Chrome, Chromium, Edge, and Brave in platform-appropriate locations.

---

### `clipboard`

Writes text to the system clipboard.

```go
err := clipboard.Write("text to copy")
```

| Platform | Tool used |
|---|---|
| macOS | `pbcopy` |
| Linux | `xclip -selection clipboard` |
| Windows | `clip.exe` |

---

### `cmdutil`

Drop-in replacements for `exec.Command` / `exec.CommandContext` that suppress visible console windows on Windows. On non-Windows platforms they are pass-throughs.

```go
// Equivalent to exec.Command but hides console on Windows.
cmd := cmdutil.Command("git", "clone", url)

// With context.
cmd := cmdutil.CommandContext(ctx, "git", "clone", url)

// For long-running commands: contains child processes in a Windows Job Object
// so all descendants share the hidden console and are cleaned up on exit.
cleanup, err := cmdutil.StartWithJobObject(cmd)
if err != nil { return err }
defer cleanup()
err = cmd.Wait()

// Git-specific helpers that use a registered path resolver.
cmdutil.SetGitPathResolver(func() string { return resolvedGitPath })
cmd = cmdutil.GitCommand("clone", url)
cmd = cmdutil.GitCommandContext(ctx, "status")
path := cmdutil.GitPath() // returns resolved path, falls back to "git"
```

> **Windows note:** `StartWithJobObject` assigns the child process to a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, ensuring grandchild processes (e.g. `git-index-pack`) are reaped when the parent exits. Use this instead of plain `cmd.Start()` for any long-running subprocess.

---

### `fonts`

Enumerates installed system font family names (sorted, deduplicated).

```go
families := fonts.List() // []string
```

| Platform | Source |
|---|---|
| macOS | CoreText API via CGo (`CoreText.framework`, `CoreFoundation.framework`), falls back to `fc-list` |
| Linux | `fc-list` (fontconfig) |
| Windows | `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts` registry key |
| Other | Returns `nil` |

The shared `fonts.Dedup(lines []string) []string` helper removes blanks, deduplicates, and sorts—useful when building font lists from shell output.

---

### `notify`

Sends a native desktop notification on Linux and Windows. On macOS, notifications are handled by the app layer via an in-house NSWindow toast and this package is not called.

```go
err := notify.Send("Title", "Body text", nil)

// With a custom icon (Linux/Windows only):
err := notify.Send("Title", "Body text", &notify.Options{IconPath: "/path/to/icon.ico"})

// With a click handler (called when the user clicks the notification):
notify.SetClickHandler(func(clickData string) {
    // handle notification click
})
err = notify.Send("Title", "Body", &notify.Options{ClickData: "open-session-123"})

// Check whether the app window is currently in the foreground.
inForeground := notify.IsAppForeground()
```

`Options.IconPath` sets a custom icon beside the notification.

`Options.ClickData` is an opaque string returned to the registered `ClickHandler` when the user clicks the notification. `SetClickHandler` replaces the previous handler; only one handler is active at a time.

`IsAppForeground()` returns `true` if the app window is the foreground window. On Windows it queries `user32.dll` (`GetForegroundWindow` + `GetWindowThreadProcessId`); on macOS it queries `NSApplication.isActive`; on other non-macOS platforms it conservatively returns `true`.

| Platform | Mechanism |
|---|---|
| macOS | Not handled by this package — the app uses a native NSWindow toast (`toast_window_darwin.go`); `IsAppForeground` uses `NSApplication.isActive` via CGo |
| Linux | `notify-send` (`-i` flag used when `IconPath` is set) |
| Windows | WinRT Toast Notification (`Windows.UI.Notifications.ToastNotificationManager`) via PowerShell; registers a custom `AppUserModelID` so "Yansu" appears as the app name; supports `.ico` (registry icon) and sibling `appicon.png` (toast logo) via `IconPath` |

---

### `ocr`

Performs optical character recognition on JPEG image data.

```go
// Create an OCR engine (platform-specific implementation selected at compile time).
engine := ocr.New()

// Check if OCR is available on this platform.
if engine.IsSupported() {
    // Simple text extraction.
    result, err := engine.RecognizeJPEG(jpegData, []string{"en-US"}, ocr.PriorityHigh)

    // Text with bounding boxes.
    observations, err := engine.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)

    // Query supported languages at runtime.
    langs := engine.SupportedLanguages()
}
```

**Engine interface**

```go
type Engine interface {
    RecognizeJPEG(jpegData []byte, langs []string, prio Priority) (*Result, error)
    RecognizeJPEGWithBoxes(jpegData []byte, langs []string, prio Priority) ([]TextObservation, error)
    SupportedLanguages() []string
    IsSupported() bool
}
```

`TextObservation` coordinates are normalized `[0,1]` with the origin at top-left (converted from Vision framework's bottom-left origin on macOS).

**Priority levels**

| Constant | Value | Use case |
|---|---|---|
| `PriorityLow` | 0 | Bulk background work (post-session OCR worker) |
| `PriorityMid` | 1 | Realtime capture-path work (PII masking) |
| `PriorityHigh` | 2 | User-interactive work (highlight-on-click) |

**Helper functions**

```go
// Normalize fullwidth ASCII variants to halfwidth.
text := ocr.NormalizeFullwidth(rawText)

// Build an optimal language list by filtering preferred languages against platform support.
langs := ocr.ResolveLanguages(preferred, supported)
```

| Platform | Implementation | Notes |
|---|---|---|
| macOS | Apple Vision framework via CGo | macOS 10.15+; `Accurate` level for PriorityMid/High, `Fast` for PriorityLow |
| Windows | PaddleOCR-json (default) or WinRT `Windows.Media.Ocr` (fallback) | Selected via `YANSU_PADDLE_OCR` env var; Paddle is more accurate for CJK but slower |
| Other | No-op stub; `IsSupported()` returns `false` | |

**Windows environment variables**

| Variable | Effect |
|---|---|
| `YANSU_PADDLE_OCR` | Set to `0`/`false`/`no`/`off` to force WinRT backend. Unset or any other value selects PaddleOCR (default). |
| `YANSU_PADDLE_OCR_EXE` | Override the path to `PaddleOCR-json.exe`. |

---

### `shellenv`

Resolves the user's runtime environment so that CLI tools installed outside the GUI app's inherited PATH (common on macOS app bundles) are discoverable.

**Key functions**

```go
// Called once at app startup. On macOS, re-sources the login shell to get
// the full user PATH; on other platforms just ensures common tool dirs are present.
shellenv.ResolveStartupPATH()

// Locate a command using PATH, login shell, and common install directories.
resolved := shellenv.ResolveCommandPath("claude") // absolute path or ""

// Expand ~ in a path string.
expanded := shellenv.ExpandUserPath("~/bin/tool")

// Build a shell command that runs via the login shell (used for setup scripts).
cmd := shellenv.BuildSetupCommand(ctx, "npm install")

// Check whether a file exists and is executable.
ok := shellenv.IsExecutableFile("/usr/local/bin/node")

// Safe shell quoting for interpolation into shell scripts.
quoted := shellenv.ShellQuote(userInput)

// Parse a value bracketed by known markers from shell output.
val := shellenv.ParseMarkedValue(output, "BEGIN=", "=END")

// Resolve a single env var via the login shell (reads the user's full environment).
value := shellenv.ResolveEnvVar("ANTHROPIC_API_KEY") // "" if not found or name unsafe

// Resolve and apply multiple env vars to the current process in one call.
// Returns the names of variables that were successfully set.
applied := shellenv.ApplyEnvVars("ANTHROPIC_API_KEY", "OPENAI_API_KEY")
```

**PATH resolution (macOS)**

macOS app bundles launched from Finder inherit only `/usr/bin:/bin:/usr/sbin:/sbin`. `ResolveStartupPATH` runs the user's login shell (`-lc` / `-lic`) and extracts `$PATH` using sentinel markers. It then scores candidates (preferring those that contain `claude`, `codex`, or `node`) and calls `os.Setenv("PATH", ...)` with the best result. Finally it appends any missing directories for the above three tools.

**Command search order** (`ResolveCommandPath`)
1. `exec.LookPath` against the current process PATH
2. Login shell resolution (Unix) / `exec.LookPath` (Windows)
3. Hard-coded common install dirs (`.bun/bin`, `.local/bin`, Homebrew, npm globals, etc.)

---

## Architecture

All packages follow the same pattern:

```
<package>/
  <package>.go          # shared types / helpers, package doc comment
  <package>_darwin.go   # //go:build darwin
  <package>_linux.go    # //go:build linux
  <package>_windows.go  # //go:build windows
  <package>_other.go    # //go:build !darwin && !windows && !linux  (no-op fallback)
```

This ensures the public API compiles on every supported target with zero runtime panics on unsupported platforms—callers simply receive empty/nil results.

## Dependencies

| Package | External dependency | Platform |
|---|---|---|
| `activitymon` | CoreGraphics, ApplicationServices, Cocoa, Carbon (system frameworks) via CGo | macOS only |
| `cmdutil` | `golang.org/x/sys/windows` | Windows only |
| `fonts` | CoreText / CoreFoundation (system frameworks) via CGo | macOS only |
| `fonts` | `golang.org/x/sys/windows/registry` | Windows only |
| `ocr` | Vision, CoreImage (system frameworks) via CGo | macOS only |
| `ocr` | `Windows.Media.Ocr` WinRT via PowerShell subprocess (stdlib) | Windows only |
| `notify` | `user32.dll` via `golang.org/x/sys/windows` | Windows only |
| All others | stdlib only | All |

## Usage in the Wails app

```go
import (
    "wails-gui/internal/platform/activitymon"
    "wails-gui/internal/platform/appdetect"
    "wails-gui/internal/platform/browser"
    "wails-gui/internal/platform/clipboard"
    "wails-gui/internal/platform/cmdutil"
    "wails-gui/internal/platform/fonts"
    "wails-gui/internal/platform/notify"
    "wails-gui/internal/platform/ocr"
    "wails-gui/internal/platform/shellenv"
)

func main() {
    // Resolve full user PATH before anything else.
    shellenv.ResolveStartupPATH()

    // ... start Wails app ...
}
```
