# CuaDriver — macOS Desktop Automation Helper

A macOS native Swift daemon that drives desktop applications via the Accessibility
API, ScreenCaptureKit, and synthesized input events. Originally derived from
[alma/computer-use](https://github.com/trycua/cua), vendored into the Yansu
desktop app and branded as `com.yansu.cuadriver`.

**Core contract:** The user's frontmost app must never change. Every interaction
(AX actions, synthesized clicks, keyboard input) routes to the target process
without activating it, moving the real cursor, or switching Spaces.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  Yansu Wails GUI (Go)                               │
│  bundled_cua_driver.go                              │
└──────────────┬──────────────────────────────────────┘
               │ Unix domain socket (NDJSON)
┌──────────────▼──────────────────────────────────────┐
│  Daemon.swift (main loop)                           │
│  ┌────────────────────────────────────────────────┐ │
│  │         Three-Layer Dispatch                   │ │
│  │  Layer 1: AppCommandRegistry  (URL schemes)    │ │
│  │  Layer 2: AXBridge            (AX actions)     │ │
│  │  Layer 3: EventDispatch       (bgclick-rev)    │ │
│  └────────────────────────────────────────────────┘ │
│  ┌──────────────┐ ┌──────────────┐ ┌─────────────┐ │
│  │ ElementStore │ │   Capture    │ │  LensOverlay │ │
│  │ (ref → AXEl) │ │ (SCK window) │ │ (viz cursor) │ │
│  └──────────────┘ └──────────────┘ └─────────────┘ │
│  ┌──────────────┐ ┌──────────────┐ ┌─────────────┐ │
│  │FocusStealth  │ │ MCPServer    │ │ VisualIndex │ │
│  │ (no flicker) │ │ (MCP HTTP)   │ │ (Vision OCR)│ │
│  └──────────────┘ └──────────────┘ └─────────────┘ │
└─────────────────────────────────────────────────────┘
```

## Key Components

| File | Purpose |
|------|---------|
| `main.swift` | Entry point. Parses subcommands (`daemon`, `version`, `help`). |
| `Daemon.swift` | Unix domain socket server. Accepts NDJSON requests, dispatches to handlers, manages idle timeout, coordinate translation, and post-action settle logic. |
| `Protocol.swift` | Wire protocol: `Request`/`Response` structs, `CUError` definitions, `AnyCodable` for flexible JSON encoding. |
| `AXBridge.swift` | All macOS Accessibility API interactions. Snapshot walking, click/type/scroll/raise via `AXUIElementPerformAction`, Enhanced UI toggles for Electron/CEF apps. |
| `EventDispatch.swift` | Physical HID event synthesis via `CGEventPostToPid` (resolved via `dlsym`). Mouse clicks, drags, keyboard input, scroll — all routed per-pid without global HID taps. |
| `ElementStore.swift` | Maps stable string refs (`e1`, `e2`, ...) to live `AXUIElement` instances. Ambient invalidation via `AXObserver`, descriptor-based refetch for stale refs. |
| `FocusStealth.swift` | Focus-steal prevention. CPS event tap drops activation notifications for disallowed pids. `PreserveFrontmostWatcher` snap-restores user's frontmost app. `AXEnablementAssertion` forces AX tree population. |
| `Capture.swift` | Single-window screenshots via ScreenCaptureKit (`SCScreenshotManager` on macOS 14+). Outputs JPEG to disk. |
| `LensOverlay.swift` | Transparent click-through overlay rendering a virtual cursor at action targets. Flat blue pointer with white stroke, red crosshair, click marks. |
| `MCPServer.swift` | Local MCP (Model Context Protocol) HTTP server. Exposes CUA tools as MCP resources, supports OAuth 2.1 + PKCE, SecCode peer verification, whitelist management. |
| `VisualIndex.swift` | Apple Vision OCR fallback for AX-blind apps. Returns numbered visual refs (`v1`, `v2`, ...) with bounds and confidence. |
| `AppCommandRegistry.swift` | Layer 1 dispatch: pre-registered URL-scheme intents for apps (NetEase Music, Spotify, Slack, Safari, etc.). Zero flicker, zero synthesized input. |
| `ForbiddenTargets.swift` | Blocklist of sensitive apps (System Settings, loginwindow, TCC, Dock, etc.) the agent cannot drive without explicit override. |
| `WindowList.swift` | CGWindowList bridge. Finds windows by pid when AX tree is empty. Visibility checks for lens overlay gating. |
| `AppUsage.swift` | App enumeration combining running processes with Spotlight metadata for recently-used frequency hints. |
| `MenuShortcuts.swift` | Collects menu-bar keyboard shortcuts for Codex-parity in `get_app_state` responses. |
| `VictimObservation.swift` | Per-pid observation tap for forensic monitoring of activation events. |
| `FeatureFlags.swift` | Runtime toggles via environment variables (`ALMA_CU_FORCE_SYNTH`, `ALMA_CU_LOG_DLSYM`). |
| `PIPWindow.swift` | Picture-in-Picture mirror windows so the user can watch the agent's work without raising the target app. |

## Three-Layer Dispatch

Moves are dispatched in priority order:

1. **Layer 1 — AppCommandRegistry** (`intent`): Opens URL schemes (`orpheus://`, `spotify:`, `slack://`) via LaunchServices. Zero focus change, zero synthesized input. Best for apps with known URL handlers.

2. **Layer 2 — AXBridge** (`click` by ref): Routes through `AXUIElementPerformAction` (AXPress, AXPick, AXConfirm). No cursor warp, no activation. Works on native Cocoa apps and Electron/CEF apps with AXEnhancedUserInterface flipped.

3. **Layer 3 — EventDispatch** (`click` by pixel): bgclick-rev synthesis path. Constructs events via `NSEvent.mouseEvent(...).cgEvent`, writes explicit CGWindowID fields (91/92) and maskCommand filter bypass for backgrounded targets, posts via `CGEventPostToPid`. Falls back here when AX has no action or no tree.

## Wire Protocol

The daemon speaks line-delimited JSON over a Unix domain socket (`~/Library/Application Support/Alma/computer-use.sock` by default).

```json
→ {"id":"1","cmd":"ping","args":null}
← {"id":"1","ok":true,"data":{"pong":true,"version":"0.1.0","uptime_s":12345}}
```

Each request has an `id`, `cmd`, and optional `args` dictionary. Responses are correlated by `id` and carry either `data` or `error`.

## Daemon Commands

| Command | Description |
|---------|-------------|
| `ping` | Liveness check |
| `permissions` | Check AX and Screen Recording TCC grants |
| `grant` | Fire the system AX permission prompt |
| `apps` | List running AX-trusted apps |
| `list_apps` | Running + recently-used apps with frequency |
| `windows` | List windows of a specific app |
| `snap` | Raw AX snapshot (no screenshot) |
| `get_app_state` | AX snapshot + screenshot in one round trip |
| `launch_app` | Launch app in background (idempotent) |
| `click` | Click by AX ref, visual ref (`v*`), or pixel coords |
| `type` / `type_text` | Type text into a ref or the app's focused element |
| `press` / `press_key` | Press a key on an element or send a key combo to pid |
| `perform_secondary_action` | Right-click / context menu |
| `drag` | Pixel-coordinate drag |
| `set_value` | Direct AXValue write |
| `scroll` | Scroll an element by pages |
| `raise` | Raise a window (AXRaiseAction) |
| `shot` | Raw window screenshot to file |
| `open_url` | Layer-1 URL dispatch via LaunchServices |
| `intent` | Layer-1 registered intent dispatch |
| `lens` | Toggle lens overlay visibility |
| `pip_open` / `pip_close` / `pip_status` | Picture-in-Picture window management |
| `pip_arm` / `pip_disarm` | Minimise-hijack observer registration |
| `mcp_start` / `mcp_stop` / `mcp_status` | MCP server lifecycle |
| `mcp_allow` / `mcp_deny_all` / `mcp_whitelist` | MCP peer whitelist management |
| `mcp_arm_peer` | Authorize a specific pid for OAuth flow |
| `stealth_stats` | Forensic counters from activation observer |
| `turn_ended` | Release all focus-suppression state |
| `shutdown` | Graceful daemon exit |

## Build

```bash
# Default: builds into ./build/CuaDriver.app (standalone smoke test)
./build-cua-driver.sh

# Production: builds directly into a target .app bundle
./build-cua-driver.sh --out /path/to/CuaDriver.app --skip-codesign

# Single architecture for fast dev iteration
./build-cua-driver.sh --arch arm64
```

The build produces a universal binary (`arm64` + `x86_64`) targeting macOS 13.0.
It links against AppKit, ApplicationServices, AVFoundation, Carbon, CoreGraphics,
CoreMedia, CoreServices, CoreVideo, ImageIO, Network, ScreenCaptureKit, Security,
and Vision.

### Bundle Identity

| Attribute | Value |
|-----------|-------|
| Bundle ID | `com.yansu.cuadriver` |
| Executable | `cua-driver` |
| Min macOS | 13.0 |
| LSUIElement | true (no Dock icon) |

The stable bundle ID preserves macOS TCC (Transparency, Consent, and Control)
grants across upgrades.

## macOS Permissions

The helper requires two TCC permissions, declared in `Info.plist`:

- **Accessibility** (`NSAccessibilityUsageDescription`): Read and drive the AX
  tree of other applications.
- **Screen Recording** (`NSScreenCaptureUsageDescription`): Capture individual
  application windows via ScreenCaptureKit.
- **Apple Events** (`NSAppleEventsUsageDescription`): Raise windows when
  Accessibility actions are unavailable.

## MCP Integration

The daemon can start a local MCP (Model Context Protocol) server on loopback
HTTP (`127.0.0.1`). This exposes CUA tools to LLM harnesses (Claude Desktop,
Cursor, etc.) as standard MCP tools.

```bash
# Start MCP server (ephemeral port)
→ {"id":"1","cmd":"mcp_start"}

# Auth: bearer token written to ~/.alma/cu/mcp-token
# OAuth 2.1 + PKCE S256 also supported
```

Supported MCP methods: `initialize`, `tools/list`, `tools/call`,
`notifications/initialized`, `prompts/list`, `prompts/get`, `resources/list`,
`resources/read`.

## Focus-Steal Prevention

The focus-stealth system prevents the target app from visibly activating when
receiving synthetic events. Three mechanisms work in tandem:

1. **CPS Event Tap** — Drops `kCPSEventNotify` (type 14, subtype 8) activation
   notifications for disallowed pids before the Window Server commits them.
   `onlyAllowPid` locks activation to a single pid during sensitive operations.

2. **PreserveFrontmostWatcher** — Observes `NSWorkspace.didActivateApplicationNotification`
   and synchronously snap-restores the user's original frontmost app.

3. **AXEnablementAssertion** — RAII handle that sets `AXEnhancedUserInterface` +
   `AXManualAccessibility` on the target, forcing Electron/CEF axes to populate
   without activation.

## Environment Variables

| Variable | Effect |
|----------|--------|
| `ALMA_CU_FORCE_SYNTH` | Skip AX path, always use bgclick-rev synthesis |
| `ALMA_CU_LOG_DLSYM` | Log first dlsym resolution of CGEventPostToPid |

## Testing

Manual test scripts live in `Skills/cua-driver/TESTS.md`. There are no automated
unit tests in this directory — the daemon interacts with live macOS UI and
permissions, making traditional unit tests impractical. End-to-end verification
happens through the Yansu Wails GUI integration.

## Related Files

- `wails-gui/bundled_cua_driver.go` — Go code that manages the daemon lifecycle
  from the Yansu desktop app.
- `wails-gui/scripts/build-wails.sh` — Production build pipeline that invokes
  `build-cua-driver.sh`.
- `Skills/cua-driver/` — Claude Code skill teaching Claude to drive macOS apps
  via the bundled daemon.
