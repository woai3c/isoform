# agent

Public API package for the `yansu-agent` module. Provides a stable, importable interface for external consumers (e.g. `wails-gui`) that cannot import `internal/` packages directly.

## Overview

This package exposes a single `NativeAgentRunner` that drives the Anthropic Messages API directly and runs tools in-process. Every piece of work an agent does — streaming responses, tool calls, plan mode, permission prompts, extended thinking — is handled here without any subprocess boundary.

| File | Purpose |
|------|---------|
| `types.go` | Shared public types: `TaskInput`, `StreamEvent`, `PermissionOption`, `ImageAttachment`, `StreamCallback`, `AvailableCommand`, `Usage` |
| `runner.go` | `AgentRunner` interface consumed by callers (GUI, CLI, daemon) |
| `native_runner.go` | `NativeAgentRunner` — the single implementation; wires the native loop, tool registry, permission manager, and bash session manager |
| `native/` | In-process agent loop: SSE parse, tool fan-out, cache control, extended thinking, permission gate, history replay, tolerant JSON repair for partial_json deltas |
| `tools/` | Built-in tools: Read, Write, Edit, Bash, Grep, Glob, WebFetch, WebSearch, NotebookEdit, TodoWrite, plan-mode (ExitPlanMode), BashOutput / KillShell, CuaDriver |
| `model_alias_test.go` | Model alias resolution tests |
| `native_bench_test.go` | Micro-benchmarks against the live gateway (build-tagged) |
| `native_live_test.go` | End-to-end live tests against the real Yansu gateway (build-tagged) |

## Types

### `TaskInput`

Parameters for executing a task:

```go
input := &agent.TaskInput{
    Prompt:            "Investigate and fix the authentication failure...",
    WorkDir:           "/path/to/project",
    Model:             "opus",             // resolved through ModelAliases
    SessionID:         "sess_abc123",      // resume a prior session (optional)
    HistoryKey:        tag,                // key used by HistoryLoader to read persisted turns
    Images:            []agent.ImageAttachment{{MediaType: "image/png", DataBase64: "..."}},
    PlanMode:          false,              // true → read-only planning phase
    ThinkingEnabled:   false,              // true → extended thinking
    Ephemeral:         false,              // true → ephemeral session, no persistence
    AllowedTools:      []string{"Read", "Grep", "Glob"}, // restrict available tools (empty = unrestricted)
    NoTools:           false,              // true → suppress tool list entirely (pure-text tasks)
    NoSystem:          false,              // true → suppress system message entirely
    SystemText:        "",                 // override system prompt for this task
    PromptFunc:        nil,                // optional: returns (prompt, history) lazily
    History:           "",                 // fallback static history when HistoryLoader is not wired
}
```

**Full field reference:**

| Field | Type | Purpose |
|-------|------|---------|
| `Title` | `string` | Optional display title |
| `Description` | `string` | Optional description |
| `Prompt` | `string` | Initial user message text |
| `WorkDir` | `string` | Absolute working directory for tool execution |
| `Model` | `string` | Model alias or full API id; falls back to `DefaultModel` |
| `SessionID` | `string` | Resume a prior session; minted internally if empty |
| `HistoryKey` | `string` | Persistence key for `HistoryLoader`; falls back to `SessionID` |
| `Env` | `[]string` | Extra environment variables for tool execution |
| `Images` | `[]ImageAttachment` | Image attachments on the user message |
| `PlanMode` | `bool` | Restricts tools to read-only set; waits for `ExitPlanMode` |
| `ThinkingEnabled` | `bool` | Enables extended thinking mode |
| `Ephemeral` | `bool` | One-shot run; per-session state cleaned up on completion |
| `History` | `string` | Static prior conversation injected when `HistoryLoader` is absent |
| `PromptFunc` | `func() (string, string)` | Lazy prompt + history supplier; resolved before first model call |
| `AllowedTools` | `[]string` | Allowlist; empty = all registered tools available |
| `DisabledTools` | `[]string` | Removes specific tools after `AllowedTools` filtering (e.g. drop `TodoWrite` for classification tasks) |
| `NoTools` | `bool` | Suppress tool definitions entirely (pure-text/classification tasks) |
| `SystemText` | `string` | Per-task system prompt override; empty = runner default |
| `NoSystem` | `bool` | Suppress system message entirely (stronger than `SystemText=""`) |
| `RecoveryNudgeText` | `string` | Overrides the default silent-turn recovery prompt; use when the work IS tool calls and the default "summarise" nudge would abandon the task |
| `MaxNudges` | `int` | Max recovery nudges before returning `ErrSilentTurn`; 0 = default (2) |

### `StreamEvent`

Each streaming event emitted during task execution:

