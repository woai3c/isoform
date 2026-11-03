# meetingdetect

Identifies whether the frontmost application is an active meeting or voice/video call, using a per-process identifier (macOS bundle ID or Windows lowercase exe basename) and window title pattern matching. Works on both macOS and Windows; each meeting app entry lists every per-platform identifier we know about (e.g. `us.zoom.xos` on macOS, `zoom.exe` on Windows).

## Purpose

Used by the background audio pipeline to tag speech events with meeting context and trigger transcript saving. When the user is in a meeting, the pipeline can associate captured audio/transcripts with the specific meeting platform.

Three detection modes are supported:

- **Foreground detection** (`IsMeetingApp`) — called when the frontmost app changes; checks the active window's bundle ID and title. Always-meeting native apps are detected by bundle ID alone (no title required).
- **Event detection** (`IsMeetingEvent`) — stricter variant for activity events (clicks, app_switch, focus); requires title-hint confirmation for all app tiers, including always-meeting native apps. Prevents false positives from idle launchers like "Zoom Workplace".
- **Background detection** (`IsMeetingAppActive`) — called periodically against all visible windows; checks for active-meeting window title hints and window-count heuristics to avoid false positives from idle meeting apps left running after a call ends.

Meeting detection is intentionally broader than transcription. Transcript/audio
capture is allowlisted to Zoom, Slack calls/huddles, Google Meet, and Microsoft
Teams via `IsTranscriptionSupportedMeetingName`.

## Key Components

| File | Description |
|------|-------------|
| `meetingdetect.go` | Core detection logic and platform lookup tables |
| `meetingdetect_test.go` | Unit tests covering all three detection strategies |

## Architecture

Detection runs in three sequential steps within a single call to `IsMeetingApp`:

1. **Always-meeting apps** — Native video conferencing apps (Zoom, Microsoft Teams, WebEx, FaceTime, RingCentral, Amazon Chime, Skype, Tencent Meeting). `IsMeetingApp` treats being frontmost as sufficient; the app's call detection uses the strict `IsMeetingEvent` (a call title) together with the app holding the microphone.

2. **Conditional apps** — Communication apps (Slack, Discord, WhatsApp, WeChat, Telegram, Signal, LINE, KakaoTalk, Viber) where the bundle ID matches but a meeting is only active when the window title contains a call/voice indicator (e.g. "Huddle", "Voice Channel", "Call").

3. **Browser-based meetings** — If the frontmost app is a known browser (Chrome, Safari, Firefox, Arc, Edge, Brave, Opera, Vivaldi), the window title is checked for platform-specific patterns: Google Meet, Zoom, Microsoft Teams, Jitsi Meet, Whereby.

All matching uses `strings.HasPrefix` for bundle IDs / exe basenames and `strings.Contains` for window titles. Each meeting-app entry lists every per-platform identifier we know about, so the same matching code works for both macOS callers (passing a bundle ID like `us.zoom.xos`) and Windows callers (passing a lowercase exe basename like `zoom.exe`). All functions are stateless and thread-safe.

## API

### Types

```go
// Result describes whether the frontmost app is a meeting/call app.
type Result struct {
    IsMeeting   bool
    MeetingName string // human-readable platform name, e.g. "Zoom", "Google Meet"
    BundleID    string // bundle ID of the matched app (populated by IsMeetingEvent and IsMeetingAppActive)
    AppName     string // app name of the matched app (populated by IsMeetingAppActive only)
}

// VisibleWindow is a minimal representation of a visible on-screen window.
// Callers provide this from their platform-specific window-enumeration API.
type VisibleWindow struct {
    BundleID string
    AppName  string
    Title    string
}
```

A zero-value `Result` (`IsMeeting: false`) is returned for any non-meeting context.

### Functions

#### `IsMeetingApp(bundleID, windowTitle string) Result`

Foreground detection. Checks whether the given bundle ID and window title indicate a meeting or voice/video call is active. Always-meeting native apps are detected by bundle ID alone; title checking only applies to conditional and browser-based apps. Thread-safe.

```go
result := meetingdetect.IsMeetingApp(bundleID, windowTitle)
if result.IsMeeting {
    fmt.Println("In a meeting on:", result.MeetingName)
}
```

#### `IsMeetingEvent(bundleID, windowTitle string) Result`

Strict event-driven detection. Designed for activity events (clicks, app_switch, focus) where false positives are costly. Unlike `IsMeetingApp`, this requires title-hint confirmation for **all** tiers — including always-meeting native apps. Returns a `Result` with `BundleID` populated on match. Thread-safe.

```go
// Idle Zoom launcher — IsMeetingApp returns true, IsMeetingEvent returns false
r := meetingdetect.IsMeetingEvent("us.zoom.xos", "Zoom Workplace")
// r.IsMeeting == false

// Active Zoom meeting — both functions return true
r = meetingdetect.IsMeetingEvent("us.zoom.xos", "Zoom Meeting")
// r.IsMeeting == true, r.BundleID == "us.zoom.xos"
```

#### `IsMeetingAppActive(windows []VisibleWindow) Result`

Background detection. Checks a list of visible windows for an active meeting. A meeting app is only considered "active" when one of its visible windows has a title matching the `activeMeetingTitleHints` for that app. This prevents false positives when an always-meeting app (e.g. Zoom) is left running idle after a call ends.

