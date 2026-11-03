# Internal Package

This directory contains the core implementation of Yansu Agent. All modules follow Go's internal package convention - they are private to the `yansu-agent` project and cannot be imported by external projects.

## Overview

The internal package implements a local development companion that synchronizes project knowledge and context with the Yansu cloud platform. It enables AI coding assistants to access project-specific patterns, decisions, and requirements through a filesystem-based interface.

## Architecture

The internal package is organized into specialized modules following clean architecture principles:

```
internal/
├── auth/           # Authentication and token management
├── client/         # HTTP API client for Yansu backend
├── config/         # Configuration management (global and project)
├── daemon/         # Slack relay daemon with event routing
├── gitutil/        # Git utilities for auto-gitignore functionality
├── knowledge/      # Knowledge file parsing and indexing
├── skills/         # AI agent skills synchronization
├── sync/           # Synchronization engine for knowledge and skills
└── updater/        # Self-update mechanism
```

## Modules

### auth - Authentication Module

**Location:** `internal/auth/auth.go`

Handles user authentication with the Yansu platform through browser-based OAuth or manual token entry.

**Key Functions:**
- `Login()` - Interactive authentication flow (browser or manual token)
- `Logout()` - Clear stored credentials
- `Status()` - Check authentication status

**Authentication Flow:**
1. Start local HTTP server on `127.0.0.1` (random port)
2. Open browser to Yansu auth URL with callback parameter
3. Wait for callback with authentication token
4. Verify token with API
5. Store token securely in `~/.yansu-agent/token` (0600 permissions)

**Security:**
- Token stored separately from config with restrictive permissions
- Callback server only binds to localhost
- 5-minute authentication timeout
- Token verification before storage

**Authentication Success Message:**
After successful authentication, users see available commands:
- `yansu clone <org/product/project>` - Clone a project
- `yansu daemon` - Start the sync daemon (if daemon mode is supported)
- `yansu service install` - Install as system service (if service mode is supported)

### client - API Client Module

**Location:** `internal/client/api.go`

HTTP client wrapper for the Yansu backend API. Provides type-safe methods for all API operations.

**Key Types:**
```go
type Client struct {
    baseURL      string
    token        string
    organization string
    product      string
    project      string
    productUUID  string
    httpClient   *http.Client
}

type KnowledgeResponse struct {
    UUID      string
    Trigger   string
    Content   string
    Tags      []string
    Path      *string   // Optional file path for organization
    Scope     string    // "product" or "project" scope
    UpdatedAt time.Time
    CreatedAt time.Time
}
```

**API Operations:**

**Project Management:**
- `GetProject(ctx, org, product, project)` - Fetch project metadata
- `GetProjectCodeRepositories(ctx, org, product, project)` - Get GitHub URLs
- `GetProjectContext(ctx)` - Fetch project requirements and decisions

**Knowledge Management:**
- `ListKnowledge(ctx)` - Get all knowledge items for product
- `CreateKnowledge(ctx, req)` - Create new knowledge item
- `UpdateKnowledge(ctx, uuid, req)` - Update existing knowledge
- `DeleteKnowledge(ctx, uuid)` - Delete knowledge item
- `ExtractKnowledge(ctx, req)` - AI-powered knowledge extraction from diffs

**Skills Management:**
- `ListSkills(ctx)` - Get all skills for product

**Authentication:**
- `Verify(ctx)` - Validate authentication token

**Features:**
- 30-second request timeout
- Bearer token authentication
- Hierarchical API endpoints (org/product/project)
- JSON request/response handling
- Detailed error messages

### daemon - Slack Relay Daemon Module

**Location:** `internal/daemon/`

Long-running relay daemon that connects to the Yansu backend via WebSocket and executes synchronization and AI tasks in response to Slack messages.

**Key Types:**
```go
type Event struct {
    UserID    string // Platform user ID
    ChannelID string // Slack channel
    ThreadTS  string // Thread timestamp (Slack)
    Text      string // Message text
    EventTS   string // Event timestamp
    Platform  string // "slack"
    RelayID   string // Relay connection ID
}
```

**Architecture:**

```
Slack Message
    │
    ▼
RelayAdapter.Listen()   ← WebSocket connection to Yansu backend
    │
    ├─ Serial commands (sync, push, pull, status, …)
    │     └── per-user session queue (one task at a time)
    │
    └─ AI tasks (analyze, free-form requests)
          └── concurrent execution (bypass queue)
```

