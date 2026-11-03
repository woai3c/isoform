# Quick Chat Spotlight — Frontend

React + Vite + TypeScript frontend for the Isoform Quick Chat Spotlight. This is a standalone Vite project that compiles to static assets embedded in the spotlight Wails binary.

## Overview

The spotlight is a frameless, always-on-top floating panel summoned by global hotkey (Alt+Space). It has two views that cross-fade between each other:

- **Pill View** (768x88) — Compact input card with app detection chip, voice input, and suggestions
- **Conversation View** (768x650) — Full chat surface reusing main UI components (GroupedStreamList, ProcessingBlock, StreamCard, TypingIndicator)

Both views are always mounted; the inactive one is `opacity: 0; pointer-events: none`. The Go side atomically resizes the OS window while the CSS cross-fade runs (~220ms).

## Component Tree

```
main.tsx
└── App.tsx                         — Root: view state, theme sync, dual-layer stage
    ├── PillInput.tsx               — Pill view (form 1)
    │   └── DetectionChip.tsx       — App context + suggestion chip
    └── ConversationPanel.tsx       — Conversation view (form 2)
        ├── GroupedStreamList       — Reused from main UI (@/components/grouped-stream-list)
        ├── TypingIndicator         — Reused from main UI (@/components/typing-indicator)
        ├── PermissionQuestionCard  — Reused from main UI (@/components/permission-question-card)
        └── VoiceRecordingIndicator — Reused from main UI (@/components/voice-recording-indicator)
```

## Data Flow

```
Isoform Host Bridge (HTTP)
    ↓
api/yansu.ts          — fetchSuggestions, submitPrompt, subscribeStream (SSE), fetchAppearance
    ↓
chat/reducer.ts       — Pure reducer: folds AgentEvent → StreamState (items array, streaming flag, permission)
    ↓
ConversationPanel     — useReducer + SSE subscription → passes items to GroupedStreamList
```

### Key Types (`api/types.ts`)

- `DetectionContext` — Foreground app info (bundleID, appName) from the Go side
- `AgentEvent` — Mirrors `wails-gui/app.go::AgentEvent`; forwarded unchanged over SSE
- `StreamEvent` — SSE event envelope (`agent_event`, `user_message`, `assistant_started`, `deliverable_proposed`)
- `StreamState` — Chat surface input: items array, streaming flag, run/session IDs, permission state

### Chat Reducer (`chat/reducer.ts`)

Pure functions mirroring main UI's `useAgentStreaming` hook:

| Function | Purpose |
|----------|---------|
| `processAgentEvent(state, event)` | Folds one AgentEvent into StreamState (assistant/thinking deltas merge into trailing item, tool_use creates tool item, tool_result pairs by toolUseId) |
| `appendUserMessage(state, prompt)` | Appends a synthetic user bubble |
| `editAndTruncate(state, itemId, newContent)` | Replaces user item content and drops everything after it |
| `clearPermission(state)` | Clears pending permission request state |

### API Client (`api/yansu.ts`)

All calls go through the Isoform host bridge over localhost HTTP, authenticated with a per-session token obtained via `GetConfig()`:

| Function | Endpoint | Purpose |
|----------|----------|---------|
| `fetchSuggestions(bundleID)` | `GET /api/spotlight/suggestions` | App-specific suggestion chips |
| `submitPrompt(prompt, bundleID, sessionLocalId?, editOf?)` | `POST /api/spotlight/submit` | Submit a prompt; returns sessionLocalId |
| `subscribeStream(sessionLocalId, onEvent)` | `GET /api/spotlight/stream/:id` | SSE subscription; returns cancel function |
| `fetchAppearance()` | `GET /api/spotlight/theme` | User's resolved theme and UI language |
| `transcribeAudio(wavBase64)` | `POST /api/spotlight/voice/transcribe` | Offline STT via host bridge's sherpa-onnx |
| `initVoiceInput()` | `POST /api/spotlight/voice/init` | Pre-warm speech engine |
| `respondToPermission(sessionID, optionID)` | `POST /api/spotlight/permission` | Answer a permission request |
| `expand(sessionLocalId)` | `POST /api/spotlight/expand` | Open chat in main Isoform window |
| `cancel(sessionLocalId)` | `POST /api/spotlight/cancel` | Stop in-flight agent run |
| `dismiss()` | `POST /api/spotlight/dismiss` | Notify host of panel dismissal |

