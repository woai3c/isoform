package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// bundled_lark_skill.go seeds a lark-cli skill into the skill-hub so Yansu's
// LLM can operate Feishu/Lark (messages, docs, Base, Sheets, Calendar, Mail,
// contacts, meetings, tasks) via the official lark-cli (github.com/larksuite/cli),
// run through the bundled bun runtime. Credentials are pre-registered as named
// lark-cli profiles (see lark_cli.go: larkCliConfigure on Feishu/Lark connect),
// so the LLM never logs in or handles secrets — it just selects a profile.
//
// Like the cua skill, only the frontmatter description is eagerly injected into
// the prompt (metadata-only model in skillHubContext); the LLM Reads the body on
// demand. We seed whenever bun is available; readiness/scope state is surfaced
// separately (GetLarkCliStatus) and the body tells the LLM what to do when the
// CLI reports it is not configured or lacks a scope.
const (
	bundledLarkSkillSlug      = "lark-cli"
	bundledLarkSkillName      = "Feishu / Lark (lark-cli)"
	bundledLarkSkillSourceTag = "bundled-lark"
	bundledLarkSkillVersion   = "0.1.0-yansu1"
	bundledLarkSkillManaged   = "<!-- managed-by-yansu-wails:lark-cli -->"
	bundledLarkSkillFile      = "SKILL.md"
	bundledLarkSkillSourceURL = "https://github.com/larksuite/cli"
	bundledLarkSkillDesc      = "Operate Feishu/Lark from the terminal via lark-cli: send/search messages, read/write Docs, Base (Bitable), Sheets, Calendar, Mail, contacts, meetings, and tasks. Use whenever the user asks to read, write, search, or send their Feishu/Lark data. Runs in application identity — credentials are pre-configured, no login step. Read this SKILL.md for the command surface, account profiles, identity boundary, and JSON conventions before first use."
)

// ensureBundledLarkSkill seeds the lark-cli skill when the bun runtime is
// available. Idempotent; safe to call on every startup.
func ensureBundledLarkSkill() {
	bun := installedBunPath()
	if bun == "" {
		return
	}

	targetDir, err := skillHubSkillDir(bundledLarkSkillSlug)
	if err != nil {
		log.Printf("[lark-skill] resolve skill dir: %v", err)
		return
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		log.Printf("[lark-skill] create skill dir: %v", err)
		return
	}

	if !writeManagedLarkSkillFile(filepath.Join(targetDir, bundledLarkSkillFile),
		bundledLarkSkillContent(bun)) {
		return
	}

	now := time.Now().UnixMilli()
	if err := withSkillHubState(func(state *skillHubState) error {
		existing, ok := state.Skills[bundledLarkSkillSlug]
		installedAt := existing.InstalledAt
		if installedAt == 0 {
			installedAt = now
		}
		domains := existing.Domains
		if domains == nil {
			domains = defaultSkillHubDomains()
		}
		enabled := existing.Enabled
		if !ok {
			enabled = true
		}
		state.Skills[bundledLarkSkillSlug] = SkillHubInstalledSkill{
			ID:          bundledLarkSkillSlug,
			Slug:        bundledLarkSkillSlug,
			Name:        bundledLarkSkillName,
			Description: bundledLarkSkillDesc,
			Source:      bundledLarkSkillSourceTag,
			SourceURL:   bundledLarkSkillSourceURL,
			Version:     bundledLarkSkillVersion,
			Path:        targetDir,
			Enabled:     enabled,
			Domains:     normalizeSkillHubDomains(domains),
			InstalledAt: installedAt,
			UpdatedAt:   now,
		}
		return nil
	}); err != nil {
		log.Printf("[lark-skill] update state.json: %v", err)
	}
}