**Key Components:**
- `daemon.go` - Entry point; wires up the relay adapter, router, registry, and session manager
- `relay.go` (`RelayAdapter`) - WebSocket client connecting to the Yansu backend relay endpoint; handles auto-reconnect with exponential backoff (2 s → 5 min)
- `router.go` (`Router`) - Parses incoming events and dispatches to the correct handler; classifies events as serial commands or concurrent AI tasks
- `session.go` (`SessionManager`) - Per-user per-platform serial queue (capacity 5) and AI task slot semaphore (max 3 concurrent)
- `registry.go` (`ProjectRegistry`) - Tracks registered projects, persisted to `~/.yansu-agent/registry.json`
- `adapter.go` - Defines the `Adapter` interface for sending replies
- `event.go` - Event type definitions
- `service.go` - Service-level helpers

**Execution Flow:**
1. `Run()` auto-discovers Slack identity via `GetMySlackBinding()` — no manual setup needed
2. Writes PID to `~/.yansu-agent/daemon.pid` for process management
3. Loads the project registry from disk
4. Opens a WebSocket relay connection to the Yansu backend
5. Dispatches incoming events to the router
6. Gracefully shuts down on `SIGINT`/`SIGTERM`

**Reconnection Logic:**
- Exponential backoff on disconnect (2 s initial, capped at 5 min)
- Dormant mode with 30 s backoff when another daemon replica takes over

**Files:**
- `daemon.go` - Entry point and signal handling
- `relay.go` - WebSocket relay adapter
- `router.go` - Event routing and command dispatch
- `session.go` - Per-user serial session queue
- `registry.go` - Registered project persistence
- `adapter.go` - Adapter interface definition
- `event.go` - Event type definitions
- `service.go` - Service-level helpers

---

### config - Configuration Module

**Location:** `internal/config/config.go`

Manages global agent configuration and project-specific settings.

**Configuration Files:**

**Global Config** (`~/.yansu-agent/config.json`):
```json
{
  "base_url": "https://dashboard.yansu.ai",
  "sync_interval_minutes": 5
}
```

**Project Config** (`.something/project.json`):
```json
{
  "uuid": "proj_xxx",
  "name": "project-name",
  "organization": "org-name",
  "product": "product-name",
  "product_uuid": "prod_xxx",
  "yansu_url": "https://dashboard.yansu.ai/...",
  "created_at": "2024-01-01T00:00:00Z",
  "last_synced_at": "2024-01-02T10:30:00Z",
  "knowledge_count": 42
}
```

**Key Functions:**
- `Load()` - Load global configuration with priority: env vars > file > defaults
- `Save(cfg)` - Save global configuration
- `LoadToken()` - Load authentication token from separate file
- `SaveToken(token)` - Store token with 0600 permissions
- `LoadProject(somethingDir)` - Load project configuration
- `SaveProject(somethingDir, cfg)` - Save project configuration
- `FindProjectRoot()` - Locate project root by searching for `.something/`

**Environment Variables:**

| Variable | Description |
|----------|-------------|
| `YANSU_URL` | Override base URL (web + API derived automatically) |
| `SLACK_BOT_TOKEN` | Slack Bot token for daemon (overrides `daemon.json`) |
| `SLACK_APP_TOKEN` | Slack App token for Socket Mode (overrides `daemon.json`) |
| `YANSU_DAEMON_EXECUTOR` | Daemon executor: `claude` (default) or `codex` |

### sync - Synchronization Module

**Location:** `internal/sync/`

Handles bidirectional synchronization between local files and cloud.

**Key Methods:**

**`SyncAll(ctx)`** - Full bidirectional sync:
1. Push local changes to cloud
2. Pull remote changes to local
3. Update last sync timestamp

**`Push(ctx)`** - Push local changes:
1. Scan `.something/new-knowledge/*.md`
2. Parse each file for UUID, trigger, content, and action
3. Execute action (create/update/delete) via API
4. Move file to `.something/knowledge/` on success
5. Update knowledge index

**`Pull(ctx)`** - Pull remote changes:
1. Fetch remote knowledge list from API
2. Compare with local knowledge (by UUID)
3. Download new/updated items
4. Delete locally removed items
5. Update knowledge index if changes occurred

**`PushSingleKnowledge(ctx, filePath)`** - Push individual file

**`SyncContext(ctx)`** - Sync project context (PRD, scenarios, decisions)

