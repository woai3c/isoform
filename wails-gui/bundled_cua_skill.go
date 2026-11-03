package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// bundled_cua_skill.go installs a default cua-driver skill into the
// skill-hub on first launch so Yansu's own LLM picks it up via
// skillHubContext() without the user having to discover or install it.
//
// We don't re-use the upstream SKILL.md shipped inside CuaDriver.app
// directly because it doesn't know about Yansu-specific runtime
// contracts: the bundled-binary absolute path (cua-driver is never on
// $PATH for Yansu users), the daemon socket Yansu owns and forces via
// --socket, the policy proxy that hard-rejects focus-stealing calls,
// the CEF/Electron empty-AX-tree stop rule, and TCC inheritance. Our
// SKILL.md below encodes all of those, then points the LLM at the
// upstream SKILL.md (and its sub-files WEB_APPS.md / RECORDING.md /
// TESTS.md, all already shipped inside CuaDriver.app) for the
// reference material that doesn't conflict with Yansu's contracts —
// canonical loop, intent map, no-foreground discipline, etc.
//
// Under the metadata-only injection model in skillHubContext(), this
// file's body is NOT eagerly added to every prompt — only the
// frontmatter description is. The LLM Reads the body on demand when
// the description matches the user's task.

const (
	bundledCuaSkillSlug      = "cua"
	bundledCuaSkillName      = "Cua Driver (computer use)"
	bundledCuaSkillSourceTag = "bundled-cua"
	// bumped any time we change the SKILL.md body or seed semantics —
	// the skill hub re-writes the file when its content differs from
	// the desired template (only managed files, detected via the
	// bundledCuaSkillManaged sentinel).
	bundledCuaSkillVersion     = "0.5.1-yansu1"
	bundledCuaSkillManaged     = "<!-- managed-by-yansu-wails:cua-driver -->"
	bundledCuaSkillFile        = "SKILL.md"
	bundledCuaSkillDescription = "Drive native macOS apps in the background (operate, automate, or perform any GUI task in a real macOS application). IMPORTANT: this capability is already exposed as the native `cua_driver` tool — call that tool DIRECTLY; do NOT Read this SKILL.md first. The tool's own description carries the canonical loop (launch_app → get_window_state → action; re-snapshot only when needed) and is enough to start. Read this SKILL.md only if you hit an edge case: AX-empty CEF/Electron or Chinese desktop app, policy-proxy rejection, unclear ax_status, or web-rendered-app patterns. Capability detail: snapshot the AX tree plus screenshot, then click/type/scroll by element_index (or screenshot visual OCR refs / pixel coords for AX-empty apps); all paths are focus-safe via AX actions plus process-level background activation, so renderer commits do not visually foreground the target."
)

