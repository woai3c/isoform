# `lib/` — Wails Runtime & App Binding Shims

This directory contains the two hand-written shim modules that make the Quick Chat Spotlight work across platforms. The spotlight reuses chat-rendering components from the main Isoform frontend (`wails-gui/frontend/`), but the spotlight binary exposes a different (much smaller) set of Go methods and runs under two different host shells:

- **macOS**: a native `NSPanel` + `WKWebView` with no Wails runtime at all.
- **Windows/Linux**: a full Wails app with the generated `window.runtime` / `window.go` globals.

The Vite plugin in [`vite.config.ts`](../vite.config.ts) intercepts every `wailsjs/...` import inside the reused components and redirects it to one of these shims. Because the shims never import from generated `wailsjs/` files, the macOS native build can compile without ever running `wails generate`.

---

## Files

### `spotlight-runtime.ts`

Shim for `wailsjs/runtime/runtime` imports (event system, clipboard, window ops, file drop, logging).

**Dual transport:**

| Platform | Transport | Behaviour |
|----------|-----------|-----------|
| macOS | `window.webkit.messageHandlers.spotlight` + `CustomEvent` listeners | Posts to the native NSPanel; receives events the panel dispatches via `evaluateJavaScript` |
| Windows/Linux | `window.runtime` globals | Delegates to the injected Wails runtime |

**Exported symbols:**

| Symbol | macOS | Windows/Linux |
|--------|-------|---------------|
| `EventsOn(name, cb)` | Listens for `CustomEvent` on `window`; sends `{"type":"ready"}` to the native bridge when subscribing to `spotlight:show` so buffered show events are flushed | `window.runtime.EventsOn` |
| `EventsEmit(name, ...data)` | no-op (no Wails runtime to emit to) | `window.runtime.EventsEmit` |
| `BrowserOpenURL(url)` | `window.open` fallback | `window.runtime.BrowserOpenURL` |
| `ClipboardSetText(text)` | `navigator.clipboard.writeText` | `window.runtime.ClipboardSetText` |
| `LogError` / `LogInfo` | no-op | `window.runtime.LogError` / `LogInfo` |
| `WindowGetSize()` | `{w: innerWidth, h: innerHeight}` | `window.runtime.WindowGetSize` |
| `OnFileDrop` / `OnFileDropOff` | no-op | `window.runtime.OnFileDrop` / `OnFileDropOff` |

If a reused component imports a new runtime symbol, the build fails with `"X is not exported by spotlight-runtime.ts"` — add it here.

---

### `wails-app-stub.ts`

Shim for `wailsjs/go/main/App` imports. The main UI imports ~200 methods from the full Wails `App` struct; the spotlight binary only exposes a handful. This stub provides:

1. **Real spotlight bindings** — methods the spotlight binary actually implements:
   - `GetConfig()` — reads `window.__spotlightConfig` (macOS) or calls the Wails binding (Windows/Linux)
   - `HideWindow()` — posts `{"type":"hide"}` to the native bridge (macOS) or calls the Wails binding
   - `ResizeMode(mode)` — posts `{"type":"resize", mode}` (macOS) or calls the Wails binding
   - `SetAutoHideOnBlur(enabled)` — posts `{"type":"autohide", enabled}` (macOS) or calls the Wails binding
   - `SetDarkMode(dark)` — no-op on macOS (native window is transparent); forwards on Windows
   - `SetSurfaceColor(r,g,b)` — no-op on macOS; forwards on Windows

2. **Host-bridge overrides** — voice input is handled by the main Isoform binary, forwarded via the host bridge:
   - `InitVoiceInput()` → `initVoiceInput()` from `../api/yansu`
   - `TranscribeAudio(wavBase64)` → `transcribeAudio()` from `../api/yansu`

3. **Optimistic stubs** — return fake but valid data so reused hooks don't crash or render wrong UI:
   - `GetSubscriptionStatus()` → `{is_active: true, subscription_plan: "max"}` (prevents free-plan upsell in spotlight)
   - `GetPlatformPermissions()` → `{platform: "windows", items: []}` (prevents `useVoiceInput` crash)
   - `RequestPermission()` → `true`
   - `GetAuthSource()` → `{source: ""}`
   - `GetVoiceInputStatus()` → `{available: true, ready: true, downloading: false, building: false, error: ""}`
   - `ActivitySessionExists()` → `false` (spotlight has no activity jump UI)

4. **No-op stubs** — every other main-UI method returns `Promise<undefined>` after logging a one-time debug warning:
   ```ts
   export const SomeMainUIMethod = makeNoop("SomeMainUIMethod");
   ```

**Naming note:** the import path is intentionally lowercased (`wailsjs/go/main/app`, not `App`). The spotlight's Go binding struct is `type app struct`, so Wails generates lowercase `wailsjs/go/main/app.*`. The main UI uses uppercase `App`. Importing `"App"` works on macOS (case-insensitive filesystem) but breaks the Linux build with a Vite "Could not resolve" error.

---

## How It Fits Together

```
Reused main-UI component
       │
       ▼
  import { HideWindow } from 'wailsjs/go/main/App'
       │
       ▼
┌─────────────────────────────────────────┐
│  Vite spotlightWailsRedirectPlugin      │
│  (enforce: "pre", resolveId hook)       │
└─────────────────────────────────────────┘
       │
       ├── "wailsjs/go/main/[Aa]pp" ──► wails-app-stub.ts
       │
       └── "wailsjs/runtime/runtime" ──► spotlight-runtime.ts
```

Inside each shim:

```
macOS:    nativeSpotlightBridge()  ──► window.webkit.messageHandlers.spotlight
                                    (posts to NSPanel / reads injected config)

Win/Lin:  window.runtime / window.go.main.app  ──► Wails-injected globals
                                    (generated bindings at runtime)
```

---

## Adding a New Binding

If a reused component starts importing a new Wails method:

1. **Runtime method** (e.g. `WindowReload`) — add to `spotlight-runtime.ts`:
   ```ts
   export function WindowReload(): void {
       rt().WindowReload?.();
   }
   ```

2. **App method** — add to `wails-app-stub.ts`:
   ```ts
   export const NewMethod = makeNoop("NewMethod");
   ```
   Or, if the spotlight binary implements it, write a real dual-transport function like `GetConfig`.

The build will fail with an explicit message telling you which file needs the new symbol.

---

## Dependencies

- `../api/yansu` — host bridge API for voice input forwarding (used by `bridgeTranscribeAudio` and `bridgeInitVoiceInput`)

No external npm packages; the shims depend only on browser globals (`window`, `navigator.clipboard`, `CustomEvent`).
