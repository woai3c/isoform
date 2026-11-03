# CuaDriver Source

Native Swift daemon source for macOS desktop automation via the Accessibility API, ScreenCaptureKit, and synthesized input events. This directory contains the full source of the `cua-driver` binary bundled inside `CuaDriver.app`.

## Entry Point

`main.swift` parses subcommands (`daemon`, `version`, `help`) and bootstraps the daemon. `daemon` accepts `--socket`, `--idle-seconds`, and `--no-lens`, then instantiates and runs `Daemon`.

## Core Loop

```
main.swift
  └─ Daemon.run()
       ├─ bind Unix domain socket, accept connections (DispatchQueue)
       ├─ dispatch JSON-RPC requests → handler switch
       │    ├─ Layer 1: AppCommandRegistry (URL schemes, zero input synthesis)
       │    ├─ Layer 2: AXBridge (AXUIElementPerformAction, no cursor warp)
       │    └─ Layer 3: EventDispatch (CGEventPostToPid, bgclick-rev synthesis)
       ├─ manage ElementStore (ref → AXUIElement cache + AXObserver invalidation)
       ├─ manage FocusStealth (activation suppression, frontmost preservation)
       └─ idle timer → auto-exit after inactivity
       RunLoop.main.run()
```

## File Map

### Protocol & Config

| File | Purpose |
|------|---------|
| `Protocol.swift` | Wire protocol: `Request`/`Response` JSON structs, `CUError` definitions with TCC/permission error codes, `AnyCodable` for flexible JSON encoding/decoding, convenience accessors on `[String: AnyCodable]` for handler dispatch. |
| `FeatureFlags.swift` | Runtime toggles via environment variables (`ALMA_CU_FORCE_SYNTH`, `ALMA_CU_LOG_DLSYM`). |

### Daemon Core

| File | Purpose |
|------|---------|
| `Daemon.swift` | Unix domain socket server. Accepts NDJSON connections, dispatches commands to AX/event handlers, manages idle timeout, coordinate translation (screenshot-pixel to screen-point), visual ref registry (`v*` for OCR fallback), and post-action settle logic. The largest file at ~67KB. |

### Interaction Layers (Three-Layer Dispatch)

| File | Purpose |
|------|---------|
| `AppCommandRegistry.swift` | **Layer 1**: Pre-registered URL-scheme intents for specific apps (NetEase Music, Spotify, Slack, Safari, etc.). Dispatches via LaunchServices with zero focus change and zero synthesized input. |
| `AXBridge.swift` | **Layer 2**: All macOS Accessibility API interactions. Walks AX trees, performs clicks/types/scrolls/raises via `AXUIElementPerformAction`, toggles `AXEnhancedUserInterface` for Electron/CEF apps. ~41KB. |
| `EventDispatch.swift` | **Layer 3**: Physical HID event synthesis via `CGEventPostToPid` (resolved via `dlsym`). Mouse clicks, drags, keyboard input, scroll — all routed per-pid without global HID taps. Includes the bgclick-rev synthesis path with explicit `CGWindowID` field writing. ~36KB. |

### State Management

| File | Purpose |
|------|---------|
| `ElementStore.swift` | Maps stable string refs (`e1`, `e2`, ...) to live `AXUIElement` instances. Uses `AXObserver` for ambient invalidation, supports descriptor-based refetch when stale refs are detected. Keyed on `(pid, window_id)`. |
| `VisualIndex.swift` | Apple Vision OCR fallback for AX-blind apps. Returns numbered visual refs (`v1`, `v2`, ...) with bounds and confidence scores. Supports `en-US` + `zh-Hans` language recognition. |

### Focus Stealth

| File | Purpose |
|------|---------|
| `FocusStealth.swift` | Focus-steal prevention system. Carbon CPS event tap drops activation notifications for disallowed pids. `PreserveFrontmostWatcher` snap-restores the user's frontmost app via `NSWorkspace` observation. `AXEnablementAssertion` forces AX tree population on Electron/CEF targets without activation. Uses private `SetFrontProcessWithOptions` + `kSetFrontProcessFrontWindowOnly` for key-window-only activation. |
| `VictimObservation.swift` | Per-pid observation tap for forensic monitoring of activation events. Counts stealth violations for diagnostics. |
| `ForbiddenTargets.swift` | Blocklist of sensitive apps (System Settings, loginwindow, TCC, Dock, etc.) the agent cannot drive without explicit override. |

### Capture & Display