**Sync Results:**
```go
type SyncResult struct {
    Pushed       PushResult
    Pulled       PullResult
    SkillsPulled PullSkillsResult
}

type PushResult struct {
    Created []string  // Triggers of created knowledge
    Updated []string  // Triggers of updated knowledge
    Deleted []string  // Triggers of deleted knowledge
    Errors  []string  // Error messages
}

type PullSkillsResult struct {
    Created []string  // Skill names created
    Updated []string  // Skill names updated
    Deleted []string  // Skill names deleted
    Errors  []string  // Error messages
}
```

### knowledge - Knowledge Management Module

**Location:** `internal/knowledge/`

Handles parsing, writing, and indexing of knowledge markdown files.

#### parser.go - File Parser

**Knowledge File Format:**
```markdown
# UUID
know_xxx

# Action
create

# Trigger
error handling pattern

# Tags
backend, api, error-handling

# Path
internal/handlers

# Content
All API errors should return unified format with:
- status_code
- error_message
- error_code
```

**Key Functions:**
- `ParseFile(filePath)` - Parse knowledge file into struct
- `LoadAll(dir)` - Load all knowledge files from directory
- `WriteKnowledgeFile(dir, uuid, trigger, content)` - Write knowledge file
- `FindKnowledgeFileByUUID(dir, uuid)` - Locate file by UUID
- `DeleteKnowledgeFile(dir, uuid)` - Remove knowledge file
- `TriggerToFileName(trigger)` - Convert trigger to valid filename

**Actions:**
- `create` - Create new knowledge item
- `update` - Update existing knowledge (requires UUID)
- `delete` - Delete knowledge (requires UUID)

**Filename Generation:**
- Converts trigger to lowercase
- Replaces non-alphanumeric characters with hyphens
- Limits length to 50 characters
- Adds numeric suffix if duplicate

#### index.go - Index Generation

**Key Functions:**
- `UpdateIndex(somethingDir)` - Generate `_index.md` in knowledge directory
- `GenerateContextFile(somethingDir, prd, summary, requirements, scenarios, decisions)` - Create `context.md`, `prd.md`, and `scenarios/*.md`
- `EnsureDirectories(somethingDir)` - Create required subdirectories

**Index Format:**
```markdown
# Knowledge Index

Quick lookup for available knowledge. See individual files for full content.

## internal/handlers

| Trigger | Tags | File |
|---------|------|------|
| auth pattern | backend, security | [auth-pattern.md](auth-pattern.md) |
| error handling | backend, api | [error-handling.md](error-handling.md) |

## (uncategorized)

| Trigger | Tags | File |
|---------|------|------|
| git workflow | tools | [git-workflow.md](git-workflow.md) |

---

**Total: 3 knowledge items**
```

**Context Structure:**
- `context.md` - Main context file with references and summaries
- `prd.md` - Full Product Requirements Document (if available)
- `scenarios/` - Directory containing individual scenario files (if available)
  - Each scenario in format: `01-scenario-name.md`, `02-another-scenario.md`, etc.

### skills - AI Agent Skills Module

**Location:** `internal/skills/`

Synchronizes AI agent skills from the Yansu cloud to multiple AI coding assistant directories.

#### agents.go - Agent Path Configuration

Defines supported AI coding assistants and their skills directory paths.

**Supported Agents (14 total):**
- OpenCode → `.opencode/skill`
- Claude Code → `.claude/skills`
- Codex → `.codex/skills`
- Cursor → `.cursor/skills`
- Amp → `.agents/skills`
- Kilo Code → `.kilocode/skills`
- Roo Code → `.roo/skills`
- Goose → `.goose/skills`
- Gemini CLI → `.gemini/skills`
- Antigravity → `.agent/skills`
- GitHub Copilot → `.github/skills`
- Clawdbot → `skills`
- Droid → `.factory/skills`
- Windsurf → `.windsurf/skills`

**Type:**
```go
type AgentPath struct {
    Name string // Agent name (e.g., "Claude Code")
    Path string // Skills directory path (relative to project root)
}
```

#### extractor.go - Skills Extraction

Handles decoding and extracting skills from cloud storage to local directories.

**Key Functions:**
- `ExtractToAgentPaths(content, skillName, projectRoot)` - Extract skill to all agent paths
- `CleanSkillFromAgentPaths(skillName, projectRoot)` - Remove skill from all agent paths
- `extractTarGz(data, targetDir)` - Extract gzip-compressed tar archive

