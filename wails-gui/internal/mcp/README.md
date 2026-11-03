# mcp

MCP (Model Context Protocol) server management for the Yansu Desktop (Wails GUI) agent loop. This package spawns, monitors, and tears down MCP-compliant subprocesses, and bridges their tools into the yansu-agent tool registry so the AI model can discover and invoke them.

## Package structure

| File | Responsibility |
|---|---|
| `manager.go` | Subprocess lifecycle: spawn, shutdown, restart, health, tool dispatch, stderr capture, exit callbacks |
| `spec.go` | Server specification (`Spec`) — id, command, args, env, transport mode, URL, headers, working directory |
| `bridge.go` | Wires MCP tools into the yansu-agent `tools.Registry` under the `mcp__<id>__` prefix; manages exposure modes (direct vs gateway) |
| `tool_adapter.go` | Wraps a single MCP tool as a `tools.Tool` implementation, including name sanitization for the Anthropic API |
| `gateway_tool.go` | Gateway exposure mode: `search_tools`, `describe_tool`, `call_tool` facade tools for servers with many upstream tools |
| `gateway_result.go` | Result formatting for gateway tools — Gmail-specific JSON slimming, byte-cap truncation with metadata |
| `ringbuf.go` | Fixed-capacity line-oriented ring buffer for stderr log capture |

## Architecture

```
┌──────────────────────────────────────────┐
│ Manager                                  │
│  owns subprocess lifecycle               │
│  Spawn / Shutdown / Restart / CallTool   │
│  captures stderr → ring buffer           │
│  reports Health / Tools / Logs / Info    │
│  fires ExitCallback on unexpected death  │
└────────────┬─────────────────────────────┘
             │ BridgeManager (interface)
┌────────────▼─────────────────────────────┐
│ Bridge                                   │
│  reads tools from Manager                │
│  sanitizes names (Anthropic-safe)        │
│  installs into tools.Registry            │
│  Register / Refresh / Unregister         │
│  SetExposureMode (direct vs gateway)     │
└────────────┬─────────────────────────────┘
             │ tools.Tool interface
┌────────────▼─────────────────────────────┐
│ McpToolAdapter / GatewayTool             │
│  Name / Description / JSONSchema         │
│  NeedsPermission → user prompt           │
│  Execute → manager.CallTool              │
└──────────────────────────────────────────┘
```

**Key design points:**

- **Manager owns the subprocess**, not any caller context. Caller ctx only governs the initialize handshake timeout (default 30s).
- **Bridge is intentionally thin** — it only synchronizes tool snapshots. Lifecycle decisions stay in the Manager.
- **Both expose interfaces** (`BridgeManager`, `ToolCaller`) so tests can substitute fakes without spawning real subprocesses.
- **No auto-restart.** If a subprocess exits unexpectedly, the Manager marks it unhealthy and fires an exit callback. The caller must explicitly call `Restart`.

## Transport modes

The package supports two transports, selected via `Spec.Transport`:

| Transport | Constant | Description |
|---|---|---|
| `stdio` (default) | `TransportStdio` | Subprocess with JSON-RPC over stdin/stdout. Requires `Command` and `Args`. |
| `streamable_http` | `TransportStreamableHTTP` | HTTP client to a remote MCP server. Requires `URL`. Optional `Headers` are forwarded on requests. |

An empty `Transport` field defaults to `stdio` for backward compatibility. The legacy `http` value is mapped to `streamable_http`.

## Exposure modes

The Bridge supports two exposure modes, controlling how a server's tools appear in the agent registry:

### Direct mode (default)

Every upstream MCP tool is registered as a separate model-visible tool. This is the historical behavior and works well for servers with a small, stable tool set.

```
mcp__filesystem__read_file
mcp__filesystem__write_file
mcp__filesystem__list_directory
```

### Gateway mode

Only three facade tools are registered, regardless of how many upstream tools the server exposes. The model searches for and describes tools before calling them. This is useful for servers with many tools (e.g., 50+ Google API tools) where flooding the model's tool list would degrade performance.

```
mcp__google__search_tools    — search available tools by name/description
mcp__google__describe_tool   — get one tool's input schema
mcp__google__call_tool       — invoke a tool by its original upstream name
```

Set the mode before registering:

```go
bridge.SetExposureMode("google", mcp.ExposureModeGateway)
bridge.Register("google")
```

### Gateway result processing

`FormatGatewayToolResult` applies two transformations to gateway tool results:

1. **Gmail sanitization**: For `GOOGLESUPER_FETCH_EMAILS`, drops large fields (`payload`, `body`, `raw`, `parts`, `attachments`) and filters email headers to a small allowlist (`from`, `to`, `subject`, `date`).
2. **Byte cap**: Results exceeding 64KB are truncated with metadata (`original_bytes`, `cap_bytes`, `prefix_bytes`, `content_prefix`), preserving valid UTF-8 boundaries.