| File | Purpose |
|------|---------|
| `Capture.swift` | Single-window screenshots via ScreenCaptureKit (`SCScreenshotManager` on macOS 14+). Outputs JPEG to disk. Used by `shot`, `get_app_state`, and `get_window_state` commands. |
| `Overlay.swift` | Transparent click-through window rendering a virtual cursor at action targets. Flat blue pointer with white stroke, red crosshair, and click-ring animations. Bezier-glides between targets, hides after ~1.5s idle. |
| `PIPWindow.swift` | Picture-in-Picture mirror windows so the user can watch the agent's work without raising the target app. Supports open/close/status, and minimise-hijack observer registration via `pip_arm`/`pip_disarm`. ~40KB. |

### App Discovery

| File | Purpose |
|------|---------|
| `WindowList.swift` | `CGWindowList` bridge. Finds windows by pid when AX tree is empty. Provides visibility checks and `CGWindowID` resolution for lens overlay gating. |
| `AppUsage.swift` | App enumeration combining `NSWorkspace.runningApplications` with Spotlight metadata (`NSMetadataQuery`) for recently-used frequency hints. |
| `MenuShortcuts.swift` | Collects menu-bar keyboard shortcuts from `AXMenuBarItem` + `AXMenuItemCmdVirtualKey` attributes for Codex-parity in `get_app_state` responses. |

### MCP Integration

| File | Purpose |
|------|---------|
| `MCPServer.swift` | Local MCP (Model Context Protocol) HTTP server on loopback. Exposes CUA tools as standard MCP resources (`initialize`, `tools/list`, `tools/call`, `prompts/*`, `resources/*`). Supports bearer token auth, OAuth 2.1 + PKCE S256, and SecCode peer verification. ~48KB. |

## Command Dispatch Flow

Every incoming NDJSON request hits `Daemon.dispatch(_:)` which routes by `cmd`:

```
Request {id, cmd, args}
  ├─ ping              → liveness check
  ├─ permissions       → AX + Screen Recording TCC status
  ├─ apps / list_apps  → AppUsage + AXBridge.runningApps()
  ├─ windows           → WindowList.windows(forPid:)
  ├─ snap / get_app_state → AXBridge.snapshot() + Capture.capture()
  ├─ launch_app        → NSWorkspace.openApplication (hidden launch)
  ├─ click             → Layer1 → Layer2 → Layer3 (priority order)
  ├─ type / type_text  → AXBridge.typeText() → EventDispatch fallback
  ├─ press / press_key → AXBridge.press() → EventDispatch fallback
  ├─ scroll            → EventDispatch.scroll()
  ├─ drag              → EventDispatch.drag()
  ├─ shot              → Capture.capture()
  ├─ open_url / intent → AppCommandRegistry
  ├─ lens              → Overlay toggle
  ├─ pip_*             → PIPWindow
  ├─ mcp_*             → MCPServer lifecycle
  ├─ stealth_stats     → VictimObservation
  ├─ turn_ended        → FocusStealth release
  └─ shutdown          → graceful exit
```

## Build

Sources are compiled in the order listed in `build-cua-driver.sh` (dependency order matters for Swift single-module compilation):

```bash
./build-cua-driver.sh              # → build/CuaDriver.app
./build-cua-driver.sh --arch arm64 # fast dev build, single arch
```

Produces a universal binary (`arm64` + `x86_64`) targeting macOS 13.0. Links against AppKit, ApplicationServices, AVFoundation, Carbon, CoreGraphics, CoreMedia, CoreServices, CoreVideo, ImageIO, Network, ScreenCaptureKit, Security, and Vision.

## Key Design Decisions

**No global HID taps.** Every mouse/key event is routed per-pid via `CGEventPostToPid` or `AXUIElementPerformAction`. This is what allows backgrounded operation without stealing focus or warping the real cursor.

**Three-layer dispatch for clicks.** Layer 1 (URL schemes) avoids input synthesis entirely. Layer 2 (AX actions) works on hidden/occluded/off-Space windows. Layer 3 (bgclick-rev synthesis) is the pixel-level fallback that writes explicit `CGWindowID` fields and uses `kCGEventFlagMaskCommand` filter bypass for backgrounded targets.

**Element refs are intentionally short-lived.** `ElementStore` invalidates on every `snap`/`get_window_state` call, keyed by `(pid, window_id)`. Indices from turn N do not resolve in turn N+1. This prevents stale-element bugs at the protocol level.

**Focus-steal prevention is multi-layered.** CPS event tap (drops activation notifications) + NSWorkspace observation (snap-restores frontmost) + AXEnablementAssertion (forces AX tree without activation). No single mechanism is sufficient alone.

## Related Files

- `../build-cua-driver.sh` — Build script that compiles these sources
- `../Info.plist` — Bundle metadata, TCC permission declarations
- `../cua-driver.entitlements` — Hardened runtime entitlements
- `../Skills/cua-driver/` — Claude Code skill for driving macOS apps via the daemon
- `../../bundled_cua_driver.go` — Go code that manages the daemon lifecycle from Yansu
