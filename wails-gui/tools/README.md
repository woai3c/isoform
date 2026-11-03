# wails-gui/tools

Developer tools for testing and debugging the Wails GUI desktop application.

## Contents

### `test-computer-use/`

A macOS-only CLI tool and E2E test suite for exercising the `activitymon` computer-use API.

**Build constraint:** `//go:build darwin` — only compiles and runs on macOS.

## test-computer-use

### Purpose

Provides two things:

1. **`main.go`** — an interactive CLI for manually exercising the `activitymon` package (accessibility tree inspection, mouse/keyboard automation, URL opening).
2. **`e2e_test.go`** — Go test suite covering both the direct `activitymon` API and the host-bridge HTTP endpoints exposed by Yansu at runtime.

### CLI Usage

```bash
# Build and run from wails-gui root
go run ./tools/test-computer-use <command> [args]
```

| Command | Description |
|---|---|
| `apps` | List all running macOS applications |
| `windows <app>` | List windows for a named app |
| `see <app>` | Dump the accessibility element tree for an app |
| `open <url>` | Open a URL in the default browser |
| `click <x> <y>` | Click at screen coordinates |
| `type <text>` | Type text via keyboard automation |
| `press <key>` | Press a single key |
| `hotkey <combo>` | Press a key combination (e.g. `cmd,c`) |
| `scroll <dy>` | Scroll vertically (negative = down) |
| `demo [query]` | Full Amazon search demo (defaults to "iphone case") |

**Requirements:** macOS Accessibility permission must be granted in _System Settings > Privacy & Security > Accessibility_.

### E2E Tests

```bash
# Run direct activitymon tests (no Yansu needed)
cd wails-gui && go test ./tools/test-computer-use/...

# Run host-bridge integration tests (requires running Yansu, started with
# YANSU_HOST_BRIDGE_TOKEN=<32 or more characters>; pass the same value)
HOST_BRIDGE_URL=http://127.0.0.1:<port> HOST_BRIDGE_TOKEN=<same value> go test ./tools/test-computer-use/... -v
```

The bridge port is logged by Yansu at startup: `[host-bridge] listening on 127.0.0.1:<port>`.

Tests skip automatically when:
- Not running on macOS
- Accessibility permission is not granted
- `HOST_BRIDGE_URL` is unset (for bridge-specific tests)

#### Test Coverage

| Test | Requires bridge |
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

- `wails-gui/internal/platform/activitymon` — macOS accessibility and input automation package
- Standard library: `encoding/json`, `net/http`, `os`, `strings`, `time`
