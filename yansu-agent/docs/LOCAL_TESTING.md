# Local Testing Guide

This document explains how to test yansu agent in a local development environment.

## Prerequisites

Ensure the development environment is running:

```bash
cd /path/to/something && ./start-demo.sh
```

This starts:
- Backend service: http://localhost:8080
- Frontend service: http://localhost:3000

## Quick Start

Use the test script to check your environment:

```bash
cd yansu-agent
./scripts/test-local.sh
```

This script automatically checks backend status, builds the agent, verifies authentication, and displays available commands.

## Manual Testing Steps

### Step 1: Build yansu agent

```bash
cd yansu-agent
go build -o yansu ./cmd/yansu/
```

### Step 2: Set local environment variable

```bash
export YANSU_URL=https://localhost:3000
```

### Step 3: Login to yansu agent

```bash
./yansu login
```

This will open your browser to the CLI authentication page. After signing in with GitHub or Google, you'll be automatically redirected back to the terminal.

**Alternative: Manual token entry**

If browser login doesn't work, you can manually paste the token:

1. Select option **2** (Paste token manually) when prompted
2. Open http://localhost:3000 in your browser and login
3. Open Developer Tools (F12) → **Application** → **Cookies** → localhost:3000
4. Copy the `something-session` cookie value and paste it

### Step 4: Verify login status

```bash
./yansu status
```

This should display authenticated status and user information.

## Feature Testing

### Clone a project

```bash
./yansu clone <org>/<product>/<project>
```

This will:
- Clone the associated Git repository
- Sync project knowledge to local `.something/` directory
- Automatically install git hook

### Sync knowledge

```bash
cd <cloned-project>
./yansu sync
```

Pulls the latest project knowledge from cloud.

### Git Hook management

```bash
# Install hook
./yansu hook install

# Check status
./yansu hook status

# Uninstall hook
./yansu hook uninstall
```

Once installed, every `git commit` will automatically analyze code changes and extract knowledge.

### Push code and knowledge

```bash
# Push code + knowledge (default behavior)
./yansu push

# Push code only
./yansu push --code

# Push knowledge only
./yansu push --knowledge
```

### Start sync daemon

```bash
./yansu daemon
```

The daemon will:
- Watch for file changes
- Periodically sync knowledge
- Run continuously in the background

## Troubleshooting

### Q: "Backend is not running" error

Make sure you've started the development environment with `./start-demo.sh`.

### Q: "Not logged in" error

Follow Steps 3-4 to obtain a new token and login again.

### Q: "project not found" when cloning

Ensure:
1. The project has been created in the web interface
2. You're using the correct `org/product/project` format
3. Your user has permission to access the project

### Q: No output from hook run

This is normal. The `hook run` command is called automatically by the git hook and only produces output when significant knowledge is found.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `YANSU_URL` | API service URL | `https://yansu.ai` |
| `YANSU_TOKEN` | Auth token (optional, takes priority over config file) | - |

## Related Files

- `~/.config/yansu-agent/config.yaml` - Global configuration
- `~/.config/yansu-agent/token` - Authentication token
- `.something/` - Project local knowledge directory
  - `.something/context.md` - Project context (requirements, decisions)
  - `.something/knowledge/` - Knowledge base (guidelines, patterns)
  - `.something/knowledge/_index.md` - Knowledge quick lookup index
  - `.something/new-knowledge/` - New knowledge to be pushed
- `.git/hooks/post-commit` - Automatic knowledge capture hook
