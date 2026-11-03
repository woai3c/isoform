# native — Anthropic Agent Loop

Go package implementing a streaming, multi-turn agent loop that drives Yansu's desktop AI assistant via the Anthropic Messages API.

## Overview

This package contains everything needed to run an agentic conversation:

- A hand-rolled Anthropic HTTP/SSE client (no external SDK)
- The main `Run()` loop: streaming, tool execution, permission gating, and error recovery
- Conversation history serialization with Anthropic API invariant enforcement
- A multi-stage tolerant JSON parser for repairing LLM tool-call output
- System prompt templates with plan-mode support
- A request/response permission handshake manager
- Runaway-loop guards: cumulative tool-call detection, tool error streak detection, and orphan repair

## Key Components

| File | Purpose |
|---|---|
| `loop.go` | Main agent loop — streaming, tool dispatch, silent-turn recovery, auto-compaction, error streak detection |
| `anthropic.go` | Anthropic Messages API client with SSE parsing and retry logic |
| `compact.go` | Context window management — summarisation, microcompaction, tail preservation, per-model context limits |
| `history.go` | Converts flat `HistoryItem` records into `Message` arrays for the API; enforces tool_use/tool_result pairing |
| `loop_guard.go` | Cumulative tool-call deduplication guard — detects repeated identical tool invocations and short-circuits them |
| `orphan_repair.go` | Bidirectional tool_use ↔ tool_result pairing reconciliation; runs before every API call as a safety net |
| `permission.go` | Blocking permission request/response handshake with timeout |
| `prompts.go` | System prompt templates; injects plan-mode restrictions |
| `tolerant_json.go` | 5-stage JSON repair for malformed LLM tool-call inputs |

## Architecture

```
Caller
  │
  ▼
Run(ctx, *LoopConfig)           ← loop.go
  │
  ├─ BuildSystemBlocks()        ← prompts.go
  ├─ BuildMessages()            ← history.go
  │
  └─ Loop (up to MaxTurns=100)
       │
       ├─ RepairOrphans()       ← orphan_repair.go (every turn)
       ├─ ApplyCachePrefix()    ← history.go
       │
       ├─ Client.Stream()       ← anthropic.go  (SSE + auto-retry)
       │    └─ ParseSSE()
       │
       ├─ Tool dispatch
       │    ├─ CheckForLoop() ← loop_guard.go (before each execute)
       │    ├─ Parse()                   ← tolerant_json.go
       │    ├─ PermissionManager.Await() ← permission.go
       │    └─ Registry.Execute()
       │
       ├─ Auto-compaction       ← compact.go
       │    ├─ microcompactToolResults() (prune oversized results)
       │    ├─ summarizeMessages()       (LLM summarisation)
       │    └─ dropOldestHalf()          (fallback)
       │
       └─ emit(LoopEvent)       ← caller callback
```

### Conversation flow

1. History is loaded via `HistoryLoader` (or `PriorHistory`) and converted to Anthropic `Message` objects, with synthetic `[interrupted]` blocks injected for any orphaned `tool_use` IDs.
2. `RepairOrphans` runs before every API call, reconciling tool_use ↔ tool_result pairing in both directions. This is the universal bulletproof against "session locked by 400" failures.
3. Each turn streams a response; text, thinking, and tool-use blocks are accumulated.
4. Tool-use blocks are executed sequentially — each call first passes through the `LoopGuard` (`CheckForLoop` / `RecordToolCall`), then the `PermissionManager` for user consent.
5. If a turn produces only thinking (no text, no tool calls) the loop sends one recovery nudge asking the model for a final answer. A second silent turn emits an error and stops.
6. Plan mode swaps in a restricted tool set (Read, Grep, Glob, TodoWrite, ExitPlanMode, WebFetch, WebSearch) and appends additional system instructions.

### Runaway-loop guards

The loop has four independent safety mechanisms that catch different failure modes:

**Loop guard** (`loop_guard.go`): Cumulative detection of identical (tool, input) calls using a stable JSON hash. Soft threshold (3) short-circuits the call with a synthetic tool_result; hard threshold (5) additionally injects a stop-nudge text block. `BashOutput` is exempt because repeated polling with identical arguments is its intended contract.

**Tool error streak** (`loop.go`): Consecutive `is_error` tool_results per tool name. At 5 consecutive failures of the same tool, the loop injects a stop-nudge text block asking the model to stop calling that tool.

