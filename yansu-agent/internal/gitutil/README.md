# gitutil

Git utility package for managing `.gitignore` entries and checking Git file tracking status.

## Overview

This package provides helper functions to inspect and modify a project's `.gitignore` file, and to check whether files are tracked by Git. It is used by the agent to automatically protect sensitive or generated files from being committed.

## Key Components

| File | Description |
|------|-------------|
| `gitignore.go` | Core logic for `.gitignore` management and Git tracking checks |
| `procattr_windows.go` | Windows-specific: hides console windows spawned by `git` subprocesses |
| `procattr_other.go` | Non-Windows stub for the same `hideConsoleWindow` interface |

## API

### `IsTrackedByGit(projectRoot, relativePath string) bool`

Returns `true` if the given relative path is already tracked by Git (via `git ls-files`).

### `IsInGitIgnore(projectRoot, pattern string) bool`

Returns `true` if the given pattern already exists in `.gitignore` (normalized comparison, ignoring leading/trailing slashes).

### `EnsureGitIgnore(projectRoot, relativePath string) bool`

Adds `relativePath` to `.gitignore` only if it is **not** already tracked by Git and not already present in `.gitignore`. Returns `true` if the pattern was added.

### `AddToGitIgnore(projectRoot, pattern string) bool`

Appends `pattern` to `.gitignore`, creating the file if necessary. Handles newline normalization. Returns `true` on success.

### `GetAgentInstructionPatterns() []string`

Returns the list of agent instruction file patterns that should be gitignored when not already tracked:

```go
[]string{
    ".cursorrules",
    ".windsurfrules",
    "AGENTS.md",
}
```

> `CLAUDE.md` and `.github/copilot-instructions.md` are intentionally excluded because they are often tracked by users.

## Usage Example

```go
import "github.com/your-org/yansu-agent/internal/gitutil"

// Ensure a generated config file won't be committed
added := gitutil.EnsureGitIgnore("/path/to/project", ".env.local")

// Add all agent instruction patterns
for _, pattern := range gitutil.GetAgentInstructionPatterns() {
    gitutil.EnsureGitIgnore(projectRoot, pattern)
}
```

## Platform Notes

On Windows, `git` subprocesses are launched with `CREATE_NO_WINDOW` to prevent console flicker. On all other platforms this is a no-op.

## Dependencies

- Standard library only (`bufio`, `os`, `os/exec`, `path/filepath`, `strings`, `syscall` on Windows)
- Requires `git` to be available on `PATH` at runtime
