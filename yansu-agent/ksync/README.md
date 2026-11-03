# ksync

Public API wrapper for knowledge and skill synchronization in the yansu-agent project.

## Overview

`ksync` provides a clean, external-facing interface for knowledge and skill sync operations. It wraps the internal `sync`, `client`, `config`, and `knowledge` packages so that external consumers (e.g. the Wails GUI) can use sync functionality without importing internal packages directly.

## Files

| File | Description |
|------|-------------|
| `syncer.go` | Core `KSyncer` type with all sync methods and constructors |
| `types.go` | Public result and status types returned by sync operations |

## Types

### Result Types

All change-tracking types follow a consistent pattern:

```go
type PushResult struct {
    Created []string
    Updated []string
    Deleted []string
    Errors  []error
}

type PullResult struct { ... }       // Same shape as PushResult
type PullSkillsResult struct { ... } // Same shape as PushResult
```

### Aggregate Types

```go
type SyncResult struct {
    Pushed       *PushResult
    Pulled       *PullResult
    SkillsPulled *PullSkillsResult
}

type SyncStatus struct {
    LastSyncedAt          time.Time
    LocalKnowledgeCount   int
    PendingNewKnowledge   int
}
```

## Usage

### Constructing a KSyncer

**From explicit credentials:**

```go
syncer := ksync.NewKSyncer(baseURL, token, org, product, projectName, projectRoot)
```

**Auto-loading from disk** (reads `~/.yansu-agent/` and `.something/project.json`):

```go
syncer, err := ksync.NewKSyncerFromProject(projectRoot)
if err != nil {
    // handle config loading error
}
```

### Sync Operations

```go
ctx := context.Background()

// Push local new-knowledge to cloud
result, err := syncer.Push(ctx)

// Pull remote knowledge to local
result, err := syncer.Pull(ctx)

// Sync skills to all agent directories
result, err := syncer.PullSkills(ctx)

// Sync project context (PRD, scenarios, decisions)
err := syncer.SyncContext(ctx)

// Full bidirectional sync (Push + Pull + PullSkills)
result, err := syncer.SyncAll(ctx)
```

### Status and Setup

```go
// Get current sync status
status, err := syncer.GetSyncStatus()

// Create .something subdirectories
err := syncer.EnsureDirectories()

// Regenerate the knowledge index file
err := syncer.UpdateIndex()
```

## Configuration

`NewKSyncerFromProject` loads configuration from two sources:

| Source | Path | Contents |
|--------|------|----------|
| Global config | `~/.yansu-agent/config.json` | `BaseURL`, org credentials |
| Global token | `~/.yansu-agent/token` | API authentication token |
| Project config | `.something/project.json` | `ProjectName`, optional `YansuURL`, `ProductUUID` |

URL resolution order: project-specific `YansuURL` → global `BaseURL`.

## Architecture

`ksync` is a thin facade and adapter layer. Each method delegates to the internal `sync.Syncer` and adapts its result types to the public types defined in `types.go`.

```
ksync (public API)
  └── internal/sync.Syncer (core engine)
        ├── internal/client   (HTTP API)
        ├── internal/knowledge (file I/O)
        └── internal/skills   (skill extraction)
```

The package exists purely as a boundary — it does not contain business logic. This lets external packages like `wails-gui` depend on a stable public API while the internal implementation evolves freely.

## Fitting Into the Larger Project

```
yansu-agent/
├── ksync/          ← this package (public API)
├── internal/
│   ├── sync/       ← core bidirectional sync engine
│   ├── client/     ← Yansu API HTTP client
│   ├── knowledge/  ← markdown file parsing & indexing
│   └── skills/     ← multi-agent skill distribution
└── cmd/yansu/      ← CLI entry point
```

External consumers (`wails-gui`) import only `ksync`, keeping them isolated from internal package churn.