**Extraction Flow:**
1. Base64 decode skill content
2. Decompress gzip data
3. Extract tar archive
4. Write files to each agent's skills directory
5. Apply sanitized permissions (no setuid/setgid/sticky bits)

**Security Features:**
- Path traversal attack prevention
- Symlink skipping
- File size limits (10MB per file)
- Permission sanitization
- Target directory validation

#### state.go - Skills State Management

Tracks local synchronization state for skills.

**Types:**
```go
type SkillState struct {
    UUID      string
    Name      string
    UpdatedAt time.Time
}

type SkillsState struct {
    Skills map[string]*SkillState // UUID -> SkillState
}
```

**State File:** `.something/skills-state.json`

**Key Functions:**
- `LoadSkillsState(somethingDir)` - Load skills state from disk
- `SaveSkillsState(somethingDir, state)` - Save skills state to disk
- `UpdateFromRemote(uuid, name, updatedAt)` - Update state from remote
- `RemoveSkill(uuid)` - Remove skill from state
- `GetSkill(uuid)` - Get skill state by UUID

**Purpose:**
The state file tracks which skills are synced and their last update time, enabling efficient incremental synchronization (only sync changed skills).

### gitutil - Git Utilities Module

**Location:** `internal/gitutil/gitignore.go`

Provides utilities for automatically managing `.gitignore` entries for synced directories.

**Key Functions:**
- `IsTrackedByGit(projectRoot, relativePath)` - Check if a path is tracked by Git
- `IsInGitIgnore(projectRoot, pattern)` - Check if a pattern exists in .gitignore
- `EnsureGitIgnore(projectRoot, relativePath)` - Add pattern to .gitignore if path is untracked
- `AddToGitIgnore(projectRoot, pattern)` - Append pattern to .gitignore
- `GetAgentInstructionPatterns()` - Returns patterns for agent instruction files to ignore

**Purpose:**
When skills are synced to agent directories (e.g., `.claude/skills/`), this module automatically adds untracked directories to `.gitignore` to prevent accidental commits of synced content. It ensures that:
1. Only untracked paths are added (tracked files remain tracked)
2. Patterns aren't duplicated in .gitignore
3. Proper file formatting is maintained

**Auto-ignored Patterns:**
- `.cursorrules`
- `.windsurfrules`
- `AGENTS.md`

**Not Auto-ignored:**
- `CLAUDE.md` - Often intentionally tracked by users
- `.github/copilot-instructions.md` - .github directory may have other tracked files

### updater - Self-Update Module

**Location:** `internal/updater/updater.go`

Implements self-update mechanism using GitHub Releases.

**Key Functions:**
- `CheckLatestVersion(ctx)` - Get latest version from GitHub
- `Update(ctx, currentVersion, force)` - Download and apply update

**Update Flow:**
1. Fetch latest release from GitHub API
2. Compare versions (semantic versioning)
3. Find platform-specific binary (e.g., `yansu-darwin-arm64`)
4. Download binary to temp file
5. Make executable (chmod 0755)
6. Replace current executable
7. Remove temp file

**Features:**
- Platform detection (GOOS/GOARCH)
- Version comparison (semantic versioning)
- Binary replacement (atomic on Unix, two-step on Windows)
- 5-minute download timeout

**Release URL:**
```
https://github.com/Isoform/yansu-releases/releases/latest
```

## Data Flow

