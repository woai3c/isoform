# guard

Package `guard` provides the safety layer for the native agent loop's tool execution. It enforces path validation, per-file concurrency control, and Bash command risk classification before any tool interacts with the filesystem or shell.

## Purpose

Every tool in the agent loop (Read, Write, Edit, Glob, Bash) passes through `guard` before executing. The package answers three questions:

1. **Is this path safe to access?** — ensures absolute paths and workspace confinement
2. **Is this file already being written?** — serializes concurrent edits to the same file
3. **Is this shell command dangerous?** — hard-blocks catastrophic commands and flags risky ones for user confirmation

## Key Components

### `path.go` — Path Validation

**`ValidateAbsolutePath(path string) (string, error)`**

Rejects empty strings, relative paths, and any path containing `..` components after cleaning. Returns the `filepath.Clean`-ed absolute path on success.

**`WithinAllowed(path string, allowedRoots []string) error`**

Checks that `path` falls inside one of the declared workspace roots. An empty `allowedRoots` slice disables workspace scoping (useful for read-only probes). Non-absolute roots are a hard programming error.

```go
cleaned, err := guard.ValidateAbsolutePath(req.Path)
if err != nil {
    return err
}
if err := guard.WithinAllowed(cleaned, workspaceRoots); err != nil {
    return err
}
```

### `filelock.go` — Per-Path Mutex Manager

**`FileLockManager`** issues one `sync.Mutex` per canonical file path, so parallel Write or Edit calls targeting the same file serialize naturally. The manager is LRU-capped (default 1024 entries) to prevent unbounded memory growth in long sessions.

**`NewFileLockManager(cap int) *FileLockManager`** — `cap ≤ 0` uses the default of 1024.

**`(m *FileLockManager) Lock(path string) *sync.Mutex`** — returns (or creates) the mutex for the given path and promotes it to the front of the LRU.

```go
flm := guard.NewFileLockManager(0) // default cap

mu := flm.Lock(targetPath)
mu.Lock()
defer mu.Unlock()
// ... safe to write to targetPath ...
```

### `cmdblacklist.go` — Command Risk Classification

**`ClassifyCommand(command string) CommandClassification`** tokenizes a shell command string and returns one of three risk tiers:

| Tier | Constant | Meaning |
|---|---|---|
| 0 | `RiskSafe` | Execute without user prompt |
| 1 | `RiskNeedsPermission` | Pause and ask the user |
| 2 | `RiskBlocked` | Refuse immediately; return an error |

**Hard-blocked patterns** (regex-matched, `RiskBlocked`):

- `rm -rf /`, `rm -rf ~`, `rm -rf $HOME` and variants
- `mkfs.*`, `dd of=/dev/*`, `shred /dev/*`
- Fork bomb `:(){ :|:& };:`
- `curl … | sh`, `wget … | bash`
- `sudo rm -rf /`

**Permission-required prefixes** (token-matched, `RiskNeedsPermission`):

`rm`, `git push`, `git reset --hard`, `git clean`, `git checkout --`, `git branch -D`, `npm publish`, `pip install`, `ssh`, `scp`, `sudo`

**`ScrubSensitiveEnv(env []string) []string`**

Strips any environment variable whose name ends with `_TOKEN`, `_KEY`, `_SECRET`, `_PASSWORD`, or `_PASSWD` before the Bash tool forks a subprocess, preventing credential leakage.

```go
result := guard.ClassifyCommand(cmd)
switch result.Risk {
case guard.RiskBlocked:
    return fmt.Errorf("command blocked: %s", result.Reason)
case guard.RiskNeedsPermission:
    // pause and ask user, surface result.Prefix for "always allow" option
}

safeEnv := guard.ScrubSensitiveEnv(os.Environ())
```

## Architecture

All three concerns are stateless pure functions except `FileLockManager`, which holds shared mutex state and must be constructed once and shared across the tool layer (not recreated per request).

```
Tool call (Read / Write / Edit / Glob / Bash)
         │
         ▼
   guard.ValidateAbsolutePath      ← reject non-absolute / traversal paths
         │
         ▼
   guard.WithinAllowed             ← reject paths outside workspace roots
         │
         ├── (Write / Edit only)
         │       │
         │       ▼
         │   FileLockManager.Lock  ← serialize concurrent writes
         │
         └── (Bash only)
                 │
                 ▼
             ClassifyCommand       ← hard-block or request user confirmation
             ScrubSensitiveEnv     ← strip credentials from subprocess env
```

## Dependencies

Standard library only — no external packages.

| Import | Used by |
|---|---|
| `fmt`, `path/filepath`, `strings` | path.go, cmdblacklist.go |
| `container/list`, `sync` | filelock.go |
| `regexp` | cmdblacklist.go |

## Testing

Tests live in `guard_test.go` and cover all public surfaces:

```bash
go test ./yansu-agent/agent/tools/guard/...
```

Test categories:

- `TestValidateAbsolutePath` — good and bad path inputs
- `TestWithinAllowed` — prefix matching, empty roots, non-absolute root error
- `TestFileLockManager_*` — same-path identity, different-path isolation, LRU cap eviction, concurrent serialization (50 goroutines)
- `TestClassifyCommand_HardBlock` — 13 catastrophic patterns
- `TestClassifyCommand_NeedsPermission` — 10 risky-but-allowed prefixes
- `TestClassifyCommand_Safe` — 10 ordinary commands including env-var prefixed and `cd &&` preludes
- `TestScrubSensitiveEnv` — strips keys/tokens/secrets, keeps PATH/HOME/LANG