// ensureBundledCuaSkill seeds the cua-driver skill into the skill hub
// when (a) the bundled CuaDriver.app is available on this host and (b)
// the user hasn't customized either of the managed files. It's
// idempotent and safe to call on every startup.
func ensureBundledCuaSkill() {
	paths, err := LocateBundledCuaDriver()
	if err != nil || paths == nil {
		// cua-driver isn't shipped on this platform — don't seed a skill
		// that references a binary the LLM can't run.
		return
	}

	targetDir, err := skillHubSkillDir(bundledCuaSkillSlug)
	if err != nil {
		log.Printf("[cua-skill] resolve skill dir: %v", err)
		return
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		log.Printf("[cua-skill] create skill dir: %v", err)
		return
	}

	// Resolve the daemon socket path the same way CuaDriverDaemon does
	// so SKILL.md teaches the LLM to point its CLI calls at the
	// already-running Yansu daemon instead of spawning its own. If the
	// path lookup fails (no $HOME, etc.) we fall back to an empty string
	// — the rendered body will tell the LLM to omit `--socket` and run
	// each call standalone (no element_index cache across calls), which
	// degrades but doesn't crash.
	socketPath := ""
	if p, err := cuaDaemonSocketPath(); err == nil {
		socketPath = p
	} else {
		log.Printf("[cua-skill] resolve daemon socket: %v", err)
	}

	upstreamSkillsDir := filepath.Join(paths.AppPath, "Contents", "Resources", "Skills", "cua-driver")

	if !writeManagedSkillFile(filepath.Join(targetDir, bundledCuaSkillFile),
		bundledCuaSkillContent(paths.BinaryPath, socketPath, upstreamSkillsDir)) {
		return
	}

	// Insert/refresh the state.json record. Preserve the user's
	// enabled flag and per-domain selections if they've toggled them.
	now := time.Now().UnixMilli()
	if err := withSkillHubState(func(state *skillHubState) error {
		existing, ok := state.Skills[bundledCuaSkillSlug]
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
		state.Skills[bundledCuaSkillSlug] = SkillHubInstalledSkill{
			ID:          bundledCuaSkillSlug,
			Slug:        bundledCuaSkillSlug,
			Name:        bundledCuaSkillName,
			Description: bundledCuaSkillDescription,
			Source:      bundledCuaSkillSourceTag,
			SourceURL:   "https://github.com/trycua/cua",
			Version:     bundledCuaSkillVersion,
			Path:        targetDir,
			Enabled:     enabled,
			Domains:     normalizeSkillHubDomains(domains),
			InstalledAt: installedAt,
			UpdatedAt:   now,
		}
		return nil
	}); err != nil {
		log.Printf("[cua-skill] update state.json: %v", err)
	}
}

// writeManagedSkillFile writes desired content to path when (a) the file
// doesn't exist, or (b) the file exists and contains the managed
// sentinel (so we won't clobber user edits). Returns true on success or
// no-op, false on a hard failure that should abort the whole seed.
func writeManagedSkillFile(path, desired string) bool {
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := writeAtomic(path, desired); err != nil {
			log.Printf("[cua-skill] write %s: %v", filepath.Base(path), err)
			return false
		}
	case err != nil:
		log.Printf("[cua-skill] read existing %s: %v", filepath.Base(path), err)
		return false
	case !strings.Contains(string(existing), bundledCuaSkillManaged):
		// User-edited file — leave it alone. Caller still registers the
		// skill in state so it appears in the UI.
	case string(existing) == desired:
		// Already up to date.
	default:
		if err := writeAtomic(path, desired); err != nil {
			log.Printf("[cua-skill] update %s: %v", filepath.Base(path), err)
			return false
		}
	}
	return true
}