### Synchronization Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                        Local Machine                              │
│                                                                  │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │ Project Root                                             │   │
│  │   ├── .something/                                        │   │
│  │   │   ├── new-knowledge/                                 │   │
│  │   │   │   └── my-pattern.md  ◄────── AI Agent           │   │
│  │   │   ├── knowledge/                                     │   │
│  │   │   │   ├── _index.md                                  │   │
│  │   │   │   ├── auth-pattern.md                            │   │
│  │   │   │   └── error-handling.md                          │   │
│  │   │   ├── prd.md                                         │   │
│  │   │   ├── context.md                                     │   │
│  │   │   ├── scenarios/                                     │   │
│  │   │   │   ├── 01-user-login.md                           │   │
│  │   │   │   └── 02-password-reset.md                       │   │
│  │   │   └── skills-state.json                              │   │
│  │   │                                                      │   │
│  │   ├── .claude/skills/                                    │   │
│  │   │   ├── frontend-performance/                          │   │
│  │   │   ├── pg/                                            │   │
│  │   │   └── lint-fix/                                      │   │
│  │   │                                                      │   │
│  │   ├── .cursor/skills/   ◄─── Skills synced to           │   │
│  │   ├── .github/skills/        all supported agents        │   │
│  │   └── .windsurf/skills/                                  │   │
│  └───────────────┬───────────────────────────────────────────┘   │
│                  │                                                │
│                  ▼                                                │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │ CLI Commands / Git Hook                                   │   │
│  │   ├── yansu sync - Full bidirectional sync               │   │
│  │   ├── yansu pull - Pull from cloud                       │   │
│  │   ├── yansu push - Push to cloud                         │   │
│  │   └── Git post-commit hook - Auto extract & push         │   │
│  └───────────────────────────┬───────────────────────────────┘   │
│                              │                                   │
└──────────────────────────────┬───────────────────────────────────┘
                                │
                                ▼
                   ┌────────────────────────┐
                   │     Yansu Cloud API    │
                   │  - Knowledge Base      │
                   │  - Project Context     │
                   │  - PRD & Scenarios     │
                   │  - Skills Library      │
                   │  - AI Extraction       │
                   └────────────────────────┘
```

### Authentication Flow

```
1. User runs: yansu login
2. Auth module starts local HTTP server (127.0.0.1:random_port)
3. Opens browser to: https://dashboard.yansu.ai/cli-auth?callback=http://localhost:PORT/callback
4. User logs in via GitHub OAuth on Yansu dashboard
5. Dashboard redirects to: http://localhost:PORT/callback?token=xxx
6. Local server receives token
7. Client module verifies token: GET /api/auth/profile
8. Config module saves token: ~/.yansu-agent/token (0600)
```

## Dependencies

```go
github.com/spf13/cobra v1.8.0          // CLI framework (used by cmd/)
github.com/google/uuid v1.6.0          // UUID generation
github.com/gorilla/websocket v1.5.3    // WebSocket relay connection (used by daemon/)
```

## Error Handling

The internal package follows consistent error handling patterns:

1. **API Errors**: Wrapped with context using `fmt.Errorf("operation failed: %w", err)`
2. **File Operations**: Gracefully handle missing files with `os.IsNotExist(err)`
3. **Network Errors**: 30-second timeouts for HTTP requests
4. **Hook Errors**: Logged to `.something/logs/hook.log` for debugging without blocking commits
5. **CLI Errors**: Provide actionable error messages with clear next steps

## Testing

Each module can be tested independently:

```bash
# Test specific module
go test ./internal/config/
go test ./internal/knowledge/

# Test all internal packages
go test ./internal/...

# With coverage
go test -cover ./internal/...
```

## Security Considerations

1. **Token Storage**: Stored in separate file (`~/.yansu-agent/token`) with 0600 permissions
2. **Config Separation**: Secrets never stored in `config.json`
3. **HTTPS Only**: All API communication uses HTTPS
4. **Localhost Binding**: Auth callback server only binds to 127.0.0.1
5. **Timeout Protection**: 5-minute timeout for browser authentication

## Key Design Patterns

1. **Context-First**: All I/O operations accept `context.Context` for cancellation
2. **Dependency Injection**: Modules accept dependencies as parameters
3. **Interface Segregation**: Small, focused interfaces for clean module boundaries
4. **Error Wrapping**: Preserve error chains with `%w` formatting
5. **Command-Line First**: Single-binary CLI tool with optional daemon mode for Slack integration
6. **Hook-Based Automation**: Git hooks for automatic knowledge capture without blocking commits
7. **Multiple Execution Modes**: On-demand via CLI or always-on via daemon

## Usage Examples

### Using the API Client

```go
import (
    "context"
    "yansu-agent/internal/client"
    "yansu-agent/internal/config"
)

cfg, _ := config.Load()
apiClient := client.New(cfg.BaseURL, cfg.Token)
apiClient.SetProject("acme", "web-app", "auth-feature")

// List knowledge
knowledge, err := apiClient.ListKnowledge(context.Background())

// Create knowledge
newKnowledge, err := apiClient.CreateKnowledge(context.Background(), &client.CreateKnowledgeRequest{
    Trigger: "auth pattern",
    Content: "Use JWT tokens with refresh mechanism...",
})
```

### Using the Knowledge Parser

```go
import "yansu-agent/internal/knowledge"

// Parse knowledge file
k, err := knowledge.ParseFile(".something/new-knowledge/auth.md")
if err != nil {
    return err
}