**Silent-turn recovery** (`loop.go`): When a turn produces no text and no tool calls, a nudge message is sent. After `MaxNudges` (default 2) consecutive silent turns, the loop returns `ErrSilentTurn`.

**Max turns / wall time** (`loop.go`): Hard caps at 100 turns or 30 minutes.

### Orphan repair

`RepairOrphans` (`orphan_repair.go`) reconciles tool_use ↔ tool_result pairing in the assembled message slice before every API call. It handles two failure modes:

- **Forward-orphans**: assistant tool_use blocks without a matching tool_result — injects synthetic `[interrupted]` tool_results.
- **Reverse-orphans**: tool_result blocks referencing a tool_use ID that doesn't exist — drops them and replaces with placeholder text to preserve user/assistant alternation.

The operation is idempotent and cheap (O(N) single pass). When Anthropic returns a tool-pairing 400, the loop runs a second `RepairOrphans` and retries the turn once.

### Prefix caching

`ApplyCachePrefix` marks the last block of the penultimate message with `cache_control: ephemeral` so Anthropic's prefix cache is used across turns.

## API

### `Run(ctx, cfg)`

```go
func Run(ctx context.Context, cfg *LoopConfig) error
```

`LoopConfig` fields:

| Field | Type | Description |
|---|---|---|
| `Client` | `*Client` | Anthropic API client |
| `Registry` | `*tools.Registry` | Executable tool registry |
| `History` | `HistoryLoader` | Loads prior conversation items |
| `Permission` | `*PermissionManager` | Consent gate for tool calls |
| `SystemText` | `string` | Override system prompt (empty = default) |
| `Emit` | `func(LoopEvent)` | Receives events during execution |
| `RunID` | `string` | Identifies this task run (stamped on every event) |
| `SessionID` | `string` | Session identifier for permissions |
| `HistoryKey` | `string` | Lookup key for `HistoryLoader`; falls back to `SessionID` |
| `WorkDir` | `string` | Working directory for tool execution |
| `ThinkingEnabled` | `bool` | Enable extended-thinking mode |
| `PlanMode` | `*tools.PlanModeState` | Restrict to read-only tools when active |
| `AllowedTools` | `[]string` | Restrict tool list to these names (empty = all) |
| `DisabledTools` | `[]string` | Remove specific tools from the available list (applied after `AllowedTools` / plan-mode filtering) |
| `NoTools` | `bool` | Suppress tool list entirely (pure-text tasks) |
| `NoSystem` | `bool` | Suppress system message entirely |
| `ModelID` | `string` | Anthropic model ID |
| `MaxTokens` | `int` | Per-turn output token budget (defaults to per-model cap; see `MaxOutputTokensForModel`) |
| `Prompt` | `string` | User message for this turn |
| `Images` | `[]ImageSource` | Optional image attachments on the user message |
| `PriorHistory` | `[]HistoryItem` | Pre-loaded history (bypasses `HistoryLoader`) |
| `RecoveryNudgeText` | `string` | Custom message sent after a silent turn; empty falls back to the default conversational text. Tool-heavy flows should override with "keep working with tools" phrasing. |
| `MaxNudges` | `int` | Max recovery nudges after silent turns before giving up; 0 defaults to 2 |

### `LoopEvent`

Events emitted via `LoopConfig.Emit`:

| `Type` | Meaning |
|---|---|
| `run_created` | Loop started |
| `assistant` | Text chunk from the model |
| `thinking` | Extended-thinking chunk |
| `tool_use` | Tool about to be called (emitted after input is fully parsed) |
| `tool_result` | Tool output |
| `permission_request` | Awaiting user consent |
| `plan_complete` | Plan mode finished |
| `compact_start` | Context compaction pass beginning |
| `compact_end` | Context compaction pass completed |
| `compact_error` | Context compaction failed (loop continues via `dropOldestHalf` fallback) |
| `done` | Loop finished successfully; `Usage` field carries token counts |
| `error` | Loop stopped with an error |

### Tolerant JSON

```go
result, err := Parse(raw, ParseOptions{})
```

Attempts five stages in order — strict JSON, markdown fence stripping, repair (smart quotes, Python literals, bare keys, trailing commas, bracket balancing), truncation repair, schema-guided heuristic extraction — and returns the first that succeeds. `result.Warning` is non-empty when a repair was applied.