// writeAtomic writes data to path via a same-directory tempfile + rename
// so a crash mid-write can't leave a half-formed SKILL.md (which the LLM
// would then read on demand and act on as if it were authoritative).
func writeAtomic(path, data string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// shellQuotePOSIX returns s safe for inclusion in a /bin/sh -c string. Paths
// containing whitespace or shell metacharacters are wrapped in single quotes;
// embedded single quotes are escaped via the standard POSIX sequence.
func shellQuotePOSIX(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`*?|&;<>(){}[]#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// bundledCuaSkillContent renders the full SKILL.md: frontmatter
// description (the only thing skillHubContext eagerly injects),
// followed by Yansu's runtime contract (bundled binary path, --socket
// invocation, policy-proxy rejection rules, CEF/Electron stop rule,
// TCC inheritance), and ending with absolute-path pointers to the
// upstream cua-driver SKILL.md and its sub-files. We deliberately do
// NOT duplicate upstream content (no-foreground discipline, canonical
// loop, intent map) — the LLM follows the tail pointer to read those
// directly from upstream when it needs that depth.
func bundledCuaSkillContent(binaryPath, socketPath, upstreamSkillsDir string) string {
	bin := shellQuotePOSIX(binaryPath)
	cua := bin
	socketLine := "_(daemon socket unknown — element_index cache will not persist across calls; the rest of this file still applies)_"
	if strings.TrimSpace(socketPath) != "" {
		cua = bin + " --socket " + shellQuotePOSIX(socketPath)
		socketLine = fmt.Sprintf("- Daemon socket: `%s` (Isoform starts and owns the daemon; you do NOT spawn it)", socketPath)
	}
	upstreamSkill := filepath.Join(upstreamSkillsDir, "SKILL.md")
	upstreamWebApps := filepath.Join(upstreamSkillsDir, "WEB_APPS.md")
	upstreamRecording := filepath.Join(upstreamSkillsDir, "RECORDING.md")
	upstreamTests := filepath.Join(upstreamSkillsDir, "TESTS.md")

	return fmt.Sprintf(`---
name: cua
description: %s
---

%s

# Cua Driver — bundled with Isoform

This file holds Isoform's runtime contract for the bundled cua-driver.
It augments — and where they conflict, overrides — the upstream
SKILL.md at `+"`%s`"+` (referenced again at the bottom for on-demand
Reading). Isoform's rules below win when the two disagree, because they
reflect what the runtime actually accepts.

## Bundled binary and daemon

- Binary: %s
%s

> Isoform spawns the daemon as a child process, so macOS attributes its
> Accessibility + Screen Recording grants to **Isoform**, not to the
> bundled cua-driver binary. Isoform already needs both grants for the
> activity recorder, so cua-driver inherits them with no extra prompt
> in the steady state. If a tool call fails with a permissions error,
> run `+"`call check_permissions '{\"prompt\":true}'`"+` to surface
> Isoform's TCC dialog (or tell the user to grant Isoform under System
> Settings → Privacy & Security → Accessibility / Screen Recording).

## Invocation prefix — paste this verbatim

	%s

Every cua-driver call uses that prefix. `+"`--socket`"+` makes the CLI
auto-attach to Isoform's already-running daemon so the per-pid
element_index cache survives across calls. Without it every command
spawns a fresh AppStateEngine and any `+"`element_index`"+` workflow
fails with "No cached AX state".

**Do not** run %s `+"`serve`"+` yourself. Isoform owns the daemon
lifecycle; spawning a second one fights for the same socket and
corrupts cache state.

## Policy proxy — what Isoform rejects at the socket

A policy proxy sits in front of the bundled cua-driver and hard-rejects
calls that would steal focus, returning a structured error. If you try
anyway you'll see `+"`{\"ok\":false,\"error\":\"yansu policy: ...\"}`"+`
— read the error and switch to the suggested alternative; do not retry
the same call.

The proxy actively blocks legacy `+"`hotkey`"+` calls for keystrokes
the WindowServer routes itself:

- `+"`cmd+Tab`"+` (app switcher) and `+"`cmd+\\``"+` (window cycler)
  are OS-owned and cannot be delivered to a background target.
- Older `+"`hotkey`"+` calls for browser-style `+"`cmd+L`"+` /
  `+"`cmd+1..cmd+9`"+` are still rejected for compatibility with the
  previous contract. Use `+"`press_key({pid, key, modifiers:[...]})`"+`
  for app-defined shortcuts in new flows; it goes through the daemon's
  process-level background activation path.

For URL navigation use `+"`launch_app({bundle_id, urls:[…]})`"+`. For
tab switching in a background browser, ask the user.

Everything else — `+"`click {pid, x, y}`"+`, `+"`click {ref:\"v*\"}`"+`,
`+"`drag`"+`, `+"`type_text`"+`, etc. — is now allowed. The bundled
daemon performs process-level background activation before reads and
actions, then routes input directly to the target pid/window so the
renderer commits without making the app visually frontmost. See the
"When the AX tree is empty" section in the upstream SKILL.md for the
full fallback chain.

For the broader no-foreground discipline outside the proxy
(`+"`open`"+`, `+"`osascript ... activate/launch`"+`, `+"`cliclick`"+`,
etc.) — covered exhaustively in the upstream SKILL.md's
"no-foreground contract" section. Read it.

## When `+"`get_window_state`"+` returns an empty AX tree

CEF/Electron shells (Cursor, VS Code, Discord, Slack) and most
Chinese desktop apps (网易云音乐, 微信, 钉钉, 飞书) paint their UI
inside a webview without exposing AX content nodes. The bundled
daemon handles this case automatically:

1. `+"`get_window_state`"+` auto-waits up to 1.5s for an AX wake-up
   (Electron apps sometimes populate late). The Isoform legacy surface
   includes a screenshot by default so chat can show exactly what the
   agent saw. Pass `+"`include_screenshot:false`"+` only for AX-only
   reads where you deliberately do not need an image.
   Read-only snapshots keep minimized windows minimized and use
   backing-store capture when possible. Slack / Feishu / other
   Chromium-shell windows may be restored from the Dock only for
   actions that need a visible background window; this is allowed as
   long as the target does not become frontmost and the user's current
   app keeps focus.
2. The response includes an `+"`ax_status`"+` field describing what
   the tree looks like — branch on it before reaching for
   `+"`element_index`"+`:
   - `+"`populated`"+` / `+"`woke_up`"+` → use `+"`element_index`"+`
     from `+"`ui_elements`"+` (the normal AX loop).
   - `+"`empty_after_wakeup`"+` → use a `+"`v*`"+` ref from
     `+"`visual_index`"+` (macOS Vision OCR with `+"`zh-Hans`"+`
     enabled, so `+"`每日推荐`"+` / `+"`推荐歌单`"+` resolve), or
     fall back to pixel `+"`click({pid, x, y})`"+`. If you explicitly
     disabled screenshots, re-run with `+"`include_screenshot:true`"+`
     first.
3. Both `+"`v*`"+` refs and pixel coords route through background
   pid/window delivery — no focus steal.

So the old "stop and ask the user to bring it forward" rule no
longer applies for AX-empty apps; only for cases where you can't
identify the target in `+"`visual_index`"+` and the screenshot
itself doesn't give you usable coordinates (icon-only UI with no
OCR-able text and no fixed layout you can rely on). Even then,
attempt a screenshot-based pixel click before escalating.

## Yansu-specific failure modes

- **`+"`yansu policy: ...`"+`** — the policy proxy rejected a legacy
  `+"`hotkey`"+` call. Read the error. For OS-owned app/window
  switching (`+"`cmd+Tab`"+`, `+"`cmd+\\``"+`), ask the user. For
  app-defined shortcuts, use `+"`press_key({pid, key, modifiers})`"+`
  so the daemon can prepare process-level background activation first.
- **`+"`Connection refused`"+`** on the daemon socket — Isoform's daemon
  isn't up yet; any `+"`/api/cua/*`"+` request from Isoform's HTTP layer
  triggers the spawn. From the LLM side, just retry the cua-driver
  call once after a moment.
- **First-run permission errors** —
  `+"`call check_permissions '{\"prompt\":true}'`"+` to surface
  Isoform's TCC dialog. Grants flow to Isoform, not to the bundled
  binary.

## Full upstream reference — Read on demand

The upstream cua-driver skill lives unmodified inside the bundled
CuaDriver.app. Read these files when the topic isn't covered above:

- `+"`%s`"+` — canonical loop, snapshot-before-and-after invariant,
  no-foreground discipline (the full list of focus-stealing shell
  calls the policy proxy can't see), behavior matrix, intent → tool
  map, native menu-bar navigation, web-rendered apps overview.
- `+"`%s`"+` — browser/Electron/Tauri patterns: tabs vs windows,
  omnibox navigation, AXWebArea inspection.
- `+"`%s`"+` — session recording for replay and debugging.
- `+"`%s`"+` — writing automated tests against cua-driver.
`,
		bundledCuaSkillDescription,
		bundledCuaSkillManaged,
		upstreamSkill,
		bin,
		socketLine,
		cua,
		bin,
		upstreamSkill,
		upstreamWebApps,
		upstreamRecording,
		upstreamTests,
	)
}