// Write knowledge file
err = knowledge.WriteKnowledgeFile(
    ".something/knowledge",
    "know_123",
    "authentication pattern",
    "Use JWT tokens with 15-minute expiry...",
)

// Update index
err = knowledge.UpdateIndex(".something")
```

### Using the Syncer

```go
import (
    "context"
    "yansu-agent/internal/client"
    "yansu-agent/internal/config"
    yansusync "yansu-agent/internal/sync"
)

cfg, _ := config.Load()
projectRoot, _ := config.FindProjectRoot()
somethingDir := config.GetSomethingDir(projectRoot)
projectCfg, _ := config.LoadProject(somethingDir)

apiClient := client.New(cfg.BaseURL, cfg.Token)
apiClient.SetProject(projectCfg.Organization, projectCfg.Product, projectCfg.Name)

syncer := yansusync.NewSyncer(projectRoot, somethingDir, apiClient)

// Full bidirectional sync
result, err := syncer.SyncAll(context.Background())

// Or individual operations
pushResult, _ := syncer.Push(context.Background())
pullResult, _ := syncer.Pull(context.Background())
syncer.SyncContext(context.Background())
```

### Using Skills Module

```go
import (
    "yansu-agent/internal/client"
    "yansu-agent/internal/skills"
)

// Fetch skills from cloud
apiClient := client.New(cfg.BaseURL, cfg.Token)
apiClient.SetProject("acme", "web-app", "main")

skillsList, err := apiClient.ListSkills(context.Background())

// Load local skills state
state, err := skills.LoadSkillsState(".something")

// Extract new/updated skills to all agent paths
for _, skill := range skillsList {
    localState, exists := state.GetSkill(skill.UUID)

    // Extract if new or updated
    if !exists || skill.UpdatedAt.After(localState.UpdatedAt) {
        err := skills.ExtractToAgentPaths(
            skill.Content,      // base64-encoded gzip tar
            skill.Name,         // skill name
            projectRoot,        // project root path
        )

        // Update state
        state.UpdateFromRemote(skill.UUID, skill.Name, skill.UpdatedAt)
    }
}

// Save updated state
skills.SaveSkillsState(".something", state)

// Clean removed skills
for _, uuid := range state.GetAllUUIDs() {
    found := false
    for _, skill := range skillsList {
        if skill.UUID == uuid {
            found = true
            break
        }
    }
    if !found {
        localState, _ := state.GetSkill(uuid)
        skills.CleanSkillFromAgentPaths(localState.Name, projectRoot)
        state.RemoveSkill(uuid)
    }
}
```

## File Locations

**Global Configuration:**
```
~/.yansu-agent/
├── config.json          # Global settings
├── token                # Auth token (0600)
├── daemon.json          # Daemon configuration (0600)
├── daemon.pid           # Daemon process ID
├── registry.json        # Registered projects (daemon)
└── logs/
    └── daemon.log       # Daemon output log
```

**Project Configuration:**
```
project-dir/.something/
├── project.json         # Project metadata
├── context.md           # Auto-generated context with references
├── prd.md               # Full PRD content (if available)
├── skills-state.json    # Skills synchronization state
├── scenarios/           # User scenarios (if available)
│   ├── 01-user-login.md
│   └── 02-password-reset.md
├── knowledge/           # Synced knowledge (read-only)
│   ├── _index.md        # Generated index
│   └── *.md             # Knowledge files
├── new-knowledge/       # Create files here to push
│   └── *.md             # Pending push
└── activity/            # Activity logs
    └── *.md             # Activity records
```

**AI Agent Skills (synced to all paths):**
```
project-dir/
├── .claude/skills/           # Claude Code skills
├── .cursor/skills/           # Cursor skills
├── .github/skills/           # GitHub Copilot skills
├── .windsurf/skills/         # Windsurf skills
├── .goose/skills/            # Goose skills
└── [+9 more agent paths]     # See skills/agents.go for full list
```

## Future Enhancements

See [ARCHITECTURE.md](../ARCHITECTURE.md) for planned improvements:
- Conflict detection for concurrent edits
- Retry mechanism for failed API calls
- Debounce map cleanup to prevent memory growth
- Activity logging API implementation
- Enhanced error recovery

## Related Documentation

- [Architecture Overview](../ARCHITECTURE.md) - System architecture and design
- [Main README](../README.md) - User documentation and quick start
- [Command Documentation](../docs/) - CLI command reference
