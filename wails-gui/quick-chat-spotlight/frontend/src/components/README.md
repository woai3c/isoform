# components/

React components for the Quick Chat Spotlight panel. Three components covering two views (pill entry and conversation surface), cross-faded via the parent `App.tsx` stage.

## Components

### PillInput

The spotlight's entry-point card ("form 1"). A 768x88px rounded input surface with:

- **Top row**: Isoform logo + `DetectionChip` (app context badge)
- **Bottom row**: Textarea (autofocused with retry logic for Win32 WebView2 focus forwarding), voice input button, send button
- **Voice input**: Reuses main UI's `useVoiceInput` hook — same sherpa-onnx STT engine, same UX
- **Esc to dismiss**: Global keydown listener hides the Wails window
- **Submit on Enter**: Plain Enter submits; Shift+Enter for newlines is blocked by the single-row design

The component is deliberately **not** a wrapper around main UI's `<Composer>`. The main composer carries model selectors, plan/thinking toggles, image upload, slash commands, and queue controls — all inappropriate for the 88px pill form factor.

### DetectionChip

A small badge rendered inside `PillInput` showing the foreground app context:

- **With detection**: `[icon] {AppName} — Detected` — clickable when a `topSuggestion` exists, fills the textarea
- **Without detection**: `Isoform` — neutral fallback, non-clickable
- **App icon**: Renders a colored letter tile via inline SVG (v1 stand-in for vendor logos)

The label intentionally does not repeat the top suggestion's title, since the suggestion prompt already populates the textarea placeholder.

### ConversationPanel

The spotlight's full chat surface ("form 2"). A 650px-tall card with three horizontal bands:

```
┌────────────────────────────────────────────────────────┐
│ [×] App Name                                  [□]      │  53px header
├────────────────────────────────────────────────────────┤
│   chat messages, scrollable                            │  flex: 1
├────────────────────────────────────────────────────────┤
│ Refine or confirm…           [mic] [send/stop]         │  57px input
└────────────────────────────────────────────────────────┘
```

Key behaviors:

- **StreamState via useReducer**: Folds `AgentEvent` SSE payloads into the same `StreamItem[]` shape the main UI uses, so imported components (`GroupedStreamList`, `TypingIndicator`, `PermissionQuestionCard`) render without adaptation
- **Permission handling**: Renders a `PermissionQuestionCard` when the agent requests tool approval; dispatches `respondToPermission` or `clearPermission`
- **Edit/retry**: `editAndTruncate` replaces a user message and drops everything after it, then resubmits — backend rewrites the DB row and cancels the in-flight turn
- **Expand to main window**: Opens the chat in the full Isoform app via `expandAPI` + `HideWindow`
- **Voice input**: Same `useVoiceInput` hook as `PillInput`
- **Typing indicator**: Mirrors main UI logic — shows three-dot indicator only in the gap between user message and first agent content

## Design patterns

### Wails drag conventions

The outer card surfaces use `--wails-draggable: drag` so the frameless window can be dragged by the header. Interactive regions (buttons, textarea) opt out with `--wails-draggable: no-drag`.

### Inline styles via `<style>` tags

Each component embeds its CSS in a `<style>` tag rendered inline rather than importing external stylesheets. This keeps spotlight styles self-contained and avoids leaks into the main UI's CSS (`@import`ed via `index.css`).

### Cross-fade layer architecture

`PillInput` and `ConversationPanel` are both always-mounted inside `App.tsx`'s stage container. The inactive layer gets `opacity: 0; pointer-events: none`. Both layers are `position: absolute; bottom: 0` with fixed heights — the conversation panel is laid out at its intrinsic 650px before the cross-fade starts, so the OS window resize (which grows the window upward) and the CSS opacity transition (220ms) overlap without layout reflow.

### Component reuse via `@/` alias

Both components import main UI components (`GroupedStreamList`, `TypingIndicator`, `PermissionQuestionCard`, `VoiceRecordingIndicator`) through the `@/` path alias. The Vite plugin redirects these components' Wails binding imports to `wails-app-stub.ts`, which provides real bindings for the 6 methods the spotlight binary exposes and no-op stubs for the rest.

## Dependencies

- **lucide-react**: Icons (`ArrowUp`, `Loader2`, `Mic`, `Square`, `X`)
- **`@/components/*`**: Reused main UI components (GroupedStreamList, TypingIndicator, PermissionQuestionCard, VoiceRecordingIndicator)
- **`@/hooks/useVoiceInput`**: Voice recording/transcription hook
- **`@/assets/icons/LogoIcon`**: Isoform logo SVG
- **`../api/types`**: TypeScript types (`DetectionContext`, `Suggestion`, `AgentEvent`, `StreamEvent`, etc.)
- **`../api/yansu`**: HTTP client for the Isoform host bridge
- **`../chat/reducer`**: Pure reducer functions (`processAgentEvent`, `appendUserMessage`, `editAndTruncate`, `clearPermission`)
- **`../../wailsjs/go/main/app`**: Wails Go bindings (`HideWindow`, `SetAutoHideOnBlur`, `ResizeMode`, `SetSurfaceColor`, `SetDarkMode`)
- **`../../wailsjs/runtime/runtime`**: Wails runtime (`EventsOn` for `spotlight:show` event)

## File structure

```
components/
├── PillInput.tsx           # Pill entry view (form 1)
├── DetectionChip.tsx       # App context badge inside the pill
├── ConversationPanel.tsx   # Full chat surface (form 2)
└── README.md
```