```go
type StreamEvent struct {
    Type              string
    RunID             string
    SessionID         string
    Content           string
    ToolName          string
    ToolInput         any
    ToolUseID         string             // tool_use id for tool_use / tool_result events
    AvailableCommands []AvailableCommand // reserved for future use
    Error             string
    PermissionOptions []PermissionOption // populated for "permission_request" events
    Usage             *Usage             // populated on "done" events
}
```

**Event Types:**

| Type | Description |
|------|-------------|
| `run_created` | Run ID assigned; execution starting |
| `thinking` | Extended thinking content chunk |
| `assistant` | Assistant text message chunk |
| `tool_use` | Tool invocation with `ToolName`, `ToolInput`, and `ToolUseID` |
| `tool_result` | Tool call output with `ToolName`, `Content`, and `ToolUseID` |
| `permission_request` | User must select one of `PermissionOptions` via `RespondToPermission()` |
| `plan_complete` | Planning phase finished (plan mode) |
| `compact_start` | Auto-compaction triggered; `Content` carries status text |
| `compact_end` | Compaction finished; `Content` carries the generated summary |
| `compact_error` | Compaction failed and fell back; `Error` contains the reason |
| `done` | Execution completed successfully; `Usage` field populated |
| `close` | Stream closed (always emitted after `done` or `error`) |
| `error` | Execution failed; `Error` field contains the message |

### `Usage`

Token usage and cache statistics reported on `done` events:

```go
type Usage struct {
    InputTokens              int
    OutputTokens             int
    CacheReadInputTokens     int
    CacheCreationInputTokens int
}
```

### `AvailableCommand`

Describes a slash command exposed to the UI:

```go
type AvailableCommand struct {
    Name        string // command name (e.g. "review")
    Description string // human-readable description
    InputHint   string // hint for expected input
}
```

### `PermissionOption`

Represents a choice presented to the user during a permission request:

```go
type PermissionOption struct {
    ID   string // pass to RespondToPermission()
    Name string // human-readable label
    Kind string // "allow_once", "allow_always", "reject_once", "reject_always"
}
```

### `StreamCallback`

Function called for every event during execution:

```go
type StreamCallback func(event *StreamEvent) error
```

Return a non-nil error from the callback to abort the stream.

## AgentRunner Interface

```go
type AgentRunner interface {
    Init(ctx context.Context) error
    ExecuteTask(ctx context.Context, input *TaskInput, callback StreamCallback) (runID string, err error)
    CancelRun(runID string) error
    RespondToPermission(sessionID, optionID string) error
    CancelPermission(sessionID string)
    Shutdown() error
    IsReady() bool
}
```

### `NativeAgentRunner`

The single implementation. Constructed with a `NativeRunnerConfig`:

```go
runner := agent.NewNativeAgentRunner(agent.NativeRunnerConfig{
    DefaultModel: "opus",
    ModelAliases: map[string]string{
        "sonnet": "claude-sonnet-4-6",
        "opus":   "claude-opus-4-6",
        "haiku":  "claude-haiku-4-5",
    },
    History: myHistoryLoader, // optional
})
if err := runner.Init(ctx); err != nil {
    log.Fatal(err)
}

runID, err := runner.ExecuteTask(ctx, input, func(event *agent.StreamEvent) error {
    switch event.Type {
    case "assistant":
        fmt.Print(event.Content)
    case "permission_request":
        runner.RespondToPermission(event.SessionID, event.PermissionOptions[0].ID)
    case "done":
        fmt.Println("\n[done]")
    }
    return nil
})
```

**Key config fields** (`NativeRunnerConfig`):

| Field | Purpose |
|-------|---------|
| `BaseURL` | Overrides the Anthropic API base (use this for sub2api-style gateways). Empty = `https://api.anthropic.com`. Falls back to `ANTHROPIC_BASE_URL` env var. |
| `APIKey` | Sent as `x-api-key`. Takes precedence over `AuthToken`. Falls back to `ANTHROPIC_API_KEY` env var. |
| `AuthToken` | Sent as `Authorization: Bearer <token>` — the usual shape for sub2api / Yansu gateway. Falls back to `ANTHROPIC_AUTH_TOKEN` env var. |
| `DefaultModel` | Used when `TaskInput.Model` is empty. May itself be a short alias. |
| `SystemText` | Overrides the default system prompt for all tasks. |
| `History` | `native.HistoryLoader` for resuming prior sessions. |
| `ModelAliases` | Short name → pinned API id. Resolved before every request. |

`NativeAgentRunner` also exposes:

```go
// Registry returns the base tool registry. The MCP bridge calls
// ReplaceGroup on this registry to inject server-provided tools.
func (r *NativeAgentRunner) Registry() *tools.Registry
```

## Tool Registry

The in-process tool registry (`agent/tools/`) ships with:

