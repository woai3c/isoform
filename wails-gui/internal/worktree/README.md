# worktree

Git worktree lifecycle management for Yansu Agent workspaces.

## Overview

The `worktree` package creates and manages isolated git worktrees as "workspaces". Each workspace is a named git branch checked out into its own directory under `~/.yansu-agent/worktrees/<project>/<branch-suffix>/`. Workspace metadata is persisted to a SQLite store so workspaces survive application restarts.

## Key Components

| File | Responsibility |
|---|---|
| `service.go` | `Service` struct — CRUD operations for workspaces (create, list, rename, merge, remove) |
| `diff.go` | File-change detection and diff generation against a workspace's base branch |
| `helpers.go` | Low-level utilities: `RunGit`, branch/directory naming, safe path removal, timeout configuration |
| `helpers_test.go` | Unit tests for timeout logic, uniqueness helpers, and path-safety guards |

## Architecture

```
Service
  ├── storage.Store            (SQLite – persists WorkspaceRow records)
  ├── isGitRepo()              (injected – detects valid git repos)
  ├── worktreeBaseDir()        (injected – resolves ~/.yansu-agent/worktrees)
  ├── defaultWorktreeBaseDir() (injected – fallback root for safe-removal checks)
  └── branchPrefix()           (injected – default: "workspace")
```

Dependencies are injected via the `Dependencies` struct so callers can override behaviour in tests without touching the filesystem or the real git index.

## Public API

### Service

```go
svc := worktree.NewService(store, worktree.Dependencies{})

// Create a new isolated workspace
info, err := svc.CreateWorkspace(projectPath, projectID, workspaceID, label, baseBranch)

// List all workspaces for a project
workspaces, err := svc.ListWorkspaces(projectID)

// Rename a workspace (branch + directory + DB label)
result, err := svc.RenameWorkspaceBranch(projectPath, workspaceID, newLabel)

// Merge workspace branch into main worktree and clean up
err = svc.MergeWorkspace(projectPath, workspaceID, worktreePath, branch)

// Remove a workspace (worktree, branch, DB record)
err = svc.RemoveWorkspace(projectPath, workspaceID)
```

### Diff & staging (package-level functions)

```go
// List changed files (staged, unstaged, committed on branch)
files, err := worktree.GetChangedFiles(worktreePath, baseBranch)

// Full unified diff for a workspace
diff, err := worktree.GetDiff(worktreePath, baseBranch)

// Unified diff for a single file (handles untracked files via --no-index)
diff, err := worktree.GetFileDiff(worktreePath, baseBranch, filePath)

// Stage / unstage / discard files
worktree.StageFiles(repoPath, []string{"file.go"})
worktree.UnstageFiles(repoPath, []string{"file.go"})
worktree.DiscardFiles(repoPath, tracked, untracked)

// Commit staged changes
worktree.Commit(repoPath, "message")
```

### Data types

```go
type WorkspaceInfo struct {
    WorkspaceID string `json:"workspaceId"`
    Label       string `json:"label"`
    Branch      string `json:"branch"`
    BaseBranch  string `json:"baseBranch"`
    Path        string `json:"path"`
    CreatedAt   int64  `json:"createdAt"`
}

// Returned by RenameWorkspaceBranch.
type RenameResult struct {
    Branch string `json:"branch"`
    Path   string `json:"path"`
}

type ChangedFile struct {
    Path   string `json:"path"`
    Status string `json:"status"` // "added" | "modified" | "deleted" | "renamed" | "untracked"
    Staged bool   `json:"staged"`
}
```

## Configuration

| Environment variable | Default | Description |
|---|---|---|
| `YANSU_GIT_TIMEOUT_MS` | — | Override git command timeout in milliseconds |

Default timeouts (when the env var is unset):

- Heavy commands (`merge`, `worktree`, `fetch`, `pull`, `checkout`, `commit`): **5 minutes**
- All other commands: **90 seconds**

Worktrees are stored under `~/.yansu-agent/worktrees/<sanitized-project-name>/<sanitized-branch-suffix>/`.

## Dependencies

- **Go standard library** (`os`, `os/exec`, `path/filepath`, `context`, …)
- **`wails-gui/storage`** — SQLite store for persisting workspace rows
- **`git` CLI** — must be available on `$PATH`
- **`claude` CLI** _(optional)_ — used by `TranslateToEnglish` to convert non-ASCII workspace labels into valid git branch names; falls back to the original text if unavailable

## Safety Guarantees

`RemovePathSafe` and `CanSafelyRemovePath` guard every directory deletion: a path is only removed when it is **strictly inside** one of the managed worktree roots. This prevents accidental deletion of arbitrary directories if a worktree path is misconfigured.

## Testing

Tests use real git repositories initialised in `t.TempDir()` — no mocks for git operations.

```bash
cd wails-gui
go test ./internal/worktree/...
```

Key test scenarios:

- `TestGitCommandTimeout` — timeout selection by git subcommand
- `TestUniqueBranchName*` — branch deduplication with `-2`, `-3`, … suffixes
- `TestUniqueDirPath*` — directory deduplication
- `TestCanSafelyRemovePath` — path-safety boundaries
