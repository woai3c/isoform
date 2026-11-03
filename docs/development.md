# Development

> This document preserves the previous root README. Identity, paths, and release behavior described below may reflect older builds; check the current scripts before using them. Dev builds can access real local data, so choose the flavor deliberately.

This repository contains the desktop app (`wails-gui/`) and its agent runtime (`yansu-agent/`), split out of the Isoform/something monorepo at `main` commit `0971ed84e`. See [wails-gui/README.md](../wails-gui/README.md) for the full desktop app documentation and [yansu-agent/README.md](../yansu-agent/README.md) for the agent runtime.

## Technology Stack

### Desktop App (Wails GUI)
- **Framework**: [Wails v2](https://wails.io/) (Go + native WebView, not Electron)
- **Backend**: Go 1.25+, SQLite (via `modernc.org/sqlite`)
- **Frontend**: React 18 + TypeScript + Vite + TailwindCSS 4
- **Key features**: Activity monitoring, automations, memory/knowledge management, multi-channel integrations
- **See**: [wails-gui/README.md](../wails-gui/README.md) for full architecture

## Getting Started

### Prerequisites
- Go 1.25+ (required by Wails GUI)
- Node.js 20.19+ and pnpm
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) (for desktop app development): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

### Wails GUI (Desktop App) Development

The desktop app lives in `wails-gui/` and is built with **Wails v2** (Go + native WebView), **not Electron**. See [wails-gui/README.md](../wails-gui/README.md) for full architecture details.

#### Quick Start (Dev Mode)

```bash
./wails-gui/scripts/start-wails-gui.sh
```

Development and release builds use the Isoform identity and data directory by default. Development builds may access existing local data; inspect the scripts and choose the app flavor deliberately.

This script handles everything: checks prerequisites, downloads the bundled Bun runtime, builds the in-tree yansu CLI, CuaDriver, and Quick Chat Spotlight bundles, starts the Vite dev server on port 5173, and runs Wails with hot reload (Go rebuilds on `.go` changes, HMR for frontend).

Options:
- `--skip-cli` — Skip bundled Yansu CLI check
- `--skip-bun` — Skip Bun runtime check

#### Production Build

**Always use the build script** — never run raw `wails build`:

```bash
./wails-gui/scripts/build-wails.sh                           # full build
./wails-gui/scripts/build-wails.sh --platform darwin/arm64   # cross-compile
./wails-gui/scripts/build-wails.sh --version 0.2.0           # inject version
./wails-gui/scripts/build-wails.sh --dmg                     # create .dmg (macOS)
./wails-gui/scripts/build-wails.sh --flavor isoform          # development identity: Isoform.app (see Common Pitfalls)
./wails-gui/scripts/build-wails.sh --flavor isoform --install # build, sign and install ~/Applications/Isoform.app
./wails-gui/scripts/build-wails.sh --flavor isoform --install --dry-run # inspect the plan, including on Linux
./wails-gui/scripts/dev-signing-identity.sh create           # one-time: signing identity for dev builds (see Common Pitfalls)
```

The build script embeds frontend dist and bundled binaries via Go embed, handles code signing on macOS, and injects version via ldflags.

For dogfooding, use `--flavor isoform --install`, then launch `~/Applications/Isoform.app`. Installation uses macOS system `rsync` to replace the bundle contents, remove stale files, preserve symlinks and extended attributes, and verify the copied signature without re-signing. The release flavor installs to `~/Applications/Yansu.app`. Existing apps without this installer's signed resource marker are refused; move an unmanaged app aside before running `--install`. Start at Login prefers an installed app for the current flavor only when its signed `Info.plist` declares the supported login-command protocol. Older or incompatible installed copies are never launched to probe their capabilities; login management falls back to the current bundle's native service. `--dry-run` prints the build and install plan without downloading, building or copying anything; it works on Linux. Check shell syntax with `bash -n wails-gui/scripts/build-wails.sh`.