### Loop guard

```go
// Before executing a tool call:
w := &LoopGuardWindow{}  // created once per run
guard := CheckForLoop(w, toolName, rawInput)
RecordToolCall(w, toolName, rawInput, guard.Hash)

if guard.Kind == "soft" {
    // Short-circuit: push SyntheticLoopBlockResult(tu.ID, tu.Name, guard.Count)
}
if guard.Kind == "hard" {
    // Short-circuit AND inject HardLoopNudgeText(toolName, guard.Count)
}
```

`CheckForLoop` returns `"ok"`, `"soft"` (at 3 matching calls in the rolling window), or `"hard"` (at 5). `RecordToolCall` must be called after the decision to append the call to the window. Both use stable JSON hashing so key-reordered inputs are detected as identical. The rolling window (size 8) means identical calls separated by many distinct calls fall out and do not accumulate.

### Permission handshake

```go
pm := NewPermissionManager()

// In the loop goroutine:
result, err := pm.Await(ctx, sessionID, timeout)

// In the UI goroutine:
err := pm.Respond(sessionID, "allow_once")

// To cancel without granting/rejecting:
pm.Cancel(sessionID)
```

Recognised `optionID` values that set `Granted = true`: `acceptEdits`, `allow_once`, `allow_always`, `accept`.
`allow_always` additionally sets `Always = true`.

### Orphan repair

```go
repaired := RepairOrphans(&messages)
```

Reconciles tool_use ↔ tool_result pairing in both directions. Called before every API call and on tool-pairing 400 retry. Returns `true` when any repair happened. Idempotent and O(N).

### Per-model output caps

```go
maxOut := MaxOutputTokensForModel("claude-sonnet-4-6") // 64000
```

Models match by version substring — Opus 4.6/4.7 → 128k, Opus 4.5 → 64k, Sonnet 4.x → 64k, Haiku 4.5 → 64k. Unknown models fall back to 8192. The loop uses this default when `LoopConfig.MaxTokens` is 0.

## Dependencies

**External:**
- `yansu-agent/agent/tools` — `Registry`, `Tool`, and `PlanModeState` interfaces

**Standard library only** for all other packages; no third-party HTTP or JSON libraries are used so that streaming types remain stable.

## Limits

| Constant | Value | Purpose |
|---|---|---|
| `MaxTurns` | 100 | Prevents runaway loops |
| `MaxWallTime` | 30 min | Per-task wall-clock timeout |
| `MaxOutputTokens` | 8192 | Fallback per-turn output token budget (per-model caps override this) |
| `SoftLoopThreshold` | 3 | Cumulative identical tool calls before short-circuit |
| `HardLoopThreshold` | 5 | Cumulative identical tool calls before stop-nudge injection |
| `toolErrorStreakLimit` | 5 | Default consecutive tool errors before stop-nudge injection (`cua_driver` uses 3) |
| `maxContinuations` | 3 | Max auto-continue nudges for max_tokens truncation |
| Permission timeout | 10 min | Default wait for user consent |
| Retry attempts | 3 | For 429/5xx Anthropic errors |
| `AutoCompactThreshold` | 0.75 | Fraction of context window that triggers compaction |
| `SummarizeMaxTokens` | 8192 | Summary output length cap |
| `SummarizeTimeout` | 60 s | Max time for a summarise call before fallback |

## Testing

```bash
go test ./yansu-agent/agent/native/...
```

Tests are fully offline except for `loop_test.go`, which spins up an `httptest.Server` to replay scripted SSE responses. There are no mocks that require network access.

Key test coverage:
- SSE stream parsing and multi-line payload assembly (`anthropic_test.go`)
- Silent-turn recovery and max-tokens truncation edge cases (`loop_test.go`)
- `tool_use` events carrying fully-parsed input after stream completion (`loop_test.go`)
- Orphaned `tool_use` injection and image block parsing (`history_test.go`)
- Permission timeout, cancellation, and concurrent-request safety (`permission_test.go`)
- All five tolerant-JSON repair stages and helper functions (`tolerant_json_test.go`)
- Loop-guard hash stability, thresholds, and exemption logic (`loop_guard_test.go`)
- Orphan repair bidirectional reconciliation and edge cases (`orphan_repair_test.go`)
- Context compaction summarisation, tail preservation, and microcompaction pruning (`compact_test.go`)