func writeManagedLarkSkillFile(path, desired string) bool {
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := writeAtomic(path, desired); err != nil {
			log.Printf("[lark-skill] write %s: %v", filepath.Base(path), err)
			return false
		}
	case err != nil:
		log.Printf("[lark-skill] read existing %s: %v", filepath.Base(path), err)
		return false
	case !strings.Contains(string(existing), bundledLarkSkillManaged):
	case string(existing) == desired:
	default:
		if err := writeAtomic(path, desired); err != nil {
			log.Printf("[lark-skill] update %s: %v", filepath.Base(path), err)
			return false
		}
	}
	return true
}

func bundledLarkSkillContent(bunPath string) string {
	run := shellQuotePOSIX(bunPath) + " x @larksuite/cli@latest"
	return strings.Join([]string{
		"---",
		"name: " + bundledLarkSkillSlug,
		"description: " + bundledLarkSkillDesc,
		"---",
		bundledLarkSkillManaged,
		"",
		"# Feishu / Lark via lark-cli",
		"",
		"Operate the user's Feishu/Lark workspace by shelling out to the official",
		"`lark-cli` (github.com/larksuite/cli), launched through the bundled bun runtime.",
		"",
		"## How to run",
		"",
		"Run every command as:",
		"",
		"```bash",
		run + " <command> [subcommand] [options]",
		"```",
		"",
		"Credentials are **already configured** as named profiles — the CLI runs in",
		"*application identity* (auto-refreshing tenant token). Do NOT run `auth login`",
		"or `config init`; do NOT ask the user for credentials.",
		"",
		"## Accounts (profiles)",
		"",
		"Feishu (mainland, feishu.cn) and Lark (international, larksuite.com) are separate",
		"accounts and each is a separate profile when connected: `feishu` and/or `lark`.",
		"Select one with `--profile`:",
		"",
		"```bash",
		run + " --profile feishu <command> ...   # operate the Feishu account",
		run + " --profile lark   <command> ...   # operate the Lark account",
		"```",
		"",
		"If only one is connected it is the default; pass `--profile` explicitly when",
		"both are connected. List configured profiles with `" + run + " profile list`.",
		"",
		"Prefer machine-readable output when a command supports it. Most domain commands",
		"use `--format json`; check `<command> --help` before assuming a JSON flag.",
		"",
		"## Discover commands (do this instead of guessing)",
		"",
		"```bash",
		run + " --help                 # top-level command list",
		run + " <domain> --help        # e.g. im / docs / base / sheets / calendar / mail / contact",
		run + " schema <service.resource.method>   # exact params for an API",
		run + " api GET /open-apis/...  # generic raw API call",
		"```",
		"",
		"## Common domains",
		"",
		"- **im** — search/send/reply messages, list chats",
		"- **docs** — create/read/update docs, add comments",
		"- **base** — Bitable: create tables, read/write/batch-update records",
		"- **sheets** — read/write cells, batch update",
		"- **calendar** — list/create events, agenda (`calendar +agenda`)",
		"- **mail** — search/read/draft/send/reply",
		"- **contact** — search users",
		"- **meeting / task** — minutes, todos",
		"",
		"## Identity boundary (important)",
		"",
		"Application identity can do bot-style actions and operate resources the app has",
		"permission for. Accessing a user's PRIVATE data (personal calendar, mailbox,",
		"private docs) needs USER identity: pass `--as user`. If a command returns",
		"`identity \"bot\" ... only supports: user`, tell the user that personal-data access",
		"requires upgrading to personal identity in Settings → Feishu/Lark — do not attempt",
		"the OAuth flow yourself.",
		"",
		"## When the CLI is not ready",
		"",
		"- `not_configured` / no profile → the user has not connected Feishu/Lark yet. Tell",
		"  them to open Settings → Feishu (or Lark) and finish connecting, then retry.",
		"- A permission/scope error on a domain → the connected app is missing that business",
		"  scope. Tell the user to import the full permission package and refresh permissions",
		"  in Settings → Feishu/Lark to grant docs/sheets/base/calendar/mail/etc. access.",
		"",
		"## Conventions",
		"",
		"- Prefer `--json` and parse the `ok` / `error.{type,subtype,message}` shape.",
		"- Keep IDs/tokens out of your visible reply; summarize results for the user.",
		"",
	}, "\n")
}