> **Warning**: Running raw `wails build` will miss code signing, entitlements, and embedded bundles, causing macOS permission (TCC) resets and broken functionality.

#### Download Bundled Assets Manually

```bash
./wails-gui/scripts/download-bun.sh       # Bun JS runtime
./wails-gui/scripts/download-ffmpeg.sh    # FFmpeg (for activity video)
./wails-gui/scripts/download-git.sh       # Bundled git
```

#### Testing

```bash
cd wails-gui && go test ./...              # Go tests
cd wails-gui && go run ./cmd/pii-test      # PII detection tool
cd wails-gui/frontend && pnpm test         # Frontend unit tests
```

#### Common Pitfalls

- **Do not use `wails build` directly** — always use `wails-gui/scripts/build-wails.sh` which handles code signing and bundle embedding
- **TCC permission resets**: macOS keys an app's privacy grants (Screen Recording, Accessibility, Microphone and the rest) to its code-signing designated requirement. An ad-hoc signed build's requirement is its own cdhash, so every rebuild invalidates the previous build's grants: System Settings keeps showing the switches on, but macOS denies the new binary and prompts again, and refreshing the permission state in the app changes nothing. One-time fix: run `./wails-gui/scripts/dev-signing-identity.sh create`. It creates a self-signed "Isoform Dev" certificate in a keychain of its own under `~/.isoform-agent/dev-signing/` (no password and no dialogs; the keychain is not added to the search list and the certificate is not trusted, neither is needed to sign). From then on `build-wails.sh --flavor isoform` signs Isoform.app and its nested helpers with that identity, the requirement stays `identifier "com.wails.Isoform" and certificate leaf = H"..."` across builds (check with `codesign -d -r- wails-gui/build/bin/Isoform.app`), and the grants persist; an identity-signed build clears them only when they were made for a differently signed build or there is no record of one (`reset-dev-permissions.sh --if-signature-changed` keeps the record in `~/.isoform-agent/dev-signing/privacy-grants-requirement`, per user, so builds from several worktrees share it; ad-hoc builds and `--keep-permissions` runs drop it). `start-wails-gui.sh` does the same for `wails dev`: the `postBuildHooks` entry in `wails-gui/wails.json` runs `scripts/sign-dev-build.sh` after every Go rebuild (its log: `~/.isoform-agent/dev-signing/sign-dev-build.log`). Without the identity `build-wails.sh` clears the Isoform grants after every build and `start-wails-gui.sh` clears them once before `wails dev` (`wails-gui/scripts/reset-dev-permissions.sh`; `--keep-permissions` skips it), so the app asks once per build, and a Go rebuild during a `wails dev` session breaks them again. `dev-signing-identity.sh remove` deletes the identity and clears the grants keyed to it; do that when not working on Isoform, since the key signs without a prompt (see the security note in `wails-gui/README.md`). Release builds with `APPLE_SIGNING_IDENTITY` are unaffected. Avoid unnecessary rebuilds when only testing frontend changes
- **Port conflicts**: If port 5173 is in use, see [HOT_RELOAD_TROUBLESHOOTING.md](../wails-gui/HOT_RELOAD_TROUBLESHOOTING.md) for cleanup steps
- **This is NOT Electron**: Do not use Chrome DevTools Protocol, `electron-mcp`, or any Electron-specific tooling. Debug via Wails devtools and console logs
- **Runtime data**: All stored in `~/.yansu-agent/` (SQLite DB, extracted bundles, the model sidecar's state)
- **Product name**: user-facing copy says Isoform (since 2026-10-12); module names, bundle ids, data folders, headers and artifact names keep `yansu`
- **No sign-in**: there is no Yansu account. The app opens straight into the product; the profile is the local People "self" identity, and the Slack, Discord and Telegram connectors run as user-built bots on this Mac

## Model accounts

Isoform does not ask for API keys. Every model call goes through [magpie](https://github.com/yetone/magpie): in either magpie Isoform thinks through a routing group it owns, named Isoform (id `isoform`), shaped like magpie's "Never stuck" group: a copy of the user's never-stuck group when they have one, else magpie's steady template (the strongest model of each signed-in provider, each behind the other), else the members Isoform planned itself. When the user has their own magpie installed (binary plus `~/.config/magpie`), Isoform talks to that gateway, adds only that one group and never touches their other groups or agent settings (if the group cannot be made it falls back to the group Claude Code is set to, else the first one listed); otherwise a bundled copy runs as a local gateway with its own state and routes every call to the accounts already signed in on the Mac (Claude Code, Codex), starting from the model each agent currently uses, moving to the next one when one runs out; the bundled copy keeps the `yansu-light` group for cheap work, and a key and a local Ollama model are later members of the same routing groups. Onboarding's Tools step shows which magpie is in use and installs a missing Claude Code or Codex CLI with npm. A user with none of these sees the model-capacity card offering those options; a hosted fallback was deferred (its contract is kept in `docs/hosted-credits-gateway.md`). Settings > AI model shows the accounts, their quota windows and the chat, background and light routing order; the key form stays available under Advanced. Long unscoped work can run on a signed-in Claude Code or Codex, and a chat can be switched to that CLI from the composer, while scoped, ephemeral and screen-level work always runs on Yansu's own loop. Download the gateway for bundling with `./wails-gui/scripts/download-magpie.sh`.

## Notifications and the phone

Proactive outreach goes through one Engagement service (`wails-gui/engagement_*.go`, `wails-gui/internal/engagement`): producers turn observed work, the Purpose Map, handoffs and briefs into moments; the service scores and paces them (quiet hours, a daily interruption budget, digests, cooldowns) and sends each to the Mac, the phone or, as a last resort for time-sensitive moments, a text from Messages.app to the user's own number. Pushes carry sealed notices that the relay cannot read; the iPhone's notification extension decrypts them and offers Approve, Decline, Snooze and Open. Every kind has a switch in Settings > iPhone on the Mac and in Settings on the phone, with the counts of the last 30 days; a kind the user keeps dismissing pauses itself until it is turned on again.

## iPhone Companion

Yansu for iPhone (`ios/`) is the first contact point: a daily brief the desktop generates from the work it observed, a Chat with the Mac that can also hand the Mac one-time work, corrections to what Yansu learned, and a Home Screen widget for the latest brief. The phone pairs with the Mac by scanning a QR code; traffic travels through the end-to-end-encrypted relay in `relay/`, which stores only ciphertext and credential hashes, and the Mac only ever dials out. The contract shared by the three codebases is `mobile-protocol/PROTOCOL.md`.

- Relay: `cd relay && go test ./...`, run locally with `ISOFORM_RELAY_ADDR=127.0.0.1:8791 ISOFORM_RELAY_STATE_DIR=/tmp/isoform-relay go run ./cmd/isoform-relay`, deploy with the `Dockerfile` and `compose.yaml` in `relay/`.
- Desktop: Settings > iPhone enables the relay connection, shows the pairing code and the daily brief schedule. The Go side lives in `wails-gui/mobile_*.go`.
- iPhone: `./scripts/build-ios.sh` builds for the simulator; see `ios/README.md` for pairing in the simulator (paste the invitation link) and what is not provisioned (Apple team, APNs key).
- People: Settings > People can import contacts from the Mac's Contacts after an explicit consent, or from a vCard file on any platform; imported people stay on this Mac and are never researched automatically.

## Releases

The [release pipeline](release/pipeline.md) defines the PR checks, launch protection gates, secret setup, stable and beta channels, local fallback, and clean-machine update test. `.github/workflows/release.yml` runs on a `vX.Y.Z` tag or an explicit dispatch. Both architectures must be signed, notarized, stapled and verified before publication to `yubozhao/isoform-releases`.

Use `./scripts/release/ship.sh VERSION --channel stable|beta --dry-run` for a signed local rehearsal; `--publish` is a separate release action. The beta feed and prereleases do not move the stable latest release. Never publish either channel without the applicable release authorization.
