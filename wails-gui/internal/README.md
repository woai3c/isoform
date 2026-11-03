# wails-gui/internal

Core internal packages for the Yansu Wails GUI application, providing MCP server lifecycle management, git worktree management, terminal session handling, data persistence, activity monitoring, PII detection, speech-to-text, speaker identification, identity resolution, handoff management, single-instance enforcement, and cross-platform OS utilities.

## Directory Structure

```
internal/
├── activitybuf/   # Thread-safe ring buffer for activity events
├── asr/           # Speech-to-text engine interface (Transcriber)
├── handoff/       # File-first store for Yansu handoffs (markdown + frontmatter)
├── identity/      # Resolve chat senders/speakers to Principal entities
├── mcp/           # MCP server lifecycle management and tool bridging
├── meetingdetect/ # Meeting/video call application detection
├── onnxdl/        # ONNX Runtime download and extraction (shared by pii and memory embedding)
├── singleinstance/# Single-instance lock enforcement and IPC
├── speakerid/     # Speaker embedding extraction for audio identity attribution
├── worktree/      # Git worktree-backed workspace management
├── terminal/      # PTY/terminal session management via WebSocket
├── persistence/   # Data persistence layer (SQLite-backed)
├── pii/           # PII detection (GLiNER ONNX) and sensitive app masking
│   └── eval/      #   PII evaluation framework (scorer, risk tiers, scorecard)
├── sshconn/       # SSH connection management and remote deployment
├── wsl/           # Windows Subsystem for Linux integration
└── platform/      # Cross-platform OS utilities
    ├── activitymon/ #   User activity monitoring (clicks, keyboard, app switches, screen capture)
    ├── appdetect/   #   Detect installed editors, terminals, file managers
    ├── browser/     #   Detect and launch Chromium-based browsers
    ├── clipboard/   #   Read/write system clipboard
    ├── cmdutil/     #   Execute commands (Windows console-hiding support)
    ├── fonts/       #   Enumerate system fonts
    ├── notify/      #   Desktop notifications
    ├── ocr/         #   Optical character recognition (macOS Vision framework)
    │   └── eval/    #     OCR evaluation framework (CER/WER metrics, reporter)
    └── shellenv/    #   Shell PATH resolution and environment setup
```

---

## Packages

### `activitybuf` — Activity Event Ring Buffer

A thread-safe circular buffer for `activitymon.ActivityEvent` values. Maintains a rolling window of events bounded by both capacity and time.

**Core type:**

```go
buf := activitybuf.NewRingBuffer(1000, 30*time.Second)

buf.Push(evt)           // Add an event; consecutive keyboard events from the same app are merged
events := buf.Snapshot() // Copy of all live events, oldest to newest
buf.Len()               // Current event count
buf.Clear()             // Discard all events
```

**Keyboard event merging:** Consecutive keyboard events from the same application within 2 seconds are merged into a single event (characters appended), reducing noise in the event stream.

**Eviction:** Events older than the configured time window are automatically evicted on each `Push` and `Snapshot`.

---

### `asr` — Speech-to-Text Engine Interface

Defines the engine-agnostic `Transcriber` interface used by wails-gui for speech-to-text. The wails-gui main package owns engine selection (whisper.cpp via HTTP subprocess, or sherpa-onnx in-process via cgo). The interface lets call sites (foreground push-to-talk, background listening capture loop, transcription scheduler) talk to whichever engine is wired up.

**Core type:**

```go
type Transcriber interface {
    // Decode a complete WAV blob and return plain text (foreground / one-shot).
    Transcribe(wav []byte) (string, error)

    // Decode from the background listening loop. Source is "microphone" or
    // "system_audio". Returns empty string when the engine determines the
    // chunk contains no reliable speech.
    TranscribeBackground(wav []byte, source string) (string, error)

    // Reports whether the engine is initialized and ready.
    IsRunning() bool

    // Flags whether background listening is consuming the engine. Engines may
    // use this to keep a subprocess warm or skip idle-shutdown.
    SetBackgroundActive(active bool)

    // Releases engine resources. Safe to call more than once.
    Stop()
}
```

**Implementations** (in the main `wails-gui` package):
- `whisperASR` — wraps a whisper.cpp HTTP server subprocess
- `sherpaTranscriber` — uses sherpa-onnx in-process via cgo

---

### `handoff` — Handoff File Store

File-first store for Yansu handoffs — one markdown file per handoff under `~/.yansu-agent/handoff/<id>.md`. Deliberately keeps no database: handoffs are few and queried by status, so a directory scan suffices. The package is pure (stdlib only) so it compiles and tests in isolation, without the cgo-heavy `package main`.

**Core types:**

```go
type Status string
const (
    StatusActive     Status = "active"
    StatusInProgress Status = "in_progress"
    StatusCompleted  Status = "completed"
    StatusStale      Status = "stale"
    StatusAborted    Status = "aborted"
)

type Entry struct {
    ID            string   // handoff id
    Title         string
    Status        Status
    Source        Source   // triage, suggestion-fallback, onboarding
    SourceID      string   // grouping id for superseding
    AppName       string
    Workdir       string
    KeyFiles      []string
    ChatSessionID string
    CronJobID     string   // at-cron job created on accept
    CreatedAt     int64    // unix ms
    UpdatedAt     int64    // unix ms
    Body          string   // Handoff.md document (after frontmatter)
}
```

**Usage:**

```go
store, err := handoff.NewStore("~/.yansu-agent/handoff")

// Save a handoff (atomically via temp+rename)
saved, err := store.Save(entry)

// Load by ID
entry, err := store.Get(id)

// List all handoffs, newest first
entries, err := store.List()

// Get the single active handoff for the composer chip
active, err := store.ActiveForChip()

// Transition status with allowed-status validation
entry, err := store.MutateIfStatus(id, []handoff.Status{handoff.StatusActive}, func(e *handoff.Entry) {
    e.Status = handoff.StatusInProgress
})

// Mark aged-out handoffs as stale
expired, err := store.MarkStale(24 * time.Hour)
```

**Persistence format:** Each handoff is stored as a markdown file with a `---`-fenced frontmatter block followed by the document body. The fence is the only unambiguous separator because the body itself contains `- **bold**:` bullets.

**Fallback detector:** `ShouldRunDaytimeFallback` controls when the suggestion-generator fallback may author a handoff. Only fires within the daytime window `[08:00, 23:00)` and at least 4 hours after the last handoff of any source.

