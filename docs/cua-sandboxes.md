# Sandboxes for Isoform: the CUA option

Date: 2026-10-15. Research only. Nothing was installed, downloaded or run. Items marked "(unconfirmed)" could not be checked against a primary source.

## Bottom line

CUA's macOS VM is still the only option checked that gives a separate macOS desktop and user account. It fits unattended computer use (risk 2) best. Plugins (risk 1) fit a capability-limited runtime such as WASI better. For headless Claude Code and Codex runs (risk 3), Claude Code now has a no-Docker option on macOS: its sandbox runtime, which is beta. A Linux container or VM remains the stronger fallback. Run a spike that needs no CUA install first, and get your OK before any large download.

## 1. What CUA's sandbox offering is

From the README at github.com/trycua/cua (checked 2026-10-15):

- **Lume**: VM manager for macOS and Linux guests on Apple silicon. MIT. Release v0.6.1 is dated 4 Oct (stable label unconfirmed). A nightly v0.6.2 dated 15 Oct is pre-release.
- **Lumier**: Docker-compatible interface for Lume VMs. Not marked deprecated.
- **cua SDK and CLI**: Python, TypeScript and Swift SDKs, with Kotlin as generated bindings. No Go SDK.
- **cua-spacesd**: in-sandbox daemon on port 3211 with processes, files, desktop and streaming over gRPC. MCP and the health endpoint are unconfirmed.
- **Cua Driver**: background computer-use agent for macOS, Windows and Linux. MIT. Isoform already vendors it in `wails-gui/cua-driver-src` (confirmed locally). It is host-side, not a sandbox.
- **Cua Spaces**: desktop app for agents on machines you can watch and take over. Installs on macOS 26 or later. Free at launch is a third-party claim only (unconfirmed).

Deprecated per the README: cua-computer, cua-computer-server, cua-agent, cua-som and cuabot. The README says of cuabot: "Do not run it: it has known security issues."

Licences: everything outside Cua Spaces and Cua Driver Bench is MIT. Cua Spaces and cua-spacesd are FSL-1.1-MIT. FSL converts to MIT, or Apache 2.0 for the ALv2 variant, two years after each version is made available (fsl.software, checked 2026-10-15).

Hosting: local macOS sandboxes run on your own Mac through Apple's Virtualization framework. Cloud Windows is generally available. Cloud macOS is an invite-only early preview on bare-metal M1, M2 and M4 hosts (cua.ai/blog/cloud-windows-ga-macos-preview, checked 2026-10-15). The Windows GA date of 2025-11-17 is unconfirmed.

Apple's licence allows up to two additional macOS instances per Apple-branded Mac you own or control. It covers software development, testing during development, macOS Server use and personal non-commercial use. It excludes service bureau, time-sharing and terminal-sharing use. This is from older licence text (apple.com/legal/sla, checked 2026-10-15, did not show Tahoe's text). The framework refused a third active guest in a 2022 test (eclecticlight.co, 2022-08-04). This is my reading, not legal advice.

## 2. Best option and fallback for each job

### A. Third-party plugins (risk 1)

Plugins run as the user and reach files and the network directly (`docs/plugins-design.md`, section 3).

- **Best: a capability-limited runtime.** Plugins that only read files and call HTTP can run in WASI or a JavaScript isolate, with no ambient authority. wazero is a pure-Go WebAssembly runtime with no cgo and built-in WASI preview1 (github.com/tetratelabs/wazero, checked 2026-10-15). Its WASI 0.2 status is unconfirmed.
- **Fallback: a Linux container** (Apple `container`, or a CUA Linux sandbox) for plugins that need a real Linux toolchain or native binaries. CUA's Linux isolation strength is not documented.
- **Plugins that need macOS APIs** need an App Sandbox helper with its own bundle ID. The plan's TCC test already covers this.
- **Do not treat Node `--permission` as a boundary.** Node's official docs say it guards against mistakes and gives no guarantees against malicious code. Several CVEs in 2025 and 2026 bypassed it (third-party advisory sites, checked 2026-10-15). Deno flags are not checked.
- **Cost:** a WASI runtime adds little disk or RAM (not measured). A Linux container adds an image and a VM or container runtime.
- **Does not work:** plugins that need the user's signed-in browser, real apps or files. A macOS VM is more than this job needs.

### B. Unattended playbooks and computer use (risk 2)

This is where CUA fits best. The guest is its own macOS desktop with its own accounts. Host Screen Recording and Accessibility grants do not reach it.