- **File system**: `Read`, `Write`, `Edit` (with L0–L6 fuzzy matching), `NotebookEdit`, `Glob`, `Grep`
- **Execution**: `Bash`, `BashOutput`, `KillShell` — session-scoped background shells managed by `BashTool`
- **Web**: `WebFetch`, `WebSearch`
- **CUA (Computer-Use Agent)**: `CuaDriver` — macOS-only proxy to the cua-driver daemon for UI automation; returns a friendly error on other platforms
- **Planning**: `TodoWrite`, `ExitPlanMode`
- **File locking**: `guard/` — prevents overlapping writes to the same file across parallel tool calls

Restrict available tools per task via `TaskInput.AllowedTools`. Disable all tool definitions via `TaskInput.NoTools`.

In plan mode, the tool set is automatically narrowed to: `Read`, `Grep`, `Glob`, `TodoWrite`, `ExitPlanMode`, `WebFetch`, `WebSearch`.

## Permission Prompts

Any tool may signal it needs permission by implementing `NeedsPermission()`. When triggered, the loop emits a `permission_request` event and blocks until `RespondToPermission` is called. Use `CancelPermission` to skip a request without cancelling the whole run.

## Event Sequence

A typical `ExecuteTask` call emits events in this order:

```
run_created        → run ID assigned
thinking           → extended thinking chunk (if ThinkingEnabled; may repeat)
assistant          → text content chunk (may repeat)
tool_use           → tool invocation (may repeat)
tool_result        → tool output
permission_request → user approval required (blocks until RespondToPermission called or timeout)
plan_complete      → plan phase done (if PlanMode)
compact_start      → auto-compaction triggered (context window at 75%)
compact_end        → compaction complete; Content holds summary
compact_error      → compaction failed; Error holds reason
done               → execution finished; Usage populated
close              → stream closed (always emitted)
error              → only if an error occurred
```

## Loop Limits

| Limit | Value |
|-------|-------|
| Max tool-use turns | 100 |
| Max wall time | 30 minutes |
| Max output tokens per turn | 8192 |

When `MaxTurns` is reached, the loop emits an `error` event and returns. When `MaxWallTime` is exceeded, the context is cancelled and an `error` event is emitted.

## Architecture Notes

- **Single runner, single loop.** One `NativeAgentRunner` per process; the loop lives in `agent/native/loop.go` and owns SSE parsing, tool fan-out, and retry on transient errors.
- **Tolerant JSON repair.** Partial `input_json` deltas on tool calls are accumulated and repaired so trailing-comma / truncated-string breakage doesn't corrupt tool arguments.
- **Extended thinking.** Opt in via `TaskInput.ThinkingEnabled`; thinking blocks are surfaced as `thinking` events ahead of the assistant text.
- **Cache control.** System prompt and long context blocks are tagged with `cache_control` to keep prompt-cache hit rates high across back-to-back turns.
- **History replay.** `native.HistoryLoader` pulls prior user/assistant turns by `HistoryKey` so multi-turn chats survive app restarts. Orphan `tool_use` items without a matching `tool_result` are repaired with a synthetic `[interrupted]` result so the API doesn't reject the request.
- **Silent turn recovery.** If a turn produces no text and no tool calls, the loop sends a recovery nudge asking for a final answer. If subsequent turns remain silent (after `MaxNudges` nudges, default 2), an error is emitted rather than looping to `MaxTurns`.
- **Ephemeral session cleanup.** When `SessionID` is empty, the runner mints a throwaway id and cleans up per-session plan/todo state on run completion to prevent memory leaks.

## Configuration

Runner configuration is passed at construction via `NativeRunnerConfig`. Credentials come from the caller (e.g. `wails-gui` sources `ANTHROPIC_AUTH_TOKEN` + `ANTHROPIC_BASE_URL` from env at startup) or fall through to environment variables.

## Dependencies

| Dependency | Purpose |
|-----------|---------|
| `github.com/google/uuid` | Run ID generation |

## Helpers

```go
// MarshalEventInput encodes a tool input map as JSON. Useful for tests
// and external call sites that need to produce tool_use event payloads.
func MarshalEventInput(v any) string
```

## Testing

Unit tests in the package:
- `model_alias_test.go` — model alias resolution
- `native/history_test.go`, `native/permission_test.go`, `native/tolerant_json_test.go`, `native/anthropic_test.go`, `native/loop_test.go`
- `tools/*_test.go` — each built-in tool

Live tests (gated by build tags) hit the real Yansu gateway:
- `native_live_test.go`
- `native_bench_test.go`

## Usage in wails-gui

```go
import "yansu-agent/agent"

runner := agent.NewNativeAgentRunner(agent.NativeRunnerConfig{
    DefaultModel: "opus",
    ModelAliases: modelAliases,
    History:      historyLoader,
})
```