---

### `identity` — Identity Resolution

Resolves chat senders, speakers, and other captured signals back to the Principal entities stored in `storage.Store`. Turns "@xxx said Y" into "this is Self / a known Contact / an anonymous bucket" so the activity pipeline downstream can decide whether a statement is attributable to the local user.

**Core types:**

```go
type SubjectKind string
const (
    SubjectSelf      SubjectKind = "self"
    SubjectContact   SubjectKind = "contact"
    SubjectAnonymous SubjectKind = "anonymous"
    SubjectUnknown   SubjectKind = "unknown"
)

type SubjectRef struct {
    PrincipalID  string
    Kind         SubjectKind
    DisplayName  string
    Relationship string
    Method       AttributionMethod
    Confidence   float64
}
```

**Resolution order:**
1. `(app, handleID)` lookup against `app_handles.handle_id`
2. `(app, displayName)` lookup against `app_handles.display_handle`
3. `displayName` lookup against `principals.display_name` and aliases
4. If `autoBucket` is enabled and the app is a chat app: create an anonymous Contact bucket

**Usage:**

```go
resolver := identity.NewResolver(store, true) // autoBucket = true

ref, err := resolver.ResolveSender(ctx, "slack", "U123", "alice")
if ref.IsSelf() {
    // Message from the local user
}
```

**Sender name normalization:** `NormalizeSenderName` cleans raw chat sender strings before identity resolution. Strips trailing timestamps, Discord server tags, role badges (OP, BOT, APP), and message-body fragments that bled into the sender field via buggy parsers. Prevents each `(name + timestamp)` tuple from being auto-bucketed as a separate Anonymous principal.

**App display names:** `AppDisplayName` maps lowercase app identifiers to human-friendly labels (e.g. `"wechat"` -> `"WeChat"`, `"discord"` -> `"Discord"`). Unknown IDs fall back to title-case.

**Chat app detection:** `IsChatApp` identifies apps whose senders should be auto-bucketed into Anonymous principals on first sighting. Covers WeChat, Telegram, Slack, Discord, Feishu, iMessage, LINE, QQ, WhatsApp, Lark, KakaoTalk, Messenger, Signal, Teams, and Zoom Chat.

---

### `mcp` — MCP Server Lifecycle and Tool Bridging

Manages MCP (Model Context Protocol) server subprocesses for the Yansu Desktop agent loop. Spawns, monitors, and tears down MCP-compliant stdio subprocesses, and bridges their tools into the yansu-agent tool registry so the AI model can discover and invoke them.

**Key files:**
- `manager.go` — Subprocess lifecycle (spawn, shutdown, restart, health, tool dispatch, stderr capture)
- `spec.go` — Server specification (`Spec`): id, transport (stdio/HTTP), command/URL, env, headers
- `bridge.go` — Wires MCP tools into the yansu-agent `tools.Registry` under `mcp__<id>__` prefix
- `tool_adapter.go` — Wraps a single MCP tool as a `tools.Tool`, including name sanitization for the Anthropic API
- `gateway_tool.go` — Facade tools (search_tools, describe_tool, call_tool) for gateway exposure mode
- `gateway_result.go` — Result formatting with Gmail-specific JSON slimming and byte capping
- `ringbuf.go` — Fixed-capacity line-oriented ring buffer for stderr log capture

**Architecture:**

```
┌──────────────────────────────────────────┐
│ Manager                                  │
│  owns subprocess lifecycle               │
│  Spawn / Shutdown / Restart / CallTool   │
│  captures stderr → ring buffer           │
│  reports Health / Tools / Logs / Info    │
│  supports stdio + streamable HTTP        │
└────────────┬─────────────────────────────┘
             │ BridgeManager (interface)
┌────────────▼─────────────────────────────┐
│ Bridge                                   │
│  reads tools from Manager                │
│  sanitizes names (Anthropic-safe)        │
│  installs into tools.Registry            │
│  Register / Refresh / Unregister         │
│  Two exposure modes: direct & gateway    │
└────────────┬─────────────────────────────┘
             │ tools.Tool interface
┌────────────▼─────────────────────────────┐
│ McpToolAdapter (direct mode, one/tool)   │
│  Name / Description / JSONSchema         │
│  NeedsPermission → user prompt           │
│  Execute → manager.CallTool              │
├──────────────────────────────────────────┤
│ Gateway Tools (gateway mode, per server) │
│  search_tools — query by name/desc       │
│  describe_tool — full schema for one     │
│  call_tool — dispatch by original name   │
└──────────────────────────────────────────┘
```

**Usage:**

```go
// Create a manager with exit callback.
m := mcp.New(
    mcp.WithInitTimeout(60 * time.Second),
    mcp.WithLogCapacity(500),
    mcp.WithExitCallback(func(id string, reason mcp.ExitReason, err error) {
        log.Printf("server %s exited (reason=%v): %v", id, reason, err)
    }),
)

// Spawn a server (stdio transport).
spec := mcp.Spec{
    ID:      "filesystem",
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
    Env:     map[string]string{"CUSTOM_VAR": "value"},
    WorkDir: "/tmp",
}

// Spawn with streamable HTTP transport.
specHTTP := mcp.Spec{
    ID:        "remote-server",
    Transport: mcp.TransportStreamableHTTP,
    URL:       "https://mcp.example.com/mcp",
    Headers:   map[string]string{"Authorization": "Bearer token"},
}
if err := m.Spawn(ctx, spec); err != nil {
    log.Fatal(err)
}

// Bridge it into the agent registry.
bridge := mcp.NewBridge(m, agentRegistry)
if err := bridge.Register("filesystem"); err != nil {
    log.Fatal(err)
}
// Model can now call mcp__filesystem__read_file, etc.

// On app shutdown:
m.ShutdownAll(ctx)
```

**Tool name sanitization:** MCP server tool names often contain dots, slashes, colons, and hyphens. The Anthropic API only accepts `^[a-zA-Z0-9_-]{1,128}$`. The adapter:
- Collapses disallowed characters to `_`
- Folds hyphens to `_` (LLMs auto-correct hyphens to underscores in tool_use blocks, causing "unknown tool" retry storms)
- Truncates to 128 characters
- Deduplicates collisions with `_2`, `_3` suffixes

The **original** MCP tool name is always sent on the wire — only the registry-exposed name is sanitized. Exposed name pattern: `mcp__<normalized-server-id>__<sanitized-tool-name>`.

