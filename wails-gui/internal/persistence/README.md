# persistence

Business logic layer between the Wails GUI application and the underlying SQLite storage. It translates between the frontend-facing data types (`ProjectData`, `WorkspaceData`, `SessionData`, `StreamItemData`, `SidebarGroupData`) and the raw storage row types in the `wails-gui/storage` package.

## Purpose

This package provides a clean API for loading and saving application state — projects, workspaces, sessions, conversation stream items, and sidebar groups — while keeping performance optimizations (batch queries, cursor pagination) out of the rest of the app.

## Key Components

| File | Description |
|---|---|
| `types.go` | Frontend-facing data types exchanged with the Wails frontend (JSON-serialisable structs) |
| `service.go` | `Service` — the single entry point for all persistence operations |
| `service_test.go` | Unit tests using a real SQLite store in a temp directory |

## Architecture

```
Wails App / Frontend
        │
        ▼
 persistence.Service      ← this package
        │  maps between ProjectData ↔ storage.ProjectRow, etc.
        ▼
   storage.Store          ← wails-gui/storage (SQLite, batch queries)
```

`Service` wraps a `*storage.Store` and:
- Performs the mapping between the two type systems.
- Orchestrates multi-entity batch loads to avoid N+1 queries.
- Exposes cursor-based pagination for conversation history.

If `store` is `nil` (e.g. in-memory-only mode) every method is a no-op and returns a zero value — no panics.

## Data Model

```
ProjectData
└── []WorkspaceData
    └── []SessionData
        └── []StreamItemData

SidebarGroupData
└── []SidebarGroupItemData  (references projects/workspaces by RefID + ItemType)
```

- **ProjectData** — a Claude Code project (name, path, org, active session/workspace pointers, sort order).
- **WorkspaceData** — a git-worktree-backed workspace within a project.
- **SessionData** — a Claude session with model/thinking/PRD settings.
- **StreamItemData** — a single conversation item (`user`, `assistant`, tool calls, …).
- **SSHConfigData** — SSH remote environment configuration (host, port, user, auth type, exec mode, CLI tool).
- **SidebarGroupData** — a user-defined sidebar group containing ordered item references.
- **SidebarGroupItemData** — a reference to a chat or automation within a group (`itemType` is `"chat"` or `"automation"`).

## Service API

### Loading

```go
// Load everything at startup (4 batch queries).
// Returns the last 20 conversation turns (user messages) per session;
// use LoadStreamItemsPage for older items.
projects, err := svc.LoadAllProjects(ctx)

// Cursor-based pagination for older conversation items.
// Pass cursor=-1 to get the most recent `limit` items.
page, err := svc.LoadStreamItemsPage(ctx, sessionLocalID, cursor, limit)

// Active project UI state.
id, err := svc.LoadActiveProjectID(ctx)

// Load all sidebar groups with their items.
groups, err := svc.LoadAllSidebarGroups(ctx)
```

### Saving

```go
// Full project upsert (workspaces + sessions, no stream items).
err := svc.SaveProjectState(ctx, projectData)

// Insert-only project structure (ON CONFLICT DO NOTHING) — satisfies
// foreign-key constraints before inserting stream items without
// overwriting fields updated concurrently.
err := svc.EnsureProjectStructure(ctx, projectData)

// Granular saves.
err := svc.SaveWorkspaceState(ctx, projectID, workspaceData)
err := svc.SaveSessionState(ctx, projectID, sessionData)

// Append a stream item; returns the assigned sort_order.
sortOrder, err := svc.PersistStreamItem(ctx, sessionLocalID, item)

// Update content of an existing stream item (e.g. streaming delta).
err := svc.UpdateStreamItemContent(ctx, itemID, content)

// Link a local session to its Claude session_id once known.
err := svc.UpdateSessionClaudeID(ctx, localID, claudeSessionID)
```

### Active-state helpers

```go
err := svc.SaveActiveProjectID(ctx, id)
err := svc.UpdateProjectActiveSession(ctx, projectID, sessionID)
err := svc.UpdateProjectActiveWorkspace(ctx, projectID, workspaceID)
err := svc.UpdateWorkspaceActiveSession(ctx, workspaceID, sessionID)
```

### Ordering

```go
// Reorder projects by providing the desired ID sequence.
err := svc.ReorderProjects(ctx, []string{"proj-b", "proj-a"})

// Reorder workspaces within a project.
err := svc.ReorderWorkspaces(ctx, projectID, []string{"ws-b", "ws-a"})

// Get the next available sort_order for a new project.
n, err := svc.NextProjectSortOrder(ctx)
```

### Deletion

```go
err := svc.DeleteProjectData(ctx, projectID)
err := svc.DeleteWorkspaceData(ctx, workspaceID)
err := svc.DeleteSessionData(ctx, sessionLocalID)

// Also removes sidebar group references for the deleted entity.
err := svc.DeleteProjectWithGroupCleanup(ctx, projectID)
err := svc.DeleteWorkspaceWithGroupCleanup(ctx, workspaceID)
```

### SSH Configurations

```go
err  := svc.SaveSSHConfig(ctx, sshConfigData)
confs, err := svc.LoadSSHConfigs(ctx)
err  := svc.DeleteSSHConfig(ctx, id)
```

### Sidebar Groups

```go
group, err := svc.CreateSidebarGroup(ctx, name)
err  := svc.RenameSidebarGroup(ctx, id, name)
err  := svc.DeleteSidebarGroup(ctx, id)
err  := svc.ReorderSidebarGroups(ctx, ids)
err  := svc.SetSidebarGroupCollapsed(ctx, id, collapsed)

item, err := svc.AddItemToSidebarGroup(ctx, groupID, itemType, refID)
err  := svc.RemoveItemFromSidebarGroup(ctx, groupID, itemID)
err  := svc.RemoveGroupItemsByRef(ctx, itemType, refID)
err  := svc.ReorderSidebarGroupItems(ctx, groupID, itemIDs)

// Remove stale group items whose referenced entities no longer exist.
err  := svc.CleanupOrphanGroupItems(ctx, validChatIDs, validAutomationIDs)
```

## Dependencies

- `wails-gui/storage` — SQLite-backed store (the only dependency).

## Usage

```go
import (
    "wails-gui/internal/persistence"
    "wails-gui/storage"
)

store, err := storage.New("/path/to/sessions.db")
if err != nil { ... }
defer store.Close()

svc := persistence.NewService(store)

// On startup — load everything.
projects, err := svc.LoadAllProjects(ctx)

// Lazily load older conversation history.
page, err := svc.LoadStreamItemsPage(ctx, session.LocalID, -1, 50)
for page.HasMore {
    page, err = svc.LoadStreamItemsPage(ctx, session.LocalID, page.NextCursor, 50)
}
```

## Testing

Tests use a real SQLite database in a temp directory — no mocks.

```bash
cd wails-gui
go test ./internal/persistence/...
```

### Test coverage

| Test | What it verifies |
|---|---|
| `TestServiceLoadAllProjectsIncludesNestedData` | Full round-trip: save project → append stream item → load, check nesting |
| `TestServiceLoadStreamItemsPageLatestAndBefore` | Cursor pagination: latest page, `hasMore`, `nextCursor`, older page |
| `TestServiceReorderProjectsAndNextSortOrder` | Sort-order assignment, `NextProjectSortOrder`, `ReorderProjects` |