## Tool name sanitization

MCP servers in the wild use dots, slashes, colons, and hyphens in tool names. The Anthropic API only accepts `^[a-zA-Z0-9_-]{1,128}$`. The adapter sanitizes every name:

- Dots, slashes, colons, spaces → `_`
- Hyphens → `_` (LLMs auto-correct hyphens to underscores in `tool_use` blocks, so exposing hyphens causes "unknown tool" retry storms)
- Empty names → `_`
- Result truncated to 128 characters
- Collisions deduplicated with `_2`, `_3` suffixes

The **original** MCP tool name is always sent on the wire when calling `tools/call` — only the registry-exposed name is sanitized.

Exposed name pattern: `mcp__<normalized-server-id>__<sanitized-tool-name>`

## Exit callbacks

The `ExitCallback` type lets callers react to unexpected server exits. The callback is invoked exactly once per server after the subprocess is reaped, and runs on a manager-internal goroutine (callbacks must not block on Manager methods or they will self-deadlock).

```go
m := mcp.New(
    mcp.WithExitCallback(func(serverID string, reason mcp.ExitReason, exitErr error) {
        if reason == mcp.ExitUnexpected {
            // Unregister tools so the model stops seeing a dead server.
            bridge.Unregister(serverID)
        }
    }),
)
```

`ExitIntentional` is set for `Shutdown`/`ShutdownAll`/`Restart` teardowns and suppresses the callback so callers don't double-unregister.

## Usage

```go
// Create a manager with custom options.
m := mcp.New(
    mcp.WithInitTimeout(60 * time.Second),
    mcp.WithLogCapacity(500),
    mcp.WithExitCallback(func(id string, reason mcp.ExitReason, err error) {
        log.Printf("server %s exited (reason=%v): %v", id, reason, err)
    }),
)

// Spawn a stdio server.
stdioSpec := mcp.Spec{
    ID:      "filesystem",
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
    Env:     map[string]string{"CUSTOM_VAR": "value"},
    WorkDir: "/tmp",
}
if err := m.Spawn(ctx, stdioSpec); err != nil {
    log.Fatal(err)
}

// Spawn a remote HTTP server.
httpSpec := mcp.Spec{
    ID:        "remote-api",
    Transport: mcp.TransportStreamableHTTP,
    URL:       "https://mcp.example.com/api",
    Headers:   map[string]string{"Authorization": "Bearer token"},
}
if err := m.Spawn(ctx, httpSpec); err != nil {
    log.Fatal(err)
}

// Bridge into the agent registry.
bridge := mcp.NewBridge(m, agentRegistry)

// Use gateway mode for servers with many tools.
bridge.SetExposureMode("remote-api", mcp.ExposureModeGateway)
bridge.Register("remote-api")

// Use direct mode for small servers.
bridge.Register("filesystem")

// The model can now call mcp__filesystem__read_file, etc.
// Or for gateway: mcp__remote_api__search_tools, etc.

// On app shutdown:
m.ShutdownAll(ctx)
```

## Environment isolation

Child processes receive a **denylist-free allowlist** of parent environment variables: `PATH`, `HOME`, `TZ`, `LANG`, `LC_ALL`, `USER`, `SHELL`, `TMPDIR`. Everything else (API keys, tokens, secrets) must be explicitly passed via `Spec.Env`. This ensures we can audit which servers see which credentials.

## Testing

Integration tests (`manager_test.go`) use a **test-helper subprocess pattern**: the test binary re-execs itself as a real MCP stdio server when `YANSU_MCP_TEST_HELPER=1` is set. No external dependencies needed.

```
go test ./... -count=1 -timeout 120s
```

Helper modes (`YANSU_MCP_TEST_HELPER_MODE`):

| Mode | Behavior |
|---|---|
| `echo` (default) | Exposes an `echo` tool that returns its text argument |
| `panic` | Crashes on first tool call (tests `is_error` path) |
| `noisy` | Emits stderr lines before serving (tests log capture) |
| `slow_init` | Sleeps before serving (tests init timeout) |

Streamable HTTP integration tests use `mcp-go`'s `NewTestStreamableHTTPServer` to spin up an in-process HTTP MCP server, verifying header forwarding and the full spawn-list-call-shutdown lifecycle.

Unit tests (`tool_adapter_test.go`, `gateway_result_test.go`) use fakes (`fakeCaller`, `bridgeFakeManager`) and cover name sanitization, permission prompts, content conversion (text, image, mixed), bridge register/unregister/replace, gateway search/describe/call dispatch, Gmail sanitization, and Anthropic API limit enforcement.

## Dependencies

- [`github.com/mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — MCP client (JSON-RPC over stdio, streamable HTTP) and server types
- `yansu-agent/agent/tools` — the project's own tool registry and `Tool` interface
