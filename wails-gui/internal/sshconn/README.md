# sshconn

SSH connection management package for the Yansu desktop app (wails-gui). Handles dialing, port forwarding, interactive shells, and one-off commands against the remote servers that back SSH/WSL workspaces.

## Overview

When the app needs to reach a remote server, this package:

1. Dials the server and authenticates (key or password)
2. Keeps the connection alive via keepalive
3. Exposes `RunCommand`, `OpenShell`, and `ForwardRemotePort` on top of the live client
4. Tears everything down cleanly on disconnect (also killing any leftover forwarded processes)

There is no agent subprocess deployed to the remote host — the native agent loop runs entirely in-process on the user's machine. This package is only used for remote-workspace operations (shells, port forwarding, file management).

## Key Components

| File | Purpose |
|------|---------|
| `config.go` | `Config` struct and `ParseUserSSHConfig()` — reads `~/.ssh/config` to auto-discover hosts |
| `manager.go` | `Manager` — lifecycle manager for multiple concurrent SSH connections |
| `tunnel.go` | `Tunnel` — local port forwarding over an established SSH connection |
| `shell.go` | `Shell` — interactive PTY session for the built-in terminal |

## Architecture

```
Manager
  ├── dial()               # SSH auth (key or password + keyboard-interactive)
  ├── Tunnel               # optional local→remote port forward
  └── Shell                # PTY session (xterm-256color)
```

Each connection is tracked as a `connEntry` protected by its own mutex. The `Manager` holds a read-write mutex over all entries. Status changes are emitted through an `onStatus` callback, which the Wails app forwards to the frontend as events.

## Connection States

```
disconnected → connecting → connected
                          ↘ error
```

## Usage

```go
// Create a manager with a status callback.
mgr := sshconn.NewManager(func(status sshconn.ConnectionStatus) {
    // forward to frontend via Wails event
})

mgr.SetTunnelCallbacks(
    func(configID, localAddr string) { /* tunnel ready */ },
    func(configID string)            { /* disconnected */ },
)

// Save a config.
id, err := mgr.SaveConfig(sshconn.Config{
    Name:     "my-server",
    Host:     "192.168.1.10",
    Port:     22,
    User:     "ubuntu",
    AuthType: "key",
    KeyPath:  "/home/user/.ssh/id_ed25519",
})

// Connect.
err = mgr.Connect(mgr.GetConfig(id))

// Run a one-off command.
out, err := mgr.RunCommand(id, "uname -a")

// Open an interactive shell (for the built-in terminal).
shell, err := mgr.OpenShell(id, "/home/ubuntu", 220, 50)
defer shell.Close()
io.Copy(os.Stdout, shell) // read terminal output

// Forward a remote port locally.
localPort, cleanup, err := mgr.ForwardRemotePort(id, 8080)
defer cleanup()

// Disconnect.
mgr.Disconnect(id)

// Shutdown all connections on app exit.
mgr.Shutdown()
```

### Parsing `~/.ssh/config`

```go
hosts, err := sshconn.ParseUserSSHConfig()
// hosts is []sshconn.Config with Host, Port, User, KeyPath populated
```

## Config Fields

| Field | Description |
|-------|-------------|
| `ID` | Unique identifier (auto-generated if empty) |
| `Name` | Human-readable label (must be unique) |
| `Host` | Hostname or IP |
| `Port` | SSH port (default: 22) |
| `User` | SSH username |
| `AuthType` | `"key"` or `"password"` |
| `KeyPath` | Path to private key (optional; falls back to `~/.ssh/id_*`) |
| `Password` | Password (only for `auth_type: "password"`) |
| `AutoConnect` | Connect automatically on app startup |

## Dependencies

- `golang.org/x/crypto/ssh` — SSH protocol implementation
- `golang.org/x/crypto/ssh/knownhosts` — `~/.ssh/known_hosts` verification

## Host Key Verification

Unknown hosts are automatically accepted and appended to `~/.ssh/known_hosts`. A changed host key returns an error to prevent silent MITM attacks.
