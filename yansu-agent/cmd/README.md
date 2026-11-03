# Yansu Agent CLI

The `cmd/yansu/` directory contains the command-line interface for the Yansu Agent — a local development companion that syncs project knowledge with the Yansu cloud to enhance AI coding assistants.

## Architecture

The CLI is built with [Cobra](https://github.com/spf13/cobra). Commands delegate to packages under `internal/`:

```
cmd/yansu/main.go     CLI entry point, all command definitions
internal/auth/         Login/logout/token management
internal/client/       HTTP client for the Yansu API
internal/config/       Global (~/.yansu-agent/) and project (.something/) configuration
internal/daemon/       Slack-driven local agent daemon with WebSocket relay
internal/gitutil/      Git utilities (hook management, gitignore)
internal/knowledge/    Knowledge file parsing, indexing, CRUD
internal/sync/         Bidirectional cloud sync (push/pull)
internal/updater/      Self-update from GitHub Releases
```

## Commands

### Authentication

| Command | Description |
|---------|-------------|
| `yansu login` | Authenticate with Yansu (browser OAuth or manual token) |
| `yansu logout` | Clear stored credentials |
| `yansu status` | Show auth status, project config, knowledge stats, pending changes |
| `yansu whoami` | Show the currently authenticated user |
| `yansu setup-token` | Generate a long-lived API token for Anthropic-compatible API / Claude Code |

### Discovery

| Command | Description |
|---------|-------------|
| `yansu list` / `yansu ls` | Browse cloud orgs → products → projects |
| `yansu list <org>` | List products in an org |
| `yansu list <org>/<product>` | List projects in a product |
| `yansu list <org>/<product>/<project>` | Show project details |

### Project Management

| Command | Description |
|---------|-------------|
| `yansu clone <org>/<product>/<project>` | Clone a project with full knowledge context and git hooks |
| `yansu init <org>/<product>/<project>` | Initialize an existing directory as a Yansu project (no git clone) |
| `yansu sync` | Bidirectional sync: push local knowledge → cloud, pull cloud → local |
| `yansu pull` | Pull code (git pull) + knowledge + context from cloud |
| `yansu push` | Push git commits to remote + knowledge to cloud |
| `yansu push --code` | Push only git commits |
| `yansu push --knowledge` | Push only knowledge to cloud |
| `yansu analyze` | Analyze recent changes for knowledge capture (interactive) |
| `yansu analyze --auto` | Auto-generate knowledge draft without confirmation |
| `yansu analyze --commits 5` | Analyze the last N commits |

### Git Hooks

| Command | Description |
|---------|-------------|
| `yansu hook install` | Install post-commit hook for automatic AI knowledge extraction |
| `yansu hook uninstall` | Remove the Yansu post-commit hook |
| `yansu hook status` | Show hook installation status |
| `yansu hook run` | Internal command called by the git hook (extracts knowledge via backend AI) |

The hook runs in the background after each commit and does not slow down git operations. It calls the Yansu backend AI to analyze the commit diff and extract patterns, decisions, and learnings as knowledge files.

### Knowledge CRUD

| Command | Description |
|---------|-------------|
| `yansu knowledge list` | List all knowledge items in the current project |
| `yansu knowledge search <query>` | Search by trigger or content |
| `yansu knowledge show <uuid-or-filename>` | Show full content of a knowledge item |
| `yansu knowledge add` | Interactively add a new knowledge item |
| `yansu knowledge delete <uuid-or-filename>` | Delete a knowledge item (syncs as deletion on next push) |

### Stage Management

| Command | Description |
|---------|-------------|
| `yansu stage list` | List all stages with status |
| `yansu stage show <stage>` | View stage details and content |
| `yansu stage edit <stage>` | Update stage content via stdin |
| `yansu stage trigger <stage>` | Run/trigger a stage |
| `yansu stage tasks <stage>` | View execution task summary |
| `yansu stage versions <stage>` | Show version history |
| `yansu stage restore <stage> <version>` | Restore to a previous version |

### Scenario Management

| Command | Description |
|---------|-------------|
| `yansu scenario list` | List all scenarios with status |
| `yansu scenario show <uuid>` | Show full details of a scenario |
| `yansu scenario requeue <uuid>` | Re-queue a failed or passed scenario |
| `yansu scenario cancel <uuid>` | Cancel a queued or building scenario |
| `yansu scenario checklist generate <uuid>` | Generate acceptance checklist |
| `yansu scenario checklist check <uuid> <item-id>` | Mark a checklist item as checked |
| `yansu scenario checklist uncheck <uuid> <item-id>` | Mark a checklist item as unchecked |

### Feedback Management

| Command | Description |
|---------|-------------|
| `yansu feedback list <stage>` | List feedbacks for a stage |
| `yansu feedback show <uuid>` | Show full details and comments |
| `yansu feedback pending` | List feedbacks awaiting your reply |
| `yansu feedback reply <uuid> <text...>` | Reply to a feedback |
| `yansu feedback retry <uuid>` | Retry AI response for a feedback |
| `yansu feedback resolve <uuid>` | Mark as resolved |

### Daemon (Slack-Driven Local Agent)

| Command | Description |
|---------|-------------|
| `yansu daemon start` | Start the daemon (runs until Ctrl+C) |
| `yansu daemon set-executor <claude\|codex>` | Set AI executor for free-text tasks |
| `yansu daemon register` | Register current project with the daemon |
| `yansu daemon status` | Show daemon process status |
| `yansu daemon stop` | Stop the running daemon |
| `yansu daemon restart` | Restart the daemon |
| `yansu daemon logs` | Show daemon log output |

The daemon receives Slack messages routed via the Yansu backend relay and executes yansu commands or Claude Code on the local machine.

### System Service

| Command | Description |
|---------|-------------|
| `yansu service install` | Install daemon as a launchd (macOS) or systemd (Linux) user service |
| `yansu service uninstall` | Remove from system services |
| `yansu service status` | Show service status |
| `yansu service stop` | Stop the running daemon without uninstalling |
| `yansu service restart` | Restart the daemon |

### Local Project Registry

| Command | Description |
|---------|-------------|
| `yansu local list` | List locally registered projects |
| `yansu local register` | Register the current directory with the daemon |
| `yansu local unregister [ref]` | Remove a project from the registry |
| `yansu local path <ref>` | Print filesystem path for a registered project |

### Cron (Desktop Automation Scheduler)

Commands in this group target the Yansu desktop app's local HTTP API (requires the desktop app to be running).

| Command | Description |
|---------|-------------|
| `yansu cron list` | List all cron jobs |
| `yansu cron show <id>` | Show job details |
| `yansu cron add` | Create a new job (requires `--name`, `--schedule`, `--prompt`) |
| `yansu cron update <id>` | Update name, schedule, prompt, or enabled state |
| `yansu cron delete <id>` | Delete a job |
| `yansu cron run <id>` | Trigger immediate execution |

Schedule formats: `every 5m`, `cron 0 9 * * *`, or raw cron expressions.

### Skill Hub (Desktop)

| Command | Description |
|---------|-------------|
| `yansu skill sources` | List available skill sources |
| `yansu skill list` | List installed skills |
| `yansu skill discover [query]` | Discover skills from other local agent apps |
| `yansu skill search --source <source> <query>` | Search skills from an explicit source |
| `yansu skill install --source <source> <ref>` | Install from ClawHub, GitHub, local, or agent-dirs |
| `yansu skill enable <id>` | Enable an installed skill |
| `yansu skill disable <id>` | Disable an installed skill |
| `yansu skill domains <id>` | Update where a skill is enabled (--chat, --project, --automation) |
| `yansu skill uninstall <id>` | Uninstall a skill |

### MCP Server Hub (Desktop)

| Command | Description |
|---------|-------------|
| `yansu mcp list` | List installed MCP servers with health and tool counts |
| `yansu mcp sources` | Show scanned local MCP config files |
| `yansu mcp discover` | Discover servers from Claude Desktop, Cursor, Codex, Windsurf, VS Code |
| `yansu mcp install --source manual --transport sse\|http ...` | Register a remote SSE/HTTP MCP server; add manual stdio servers in Isoform Settings |
| `yansu mcp install --source local --ref <src>:<name>` | Import a discovered server |
| `yansu mcp install --source registry --ref <slug>` | Install from curated catalog |
| `yansu mcp enable <id>` | Enable and spawn the subprocess |
| `yansu mcp disable <id>` | Disable and stop the subprocess |
| `yansu mcp restart <id>` | Restart a server |
| `yansu mcp stop <id>` | Stop without disabling persisted config |
| `yansu mcp tools <id>` | List tools exposed by a server |
| `yansu mcp tail <id>` | Tail stderr logs |
| `yansu mcp uninstall <id>` | Remove config and scratch directory |
| `yansu mcp registry list` | Browse curated MCP server catalog |
| `yansu mcp registry search <query>` | Filter catalog by name or description |
| `yansu mcp registry refresh` | Force re-fetch the catalog |

### Activity Monitor (Desktop)

| Command | Description |
|---------|-------------|
| `yansu activity list` | List recent activity sessions (with --date, --limit, --keyword flags) |
| `yansu activity show <session-id>` | Show detailed events for a session |
| `yansu activity summary` | Show daily AI-generated activity summary |
| `yansu activity search <query>` | Semantic search across sessions (RAG over OCR text) |

### Memory (Desktop)

| Command | Description |
|---------|-------------|
| `yansu memory list` | List memory entries, newest first (filterable by dimension and limit) |
| `yansu memory search <query>` | Hybrid (vector + FTS) search across memories and knowledge |
| `yansu memory show <id>` | Show full content of a memory entry |

Memory dimensions: `user_insight`, `topic_highlight`, `deep_user_model`, `agentic_memory`, `custom`.

## Configuration

### Global config (`~/.yansu-agent/`)

```
config.json    Global settings (base_url, sync interval)
token          Auth token (0600 permissions — separate from config)
daemon.json    Daemon settings (executor preference)
cron-port      Desktop app API port (written by the desktop app)
cron-secret    Non-macOS fallback only (macOS uses Keychain)
```

### Project config (`.something/`)

```
project.json        Project metadata (uuid, org, product, name, yansu_url)
knowledge/          Synced knowledge files (read-only)
  _index.md         Quick lookup index
new-knowledge/      Pending knowledge to push
context.md          Project context (auto-generated)
activity/           Activity logs
logs/hook.log       Git hook execution logs
```

Priority: environment variables > config file > defaults.

Environment variables:
- `YANSU_URL` — override base URL
- `YANSU_CRON_SECRET` — optional desktop app API credential override; the destination always comes from the desktop's `cron-port` file, and `YANSU_CRON_PORT` is ignored
- `YANSU_AGENT_HOME` — desktop data folder used for port and secret discovery

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/google/uuid` — UUID generation
- `github.com/gorilla/websocket` — daemon WebSocket relay
- `github.com/bmatcuk/doublestar/v4` — glob patterns for gitignore

## Building

```bash
cd yansu-agent
make build        # Build the yansu binary
make test         # Run all tests
```

## Testing

Unit tests cover:
- Skill installation source inference (`skill_cmd_test.go`)
- MCP environment flag parsing (`mcp_cmd_test.go`)
- Command-line argument expansion
- Domain flag handling

```bash
go test ./cmd/yansu/...
```

## Version

Current version: `0.3.1`

Self-update: `yansu update` (checks GitHub Releases for newer versions).

The macOS scheduler secret is stored in Keychain under versioned services `com.isoform.cron-api.generation.<UUID>`, with the canonical absolute data folder as the account. The reader also recognizes the older base and `.next` service names for migration. The desktop migrates an existing `cron-secret` file only after verifying the Keychain write, then deletes the file. The macOS CLI uses native Security.framework access and must be the CLI bundled with the running desktop. The desktop trusts that executable's identity, never `/usr/bin/security`; CGO-free macOS builds cannot read the Keychain credential. macOS does not fall back to a plaintext file when Keychain is unavailable. See [scheduler authentication](../../docs/cron-api-auth.md).