**Gateway exposure mode:** For servers with large tool sets (e.g. Google APIs), exposing every tool directly as a `McpToolAdapter` would bloat the system prompt and overwhelm the model. The gateway mode instead registers three facade tools per server that proxy discovery to the upstream MCP server at runtime:

| Facade tool | Purpose |
|---|---|
| `search_tools` | Search upstream tools by name/description. Returns lightweight metadata (name + description) without schemas. Supports a `query` string and `limit` (max 50). |
| `describe_tool` | Fetch the full input schema for a single upstream tool by its original MCP name. |
| `call_tool` | Dispatch a call to the upstream tool by original name with `args`. Requires user permission (`allow_once` / `allow_always` / `reject_once`). |

The model workflow becomes: search → describe → call. This keeps the system prompt small regardless of how many tools the upstream server exposes.

**Result formatting (`gateway_result.go`):** All MCP tool results pass through `FormatGatewayToolResult`, which:
- Extracts text content from MCP content items (text, structured content, JSON blobs)
- Applies Gmail-specific JSON slimming for `fetch_emails` tools: strips `payload`, `body`, `raw`, `parts`, and `attachments` fields; keeps only `from`, `to`, `subject`, `date` headers
- Enforces a 64 KB byte cap on every result, with truncation markers for JSON and plain text

**Environment isolation:** Child processes receive only a denylist-free allowlist (`PATH`, `HOME`, `TZ`, `LANG`, `LC_ALL`, `USER`, `SHELL`, `TMPDIR`). API keys and secrets must be explicitly passed via `Spec.Env` so callsites can audit which servers see which credentials.

**Health model:** `HealthUnknown` → `HealthHealthy` / `HealthUnhealthy`. No auto-restart — if a subprocess exits unexpectedly, the manager marks it unhealthy and fires the exit callback. The caller must explicitly call `Restart`.

**Dependencies:** `github.com/mark3labs/mcp-go` (MCP client and types), `yansu-agent/agent/tools` (tool registry)

---

### `meetingdetect` — Meeting App Detection

Detects whether the frontmost application is a video meeting or call. Used to suppress activity recording during sensitive meetings.

**Categories checked:**

| Category | Apps |
|---|---|
| Native video-conferencing | Zoom, Microsoft Teams, WebEx, FaceTime, RingCentral, Amazon Chime, Skype |
| Messaging apps (title-based) | Slack, Discord, WhatsApp, WeChat, Telegram, Signal, LINE, KakaoTalk, Viber |
| Browser-based meetings | Google Meet, Jitsi Meet, Whereby, and other URL-detectable services |

```go
// Lenient check — native conferencing apps always match; messaging apps require title hints
result := meetingdetect.IsMeetingApp(bundleID, windowTitle)
if result.IsMeeting {
    fmt.Printf("Active meeting: %s\n", result.MeetingName)
}

// Stricter check — requires title hints even for always-meeting apps
result = meetingdetect.IsMeetingEvent(bundleID, windowTitle)

// Check across a set of visible windows (uses window count heuristics)
result = meetingdetect.IsMeetingAppActive(windows)
```

For messaging apps, detection is heuristic — the window title must contain a hint (e.g., `"call"`, `"video"`, `"meeting"`) since these apps are used for both chat and calls. Native conferencing apps are always treated as meetings regardless of window title.

---

### `onnxdl` — ONNX Runtime Download and Extraction

Shared utility for downloading and extracting the ONNX Runtime shared library on first use. Used by both `pii` (GLiNER inference) and the memory embedding subsystem.

**Key functions:**

```go
// Returns the platform-specific library filename
name := onnxdl.LibName() // e.g. "libonnxruntime.dylib"

// Builds the download URL for the current OS and architecture
url, err := onnxdl.ArchiveURL()

// Extracts the library from a ZIP (Windows) or TGZ (macOS/Linux) archive
// Uses atomic temp-file writes to prevent partial reads on concurrent first-use
err = onnxdl.ExtractLib(reader, runtimeDir, destLib, "pii")
```

**Supported platforms:** macOS (arm64 v1.24.3, x86_64 v1.23.0), Linux (x86_64/aarch64 v1.24.3), Windows (x86_64/arm64 v1.24.3).

---

### `speakerid` — Speaker Identification

Wraps sherpa-onnx's `SpeakerEmbeddingExtractor` for use as the second layer of identity attribution on captured audio: ASR tells us WHAT was said, this tells us WHO said it.

**Core type:**

```go
type Extractor struct {
    // wraps sherpa-onnx SpeakerEmbeddingExtractor
    // thread-safe via mutex on the shared handle
}

type Config struct {
    Model      string // absolute path to speaker embedding ONNX file
    NumThreads int    // per-extractor thread count (default 1)
    Debug      bool   // verbose sherpa logging
}
```

**Standard flow:**
1. **Enrollment** — the user records a short reference clip (~10s) on a known handle. `ExtractFromPCM` produces a fixed-dimension float32 embedding persisted alongside the principal.
2. **Verification** — when a fresh microphone chunk arrives, `ExtractFromPCM` runs again and the embedding is compared to enrolled embeddings via `CosineSimilarity`.

**Usage:**

```go
ext, err := speakerid.New(speakerid.Config{Model: "/path/to/model.onnx"})
if err != nil {
    log.Fatal(err)
}
defer ext.Close()

// Extract embedding from mono 16-bit float32 PCM at 16000 Hz
embedding, err := ext.ExtractFromPCM(samples, 16000)

// Compare to enrolled embedding
score := speakerid.CosineSimilarity(embedding, enrolledEmbedding)
// ~> 0.7-0.8 = strong match (same speaker)
// ~  0.5-0.7 = weak match
// ~< 0.5     = different speakers

// Combine multiple enrollment clips into one canonical reference
combined, err := speakerid.AverageEmbeddings([][]float32{emb1, emb2, emb3})
```

The package is intentionally engine-only — it doesn't decide thresholds, load WAV files, or persist anything. That lives in the wiring layer.

**Dependencies:** `github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx`

---

### `worktree` — Git Workspace Management

Manages isolated workspaces backed by git worktrees. Each workspace corresponds to a unique branch and directory under the configured worktree base path.

**Key files:**
- `service.go` — Core workspace lifecycle (create, remove, rename, merge, list)
- `diff.go` — File diff and staging operations
- `helpers.go` — Git command execution, branch name sanitization, path safety

