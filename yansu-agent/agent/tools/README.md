# agent/tools — Native Agent Tool Layer

This package implements the tool execution layer for the Yansu native agent loop. Each tool satisfies a common `Tool` interface that the `Registry` uses to dispatch `tool_use` blocks from the Anthropic API into Go-side execution.

## Architecture

### Tool Interface

Every tool implements the [`Tool`](registry.go#L14-L30) interface:

```go
type Tool interface {
    Name() string
    Description() string
    JSONSchema() map[string]any
    NeedsPermission(input map[string]any) (bool, PermissionPrompt)
    Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}
```

### Registry Pattern

The [`Registry`](registry.go#L103-L106) provides:
- **Registration** — tools are registered at startup by name; duplicate names panic
- **Lookup** — `Get(name)` and `List(allow)` with optional allow-list filtering
- **Dispatch** — `Execute()` resolves the tool by name, validates required JSON schema fields and basic types, runs the tool, and recovers panics into `is_error` tool results so one bad tool can't crash the loop

### Execution Flow

```
API tool_use block
      │
      ▼
  Registry.Execute(name, ToolCall)
      │
      ├─ panic recovery
      ├─ schema validation (required fields + top-level type)
      │
      ▼
  Tool.Execute(ctx, call)
      │
      ▼
  ToolResult (Content, Warnings, Metadata, Blocks)
```

## Tools

### Registry — Tool Registry

**File:** `registry.go`

Central dispatch for all tools. Provides registration, lookup with allow-list filtering, input validation against JSON schema, and panic-safe execution.

### Bash — Shell Command Execution

**Files:** `bash.go`, `bash_output.go`, `bash_unix.go`, `bash_windows.go`

Runs shell commands in fresh subprocesses. Supports:
- **Foreground** execution with configurable timeout (default 2 min, max 10 min)
- **Background** execution (`run_in_background=true`) for long-running tasks, pollable via `BashOutput`
- Output capped at 32 KB / 3000 lines (head+tail preserved)
- Process group management: `SIGTERM` → 5s grace → `SIGKILL` on timeout
- Unix: `Setpgid` for whole-tree cleanup
- Windows: `CREATE_NEW_PROCESS_GROUP`

Related companion tools:
- **`BashOutput`** (`bash_output.go`) — polls incremental stdout/stderr from a background shell
- **`KillShell`** (`bash_output.go`) — terminates a background shell by `shell_id`

```go
// Start a background build
tool.Execute(ctx, ToolCall{
    Input: map[string]any{
        "command": "bun run build",
        "run_in_background": true,
    },
})

// Poll its output
tool.Execute(ctx, ToolCall{
    Input: map[string]any{
        "shell_id": "bg-1234567890",
    },
})
```

### Read — File Reader

**File:** `fs_read.go`

Reads files with line-numbered output. Supports:
- Text files (with automatic BOM stripping)
- Image files (PNG, JPEG, GIF, WebP, BMP) returned as multi-block `tool_result` with base64 image content
- PDF files
- Jupyter `.ipynb` notebooks
- Large file paging via `offset`/`limit` parameters
- Binary detection heuristic (>30% non-text bytes in first 4 KB)
- Refuses symlinks; caps at 20 MB

### Write — File Writer

**File:** `fs_write.go`

Overwrites files atomically using a temp-file + rename pattern. Features:
- Automatic parent directory creation (`os.MkdirAll`)
- CRLF preservation — if the target file uses `\r\n`, new LF-only content is reflowed
- 1 MB content limit (use Edit for larger files)
- NUL byte rejection

### Edit — File Editor with Fuzzy Matching

**Files:** `edit.go`, `edit_match.go`

Replaces exact text in files using a 6-level fuzzy matching system that gracefully handles model mis-remembered whitespace, line endings, or indentation:

| Level | Name | Relaxation |
|-------|------|------------|
| L1 | Byte-exact | — |
| L2 | EOL-normalized | CRLF → LF |
| L3 | Trim-stripped | BOM removed, trailing whitespace ignored |
| L4 | Dedent | Common leading indent stripped on both sides |
| L5 | Whitespace-loose | Runs collapsed to single space |
| L6 | Anchor+body | First/last line anchor, token-sequence body |

- `replace_all=true` replaces all occurrences
- Returns warnings about the match level so the model can verify correctness

### Glob — File Pattern Matching

**File:** `glob.go`

Filesystem pattern matching using the `doublestar` library (`**/*.go` syntax). Results sorted by modification time (most recent first), capped at 1000 entries. Excludes directories and symlinks.

### Grep — Text Search

**File:** `grep.go`

Wraps the system `ripgrep (rg)` binary. Supports:
- `content`, `files_with_matches`, and `count` output modes
- Context lines (`-A`, `-B`, `-C`)
- Case-insensitive search (`-i`)
- File type and glob filtering
- Multiline mode
- `head_limit` for output truncation

Requires `rg` to be installed on the system.

### NotebookEdit — Jupyter Notebook Editing

**File:** `notebook.go`

Modifies individual cells within `.ipynb` files. Supports replacing cell source, changing `cell_type` (code/markdown/raw), and auto-initializing `outputs` and `execution_count` for new code cells. Preserves nbformat version and only touches the targeted cell.

### Plan Mode — ExitPlanMode Tool

**File:** `plan.go`

Manages the agent's plan mode state. The `ExitPlanModeTool` is called by the model when planning is complete, returning the plan text for user approval. The outer loop tracks whether the session is in plan mode via `PlanModeState`.

### TodoWrite — TODO List Management

**File:** `todo.go`

Manages a shared in-memory TODO list for the current session. The `TodoWriteTool` replaces the entire list, validates each item (content, status, activeForm), and warns if more than one item is `in_progress`.

### CuaDriver — macOS Computer-Use Proxy

**File:** `cua_driver.go`

Proxies tool calls to the bundled [cua-driver](https://github.com/trycua/cua) daemon over a Unix socket (`~/.yansu-agent/run/cua-driver.sock`). Instead of registering 25+ individual tools (one per cua-driver operation), this single tool exposes a `name` field to select the upstream tool and an `args` object for input. This keeps the model's tool-list token spend bounded and lets cua-driver evolve its surface independently.

Supports the full cua-driver surface: `launch_app`, `get_window_state`, `click`, `type_text`, `hotkey`, `scroll`, `screenshot`, `list_apps`, `list_windows`, `set_value`, `press_key`, `drag`, `check_permissions`, and more. Three click-addressing modes are available: `element_index` from AX trees, `v*` visual refs from OCR (for CEF/Electron/Chinese apps), and raw `{pid, x, y}` pixel coords.

30-second per-call timeout. macOS-only — returns a friendly error on other platforms.

### Web — Web Fetch and Search

**File:** `web.go`

Provides:
- **`WebFetchTool`** — Fetches URLs over HTTPS (SSRF-protected: refuses internal/private IPs, loopback, cloud metadata). Strips HTML tags via regex, 2 MB response cap. HTTP errors (4xx/5xx) are surfaced as `is_error` to prevent the model from retrying dead URLs.
- **`WebSearchTool`** — Stub that returns an informational message; search backend not configured in this build.

## Safety Layer — `guard/`

The [`guard` sub-package](guard/README.md) provides three cross-cutting safety mechanisms:

1. **Path Validation** (`guard/path.go`) — `ValidateAbsolutePath()` rejects relative paths and `..` traversal; `WithinAllowed()` enforces workspace root confinement
2. **File Locking** (`guard/filelock.go`) — `FileLockManager` issues one `sync.Mutex` per canonical path (LRU-capped at 1024 entries), serializing concurrent Edit/Write calls to the same file
3. **Command Blacklisting** (`guard/cmdblacklist.go`) — `ClassifyCommand()` blocks catastrophic patterns (`rm -rf /`, fork bombs, `curl | sh`) and flags risky prefixes (`rm`, `git push --force`, `sudo`); `ScrubSensitiveEnv()` strips `*_TOKEN`/`*_KEY`/`*_SECRET` env vars from subprocess environment

## Dependencies

### External
- `github.com/bmatcuk/doublestar/v4` — glob pattern matching (Glob tool)

### Internal
- `yansu-agent/agent/tools/guard` — safety layer (path validation, file locks, command classification)

### System
- `rg` (ripgrep) — required by the Grep tool
- `/bin/sh` (Unix) or `cmd.exe` (Windows) — required by the Bash tool

## Testing

```bash
go test ./yansu-agent/agent/tools/...
```

Test files:
- `bash_test.go` — foreground/background execution, exit codes, timeouts, output limits
- `cua_driver_integration_test.go` — cua-driver daemon integration (requires running daemon)
- `edit_test.go` — exact and fuzzy matches, replace_all, BOM handling
- `edit_match_test.go` — all 6 fuzzy match levels, edge cases
- `fs_test.go` — Read/Write (binary detection, CRLF preservation, symlink refusal)
- `grep_glob_test.go` — glob and grep search behaviors
- `registry_test.go` — tool registration, dispatch, schema validation, panic recovery
- `web_notebook_test.go` — web fetch and notebook edit tests
- `guard/guard_test.go` — all guard safety mechanisms

## Configuration

- **Workspace roots** — passed to `guard.WithinAllowed()` to confine file access
- **Allow list** — `Registry.List(allow)` filters available tools per session
- **Timeouts** — Bash: 2 min default, 10 min max (configurable per call via `timeout` parameter)