```go
windows := []meetingdetect.VisibleWindow{
    {BundleID: "us.zoom.xos", AppName: "Zoom", Title: "Zoom Meeting"},
}
result := meetingdetect.IsMeetingAppActive(windows)
// result.IsMeeting == true, result.BundleID == "us.zoom.xos"
```

Note: Browser-based meetings are not covered by `IsMeetingAppActive`. Use `IsMeetingApp` with the frontmost window title for those.

#### `IsKnownMeetingBundle(bundleID string) bool`

Returns true if the bundle ID matches any registered always-meeting app, regardless of whether a meeting is currently active. Used to narrow the window enumeration scope before calling `IsMeetingAppActive`.

```go
if meetingdetect.IsKnownMeetingBundle("us.zoom.xos") {
    // Only fetch windows for this app
}
```

#### `IsBrowserBundle(bundleID string) bool`

Returns true if the bundle ID is a known browser. Used to identify browser windows whose titles may indicate a web-based meeting (Google Meet, Teams web, Jitsi, Whereby, etc.).

```go
if meetingdetect.IsBrowserBundle("com.google.Chrome") {
    // Check window title for meeting patterns
}
```

## Usage Example

```go
import "github.com/org/repo/wails-gui/internal/meetingdetect"

// Foreground detection — called when the active app changes:
r := meetingdetect.IsMeetingApp("us.zoom.xos", "")
if r.IsMeeting {
    fmt.Println("In a meeting on:", r.MeetingName) // "In a meeting on: Zoom"
}

// Browser-based meeting:
r = meetingdetect.IsMeetingApp("com.google.Chrome", "Meet - abc-defg-hij")
// r.IsMeeting == true, r.MeetingName == "Google Meet"

// Conditional app — no call in progress:
r = meetingdetect.IsMeetingApp("com.tinyspeck.slackmacgap", "Slack - #general")
// r.IsMeeting == false

// Background detection — scan all visible windows:
windows := getVisibleWindows() // platform-specific
r = meetingdetect.IsMeetingAppActive(windows)
if r.IsMeeting {
    fmt.Printf("Active meeting: %s (%s)\n", r.MeetingName, r.BundleID)
}
```

## Supported Platforms

Each app lists its known macOS bundle-ID prefixes and Windows exe basenames (lowercase).

### Always-meeting (identifier only)
- Zoom — `us.zoom.xos`, `zoom.exe`
- Microsoft Teams (classic + new) — `com.microsoft.teams`, `com.microsoft.teams2`, `teams.exe`, `ms-teams.exe`
- WebEx — `com.cisco.webex`, `webex.exe`, `webexhost.exe`, `webexmta.exe`
- FaceTime — `com.apple.FaceTime` (macOS only)
- RingCentral — `com.ringcentral`, `ringcentral.exe`
- Amazon Chime — `com.amazon.chime`, `amazon chime.exe`
- Skype — `com.skype.skype`, `skype.exe`
- Tencent Meeting / VooV Meeting — `com.tencent.tencentmeeting`, `wemeetapp.exe`

### Conditional (identifier + window title hint)
- Slack (`slack.exe`) — "Huddle" or "Call" in title
- Discord (`discord.exe`) — "Voice" or "Call" in title
- WhatsApp (`whatsapp.exe`) — "Call" or "Video" in title
- WeChat (`wechat.exe`) — "Call" or "Video" in title (English) or "视频通话" / "语音通话" / "群语音" / "群视频" (Chinese)
- Telegram (`telegram.exe`) — "Call" or "Group Call" in title
- Signal (`signal.exe`) — "Call" in title
- LINE (`line.exe`) — "Call" or "Video" in title
- KakaoTalk (`kakaotalk.exe`) — "Call" or "Video" in title
- Viber (`viber.exe`) — "Call" in title

### Browser-based
- Google Meet (`meet.google.com`, `Meet - ` title prefix)
- Zoom in browser (`Zoom Meeting` title prefix, `zoom.us/j/`, `zoom.us/wc/`)
- Microsoft Teams in browser (`| Microsoft Teams` in title; a bare `Microsoft Teams` title can be any chat page)

A browser call is started only from the front tab's address (`BrowserMeetingForURL`: meet.google.com/<code>, zoom.us or zoom.com /wc/ or /j/, Teams meeting paths, meet.jit.si, whereby.com); page titles only keep a call already confirmed going while its tab is in the background.
- Jitsi Meet (`Jitsi Meet` in title)
- Whereby (`whereby.com` in title)

Supported browsers — macOS: Chrome, Safari, Firefox, Arc, Edge, Brave, Opera, Vivaldi. Windows: `chrome.exe`, `msedge.exe`, `firefox.exe`, `brave.exe`, `opera.exe`, `vivaldi.exe` (Safari and Arc are macOS-only).

## Testing

```bash
cd wails-gui && go test ./internal/meetingdetect/...
```

Tests cover all detection functions and strategies:
- `TestAlwaysMeetingApps` — verifies every always-meeting bundle ID is detected by `IsMeetingApp`
- `TestConditionalApps` — verifies title-hint logic (both positive and negative cases)
- `TestBrowserMeetings` — verifies browser + title pattern combinations
- `TestNonMeetingApps` — verifies common non-meeting apps return `IsMeeting: false`
- `TestIsMeetingEvent` — verifies strict event detection, including that always-meeting apps return false without a title hint (e.g. idle "Zoom Workplace")

## Dependencies

Standard library only (`strings`). No external dependencies.