**Core types:**

```go
type WorkspaceInfo struct {
    WorkspaceID string `json:"workspaceId"`
    Label       string `json:"label"`
    Branch      string `json:"branch"`
    BaseBranch  string `json:"baseBranch"`
    Path        string `json:"path"`
    CreatedAt   int64  `json:"createdAt"` // Unix milliseconds
}

type RenameResult struct {
    Branch string `json:"branch"`
    Path   string `json:"path"`
}

type Dependencies struct {
    IsGitRepo              func(path string) bool
    WorktreeBaseDir        func() (string, error)
    DefaultWorktreeBaseDir func() (string, error)
    BranchPrefix           func() string
    Translator             func(prompt string) (string, error)
}
```

**Workspace lifecycle:**

```go
svc := worktree.NewService(store, deps)

// Create a workspace (creates branch + worktree directory)
info, err := svc.CreateWorkspace(projectPath, projectID, workspaceID, label, baseBranch)

// Stage and commit changes
err = worktree.StageFiles(workspacePath, []string{"file.go"})
err = worktree.Commit(workspacePath, "feat: my change")

// Merge worktree branch back into main and clean up
err = svc.MergeWorkspace(projectPath, workspaceID, worktreePath, branch)

// Remove without merging
err = svc.RemoveWorkspace(projectPath, workspaceID)
```

**File diff operations:**

```go
// List changed files (committed + uncommitted)
files, err := worktree.GetChangedFiles(workspacePath, baseBranch)
// Returns []ChangedFile with Status: "added"|"modified"|"deleted"|"renamed"|"untracked"
// and Staged: true/false for partially-staged files

// Get unified diff
diff, err := worktree.GetDiff(workspacePath, baseBranch)

// Discard changes (tracked and untracked files passed separately)
err = worktree.DiscardFiles(workspacePath, tracked, untracked)
```

**Git timeout configuration:**

| Env Var | Default | Description |
|---|---|---|
| `YANSU_GIT_TIMEOUT_MS` | 90000 (90s) | Standard git operation timeout |

Heavy operations (clone, fetch, merge, worktree) use a fixed 5-minute timeout.

**Branch name sanitization:**
- Unicode letters/digits and hyphens are kept; other characters are replaced with `-`
- Maximum 50 characters
- Non-ASCII labels are translated via the `Translator` callback (when set) before sanitization; when nil the original label passes through
- Uniqueness enforced by appending `-2`, `-3`, etc.

**Path safety:**
All file removal operations validate the path is within a managed worktree root (`CanSafelyRemovePath`) to prevent accidental deletion of unmanaged directories.

---

### `terminal` — PTY Terminal Sessions

Manages pseudo-terminal (PTY) sessions exposed to the frontend via WebSocket. Handles terminal I/O with transparent interception of ANSI/VT100 query escape sequences.

**Key files:**
- `manager.go` — Session lifecycle and WebSocket server
- `responder.go` — Escape sequence state machine (query interception and response)

**Core types:**

```go
type Info struct {
    ID     string `json:"id"`
    WSPort int    `json:"wsPort"`
}

type ThemeColors struct {
    Foreground string `json:"foreground"` // hex, e.g. "#cccccc"
    Background string `json:"background"` // hex, e.g. "#1e1e1e"
}

type RemoteEnv struct {
    Type        string `json:"type"`        // "wsl" or "ssh"
    Distro      string `json:"distro"`      // WSL distribution name (empty = default)
    SSHConfigID string `json:"sshConfigId"` // SSH connection config ID
}
```

**Terminal lifecycle:**

```go
mgr := terminal.NewManager(onExit)

// Create a terminal in a given directory (remoteEnv is nil for local terminals)
info, err := mgr.CreateTerminal(ctx, "/path/to/workspace", &themeColors, remoteEnv)
// Returns *Info{ID, WSPort} — frontend connects to ws://localhost:{WSPort}/terminal/{ID}
//
// For WSL: remoteEnv = &terminal.RemoteEnv{Type: "wsl", Distro: "Ubuntu"}
// For SSH: remoteEnv = &terminal.RemoteEnv{Type: "ssh", SSHConfigID: "cfg-id"}

// Resize (rows before cols)
err = mgr.ResizeTerminal(id, rows, cols)

// Close one or all
mgr.CloseTerminal(id)
mgr.CloseAll()
```

**WebSocket server:**
- Starts on-demand on `localhost:0` (random port) when the first terminal is created
- Stops automatically when the last terminal is closed
- Routes: `ws://localhost:{wsPort}/terminal/{id}`

**Security:**
- Connections restricted to loopback addresses (`127.0.0.1`, `::1`)
- Allowed origins: `localhost`, `127.0.0.1`, `::1`, `wails`

**Escape sequence interception (`responder.go`):**

The `Responder` state machine transparently handles terminal queries that xterm.js would otherwise send and handle redundantly. Intercepted sequences are stripped from the PTY output and answered directly:

| Sequence | Response |
|---|---|
| `CSI c` (DA1) | VT102 device attributes |
| `CSI > c` (DA2) | Secondary device attributes |
| `OSC 10 ?` / `OSC 11 ?` | Foreground/background color (from `ThemeColors`) |
| `CSI ? ... $p` (DECRQM) | "Unrecognized mode" |
| `CSI ? u` (Kitty keyboard) | No-op |
| `CSI 6n` (CPR) | Fixed cursor position `1;1` |
| `CSI > q` (XTVERSION) | Empty DCS response |
| DCS sequences | Stripped (pass-through filtered) |

**Debug logging:** Enabled by writing `/tmp/yansu-pty-debug.log`; the file is created automatically when debug mode is active.

---

### `persistence` — State Persistence

Maps frontend application state to the `wails-gui/storage` SQLite layer. Provides batch-optimized loading and cursor-based pagination for conversation history.

**Key files:**
- `service.go` — High-level persistence operations
- `types.go` — Shared data types for frontend/backend exchange

**Core types:**

