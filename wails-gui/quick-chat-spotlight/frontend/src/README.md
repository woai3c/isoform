# src/

Source code for the Quick Chat Spotlight frontend — a React + TypeScript app rendered inside a frameless, always-on-top Wails WebView panel. Summoned by global hotkey (Alt+Space), it provides two views that cross-fade between each other: a compact pill input and a full chat surface.

See the [parent README](../README.md) for build tooling, dev setup, dependency lists, and the Wails import redirect plugin.

## Directory layout

```
src/
├── main.tsx                        # DOM entry point
├── App.tsx                         # Root: view state, theme sync, dual-layer stage
├── env.d.ts                        # Vite env type declarations
├── index.css                       # Spotlight CSS (theme tokens, platform overrides)
├── api/
│   ├── types.ts                    # TypeScript types shared across spotlights
│   └── yansu.ts                    # HTTP client for the Isoform host bridge
├── chat/
│   └── reducer.ts                  # Pure reducer: AgentEvent → StreamState
├── components/
│   ├── PillInput.tsx               # Pill view (form 1)
│   ├── DetectionChip.tsx           # App context chip inside the pill
│   └── ConversationPanel.tsx       # Conversation view (form 2)
└── lib/
    └── wails-app-stub.ts           # Wails binding stubs for reused main UI components
```

## Architecture

### Two always-mounted layers

`App.tsx` mounts both views simultaneously in a `stage` container. The inactive view gets `opacity: 0; pointer-events: none`. When the user submits from the pill:

1. `setView("conversation")` fires — CSS opacity cross-fade begins
2. `ResizeMode("conversation")` resizes the OS window atomically
3. Both motions overlap (~220ms total)

Both layers are `position: absolute; bottom: 0` with fixed heights (pill: 88px, conversation: 650px). The conversation layer is already laid out at its intrinsic height before the cross-fade starts, avoiding mid-animation reflow.

### Data flow

```
Isoform Host Bridge (localhost HTTP, per-session token)
    │
    ▼
api/yansu.ts
    │  fetchSuggestions, submitPrompt, subscribeStream (SSE),
    │  transcribeAudio, respondToPermission, expand, cancel, dismiss
    │
    ▼
chat/reducer.ts
    │  Pure functions: processAgentEvent, appendUserMessage,
    │  editAndTruncate, clearPermission
    │
    ▼
ConversationPanel  (useReducer + SSE subscription)
    │
    ▼
GroupedStreamList, TypingIndicator, PermissionQuestionCard
    (reused from main UI via @/ path alias)
```

### Component reuse

The spotlight reuses main UI chat components (`GroupedStreamList`, `TypingIndicator`, `PermissionQuestionCard`, `VoiceRecordingIndicator`) via the `@/` path alias. These components import Wails bindings from `wailsjs/go/main/App` — the Vite plugin in `vite.config.ts` redirects those imports to `wails-app-stub.ts`, which provides real bindings for the 6 methods the spotlight binary exposes and no-op stubs for the remaining ~200.

### Chat reducer (`chat/reducer.ts`)

A pure reducer that mirrors the main UI's `useAgentStreaming` hook. Folds `AgentEvent` payloads (received via SSE) into a `StreamState` object consumed by `GroupedStreamList`:

| Function | Purpose |
|----------|---------|
| `processAgentEvent(state, event)` | Folds one AgentEvent into StreamState (deltas, tool use/results, permissions, errors, compaction boundaries) |
| `appendUserMessage(state, prompt)` | Appends a synthetic user bubble with `sortOrder` |
| `editAndTruncate(state, itemId, newContent)` | Replaces a user item and drops everything after it |
| `clearPermission(state)` | Clears the pending permission request |

### API client (`api/yansu.ts`)

All calls go through the Isoform host bridge over localhost HTTP. The bridge port and per-session token are obtained from `GetConfig()` (a real Wails binding) and cached. In standalone dev (`pnpm dev` without the Go binary), falls back to `VITE_YANSU_PORT`.

| Function | Endpoint | Notes |
|----------|----------|-------|
| `fetchSuggestions` | `GET /api/spotlight/suggestions` | App-specific suggestion chips |
| `submitPrompt` | `POST /api/spotlight/submit` | Returns `sessionLocalId`; accepts optional `editOf` for edit/retry |
| `subscribeStream` | `GET /api/spotlight/stream/:id` | Manual SSE via fetch+reader (EventSource doesn't support custom auth headers) |
| `transcribeAudio` | `POST /api/spotlight/voice/transcribe` | Offline STT via host bridge's sherpa-onnx |
| `respondToPermission` | `POST /api/spotlight/permission` | Unblocks the agent loop after a permission_request |
| `fetchAppearance` | `GET /api/spotlight/theme` | Returns the resolved theme and UI language |
| `expand` | `POST /api/spotlight/expand` | Opens chat in the main Isoform window |
| `cancel` | `POST /api/spotlight/cancel` | Stops in-flight agent run |
| `dismiss` | `POST /api/spotlight/dismiss` | Notifies host of panel dismissal |
| `initVoiceInput` | `POST /api/spotlight/voice/init` | Pre-warms the speech engine |

### Theme sync

The spotlight follows the main UI's user-selected theme, not the OS dark-mode setting. Theme and the host's saved UI language are fetched on mount, every `spotlight:show` event and window focus. Spotlight always renders with `html[data-texture="off"]`, independently of the main window's saved preference. There is no appearance poll or hide listener; live cross-window sync moves to a follow-up PR. The helper has a separate storage origin, so it applies the host language to the shared i18n instance without persisting a separate language choice. Applied via `<html data-theme>` attribute swap + Win32 DWM dark-mode hint (`SetDarkMode`). The CSS `@import`s main UI's full theme tokens so reused components render identically.

### Platform-specific CSS

`App.tsx` tags `<html>` with `platform-windows`, `platform-mac`, or `platform-linux` based on `navigator.userAgent`. `index.css` uses these classes to:
- Paint body with `--spotlight-surface` on Windows (WebView2 default white bleeds through rounded corners)
- Keep body transparent on macOS (OS vibrancy shows through)
- Disable the global text-selection lock the main UI applies on body
