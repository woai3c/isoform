# Quick Chat Spotlight — API Layer

Thin HTTP client and TypeScript type definitions for communicating with the Isoform host bridge from the Spotlight WebView. This directory is the single source of truth for all Spotlight ↔ Isoform network contracts.

## Directory Overview

Two files:

| File | Purpose |
|---|---|
| `types.ts` | TypeScript interfaces mirroring the Go-side wire format (types used by both the SSE stream and REST endpoints). |
| `yansu.ts` | HTTP client that resolves the dynamic Isoform host-bridge port, attaches auth tokens, and exposes typed functions for every Spotlight API endpoint. |

## Architecture

```
┌─────────────────────────┐     HTTP (localhost)     ┌──────────────────┐
│  Spotlight WebView      │ ──────────────────────→  │  Isoform host      │
│  (this API layer)       │ ←── SSE stream ────────  │  bridge (Go)     │
│                         │                          │                  │
│  yansu.ts ── fetch() ──→│                          │  /api/spotlight/*│
│  types.ts  ←── JSON ────│                          │                  │
└─────────────────────────┘                          └──────────────────┘
```

The Spotlight helper is a separate WebView2/WKWebView window spawned by Isoform. It has no direct access to Isoform internals — all communication goes over HTTP to the host bridge, which runs on a random port assigned at launch.

### Port & Auth Resolution

The host bridge port and session token are unpredictable (assigned at startup). They're resolved at runtime:

1. **Production path**: Calls the Wails binding `GetConfig()` (exposed by `main.go` from CLI flags `--yansu-port` and `--ipc-token` passed by the Spotlight supervisor).
2. **Dev fallback**: Reads `VITE_YANSU_PORT` from Vite env vars, allowing developers to point at a manually-running host bridge. Without the IPC token the bridge refuses `/api/spotlight/*`, so this only renders the UI.
3. Results are cached in memory — subsequent calls are free.

## API Endpoints

### `fetchSuggestions(bundleID, lang?)` → `Suggestion[]`
Fetches contextual suggestion chips for the foreground app. Called on every Spotlight open.
```
GET /api/spotlight/suggestions?bundleID=com.figma.Desktop&lang=en
```

### `submitPrompt(prompt, bundleID, sessionLocalId?, editOf?)` → `{ sessionLocalId }`
Submits a user prompt and starts (or continues) a chat turn. The first call omits `sessionLocalId` and the server mints one. Follow-up calls pass the returned ID to append to the same session. When `editOf` is provided (from an edit/reset action), the server rewrites the existing user row and truncates subsequent history before running the new turn.
```
POST /api/spotlight/submit
```

### `subscribeStream(sessionLocalId, onEvent)` → `() => void`
Opens an SSE connection and invokes `onEvent` for each parsed `StreamEvent`. Returns an abort function. Uses a manual `fetch` + `ReadableStream` reader (rather than `EventSource`) so the auth token can be sent as a custom header.
```
GET /api/spotlight/stream/:id  (SSE)
```

### `respondToPermission(sessionID, optionID)` → `void`
Unblocks the agent loop after a `permission_request` event. `sessionID` is the Anthropic session ID (not the Spotlight tag).
```
POST /api/spotlight/permission
```

### `fetchAppearance()` → `{ theme, language? } | null`
Returns the main UI's resolved theme and saved `ui.language`. The helper applies them on mount, every summon, and window focus, so Settings changes reach an already-running helper. A missing language, failed request, or 500 ms timeout preserves the last applied language; stale responses cannot override newer choices. Timed-out requests are aborted so appearance cannot hold up suggestions indefinitely.
```
GET /api/spotlight/theme
```

### `expand(sessionLocalId)` → `void`
Surfaces the main Isoform window and navigates it to this Spotlight chat session.
```
POST /api/spotlight/expand
```

### `cancel(sessionLocalId)` → `void`
Cancels the in-flight agent run. The server emits a synthetic done+close event so the panel exits streaming state cleanly.
```
POST /api/spotlight/cancel
```

### `transcribeAudio(wavBase64)` → `string`
Uploads a base64-encoded WAV blob for offline STT via the shared sherpa-onnx engine (which runs only in the main Isoform binary, not in the Spotlight WebView).
```
POST /api/spotlight/voice/transcribe
```

### `initVoiceInput()` → `void`
Pre-warms the speech recognition model so the first transcription doesn't incur a cold-start delay.
```
POST /api/spotlight/voice/init
```

### `dismiss()` → `void`
Notifies the host bridge that the Spotlight window is being dismissed. Best-effort (errors are silently caught).
```
POST /api/spotlight/dismiss
```

## Stream Event Types

The SSE stream emits events with a `kind` discriminator:

| Kind | Payload | Description |
|---|---|---|
| `agent_event` | `AgentEvent` | Full agent event (tool use, assistant text, permissions, etc.) |
| `user_message` | `{ prompt }` | Echo of the user's prompt (before agent produces tokens) |
| `assistant_started` | `{}` | Synthetic event signalling the agent has begun |
| `deliverable_proposed` | `DeliverableProposal` | Legacy deliverable hint |

`AgentEvent` mirrors `wails-gui/app.go::AgentEvent` field-for-field, using snake_case keys to match Go JSON tags.

## Dependencies

- **Wails runtime**: `GetConfig` binding from `../../wailsjs/go/main/app` (codegen'd Go bindings)
- **Browser**: `fetch`, `ReadableStream`, `AbortController`, `TextDecoder` (all standard Web APIs)
- **Vite**: `import.meta.env.VITE_YANSU_PORT` for dev fallback

No external npm packages are required — the entire layer uses only platform APIs and the Wails Go binding bridge.

## Usage Example

```typescript
import { fetchSuggestions, submitPrompt, subscribeStream } from "./api/yansu";
import type { StreamEvent } from "./api/types";

// Fetch contextual suggestions for the foreground app
const suggestions = await fetchSuggestions("com.figma.Desktop", "en");

// Start a new chat turn
const { sessionLocalId } = await submitPrompt("Summarize this design", "com.figma.Desktop");

// Subscribe to the streaming response
const cancel = subscribeStream(sessionLocalId, (ev: StreamEvent) => {
  switch (ev.kind) {
    case "agent_event":
      console.log(ev.data.type, ev.data.content);
      break;
    case "user_message":
      console.log("Echo:", ev.data.prompt);
      break;
  }
});

// Later: cancel the stream
cancel();
```

## Testing

No dedicated tests exist in this directory. The API layer is tested indirectly through the Spotlight integration tests in the Go backend (`/api/spotlight/*` handlers) and through manual QA of the Spotlight panel. The thin nature of the client (mostly `fetch` wrappers) makes unit tests low-value compared to end-to-end Spotlight tests.