```go
type ProjectData struct {
    ID                string          `json:"id"`
    Name              string          `json:"name"`
    Path              string          `json:"path"`
    Organization      string          `json:"organization,omitempty"`
    Product           string          `json:"product,omitempty"`
    IsYansuProject    bool            `json:"isYansuProject"`
    ActiveSessionID   string          `json:"activeSessionId"`
    ActiveWorkspaceID string          `json:"activeWorkspaceId"`
    SortOrder         int             `json:"sortOrder"`
    Workspaces        []WorkspaceData `json:"workspaces"`
}

type WorkspaceData struct {
    ID              string        `json:"id"`
    Label           string        `json:"label"`
    IsDefault       bool          `json:"isDefault"`
    WorktreePath    string        `json:"worktreePath,omitempty"`
    WorktreeBranch  string        `json:"worktreeBranch,omitempty"`
    BaseBranch      string        `json:"baseBranch,omitempty"`
    ActiveSessionID string        `json:"activeSessionId"`
    SortOrder       int           `json:"sortOrder"`
    Sessions        []SessionData `json:"sessions"`
    CreatedAt       int64         `json:"createdAt"`
    UpdatedAt       int64         `json:"updatedAt"`
}

type SessionData struct {
    LocalID          string           `json:"localId"`
    WorkspaceID      string           `json:"workspaceId"`
    SessionID        string           `json:"sessionId"` // Claude API session ID
    Label            string           `json:"label"`
    Model            string           `json:"model"`
    CreatedAt        int64            `json:"createdAt"`
    PrdEnabled       bool             `json:"prdEnabled"`
    ThinkingLevel    string           `json:"thinkingLevel"`
    PlanMode         bool             `json:"planMode"`
    ThinkingEnabled  bool             `json:"thinkingEnabled"`
    Pinned           bool             `json:"pinned"`
    PinnedAt         int64            `json:"pinnedAt"`
    AutomationResult bool             `json:"automationResult"`
    UpdatedAt        int64            `json:"updatedAt"`
    Items            []StreamItemData `json:"items"`
}

type StreamItemData struct {
    ID        string `json:"id"`
    Kind      string `json:"kind"`
    Content   string `json:"content,omitempty"`
    ToolName  string `json:"toolName,omitempty"`
    ToolInput string `json:"toolInput,omitempty"` // JSON string
    ToolUseID string `json:"toolUseId,omitempty"` // Anthropic-assigned tool_use_id for pairing tool_use/tool_result
    RunID     string `json:"runId,omitempty"`
    SessionID string `json:"sessionId,omitempty"`
    SortOrder int    `json:"sortOrder"`
    Images    string `json:"images,omitempty"`    // JSON-encoded array of {mediaType, dataBase64} attachments
}

type StreamItemPageData struct {
    Items      []StreamItemData `json:"items"`
    HasMore    bool             `json:"hasMore"`
    NextCursor int              `json:"nextCursor"` // sort_order of earliest item
}
```

**Loading all projects (optimized):**

```go
svc := persistence.NewService(store)

// Batch loads all projects + workspaces + sessions + recent items
// Uses 4 queries instead of N+1; loads last 20 conversation turns per session
projects, err := svc.LoadAllProjects(ctx)
```

**Cursor-based pagination for stream items:**

```go
// Load older conversation items (sessionLocalID is the local UUID, not the Claude API ID)
page, err := svc.LoadStreamItemsPage(ctx, sessionLocalID, cursor, limit)
// page.HasMore, page.NextCursor for continued pagination
```

**Cascading deletes:**

```go
svc.DeleteProjectData(ctx, projectID)     // removes workspaces, sessions, items
svc.DeleteSessionData(ctx, sessionLocalID) // removes stream items
svc.DeleteWorkspaceData(ctx, workspaceID)  // removes sessions, items
```

**Individual saves:**

```go
svc.SaveProjectState(ctx, projectData)           // upsert project + workspaces + sessions
svc.SaveWorkspaceState(ctx, projectID, workspaceData)
svc.SaveSessionState(ctx, projectID, sessionData)
svc.PersistStreamItem(ctx, sessionLocalID, item) // returns sort_order
svc.UpdateStreamItemContent(ctx, itemID, content)
svc.UpdateSessionClaudeID(ctx, localID, sessionID)
```

**Active state tracking:**

```go
svc.SaveActiveProjectID(ctx, projectID)
svc.UpdateProjectActiveSession(ctx, projectID, sessionID)
svc.UpdateProjectActiveWorkspace(ctx, projectID, workspaceID)
svc.UpdateWorkspaceActiveSession(ctx, workspaceID, sessionID)
```

**Ordering:**

```go
svc.ReorderProjects(ctx, []string{id1, id2, id3})         // set sort_order by position
svc.ReorderWorkspaces(ctx, projectID, []string{id1, id2}) // set sort_order within project
svc.NextProjectSortOrder(ctx)                              // returns next available sort_order
```

**Sidebar groups:**

```go
svc.LoadAllSidebarGroups(ctx)                         // load all groups with items
svc.CreateSidebarGroup(ctx, name)                     // create new group
svc.RenameSidebarGroup(ctx, groupID, name)
svc.DeleteSidebarGroup(ctx, groupID)
svc.ReorderSidebarGroups(ctx, []string{id1, id2})
svc.SetSidebarGroupCollapsed(ctx, groupID, collapsed)
svc.AddItemToSidebarGroup(ctx, groupID, itemType, itemID)
svc.RemoveItemFromSidebarGroup(ctx, groupID, itemID)
svc.ReorderSidebarGroupItems(ctx, groupID, []string{id1, id2})
svc.CleanupOrphanGroupItems(ctx)                      // remove references to deleted projects/sessions
```

---

### `pii` — PII Detection and Sensitive App Masking

Detects personally identifiable information in OCR text observations using a GLiNER ONNX model (zero-shot NER). Also manages a registry of sensitive applications (password managers, macOS Keychain) for full-screenshot masking.

**Detected entity types:** `credit_card`, `cvv`, `password`, `one_time_password`, `street_address`, `api_key`

**Detection flow:**
1. OCR observations are concatenated into a single text span
2. Tokenized and fed into the GLiNER ONNX model
3. Sigmoid threshold (0.35) applied; overlapping spans suppressed with greedy NMS
4. Detected spans are mapped back to the original image bounding boxes

**Sensitive apps:** When the active application is a known sensitive app (e.g. 1Password, Bitwarden, macOS Keychain), the entire screenshot is masked rather than running per-field PII detection.

