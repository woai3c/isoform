# Yansu CLI

CLI entrypoint for the Yansu Agent — a local development companion that syncs project knowledge with the Yansu cloud and provides a Slack-driven daemon for remote AI agent execution.

## Files

| File | Purpose |
|---|---|
| `main.go` | Full CLI implementation with ~30 subcommands using [spf13/cobra](https://github.com/spf13/cobra) |
| `skill_cmd_test.go` | Tests for skill install source inference and domain flag parsing |
| `mcp_cmd_test.go` | Tests for `--args` comma expansion and `--env` key=value parsing |

## Architecture

Single-package Cobra CLI (`package main`). The `init()` function wires all subcommands into the root command tree. Commands fall into two categories:

- **Direct CLI commands** — Run locally against git, filesystem, and the Yansu cloud API via `internal/client`
- **Desktop proxy commands** — Talk to the Yansu desktop app's local HTTP API (`127.0.0.1:<port>`) via `cronRequest()`. These require the desktop app to be running.

### Internal package dependencies

```
cmd/yansu
  ├── internal/auth          — login / logout / status
  ├── internal/client        — Yansu cloud API client
  ├── internal/config        — user config, project config, daemon config, PID file
  ├── internal/daemon        — Slack relay daemon + project registry
  ├── internal/gitutil       — .gitignore helpers for agent instruction files
  ├── internal/knowledge     — local knowledge file parser and index
  ├── internal/sync          — knowledge push/pull sync engine
  └── internal/updater       — self-update check and download
```

## Command Reference

### Authentication

| Command | Description |
|---|---|
| `yansu login` | Authenticate with Yansu cloud |
| `yansu logout` | Remove stored credentials |
| `yansu whoami` | Show authenticated user profile |
| `yansu status` | Show auth status, project info, knowledge stats, pending changes |
| `yansu setup-token` | Generate a long-lived API token for Claude Code / Anthropic SDK |

### Project Management

| Command | Description |
|---|---|
| `yansu clone <org>/<product>/<project>` | Clone a project via Yansu Git proxy, sync knowledge, install git hook, register with daemon |
| `yansu init <org>/<product>/<project>` | Initialize current directory as a Yansu project (no clone) |
| `yansu pull` | Git pull + pull knowledge + sync context |
| `yansu push` | Push git commits and/or knowledge to cloud. Flags: `--code`, `--knowledge` |
| `yansu sync` | Full bidirectional sync of knowledge with cloud |
| `yansu list` / `yansu ls` | Browse org → product → project hierarchy |
| `yansu local list` | List locally registered projects |
| `yansu local register` | Register current project with the daemon |
| `yansu local unregister [ref]` | Remove a project from the daemon registry |
| `yansu local path <ref>` | Print filesystem path for a registered project |

### Knowledge Management

| Command | Description |
|---|---|
| `yansu knowledge list` | List all knowledge items in current project |
| `yansu knowledge search <query>` | Search by trigger or content |
| `yansu knowledge show <uuid-or-filename>` | Display full knowledge entry |
| `yansu knowledge add` | Interactively create a new knowledge entry |
| `yansu knowledge delete <uuid-or-filename>` | Delete a knowledge entry |

### Analysis & Hooks

| Command | Description |
|---|---|
| `yansu analyze` | Analyze staged/unstaged changes and recent commits, generate knowledge draft. Flags: `--auto`, `--commits N` |
| `yansu hook install` | Install post-commit hook for automatic AI knowledge extraction |
| `yansu hook uninstall` | Remove Yansu hook from post-commit |
| `yansu hook status` | Show whether the hook is installed |
| `yansu hook run` | Internal command called by git hook (hidden) |

### Daemon & Service

| Command | Description |
|---|---|
| `yansu daemon start` | Start the Slack relay daemon (runs until Ctrl+C) |
| `yansu daemon set-executor <claude\|codex>` | Choose AI executor for free-text Slack tasks |
| `yansu daemon register` | Register current project (alias for `yansu local register`) |
| `yansu daemon status` | Show whether the daemon process is running |
| `yansu daemon stop` | Stop a running daemon via SIGTERM |
| `yansu daemon restart` | Stop then start the daemon |
| `yansu daemon logs` | Tail daemon log output. Flags: `--follow`, `--lines N` |
| `yansu service install` | Register daemon as a launchd/systemd user service |
| `yansu service uninstall` | Remove from system services |
| `yansu service status` | Show service status |
| `yansu service stop` | Stop the running service (without uninstalling) |
| `yansu service restart` | Restart the daemon service |

### Stages

| Command | Description |
|---|---|
| `yansu stage list` | List all project stages with status |
| `yansu stage show <stage>` | View stage details and content |
| `yansu stage edit <stage>` | Update stage content via stdin. Flag: `--note` |
| `yansu stage trigger <stage>` | Run/trigger a stage |
| `yansu stage tasks <stage>` | View execution task summary |
| `yansu stage versions <stage>` | Show version history |
| `yansu stage restore <stage> <version>` | Restore stage to a previous version |

### Scenarios

| Command | Description |
|---|---|
| `yansu scenario list` | List all scenarios with status |
| `yansu scenario show <uuid>` | Show full scenario details including checklist |
| `yansu scenario requeue <uuid>` | Re-queue a failed or passed scenario |
| `yansu scenario cancel <uuid>` | Cancel a queued or building scenario |
| `yansu scenario checklist generate <uuid>` | Generate acceptance checklist for a passed scenario |
| `yansu scenario checklist check <uuid> <item-id>` | Mark a checklist item as checked |
| `yansu scenario checklist uncheck <uuid> <item-id>` | Mark a checklist item as unchecked |

### Feedback

| Command | Description |
|---|---|
| `yansu feedback list <stage>` | List feedbacks for a stage |
| `yansu feedback show <uuid>` | Show feedback details and comment thread |
| `yansu feedback pending` | List feedbacks awaiting your reply |
| `yansu feedback reply <uuid> <text>` | Reply to a feedback |
| `yansu feedback retry <uuid>` | Retry AI response for a feedback |
| `yansu feedback resolve <uuid>` | Mark a feedback as resolved |

### Desktop Features (require desktop app running)

These commands communicate with the Isoform desktop app's local HTTP API. The CLI always reads the destination from `cron-port` in its data folder and ignores `YANSU_CRON_PORT`. Authenticated requests refuse HTTP redirects. `YANSU_CRON_SECRET` can supply the credential; otherwise it comes directly from macOS Keychain through Security.framework. On macOS use the CLI bundled with the running desktop; its identity is explicitly trusted, and CGO-free builds cannot read that credential; other platforms keep a private `cron-secret` file. `YANSU_AGENT_HOME` selects the desktop's data folder (for example, `~/.isoform-agent`). See [scheduler authentication](../../../docs/cron-api-auth.md) for migration and direct API clients.

| Command | Description |
|---|---|
| `yansu cron list` | List scheduled automation jobs |
| `yansu cron show <id>` | Show job details |
| `yansu cron add` | Create a job. Flags: `--name`, `--schedule`, `--prompt`, `--project`, `--model` |
| `yansu cron update <id>` | Update a job. Flags: `--name`, `--schedule`, `--prompt`, `--enabled` |
| `yansu cron delete <id>` | Delete a job |
| `yansu cron run <id>` | Trigger immediate execution |
| `yansu skill list` | List installed skills |
| `yansu skill sources` | List available skill sources |
| `yansu skill search --source <src> <query>` | Search skills from a specific source |
| `yansu skill discover [query]` | Discover skills from other local agent apps |
| `yansu skill install <ref>` | Install a skill. Flags: `--source`, `--yes` |
| `yansu skill enable <id>` | Enable an installed skill |
| `yansu skill disable <id>` | Disable an installed skill |
| `yansu skill domains <id>` | Update domain settings. Flags: `--chat`, `--project`, `--automation` |
| `yansu skill uninstall <id>` | Uninstall a skill |
| `yansu mcp list` | List installed MCP servers with health/tool counts |
| `yansu mcp sources` | Show which local MCP config files are scanned |
| `yansu mcp discover` | Discover MCP servers from other tools (Claude Desktop, Cursor, etc.) |
| `yansu mcp install` | Install an MCP server. Flags: `--source`, `--ref`/`--id`/`--command`/`--args`/`--env`/`--url` |
| `yansu mcp enable <id>` | Enable and start an MCP server |
| `yansu mcp disable <id>` | Disable and stop an MCP server |
| `yansu mcp restart <id>` | Restart an MCP server |
| `yansu mcp stop <id>` | Stop without disabling |
| `yansu mcp tools <id>` | List tools exposed by an MCP server |
| `yansu mcp tail <id>` | Tail stderr from an MCP server. Flag: `--lines N` |
| `yansu mcp uninstall <id>` | Uninstall an MCP server |
| `yansu mcp registry list` | Browse curated MCP server catalog |
| `yansu mcp registry search <query>` | Search the catalog |
| `yansu mcp registry refresh` | Force re-fetch the catalog |
| `yansu activity list` | List recent activity sessions. Flags: `--date`, `--limit`, `--keyword` |
| `yansu activity show <id>` | Show detailed events for a session |
| `yansu activity summary` | Show daily activity summary. Flag: `--date` |
| `yansu activity search <query>` | Semantic search across activity. Flag: `--limit` |
| `yansu memory list` | List memory entries. Flags: `--dimension`, `--limit` |
| `yansu memory search <query>` | Hybrid vector+FTS search across memories. Flags: `--dimension`, `--limit` |
| `yansu memory show <id>` | Show full content of a memory entry |

### Maintenance

| Command | Description |
|---|---|
| `yansu update` | Check for and install the latest version. Flag: `--force` / `-f` |

## Configuration

Configuration is stored in `~/.yansu-agent/`:

| File | Purpose |
|---|---|
| `config.json` | User auth token, base URL |
| `daemon.json` | Daemon settings (executor preference) |
| `registry.json` | Project ref → local path mappings |
| `cron-port` | Desktop app API port (written by desktop app) |
| `cron-secret` | Non-macOS fallback only; on macOS the desktop deletes this legacy file after verifying its new Keychain credential |

Environment variables: `YANSU_CRON_SECRET`, `YANSU_AGENT_HOME`. `YANSU_CRON_PORT` is ignored: the desktop must publish its port in `cron-port`. macOS uses native Keychain access with the canonical absolute data folder as the account. CGO-free CLI builds fail closed; no shared helper reads the credential. Start the desktop once to migrate an old file. A copied trusted CLI has the same Keychain identity, so agent isolation depends on the agent sandbox and the planned scoped token described in [scheduler authentication](../../../docs/cron-api-auth.md).

Project-level configuration lives in `.something/project.json` within each project directory.

## Testing

```bash
# From the yansu-agent root
cd yansu-agent
go test ./cmd/yansu/...
```

Tests cover:
- `skill_cmd_test.go`: Skill install source inference (URL patterns, local dirs, agent-dirs IDs) and domain flag parsing
- `mcp_cmd_test.go`: Comma-separated `--args` expansion and `--env KEY=VALUE` parsing

## Version

Current version: `0.3.1` (set at build time via the `version` variable in `main.go`).

## Dependencies

- Go 1.23+
- [spf13/cobra](https://github.com/spf13/cobra) — CLI framework
- [spf13/pflag](https://github.com/spf13/pflag) — flag parsing (transitive)
- `yansu-agent/internal/*` — internal packages for auth, API client, config, daemon, sync, knowledge, updater
- `git` — required on PATH for clone, pull, push, analyze, hook commands