- **Best: a Lume macOS guest on Tahoe**, one of the two slots. Lume docs say Tahoe is the verified preset and Sequoia can still show an Accessibility step on first boot (cua.ai Lume docs, checked 2026-10-15). Issue #2155 on that step was closed 2026-08-12 via PR #2156. It was reported on Lume 0.3.11.
- **Fallback: a Linux sandbox for browser-only playbooks.** This keeps macOS for native apps.
- **Cost:**
  - Host: macOS 13 or later per the Lume install page. The sandbox reference says macOS 14 or later. Plan on 14 or later.
  - Disk: about 50 GB free per macOS guest per the Lume install page. The sandbox reference says about 40 GB. Plan on 50.
  - RAM: 8 GB free memory per both Cua pages. Host sizing of 16 GB is a secondary source (unconfirmed).
  - Image: `ghcr.io/trycua/macos:26` is 23 GB. The first pull takes about 8 minutes. Later clones are ready in about 25 seconds (desktop sandbox page, checked 2026-10-15). Prebuilt images may be unmaintained (see Risks).
  - Slots: two macOS guests per Mac, per Cua's blog and the framework limit.
- **Does not work:**
  - The user's signed-in browser sessions and saved logins. Accounts must be signed in again inside the VM.
  - The user's real apps and files, unless shared. Shared-folder syntax is unconfirmed.
  - The host Cua Driver, which drives host apps. Check the guest image for a duplicate driver. I found no Cua doc that says not to install Cua Driver in a sandbox.
  - App teleportation, which hands a live signed-in session to an agent. Not checked. Avoid it for unattended runs.
- **Setup:** the `tahoe` unattended preset uses user `lume` with password `lume`. Change the password before the VM is reachable (Lume install page, checked 2026-10-15). Telemetry is on by default. Run `lume config telemetry disable` (Cua telemetry docs via search; the page fetch failed).

### C. Headless Claude Code and Codex runs (risk 3)

Backlog items 14 and 16 move unattended work to Claude Code and Codex running headless on the user's Mac.

What the vendors document:

- **Claude Code's built-in sandbox** uses Seatbelt on macOS. It covers Bash and its child processes only. Read, Edit and WebFetch follow permission rules. Hooks, MCP servers, the status line and `apiKeyHelper` run with full access. It fails open by default. `sandbox.failIfUnavailable: true` makes Claude Code exit instead. `allowUnsandboxedCommands: false` disables the retry escape hatch. Commands typed at the `!` prompt usually run unsandboxed (code.claude.com/docs/en/sandboxing, checked 2026-10-15).
- **Claude Code's sandbox runtime** wraps the whole Claude Code process in Seatbelt on macOS, with no Docker. It constrains file tools, MCP servers and hooks as well as shell commands. It denies network and most writes by default, so write the config (`~/.srt-settings.json`) before the first launch. It is a beta research preview. The docs say unattended `--dangerously-skip-permissions` sessions should run inside a container, a VM or this runtime (code.claude.com/docs/en/sandbox-environments, checked 2026-10-15).
- **Codex** runs workspace-write as its local default, with network off by default. Per official docs it turns on with `sandbox_workspace_write.network_access = true`. On macOS, issue #10390 reports that the Seatbelt profile ignores this. Only the CLI flag reportedly works (developers.openai.com/codex/security and openai/codex#10390, checked 2026-10-15). `danger-full-access` removes both limits. Codex's MCP handling is not checked.
- **Seatbelt itself** (`sandbox-exec`) is marked DEPRECATED in its man page, which points app developers to App Sandbox. Third-party sources say Apple has no replacement for command-line sandboxing. It still works on current macOS (sandbox-exec man page via leancrew.com, checked 2026-10-15).

Options:

- **Best for Claude Code on the Mac: the sandbox runtime.** It needs no Docker and no VM, and covers the whole process. It is beta, so the spike must test it.
- **Best for stronger isolation of either CLI: a Linux container or VM,** with only the workspace mounted and network on an allowlist. The candidate is Apple's `container` (github.com/apple/container, Apache-2.0, checked 2026-10-15). Its README says it runs Linux containers "as lightweight virtual machines on your Mac," supports macOS 26 and needs Apple silicon. Per-container VMs, the 1 GB and 4 CPU defaults, and version 1.0.0 (June 2026) come from secondary sources only (unconfirmed).
- **Fallback for Codex: stay on its Seatbelt sandbox.** Network on macOS is blocked unless the CLI flag is used. An egress allowlist would have to sit outside Codex (unconfirmed how).
- **Does not work:** native macOS work (Xcode, Keychain, iMessage, the user's apps). These need a macOS guest and a slot. Anything that needs the user's signed-in accounts. Pass narrow grants, not the whole session.
- **Cost:** the sandbox runtime and Seatbelt add little. Container image disk and VM RAM are not measured. Docker Desktop's RAM figure is not checked.

### D. A "try it safely" mode for new playbooks

- **Best: a disposable clone.** Clone a prepared macOS base in about 25 seconds, run the new playbook against fixtures, then delete the clone. This holds a macOS slot while it runs.
- **Fallback: a dry run.** The playbook plans its steps and shows each side-effect action for approval, without sending input. Non-GUI steps can run in a container with fake data. This needs no VM and fits Isoform's current approval model.
- **For Claude Code runs,** the sandbox runtime can serve as a dry-run boundary.
- **Does not work:** a dry run cannot confirm that a click lands. Only a real clone can.

## 3. Spike first (needs your OK before any download or install)

Sizes come from the sources above. Where none exists, the spike measures them. Steps 0 to 2 need no install.

0. **Check the prebuilt image.** Confirm in Lume docs whether `ghcr.io/trycua/macos:26` is still maintained, before any pull.
1. **App Sandbox helper (plugins).** Build a small helper with its own bundle ID and App Sandbox entitlement. Request Accessibility and Screen Recording. Check whether those grants stay separate from Isoform's on macOS 26. Measure start time and RAM.
2. **WASI or isolate runtime (plugins).** Run one sample plugin that reads a file and calls HTTP. Measure start time and RAM, and what happens when it reads outside its granted folder.
3. **Claude Code sandbox runtime (headless, no Docker).** Install the npm package. Run one Claude Code task in a throwaway folder. Check that file writes outside the grant, a hook and a local MCP server are all blocked. Test `failIfUnavailable`.
4. **Codex network and Apple `container`.** Test whether Codex network works on macOS with the CLI flag. Run a Claude Code or Codex image in Apple `container` with the workspace mounted and an egress allowlist. Measure cold and warm start, idle RAM, image disk, and whether the allowlist blocks other hosts.
5. **CUA macOS guest (playbooks), only if steps 1 to 4 pass.** Install Lume, disable telemetry, change the default password, and pull the image. Measure pull time, disk, clone time and RAM. Check whether input works with no host grants, whether focus stays with the user's app, and whether a third guest is refused.

Needs your OK: every download (Lume, the 23 GB image, Apple `container`, the npm sandbox runtime, CLI images), creating any VM, and installing Cua Spaces.

## 4. Risks and open questions

- **Apple licence.** Up to two guests on your own Mac for your own development or personal use appears to fit. Tahoe's exact text is unconfirmed. VMs that run other people's agents, such as a hosted Isoform service or cloud macOS, look like time-sharing, which the licence excludes. Get counsel before any hosted service. Linux guests fall outside this clause in my reading.
- **FSL on a dependency.** Only cua-spacesd and Cua Spaces are FSL. The SDK, CLI, Lume and Cua Driver are MIT.
- **Preview and status.** Cloud macOS is invite-only. Cua's README says Pro and Teams plans are "coming soon" with no date.
- **Prebuilt images.** A Lume doc snippet says prebuilt images are no longer actively maintained (unconfirmed on a fetched page).
- **Telemetry and defaults.** Lume telemetry is on by default. The `tahoe` preset uses `lume` / `lume`.
- **Missing numbers.** Lume default CPU and RAM per VM, boot time, network mode, shared folders and snapshots are unconfirmed. The Lume API port (7777 in a 2025 blog) is not on the install page. Focus behaviour is unconfirmed and must be tested.
- **No Go SDK.** Isoform would call the `cua` CLI, the `cua daemon` socket, or cua-spacesd gRPC directly.
- **Unauthenticated endpoint.** The GET /health claim is unconfirmed. Check cua-spacesd exposure before running it on a shared network.
- **Isolation strength.** Neither CUA's Linux sandboxes, Apple `container` nor the Claude Code sandbox runtime documents its isolation strength. Anthropic's own docs say sandboxing reduces impact but does not eliminate risk.
- **Node permission model.** Not a security boundary. Use OS isolation for untrusted code.
- **Release dates.** The "16 Oct" entries on the releases page are probably UTC timestamps, not future dates.

## Questions for you

1. Is it acceptable that sensitive accounts live inside a VM and must be signed in again?
2. Which playbooks really need a GUI? If most need only a browser, the Linux path may be enough.
3. Will Isoform ever run for other people? That decides the Apple licence question.
4. Is an FSL-licensed dependency acceptable for cua-spacesd and Cua Spaces?
5. Is a beta dependency (the Claude Code sandbox runtime) acceptable for headless Claude Code runs?

## Sources (all checked 2026-10-15)

- https://github.com/trycua/cua (README)
- https://github.com/trycua/cua/releases and https://github.com/trycua/cua/releases/tag/lume-v0.6.1
- https://github.com/trycua/cua/issues/2155
- https://cua.ai/docs/cua/reference/desktop-sandbox/macos
- https://cua.ai/docs/how-to-guides/lume/install-lume
- https://cua.ai/blog/cloud-windows-ga-macos-preview
- https://github.com/apple/container
- https://developer.apple.com/forums/thread/826102
- https://eclecticlight.co/2022/08/04/virtualisation-on-apple-silicon-macs-8-how-apple-limits-vms/
- https://www.apple.com/legal/sla/
- https://fsl.software/
- https://code.claude.com/docs/en/sandboxing
- https://code.claude.com/docs/en/sandbox-environments
- https://developers.openai.com/codex/security
- https://github.com/openai/codex/issues/10390
- https://forums.swift.org/t/sandbox-exec-is-deprecated/10201
- https://github.com/tetratelabs/wazero
- https://r2.nodejs.org/dist/v26.2.0/docs/api/permissions.json (Node permission docs)