```go
// Check if the active app should suppress all capture
if category, ok := pii.IsSensitiveApp(bundleID); ok {
    _ = category // e.g. "password_manager", "keychain"
    // mask entire screenshot
}

// Per-field PII detection on OCR output
detector, err := pii.NewDetector(configDir, onProgress)
results, err := detector.Detect(ocrObservations)
// results: []DetectionResult{Text, Category, BBox [x,y,w,h], Confidence}

// User-configurable sensitive apps
pii.AddUserSensitiveApp("com.example.myvault", "vault")
apps := pii.GetAllSensitiveApps() // includes IsDefault flag
```

**Image masking functions:**

```go
// Pixelate detected PII regions in a JPEG image
masked, err := pii.MaskRegions(jpegData, detections, jpegQuality)

// Pixelate arbitrary rectangular regions (normalized coords)
masked, err = pii.BlurRegionsBatch(jpegData, []pii.WindowRegion{...}, jpegQuality)

// Blur the entire screenshot (used for sensitive apps)
blurred, err = pii.BlurEntireImage(jpegData, jpegQuality)
```

**PII Evaluation (`pii/eval/`):** A comprehensive evaluation framework for benchmarking PII detectors with product-level safety gates.

```go
// Score predictions against ground-truth annotations
report := eval.Score(samples, predictions, eval.DefaultScoreOptions())
// report.Categories — per-category precision/recall/F1
// report.Benchmark — PrivacyLeakRate, PrivacyBlockRate, UtilityScore, OverallScore
// report.Scorecard — SomethingScorecard with safety gates and DecisionScore
// report.Tiers — per-risk-tier metrics (Critical/High/Medium)
```

The `SomethingScorecard` is privacy-first: critical/high-risk misses drive the protection score, while context preservation estimates non-PII text lost to false-positive masking. Safety gates enforce hard thresholds (e.g. zero critical leaks, >=95% high-risk protection, >=80% context preservation) before a detector is considered ready for user-facing claims.

**Dependencies:** `github.com/yalue/onnxruntime_go`, `platform/ocr`

---

### `sshconn` — SSH Connection Management

Manages SSH connections, remote deployment of the Claude/Codex CLI, and tunneling for remote execution environments.

**Key files:**
- `manager.go` — Connection lifecycle, config storage, status reporting
- `config.go` — SSH configuration types and `~/.ssh/config` parsing
- `tunnel.go` — Port forwarding and tunnel management
- `shell.go` — Remote shell execution

**Core types:**

```go
type Config struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Host        string `json:"host"`
    Port        int    `json:"port"`     // default 22
    User        string `json:"user"`
    AuthType    string `json:"auth_type"` // "key" or "password"
    KeyPath     string `json:"key_path,omitempty"`
    Password    string `json:"password,omitempty"` // only for password auth
    ExecMode    string `json:"exec_mode"`    // "subscription" or "own_plan"
    CLITool     string `json:"cli_tool"`     // "claude" or "codex" (own_plan only)
    AutoConnect bool   `json:"auto_connect"` // connect automatically on app startup
}

type ConnectionStatus struct {
    ConfigID string   `json:"config_id"`
    State    string   `json:"state"` // "disconnected"|"connecting"|"connected"|"error"
    Message  string   `json:"message"`
    Logs     []string `json:"logs"`
}
```

**Connection lifecycle:**

```go
mgr := sshconn.NewManager(onStatus)

mgr.SaveConfig(cfg)      // create or update SSH config
mgr.Connect(cfg)         // establish connection; status reported via onStatus callback
mgr.Disconnect(configID) // close connection

// One-off remote command execution
output, err := mgr.RunCommand(configID, "echo hello")

// Interactive PTY shell session
shell, err = mgr.OpenShell(configID, workDir, cols, rows)

// Port forwarding: returns allocated local port and a cleanup function
localPort, cleanup, err := mgr.ForwardRemotePort(configID, remotePort)

// Auto-discover SSH configs from ~/.ssh/config
configs, err := sshconn.ParseUserSSHConfig()
```

**State transitions:** `disconnected` → `connecting` → `connected` / `error`

**Host key verification:** Auto-accepts unknown hosts (appends to `~/.ssh/known_hosts`); rejects changed keys to prevent MITM attacks.

**Dependencies:** `golang.org/x/crypto/ssh`, `golang.org/x/crypto/ssh/knownhosts`

---

### `singleinstance` — Single-Instance Lock Enforcement

Enforces "only one desktop app per user session" using coordinated OS-level locking and local IPC. A second launch probes the lock and, if another instance is running, sends a "focus" message to the running instance before exiting. This matches the pattern used by Electron's `app.requestSingleInstanceLock()`, Chromium, Slack, Discord, and VS Code.

**Key files:**
- `singleinstance.go` — Public API: `Acquire`, `Send`, `Instance.Release`, `Instance.Messages()`, and IPC accept loop
- `lock_unix.go` — `flock(LOCK_EX|LOCK_NB)` on a file under `~/.yansu-agent/` (Linux/macOS)
- `lock_windows.go` — `CreateMutexW` in the `Local\` per-session namespace (Windows)
- `ipc_unix.go` — Unix domain socket listener/dialer for IPC
- `ipc_windows.go` — Windows named-pipe listener/dialer using `go-winio`

| Primitive | Unix (Linux / macOS) | Windows |
|-----------|---------------------|---------|
| **OS lock** | `flock(LOCK_EX \| LOCK_NB)` on `~/.yansu-agent/` | `CreateMutexW` in per-session namespace |
| **IPC channel** | Unix domain socket under `~/.yansu-agent/` | Named pipe via `go-winio` |

The lock is auto-released on process exit (normal or crash), so a dead instance never permanently blocks subsequent launches.

**Usage:**

```go
const appID = "yansu-desktop"

instance, err := singleinstance.Acquire(appID)
if errors.Is(err, singleinstance.ErrAlreadyRunning) {
    // Another instance is running — signal it to focus, then exit.
    if err := singleinstance.Send(appID, "focus"); err != nil {
        log.Printf("could not signal running instance: %v", err)
    }
    os.Exit(0)
}
defer instance.Release()