## Build Tooling

### vite.config.ts

Three key customizations:

1. **Wails Import Redirect Plugin** — Reused main UI components import `wailsjs/go/main/App`, but the spotlight binary doesn't expose most of those bindings. The plugin rewrites those imports:
   - `wailsjs/runtime/runtime` → spotlight's own Wails runtime
   - `wailsjs/go/main/App` → `src/lib/wails-app-stub.ts`

2. **Path Aliases** — Mirrors main UI's import convention:
   - `@/` → `../../frontend/src` (main UI components)
   - `@wails/` → `../..` (wails-gui root for assets)
   - `~spotlight/` → `./src` (spotlight's own source)

3. **Dev server** on port 5181, bound to 127.0.0.1

### wails-app-stub.ts

Re-exports the 6 real spotlight Wails bindings and provides no-op stubs for ~200 other bindings the main UI imports. Also returns optimistic values so reused hooks (useSubscription, useVoiceInput) render correctly without the real bindings.

### index.css

Imports main UI's full `index.css` to share the identical theme tokens, keyframes, and base styles. Then adds spotlight-specific overrides:

- Platform-specific body backgrounds (opaque on Windows to mask WebView2 white edges, transparent on macOS for OS vibrancy)
- Legacy token aliases (`--bg`, `--ink`, `--border`, `--accent`, etc.) for pill-form CSS
- Spotlight surface tokens (`--spotlight-surface: #ebf0f2` light, `#1f1f1f` dark)
- User bubble color overrides scoped to `.conv`

## Development

```bash
# Install dependencies
cd wails-gui/quick-chat-spotlight/frontend
pnpm install

# Standalone dev server (no Go backend — uses VITE_YANSU_PORT for host bridge)
pnpm dev
# Opens at http://127.0.0.1:5181

# For full integration with the main Isoform app:
cd wails-gui
./scripts/start-wails-gui.sh
```

### Standalone dev

When running `pnpm dev` without the Go binary, the API client falls back to `VITE_YANSU_PORT` for host bridge location (see `env.d.ts`). Calls to real Wails bindings (GetConfig, HideWindow, ResizeMode, etc.) will no-op silently, but the React tree renders and the CSS is functional. The host bridge refuses `/api/spotlight/*` calls without the helper's IPC token, so suggestions, chat and voice stay empty here.

### Build

```bash
pnpm build
# Output: dist/ (embedded by Wails into the Go binary)
```

## Dependencies

| Package | Purpose |
|---------|---------|
| react, react-dom | UI framework |
| vite, @vitejs/plugin-react | Build tooling |
| tailwindcss, @tailwindcss/vite | Styling |
| class-variance-authority, clsx, tailwind-merge | Style utilities |
| lucide-react | Icons |
| react-markdown, react-syntax-highlighter, remark-gfm | Chat message rendering |
| typescript | Type checking |

## Architecture Notes

### Cross-Fade Transition

The pill-to-conversation transition uses two always-mounted layers anchored to `bottom: 0` inside the viewport. When the user submits, `setView("conversation")` fires first (starting the CSS opacity cross-fade), then `ResizeMode("conversation")` runs the atomic OS window resize. Both motions overlap (~220ms total). The conversation layer is already laid out at its intrinsic 650px height before the cross-fade starts, avoiding mid-animation reflow.

### Component Reuse Strategy

The spotlight reuses main UI chat components directly via `@/` path alias. These components import `wailsjs/go/main/App` for Wails bindings — the Vite plugin redirects those to the stub module. This means the spotlight frontend needs to be updated whenever the main UI adds new Wails bindings (add the new export to `wails-app-stub.ts`).

### Theme Sync

The spotlight follows the main UI's theme preference (not the OS dark-mode setting). Theme and the host's saved UI language are fetched on mount, every `spotlight:show` event, and window focus, so Settings changes reach the running helper. Missing language data or a failed bridge request preserves the last applied language. Applied via `<html data-theme>` attribute swap + Win32 DWM dark-mode hint.
