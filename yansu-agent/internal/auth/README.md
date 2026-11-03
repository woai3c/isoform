# internal/auth

Handles CLI authentication for the Yansu Agent, including login, logout, and status checks.

## Overview

This package implements the authentication flow that allows users to authenticate the `yansu` CLI tool with the Yansu platform. It supports two login methods and persists the token via the `config` package.

## Key Components

| File | Description |
|------|-------------|
| `auth.go` | Core authentication logic: `Login`, `Logout`, `Status`, and browser launching |
| `procattr_windows.go` | Windows-specific: hides the console window when launching the browser (`CREATE_NO_WINDOW`) |
| `procattr_other.go` | No-op stub for non-Windows platforms |

## Authentication Flow

### Login (`Login()`)

1. Loads existing config; falls back to defaults if not found.
2. If a token already exists and is valid, prompts the user to re-authenticate.
3. Presents two authentication methods:
   - **Browser login** (default) — starts a temporary local HTTP server on a random port, opens the browser to `/cli-auth?callback=http://localhost:<port>/callback`, and waits up to 5 minutes for the token to be delivered via the callback URL.
   - **Token paste** — prompts the user to manually copy the `something-session` cookie from their browser's DevTools.
4. Verifies the received token against the API, then persists it via `config.SaveToken`.

### Logout (`Logout()`)

Clears the saved token via `config.ClearToken`. Silent no-op if not logged in.

### Status (`Status()`)

Loads the config, verifies the token against the API, and prints the current authentication state plus the configured Web/API URLs.

## Platform Support

Browser launching is handled differently per OS:

| OS | Command |
|----|---------|
| macOS | `open <url>` |
| Linux | `xdg-open`, then `google-chrome`, then `firefox` |
| Windows | `cmd /c start <url>` (console window hidden) |

## Dependencies

- `yansu-agent/internal/client` — API client for token verification (`client.Verify`)
- `yansu-agent/internal/config` — Config loading/saving (`config.Load`, `config.SaveToken`, `config.ClearToken`, `config.LoadToken`)

## Usage

These functions are invoked by the CLI command layer (e.g., `cmd/`):

```go
// Authenticate
if err := auth.Login(); err != nil {
    log.Fatal(err)
}

// Check status
if err := auth.Status(); err != nil {
    log.Fatal(err)
}

// Logout
if err := auth.Logout(); err != nil {
    log.Fatal(err)
}
```

## Configuration

The package reads from and writes to the config managed by `internal/config`. Relevant fields:

- `Token` — the session token used for API authentication
- `BaseURL` / `WebBaseURL` — used to construct the browser auth URL
- `APIBaseURL` — used to verify the token

Default URLs fall back to `config.DefaultBaseURL` when no config file exists.