// Listen for messages from second launches.
go func() {
    for msg := range instance.Messages() {
        switch msg {
        case "focus":
            // Bring the main window to the foreground.
        }
    }
}()
```

**Dependencies:** `github.com/Microsoft/go-winio` (Windows only for named-pipe IPC). Unix platforms use standard library only.

---

### `wsl` — Windows Subsystem for Linux Integration

Detects WSL distributions and converts Windows ↔ Linux paths. Built only on Windows; stub implementations are compiled on all other platforms.

```go
if wsl.Available() {                             // checks for wsl.exe in PATH
    distros, _ := wsl.ListDistros()              // parses wsl.exe --list --verbose
    def, _     := wsl.DefaultDistro()            // default distro
    linuxPath  := wsl.ToLinuxPath(`D:\project`)  // → /mnt/d/project
    winPath    := wsl.ToWindowsPath("Ubuntu", "/home/user/proj") // → \\wsl$\Ubuntu\home\user\proj
    isWSL      := wsl.IsWSLPath(`\\wsl$\Ubuntu\home\user`)       // true
    shell, args := wsl.ShellCommand("Ubuntu", "/home/user")       // wsl.exe -d Ubuntu --cd /home/user -- bash -l
    runtime, _ := wsl.ResolveJSRuntime("Ubuntu")                  // path to node or bun inside distro
}
```

---

### `platform` — Cross-Platform OS Utilities

Build-tag–based platform abstractions for OS-level operations. Each sub-package provides a common API with platform-specific implementations in separate files (e.g., `_windows.go`, `_darwin.go`, `_linux.go`).

| Sub-package | Purpose |
|---|---|
| `activitymon` | Monitor user activity (clicks, keyboard input, app switches, focus changes) and capture screen content. Defines the `Monitor` and `ScreenCapture` interfaces; implemented natively on macOS (CGEventTap, AXObserver, CGWindowListCreateImage) and Windows (SetWindowsHookExW, SetWinEventHook); stub on Linux. Exposes a Computer-Use API on macOS: `CollectAppElements`, `ListRunningApps`, `ClickAt`, `TypeText`, `PressKey`, `Hotkey`, `Scroll`, `Drag`, `PasteText`, `OpenURL`. Includes `appparser/` subpackage for structured content extraction from 20+ apps (Slack, Discord, GitHub, Gmail, Notion, Obsidian, Office, ChatGPT, etc.) via AX tree analysis and OCR fusion. Also includes browser URL detection, screenshot capture pool, event coalescing, and private/incognito mode detection. |
| `appdetect` | Detect installed applications and return `InstalledApp{Name, AppBundle, Category}` where `Category` is `"editor"`, `"terminal"`, or `"file_manager"` |
| `browser` | Detect and launch Chromium-based browsers |
| `clipboard` | Write text to the system clipboard |
| `cmdutil` | Wrap `exec.Cmd` and `exec.CommandContext` with Windows console-hiding (`CREATE_NO_WINDOW`) and Job Object cleanup; no-ops on Unix |
| `fonts` | List installed system fonts using CoreText (macOS), `fc-list` (Linux), or Registry lookup (Windows), with deduplication |
| `notify` | Send native desktop notifications |
| `ocr` | Optical character recognition. Defines the `Engine` interface with `RecognizeJPEG` and `RecognizeJPEGWithBoxes`. macOS uses the Vision framework via CGo; Windows uses a persistent PowerShell subprocess wrapping the WinRT `Windows.Media.Ocr` API (supports CJK languages); Linux is a stub. Includes `eval/` subpackage for OCR benchmarking (CER/WER metrics, reporter, dataset handling). |
| `shellenv` | Resolve a command's full path via the login-shell `PATH`, expand `~` in paths, and configure shell startup on macOS |

**Example — resolving a command through the shell PATH:**

```go
// Finds "claude" even when it's installed in a shell-managed path (e.g., via nvm/pyenv)
path := shellenv.ResolveCommandPath("claude")
```

**Example — hiding the console on Windows:**

```go
cmd := cmdutil.Command("git", "status")
// On Windows: cmd.SysProcAttr already set with CREATE_NO_WINDOW
cmd.Run()
```

---

## Architecture

```
activitybuf/buffer.go
    └── platform/activitymon  (ActivityEvent type)

asr/asr.go
    └── Transcriber interface (implemented by main package: whisperASR, sherpaTranscriber)

handoff/store.go
    └── handoff/frontmatter.go (parse/serialize markdown with frontmatter)

identity/resolver.go
    ├── identity/chat_apps.go    (IsChatApp registry)
    ├── identity/app_display.go  (human-friendly app labels)
    ├── identity/normalize.go    (sender name cleaning)
    └── storage.Store            (PrincipalRow resolution)

mcp/manager.go
    ├── mcp/spec.go           (server specification; stdio + streamable HTTP)
    ├── mcp/bridge.go         (wires MCP tools into yansu-agent tool registry)
    ├── mcp/tool_adapter.go   (sanitizes names, wraps MCP tool as tools.Tool)
    ├── mcp/gateway_tool.go   (search_tools / describe_tool / call_tool facade)
    ├── mcp/gateway_result.go (result formatting, Gmail slimming, byte capping)
    ├── mcp/ringbuf.go        (stderr log capture)
    └── github.com/mark3labs/mcp-go (MCP client, JSON-RPC over stdio/HTTP)

singleinstance/singleinstance.go
    ├── lock_unix.go / lock_windows.go   (OS-level single-instance lock)
    └── ipc_unix.go / ipc_windows.go     (IPC for signaling running instance)

speakerid/speakerid.go
    └── github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx (speaker embedding extraction)

worktree/service.go
    ├── wails-gui/storage   (persistence)
    ├── worktree/helpers.go (git execution, path safety)
    └── worktree/diff.go    (file diff, staging, commit)

terminal/manager.go
    ├── github.com/aymanbagabas/go-pty (PTY creation)
    ├── github.com/gorilla/websocket   (WebSocket server)
    ├── github.com/google/uuid         (session IDs)
    └── terminal/responder.go          (escape sequence state machine)

persistence/service.go
    └── wails-gui/storage   (SQLite read/write)

platform/activitymon/  (interfaces + macOS/Windows implementations)
    ├── activitymon.go       (Monitor, ScreenCapture interfaces + ActivityEvent)
    ├── monitor_darwin.go    (CGEvent tap, AX API)
    ├── capture_darwin.go    (CGImage screenshot + JPEG encoding)
    ├── capture_pool.go      (shared screen capture resource pool)
    ├── browser_url_darwin.go (browser URL extraction from web content)
    ├── private_mode.go      (private/incognito mode detection)
    ├── event_coalesce.go    (event deduplication and merging)
    └── appparser/           (structured content extraction from 20+ apps)

