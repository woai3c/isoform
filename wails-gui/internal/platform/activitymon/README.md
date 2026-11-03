# activitymon

Platform-specific package for monitoring user activity and capturing screen content in the Wails GUI desktop app. Implemented for macOS (darwin) and Windows; all other platforms receive no-op stubs.

## Purpose

Provides three capabilities:
1. **Activity monitoring** — captures keyboard, mouse click, app switch, UI focus, and speech events system-wide via OS-native APIs.
2. **Screen capture** — takes JPEG or raw RGBA screenshots (full-screen and per-window on macOS).
3. **Computer use / UI automation** — programmatically controls other applications via macOS Accessibility and CoreGraphics APIs (macOS only).

## Key Components

| File | Description |
|---|---|
| `activitymon.go` | Shared types, constants, and interface definitions (`Monitor`, `ScreenCapture`, `ActivityEvent`, `EventKind`). |
| `monitor_darwin.go` | macOS implementation of `Monitor`. Uses CGEventTap for mouse/keyboard events, NSWorkspace notifications for app switches, and AXObserver for UI focus changes. Embedded Objective-C/C via cgo. |
| `monitor_windows.go` | Windows implementation of `Monitor`. Uses `SetWindowsHookExW` (WH_KEYBOARD_LL, WH_MOUSE_LL) for keyboard/mouse events, and `SetWinEventHook` (EVENT_SYSTEM_FOREGROUND) for app-switch detection. Runs a Windows message pump on a locked OS thread. |
| `axtree_darwin.go` | macOS implementation of `CollectFocusedWindowText`. Walks the focused window's AX tree via `AXUIElement` APIs to extract all visible text content (up to 100 KB, max 10000 nodes / 25 levels deep). |
| `axtree_windows.go` | Windows implementation of `CollectFocusedWindowText`. Asks the embedded UIA subprocess (see `uia_windows.go` / `uia_windows.ps1`) to walk the foreground window's UI Automation tree and return its visible text. Falls back to the foreground window title when the subprocess is unavailable. |
| `axtree_tree_windows.go` | Windows implementation of `CollectFocusedWindowTree` and the bundle-targeted variants. Wraps the UIA subprocess to produce the same `AXNode` shape the macOS C walker emits, so the cross-platform `appparser` registry runs on Windows without changes. Also exposes HWND enumeration helpers used by the bundle-targeted variants. |
| `axtree_tree_types.go` | Cross-platform `AXNode` / `AXFrame` Go types shared by darwin / windows / stub builds. |
| `axtree_tree_darwin.go` | macOS implementation of `CollectFocusedWindowTree`. Walks the focused window's AX tree and returns a structured `AXNode` tree (up to 8000 nodes / 40 levels deep, serialized via an embedded C JSON writer into a 256 KB buffer). |
| `uia_windows.go` | Windows-only Go-side bridge to the embedded PowerShell UIA subprocess. Persistent process, JSON-over-stdio protocol, restart-on-failure — mirrors `ocr_windows.go`. |
| `uia_windows.ps1` | Persistent PowerShell script that talks to the Windows UI Automation managed API (`System.Windows.Automation`). Walks the requested HWND's ControlView tree with `CacheRequest` batching, maps UIA `ControlType` to macOS-style `AXRole` strings, normalizes `BoundingRectangle` by the window's DPI, and emits AXNode JSON. |
| `capture_darwin.go` | macOS implementation of `ScreenCapture`. Wraps `CaptureScreen` (CGWindowListCreateImage) and handles BGRA→RGBA conversion plus JPEG encoding. Also exports `IsScreenLocked` and `ScreenDimensions`. |
| `capture_windows.go` | Windows implementation of `ScreenCapture`. Uses `BitBlt`/`GetDIBits` (GDI) for screen capture and Go's `image/jpeg` for encoding. Includes multi-stage fallback chain (sleep-retry, CAPTUREBLT, PrintWindow) for transient DWM failures. Also exports `IsScreenLocked`, `ScreenDimensions`, `FallbackCounts`, `SupportsWindowCapture`, `FrontmostWindowInfo`, and `CaptureWindowJPEG`. |
| `screenshot_sck_darwin.go` | macOS-only per-window screenshot via ScreenCaptureKit (macOS 15+). Captures GPU-accelerated windows cleanly by windowID — cleaner than full-screen capture + focus-rect cropping for Chrome, Electron, and WKWebView content. |
| `capture_pool.go` | Cross-platform buffer pool (`sync.Pool`) for recycling per-call pixel buffers (8–14 MB each). `AcquireCaptureBuffer`/`ReleaseCaptureBuffer` reduce GC pressure on the hot screenshot path. |
| `event_coalesce.go` | Cross-platform event coalescing. `coalesceStateEvents` deduplicates consecutive `app_switch`/`focus` events within a configurable window, keeping only the latest state per window while preserving all input events (keyboard, click, speech). |
| `private_mode.go` | Cross-platform utility `IsPrivateBrowsing(windowTitle)` that detects browser private/incognito sessions by matching window title substrings. |
| `monitor_stub.go` | No-op `Monitor` + `CaptureScreen` + `GetStats` for platforms other than darwin/windows. |
| `capture_stub.go` | No-op `ScreenCapture` + `IsScreenLocked` + `ScreenDimensions` for platforms other than darwin/windows. |
| `axtree_stub.go` | No-op `CollectFocusedWindowText` for platforms other than darwin/windows. |
| `axtree_tree_stub.go` | No-op `CollectFocusedWindowTree` for non-darwin platforms. |
| `browser_url_darwin.go` | macOS implementation of `IsBrowser` and `GetBrowserURL`. Extracts the active tab URL from Chrome, Brave, Edge, Safari, Vivaldi, and Arc via AppleScript. Firefox is listed as a known browser but returns empty (no AppleScript URL support). |
| `browser_url_windows.go` | Windows stub — `IsBrowser` always returns false, `GetBrowserURL` returns empty. |
| `browser_url_stub.go` | No-op browser URL functions for other platforms. |
| `computer_use.go` | Shared types for UI automation: `UIElement`, `RunningApp`, `AppWindow`. |
| `computer_use_darwin.go` | macOS implementation of the computer-use automation API. Wraps CGEventTap (mouse/keyboard injection), AXUIElement tree walking, NSWorkspace app/window enumeration, and NSPasteboard clipboard manipulation via embedded Objective-C/cgo. |
| `computer_use_stub.go` | No-op stubs for all computer-use functions on non-macOS platforms. |
| `computer_use_windows.go` | Windows stubs for computer-use functions (no-op). |
| `appparser/` | Sub-package of app-specific content parsers — see [Content Parsing (appparser)](#content-parsing-appparser) below. |

## Interfaces

### `Monitor`
```go
type Monitor interface {
    Start(ctx context.Context, callback func(ActivityEvent)) error
    Stop()
    IsRunning() bool
}
```
Created with `activitymon.New()`.

### `ScreenCapture`
```go
type ScreenCapture interface {
    CaptureRaw() (rgbaPixels []byte, width, height int, err error)
    EncodeJPEG(rgbaPixels []byte, width, height, quality int) ([]byte, error)
}
```
Created with `activitymon.NewScreenCapture()`.

### `CollectFocusedWindowText`

```go
func CollectFocusedWindowText() string
```

Returns visible text from the focused window's accessibility tree. On macOS, walks the full AX tree (up to 100 KB / 10000 nodes). On Windows, walks the UI Automation tree of the foreground window via the embedded PowerShell subprocess and falls back to the foreground window title when the subprocess is unavailable. Returns an empty string on other platforms.

### `CollectFocusedWindowTree`

```go
func CollectFocusedWindowTree() *AXNode
```

Returns a structured `AXNode` tree for the focused window. On macOS, walks the AX tree via `AXUIElement` (up to 8000 nodes / 40 levels). On Windows, walks the UI Automation tree via the embedded PowerShell subprocess (same budget). Returns nil if no focused window, the platform is unsupported, or the walker yields nothing.

UIA control types are mapped to macOS-style `AXRole` strings (e.g. `Edit` → `AXTextField`, `Document` → `AXDocument`) so the `appparser` registry runs on Windows without per-parser forking. **One important gap**: Chromium does not expose CSS class names via UIA, so `node.DOMClassList` is always empty on Windows. Parsers that rely on `HasClassContaining` (Notion, Linear, GitHub, Asana, Gmail, Discord, code editors, etc.) will not match on Windows and the dispatch falls through to the flat-text `TextParser` path. The activity OCR worker remains the load-bearing source of text for those surfaces until per-app Windows fallbacks are added.

### `AXNode` / `AXFrame`

```go
type AXNode struct {
    Role         string    // e.g. "AXButton", "AXStaticText"
    Subrole      string
    Title        string
    Description  string
    Value        string
    Identifier   string
    Selected     bool      // true when kAXSelectedAttribute is set (user-selection signal)
    DOMClassList []string  // browser DOM class list (WebKit extension; macOS only)
    Frame        *AXFrame  // bounding box in screen coordinates
    Children     []*AXNode
}

type AXFrame struct { X, Y, W, H float64 }
```

The `Selected` field is populated from `kAXSelectedAttribute` and surfaces the "user choice" signal (Finder file rows, NSTableView selections, sidebar pickers, segmented controls).

### Browser URL functions (macOS only)

```go
func IsBrowser(bundleID string) bool
func GetBrowserURL(bundleID string) string
```

`IsBrowser` returns true for Chrome, Brave, Edge, Safari, Vivaldi, Arc, and Firefox bundle IDs. `GetBrowserURL` fetches the active tab URL via AppleScript; returns empty for Firefox or on errors.

On macOS, `main_queue_darwin.m` confines the entire `NSAppleScript` lifetime
(including compilation) and `NSWorkspace` focus reads to the main
queue. Background browser callers retain the one-second meeting budget, focus callers 250 ms;
expired queued reads are skipped. One shared admission remains held until the queued
block is disposed, so timeouts cannot accumulate retained native requests. Native
timeout and in-flight status remain available through `ReadBrowserURL`. Browser
scripts also set a one-second Apple
event timeout. A caller timeout does not interrupt an already executing script.
The copied dispatch block owns its inputs, result box and semaphore until it
finishes, so late completion never writes into a returned C/Go stack buffer.
AX walks stay off the main thread with their existing messaging timeouts;
`NSRunningApplication` metadata is thread-safe. Running-app enumeration calls
`NSWorkspace.runningApplications` directly on the calling thread, as permitted by
[Apple's threading contract](https://developer.apple.com/documentation/appkit/nsworkspace/runningapplications).
It does not require a main run loop or compete for browser/focus admission.
Unavailable focus is explicit in
`AppInfo`; audio remains excluded until a successful safe observation. Go test binaries reject native
`GetBrowserURL` calls before entering cgo; meeting tests must supply and join
browser probe stubs, including after a lookup timeout.

### `IsPrivateBrowsing` (cross-platform)

```go
func IsPrivateBrowsing(windowTitle string) bool
```

Returns true for a standalone private-mode title, a delimited specific mode prefix such as "Private Browsing - ", or a browser private-mode suffix such as "(Incognito)". A page title merely discussing private browsing does not match. Used by the activity monitor to skip capturing in private sessions.

### `ActivityEvent`

| Field | Type | Description |
|---|---|---|
| `Timestamp` | `int64` | Unix milliseconds |
| `Kind` | `EventKind` | `click`, `keyboard`, `app_switch`, `focus`, `speech`, or one Isoform records itself: `calendar_peek`, `device_in_use`, `session_state`, `clipboard` |
| `AppBundleID` | `string` | Bundle ID of the frontmost app (macOS only; Windows uses lowercase exe basename) |
| `AppName` | `string` | Localized name of the frontmost app |
| `WindowTitle` | `string` | Title of the focused window |
| `ClickX/Y` | `float64` | Screen coordinates (click events) |
| `ClickRole` | `string` | AX role of clicked element, e.g. `AXButton` (macOS only) |
| `ClickLabel` | `string` | Text/title of clicked element (macOS only) |
| `KeyCode` | `uint16` | Raw key code (macOS: CGKeyCode; Windows: virtual key code) |
| `Modifiers` | `uint64` | Modifier flags bitmask (macOS: CGEventFlags; unused on Windows) |
| `KeyChar` | `string` | **Legacy** — single typed character or shortcut string; kept for backward-compat with stored data |
| `KeyChars` | `[]string` | Typed characters, one element per keystroke; empty in sensitive areas (passwords, etc.) |
| `FocusRole` | `string` | AX role of focused element, e.g. `AXTextField` (macOS focus events) |
| `FocusLabel` | `string` | Label of focused element (macOS focus events) |
| `SpeechText` | `string` | Transcribed speech text (speech events from background audio transcription) |
| `SpeechSource` | `string` | Audio source for speech events: `"microphone"` for the local user, `"system_audio"` for remote speakers (e.g. Zoom/Teams output). Empty for events persisted before speaker diarization landed. |
| `SpeechLikelyEcho` | `bool` | True when a microphone-source transcript was captured while the Mac's speakers were also playing audible content — very likely acoustic echo rather than actual user speech. |
| `SpeechSpeakerVerified` | `*bool` | Voice-fingerprint matching result for microphone-source transcripts. `nil` = didn't run; `true` = matched enrolled Self voice; `false` = speaker is someone else. |
| `SpeechSpeakerEmbedding` | `[]float32` | Cached speaker fingerprint vector (~192–512 dims) for post-hoc clustering in the Activity detail diarization view. |
| `MeetingCandidate` | `bool` | Set when speech was captured in the conservative fallback path: no explicit meeting was detected, but supported meeting-app and audio evidence were strong enough to keep the transcript together. |
| `MeetingCandidateApp` | `string` | The app that triggered the meeting candidate detection. |
| `MeetingCandidateReason` | `string` | Human-readable reason for the meeting candidate classification. |
| `ClipboardKind` / `ClipboardLength` / `ClipboardPreview` | `string` / `int` / `string` | A `clipboard` event: text, url, file, image or other; the copied text's length (0 when unread); a scrubbed preview of at most 160 characters |

Use `evt.GetKeyChars()` to retrieve key characters in a forward-compatible way — it returns `KeyChars` when present, or falls back to `[]string{KeyChar}` for older persisted events.

## Architecture

### macOS Activity Monitor

The darwin monitor uses a C-side ring buffer (512 events) to decouple the macOS event callbacks from Go. This avoids cgo type conflicts that arise from calling back into Go directly from CGEventTap callbacks.

```
CGEventTap (mouse/keyboard) ─┐
NSWorkspace notification      ├─► C ring buffer ──► Go drain (self-pipe wake) ──► callback
AXObserver (focus change)    ─┘
```

The Go side blocks on a pipe read (zero CPU while idle) and wakes when C writes a byte on empty→non-empty queue transitions. A cross-platform `sync.Pool`-based buffer pool (`capture_pool.go`) recycles per-call pixel buffers (8–14 MB) to reduce GC pressure on the hot screenshot path.

### Event Coalescing

`coalesceStateEvents` (`event_coalesce.go`) deduplicates consecutive `app_switch`/`focus` events within a configurable time window, keeping only the latest state per window while preserving all input events (keyboard, click, speech). This prevents redundant snapshots when the user rapidly switches between apps or moves focus across UI elements.

### Windows Activity Monitor

The Windows monitor uses low-level Win32 hooks installed via `SetWindowsHookExW`. Because Windows hooks require the message pump to run on the same OS thread that installed them, the goroutine is locked via `runtime.LockOSThread`. A blocking `GetMessage` loop dispatches hook callbacks with minimal latency.

```
WH_KEYBOARD_LL hook    ─┐
WH_MOUSE_LL hook        ├─► hook callbacks ──► rawEvent chan ──► runEventDrain ──► callback
SetWinEventHook         ─┘
```

Hook callbacks enqueue minimal `rawEvent` structs into a buffered channel; the drain goroutine performs the expensive foreground-window lookups (process queries, version-info reads) off the hook thread. This eliminates the 300 ms `LowLevelHooksTimeout` risk that would otherwise cause system-wide input lag.

### Windows Screen Capture Fallbacks

Windows screen capture via GDI (`BitBlt` + `GetDIBits`) can fail during DWM transitions (compositor mode switches, fullscreen handoff, protected-content overlays). `capture_windows.go` implements a three-stage recovery chain:

1. **Sleep + retry** — many GDI failures are sub-frame transients.
2. **BitBlt with CAPTUREBLT** — forces DWM to flatten layered windows before copying.
3. **PrintWindow** — goes through `WM_PRINT` messaging, can succeed when BitBlt fails.

Success counts per fallback path are exposed via `FallbackCounts()` for diagnostics.

## Utility Functions

These functions are exported by platform-specific files and are no-ops / return zero values on other platforms.

### `FrontmostAppBundleID`

```go
func FrontmostAppBundleID() string
```

Returns the bundle ID of the current frontmost application (e.g. `"com.apple.Safari"`). On Windows, returns the lowercase executable basename (e.g. `"chrome.exe"`) as the closest stable identifier. Returns an empty string if unavailable.

### `FrontmostAppName`

```go
func FrontmostAppName() string
```

Returns the localized display name of the current frontmost application. On Windows, reads the `FileDescription` from the executable's version info with basename fallback.

### `FrontmostAppInfo`

```go
func FrontmostAppInfo() AppInfo
```

Returns the bundle ID, app name, and window title of the current frontmost application as an `AppInfo` struct. Returns a zero-value `AppInfo` if unavailable.

### `FrontmostWindowBounds`

```go
func FrontmostWindowBounds() WindowBounds
```

Returns the screen-normalized (0–1) bounds of the frontmost application's main window. Coordinates are relative to the full virtual desktop (all active displays). Returns zero bounds if the window cannot be determined. (macOS only; returns zero bounds on Windows.)

### `FindVisibleSensitiveWindows`

```go
func FindVisibleSensitiveWindows(bundlePrefixes []string) []WindowBounds
```

Scans all on-screen windows and returns normalized bounds for every window whose owning app bundle ID matches one of the given prefixes (case-insensitive). Useful for detecting sensitive app content (e.g. password managers) that may be visible even when not frontmost. Returns at most 16 results. (macOS only; returns nil on Windows.)

### `IsScreenLocked`

```go
func IsScreenLocked() bool
```

Returns true when the workstation lock screen or screensaver is active. On macOS, checks `CGSessionCopyCurrentDictionary`. On Windows, checks if the foreground window belongs to `LogonUI.exe`/`LockApp.exe`.

### `ScreenDimensions`

```go
func ScreenDimensions() (width, height int)
```

Returns the logical screen dimensions. On macOS, uses `CGDisplayPixelsWide/High` of the main display. On Windows, uses `GetSystemMetrics(SM_CXSCREEN/SM_CYSCREEN)`.

### `GetStats`

```go
func GetStats() Stats
```

Returns a snapshot of internal pipeline health counters. All values are cumulative since process start except `RingPeakDepth` which is a high-water mark. Safe to call concurrently and cheap enough to poll from a metrics panel. Fields not applicable to a given platform (e.g. ring buffer on non-darwin) are zero.

`Stats` fields: `EventsDrained` (total events dispatched from the ring buffer), `Wakeups` (total pipe wake-ups), `RingPeakDepth` (max ring buffer depth observed), `RingDrops` (events dropped due to ring buffer overflow or full hook channel on Windows).

### Windows-only diagnostics

```go
func FallbackCounts() (sleepRetry, captureBlt, printWindow uint64)
func FrontmostWindowInfo() FrontmostWindow  // ID = HWND, OwnerName, PID; BundleID empty
func FrontmostAppPID() int
func SupportsWindowCapture() bool  // false on Windows
```

## Computer Use / UI Automation (macOS only)

The `computer_use_darwin.go` file provides a programmatic automation API that lets the app control other applications via macOS Accessibility and CoreGraphics APIs. All functions are no-ops on non-darwin platforms (`computer_use_stub.go`).

### Permission check

```go
activitymon.ComputerUseSupported() bool  // true on macOS
activitymon.ComputerUsePermitted() bool  // checks AXIsProcessTrustedWithOptions (no prompt)
```

### App and window discovery

```go
apps := activitymon.ListRunningApps()        // []RunningApp — regular (non-background) apps
wins := activitymon.ListAppWindows("Safari") // []AppWindow — windows of a named app
```

### UI element collection

```go
elements := activitymon.CollectAppElements("Safari") // []UIElement
// Walks focused window's AX tree (up to 400 elements, 12 levels deep).
// Skips decorative/invisible elements (<5×5 px, scrollbars, splitters).
```

`UIElement` fields: `ID`, `Role`, `Title`, `Label`, `Value`, `Description`, `RoleDescription`, `Help`, `Identifier`, `IsActionable`, `X/Y/Width/Height`.

### Input synthesis

```go
activitymon.ClickAt(x, y float64)           // left-click at screen coordinates
activitymon.TypeText("hello")               // type text character by character (10ms per key)
activitymon.PressKey("return")              // press a named key
activitymon.Hotkey("cmd,shift,a")           // key combo (modifier,modifier,...,key)
activitymon.Scroll(deltaX, deltaY int)      // scroll wheel (positive = up/right)
activitymon.Drag(fromX, fromY, toX, toY)   // smooth drag with 10-step interpolation
activitymon.PasteText("some text")          // set clipboard, Cmd+V, restore clipboard
activitymon.OpenURL("https://example.com")  // open URL in default browser
```

Supported key names for `PressKey`/`Hotkey`: `return`/`enter`, `tab`, `space`, `delete`/`backspace`, `escape`/`esc`, `left`, `right`, `up`, `down`, `f1`–`f5`, `home`, `end`, `pageup`, `pagedown`, `forwarddelete`, or any single character.

### Computer use types

| Type | Fields | Description |
|---|---|---|
| `UIElement` | `ID`, `Role`, `Title`, `Label`, `Value`, `Description`, `RoleDescription`, `Help`, `Identifier`, `IsActionable`, `X/Y/Width/Height` | A single AX tree element |
| `RunningApp` | `Name`, `BundleID`, `PID`, `IsActive` | A running macOS application |
| `AppWindow` | `Title`, `X/Y/Width/Height`, `IsMain` | A window of an application |

## Content Parsing (appparser)

The `appparser` sub-package provides app-specific parsers that convert raw accessibility data into structured `ParsedContent`. Parsers register themselves in `init()` and are dispatched by bundle ID via the registry. See `appparser/README.md` for the full guide.

### Key types

```go
type ParsedContent struct {
    ContentType string               // "conversation", "document", "terminal", "web_page", "task_list", "events"
    Title       string
    Body        string               // flat-text rendering for prompts
    URL         string               // active URL (browser content)
    Channel     string               // chat target (Slack channel, DM name, etc.)
    Messages    []ConversationMessage // structured messages when ContentType == "conversation"
    Metadata    map[string]string
    DedupOfHash string               // set when snapshot payload was elided due to identical content
}

type ConversationMessage struct {
    Sender       string
    Content      string
    Timestamp    string
    ThreadParent string // "<parentSender>@<parentTimestamp>" for threaded messages (Slack parser)
}
```

### Parser interfaces

```go
type Parser interface {
    CanParse(bundleID string) bool
}
type TextParser interface { // flat-text fallback
    Parser
    Parse(axText, bundleID, windowTitle, url string) *ParsedContent
}
type TreeParser interface { // preferred — structured AX tree
    Parser
    ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent
}
type URLMatcher interface { // optional — for web-in-browser parsers
    CanParseURL(url string) bool
}
```

The registry calls `ParseTree` when a `TreeParser` is available, falling back to flat-text `Parse` otherwise. Dispatch entry point:

```go
content := appparser.ParseTree(tree, flatText, bundleID, windowTitle, browserURL)
```

### Parser catalog (introspection)

Parsers may optionally implement `Describer` or `MultiDescriber` to advertise their supported bundle IDs and URL hosts to UI surfaces:

```go
type Describer interface {
    Parser
    Describe() ParserCatalogEntry
}
type MultiDescriber interface {
    Parser
    DescribeAll() []ParserCatalogEntry
}
```

The `appparser.Catalog()` function returns declarative metadata for all registered describers, used by the DevTools panel and documentation generators.

### Built-in parsers

| File | Parser | Handles |
|---|---|---|
| `chatgpt.go` | ChatGPT | `com.openai.chat` — extracts conversation via AX tree layout analysis |
| `slack.go` | Slack | `com.tinyspeck.slackmacgap` — extracts channel/DM, senders, timestamps |
| `messages.go` | Messages | `com.apple.MobileSMS` — extracts iMessage conversations |
| `browser.go` | Browser | All known browser bundle IDs — strips browser chrome, returns page content |
| `terminal.go` | Terminal | `com.apple.Terminal`, `com.googlecode.iterm2`, etc. — extracts terminal buffer |
| `notion.go` | Notion | `notion.id` (native) + URL `notion.so`/`*.notion.site` — extracts page title and body |
| `gmail.go` | Gmail | URL `mail.google.com` — extracts open thread as conversation or inbox as document |
| `calendar.go` | Calendar | `com.apple.iCal` — extracts visible events as chronological bullet list |
| `google_calendar.go` | Google Calendar | URL `calendar.google.com` — extracts visible events (reuses calendar helpers) |
| `linear.go` | Linear | `com.linear`/`com.linear.linear-mac` (native) + URL `linear.app` — issue detail or task list |
| `notes.go` | Notes | `com.apple.Notes` — extracts document body from AXWebArea or AXTextArea |
| `mail.go` | Mail | `com.apple.mail` — extracts focused email thread as conversation |
| `reminders.go` | Reminders | `com.apple.reminders` — extracts visible task list as checklist |
| `github.go` | GitHub | URL `github.com` — issues/PRs as conversation, other pages as document |
| `obsidian.go` | Obsidian | `md.obsidian` — extracts active note from CodeMirror (edit) or preview (reading) |
| `finder.go` | Finder | `com.apple.finder` — extracts current directory, file list, selected filenames |
| `appstore.go` | App Store | `com.apple.AppStore` — extracts product-page metadata |
| `chatgpt_web.go` | ChatGPT (web) | `chatgpt.com`, `chat.openai.com` — DOM-class based message extraction |
| `claude_web.go` | Claude.ai | `claude.ai` — DOM-class based message extraction |
| `discord.go` | Discord | `com.hnc.Discord` — extracts channel messages |
| `whatsapp.go` | WhatsApp | `net.whatsapp.WhatsApp` — extracts conversation messages |
| `feishu.go` | Feishu/Lark | `com.electron.lark` — extracts chat messages |
| `tencent_chat.go` | WeChat | `com.tencent.xinWeChat` — extracts chat messages |
| `asana.go` | Asana | `com.asana.Asana` — extracts task/project content |
| `code_editor.go` | Code Editors | VS Code, Cursor, Zed, etc. — extracts editor content |
| `office_document.go` | Word/Pages | Microsoft Word, Apple Pages — extracts document content |
| `office_spreadsheet.go` | Excel/Numbers | Microsoft Excel, Apple Numbers — extracts sheet content |
| `office_presentation.go` | PowerPoint/Keynote | Microsoft PowerPoint, Apple Keynote — extracts slide content |
| `web_chat_common.go` | Shared | Turn-by-class detection used by `chatgpt_web.go` and `claude_web.go` |
| `electron_chat_common.go` | Shared | Common helpers for Electron-based chat apps |
| `unsupported.go` | Unsupported | Sentinel parser for apps known to have no useful AX content |

## Testing

Computer-use tests are in `computer_use_test.go` and are gated to `//go:build darwin`. They exercise the computer-use automation APIs against a live macOS session:

| Test | Description |
|---|---|
| `TestListRunningApps` | Verifies at least one app is returned by `ListRunningApps()` |
| `TestCollectAppElements_Safari` | Walks Safari's AX tree (skipped if Safari is not running) |
| `TestCollectAppElements_JSON` | Verifies `CollectAppElements` output serializes to valid JSON |
| `TestListAppWindows` | Lists Safari windows (skipped if Safari is not running) |
| `TestOpenURL` | Opens `https://example.com` in the default browser (skipped with `-short`) |

Cross-platform event coalescing tests are in `event_coalesce_test.go`:

| Test | Description |
|---|---|
| `TestCoalesceStateEventsKeepsLatestStatePerWindow` | Verifies deduplication keeps only the latest `app_switch`/`focus` per time window |
| `TestCoalesceStateEventsPreservesInputEvents` | Verifies keyboard/click events are never coalesced away |

Platform-specific tests:

| Test file | Platform | Coverage |
|---|---|---|
| `uia_windows_test.go` | Windows | UIA subprocess lifecycle, JSON parsing, restart-on-failure |
| `uia_text_clean_windows_test.go` | Windows | Text normalization for UIA output |
| `event_coalesce_test.go` | All | State event deduplication, input event preservation |

Run on macOS with Accessibility permission granted:

```sh
cd wails-gui && go test ./internal/platform/activitymon/...
```

Use `-short` to skip tests that open browser windows:

```sh
go test -short ./internal/platform/activitymon/...
```

## Dependencies

### macOS

| Framework | Used by |
|---|---|
| `CoreGraphics` | Event tap, screen capture |
| `ApplicationServices` | Accessibility (AXObserver, AXUIElement) |
| `Cocoa` | NSWorkspace app-switch notifications |
| `Carbon` | Key code constants |
| `ScreenCaptureKit` | Per-window GPU-accelerated capture (macOS 15+) |
| `CoreMedia` / `ImageIO` | ScreenCaptureKit pixel format handling |

### Windows

| DLL | Used by |
|---|---|
| `user32.dll` | Hooks, foreground window, message pump, HWND enumeration |
| `kernel32.dll` | Process handle management |
| `psapi.dll` | Process name lookup (`GetModuleBaseNameW`) |
| `gdi32.dll` | Screen capture (`BitBlt`, `GetDIBits`, `StretchBlt`) |
| `version.dll` | App name lookup (`GetFileVersionInfoW`, `VerQueryValueW`) |
| `powershell.exe` + `UIAutomationClient` / `UIAutomationTypes` assemblies | UI Automation tree walker (`uia_windows.ps1`). Loaded as a long-lived subprocess by `uia_windows.go`. |

## Permissions Required

### macOS
- **Accessibility** (`Privacy & Security > Accessibility`): required for `Monitor` (CGEventTap, AXObserver), `CollectAppElements`, `ListAppWindows`, and all input-synthesis functions (`ClickAt`, `TypeText`, etc.). `Monitor.Start()` returns an error if not granted; `ComputerUsePermitted()` checks without prompting.
- **Screen Recording** (`Privacy & Security > Screen Recording`): required for `CaptureScreen` / `ScreenCapture.CaptureRaw()`.

### Windows
No explicit permission prompts are required for low-level hooks in a standard user session. The hooks capture system-wide input by design.

## Usage

```go
// Activity monitoring
mon := activitymon.New()
err := mon.Start(ctx, func(evt activitymon.ActivityEvent) {
    fmt.Printf("[%s] %s - %s\n", evt.Kind, evt.AppName, evt.WindowTitle)
})
defer mon.Stop()

// Screenshot
sc := activitymon.NewScreenCapture()
pixels, w, h, err := sc.CaptureRaw()
jpeg, err := sc.EncodeJPEG(pixels, w, h, 80)

// Focused window text (for context extraction)
text := activitymon.CollectFocusedWindowText()

// Focused window tree (for structured parsing)
tree := activitymon.CollectFocusedWindowTree()
```
