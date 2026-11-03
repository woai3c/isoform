# test-computer-use

**Build constraint:** `//go:build darwin` — macOS only.

A CLI tool and E2E test suite for exercising the `activitymon` computer-use API, which provides macOS accessibility tree inspection and input automation.

## Files

| File | Description |
|---|---|
| `main.go` | Interactive CLI for manually testing `activitymon` functions |
| `e2e_test.go` | Go test suite for both direct `activitymon` API and host-bridge HTTP endpoints |

## CLI Usage

```bash
# Run from wails-gui root
go run ./tools/test-computer-use <command> [args]
```

| Command | Args | Description |
|---|---|---|
| `apps` | — | List all running macOS applications |
| `windows` | `<app>` | List windows for a named app |
| `see` | `<app>` | Dump the accessibility element tree |
| `open` | `<url>` | Open URL in the default browser |
| `click` | `<x> <y>` | Click at screen coordinates |
| `type` | `<text>` | Type text via keyboard automation |
| `press` | `<key>` | Press a single key |
| `hotkey` | `<combo>` | Press a key combo (e.g. `cmd,c`) |
| `scroll` | `<dy>` | Scroll vertically (negative = down) |
| `demo` | `[query]` | Full Amazon search demo (default: "iphone case") |

**Requirement:** Grant Accessibility permission in _System Settings > Privacy & Security > Accessibility_.

## Tests

```bash
# Direct activitymon tests (no Yansu needed)
cd wails-gui && go test ./tools/test-computer-use/...

# Host-bridge integration tests (requires running Yansu, started with
# YANSU_HOST_BRIDGE_TOKEN=<32 or more characters>; pass the same value)
HOST_BRIDGE_URL=http://127.0.0.1:<port> HOST_BRIDGE_TOKEN=<same value> go test ./tools/test-computer-use/... -v
```

The bridge port is logged by Yansu at startup: `[host-bridge] listening on 127.0.0.1:<port>`

Tests skip automatically when not on macOS, when Accessibility permission is absent, or (for bridge tests) when `HOST_BRIDGE_URL` is unset.

### Test Coverage

| Test | Requires `HOST_BRIDGE_URL` |
|---|---|
| `TestComputerUsePermissions` | No |
| `TestListRunningApps` | No |
| `TestListAppWindows` | No |
| `TestCollectAppElements` | No |
| `TestOpenURL` | No |
| `TestBridgeApps` | Yes |
| `TestBridgeWindows` | Yes |
| `TestBridgeSee` | Yes |
| `TestBridgeOpen` | Yes |
| `TestBridgeFullFlow` | Yes |

## Dependencies

- `wails-gui/internal/platform/activitymon` — macOS accessibility and input automation
- Standard library: `encoding/json`, `net/http`, `os`, `strings`, `time`