platform/ocr/
    ├── ocr.go             (Engine interface, Result, TextObservation types)
    ├── ocr_darwin.go      (macOS Vision framework implementation)
    └── ocr/eval/          (CER/WER metrics, reporter, dataset handling)

pii/
    ├── types.go           (DetectionResult, Detector interface)
    ├── detector.go        (GLiNER ONNX inference pipeline)
    ├── tokenizer.go       (text tokenization for ONNX model)
    ├── download.go        (model download on first use)
    ├── mask.go            (image region masking and blurring)
    ├── regex.go           (regex-based PII pattern matching)
    ├── text_redact.go     (text content redaction)
    ├── sensitive_apps.go  (built-in + user-configurable sensitive app registry)
    └── pii/eval/          (scorer, risk tiers, SomethingScorecard, performance metrics)

sshconn/manager.go
    ├── golang.org/x/crypto/ssh          (SSH client)
    ├── sshconn/config.go                (config types + ~/.ssh/config parser)
    ├── sshconn/shell.go                 (interactive PTY shell sessions)
    └── sshconn/tunnel.go                (port forwarding)

wsl/wsl.go               (Windows only; stub on other platforms)
```

**Design patterns:**
- **Dependency injection** — `worktree.Service` accepts a `Dependencies` struct; `mcp.Manager` accepts `commandFactory` and `ExitCallback` via functional options
- **Interface-based isolation** — `mcp.BridgeManager`, `mcp.ToolCaller`, `asr.Transcriber`, `identity.Storage`, and `speakerid` (engine-only) let callers substitute fakes without spawning real subprocesses or engines
- **Gateway/facade** — `mcp.gateway_tool.go` exposes a search→describe→call workflow for servers with large tool sets, keeping the system prompt small
- **State machine** — `terminal.Responder` uses a FSM to parse multi-byte escape sequences correctly across buffer boundaries
- **Batch loading** — `persistence.LoadAllProjects` avoids N+1 queries with 4-query batch strategy
- **Safety-first removal** — Worktree and file removal validates managed path containment before deletion
- **Graceful nil handling** — Persistence service is a no-op when the storage layer is `nil`
- **Environment isolation** — MCP child processes receive only an explicit allowlist; secrets must be passed via `Spec.Env`
- **No auto-restart** — MCP manager marks unhealthy servers but never auto-restarts; callers must explicitly `Restart`
- **File-first storage** — `handoff` stores each entry as a standalone markdown file with frontmatter, avoiding a database dependency
- **Auto-bucketing** — `identity.Resolver` creates anonymous Contact principals for unknown chat senders so the onboarding UI can later prompt "who is this?"

---

## Dependencies

| Package / Tool | Purpose |
|---|---|
| `github.com/aymanbagabas/go-pty` | Pseudo-terminal (PTY) creation and resizing |
| `github.com/gorilla/websocket` | WebSocket server for terminal I/O |
| `github.com/google/uuid` | UUID generation for terminal session IDs |
| `github.com/yalue/onnxruntime_go` | ONNX model inference for GLiNER PII detection |
| `github.com/mark3labs/mcp-go` | MCP client (JSON-RPC over stdio and streamable HTTP) and server types |
| `yansu-agent/agent/tools` | Tool registry and `Tool` interface (used by `mcp` bridge) |
| `golang.org/x/crypto/ssh` | SSH client for remote connections |
| `github.com/Microsoft/go-winio` | Windows named-pipe IPC for single-instance lock (Windows only) |
| `github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx` | Speaker embedding extraction (speakerid) |
| `wails-gui/storage` | SQLite persistence layer |
| `git` (CLI) | All worktree, diff, and staging operations |
| `claude` (CLI) | Optional: translates non-ASCII workspace labels to English branch names |
| `fc-list` (CLI) | Font enumeration on Linux/macOS with Fontconfig |
| `wsl.exe` (CLI) | WSL distribution listing (Windows only) |

---

## Testing

Each package has a corresponding test file:

```
activitybuf/buffer_test.go                  # Ring buffer push, merge, eviction, snapshot
handoff/helpers_test.go                     # Frontmatter parsing, daytime fallback scheduling
handoff/store_test.go                       # Save/get/list, status transitions, stale marking
identity/app_display_test.go                # App name mapping and title-casing
identity/chat_apps_test.go                  # Chat app registry
identity/normalize_test.go                  # Sender name normalization (timestamps, badges, bleeds)
identity/realstore_test.go                  # Resolver integration with real storage
identity/resolver_test.go                   # Subject resolution order, auto-bucketing
mcp/manager_test.go                         # Spawn, shutdown, health, tool dispatch (test-helper subprocess)
mcp/tool_adapter_test.go                    # Name sanitization, permission prompts, content conversion, bridge register/unregister
mcp/gateway_result_test.go                  # Gateway result formatting, Gmail slimming, byte capping
meetingdetect/meetingdetect_test.go         # Meeting app detection heuristics
singleinstance/singleinstance_test.go       # Lock exclusion, IPC delivery, no-listener error paths
speakerid/speakerid_test.go                 # Embedding extraction, cosine similarity, average embeddings
worktree/helpers_test.go                    # Branch sanitization, path safety, git helpers
terminal/responder_test.go                  # Escape sequence interception (DA1, DA2, OSC, etc.)
persistence/service_test.go                 # Load/save round-trips, pagination
pii/detector_test.go                        # PII detection pipeline
pii/download_test.go                        # Model download
pii/regex_test.go                           # PII regex patterns
pii/eval/scorer_test.go                     # Scoring logic, risk tiers, SomethingScorecard
pii/eval/performance_test.go                # Performance metrics computation
platform/activitymon/computer_use_test.go   # Computer-use API (macOS only)
platform/activitymon/event_coalesce_test.go # Event coalescing logic
platform/activitymon/appparser/*_test.go    # 18 app parser tests (Slack, Discord, GitHub, Gmail, browser, etc.)
platform/cmdutil/cmdutil_test.go            # Command execution utilities
platform/ocr/ocr_windows_test.go            # OCR WinRT integration (Windows only)
platform/ocr/ocr_windows_paddle_test.go     # OCR Paddle engine (Windows only)
platform/ocr/resolve_test.go                # OCR result resolution
ocr/eval/metrics_test.go                    # CER/WER computation, tokenization
wsl/wsl_test.go                             # WSL distro listing (Windows only)
```

Run tests from the module root:

```bash
cd wails-gui && go test ./internal/...
```
