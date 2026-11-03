# Isoform skill for assistants

The instruction-only `isoform` skill lives in
`yansu-agent/isoformskill/SKILL.md`. It is derived from the existing Yansu skill's
digest, recall, blocker analysis and handoff workflows, updated for the read-only
[Isoform MCP interface](isoform-mcp-server.md). The CLI keeps the `yansu` name.
The skill includes context and authority boundaries, the nine read tools,
bounded-result handling, CLI discovery, and the existing response contracts for
chat, one-time work and playbooks. It contains no user context or credentials.

## Consent and installation

Assistant setup and the advanced external-assistant configuration show
"Install the Isoform skill into Claude Code and Codex", checked by default.
Saving that explicit choice records consent. Merely opening settings, detecting
an assistant, or upgrading an existing configured installation does not install
anything. Existing users can accept the pending default from the Assistant
settings skill row. Unchecking the option revokes skill installation.

The choice is shared by all Isoform app flavors through
`~/.isoform-assistant-skills/consent.json`, because the assistant skill folders
are also shared. A file lock serializes consent changes and startup refreshes
across app processes. A flavor-local preference cannot restore a revoked shared
choice; missing shared consent requires a new explicit choice. The shared record
remains after revocation so another app flavor cannot silently reinstall files.
Each explicit choice also advances a shared revision. A delayed save must still
match the revision it observed; a choice made in another app flavor makes that
save stale, so it cannot undo a later revocation. Refresh and retry after a
conflict to make a new choice.

The desktop installs managed copies into:

- `~/.claude/skills/isoform/SKILL.md`
- `~/.codex/skills/isoform/SKILL.md`

These are the per-user locations requested for the supported assistants.
The local Codex installation already discovers skills from `~/.codex/skills`;
`codex --help` exposes no skill-install subcommand. OpenAI's
[skill evaluation guide](https://developers.openai.com/blog/eval-skills) documents
that user-scoped folder. Newer
[local skill guidance](https://learn.chatgpt.com/docs/build-skills) also describes
the shared `~/.agents/skills` location. This installer does not populate additional
assistant roots or edit assistant configuration. Installations using custom
configuration roots can copy the bundled skill manually; Isoform-run tasks carry
their own skill instructions.

Each copy has a managed footer with a format version, bundled version and content
hash. Unchanged files are left alone. On startup, previously accepted installs
refresh automatically; an unrecorded preference never causes a home write.
Updates and removal refuse symlinks, non-regular files and unowned or edited
copies. Revocation deletes only the verified managed file and its directory if
empty, preserving additional user files. A failure in one assistant does not
prevent reconciliation of the other. Settings offers a retry without exposing
raw filesystem errors. Revocation is persisted before cleanup, so failed cleanup
cannot trigger reinstallation at the next startup.

Optional skill failures do not block assistant setup. If the preference cannot
be read, setup leaves it unchanged and offers a warning with a retry in Settings.
A delayed setup save cannot overwrite a newer explicit skill choice, including
a revocation whose cleanup is still pending.

Skill consent is independent of model consent and MCP read grants. The skill
does not register a server, issue a token or authorize actions. Turn off the skill
option to remove managed copies; revoke an MCP connection to deny data reads.
Customized copies remain the user's files and must be removed manually.

## Handoffs

One-time work and playbooks name the skill while retaining their existing
`handoff-result` and `<playbook_result>` response contracts. The shared external
runner adds the invocation and bundled skill inline to every assistant turn,
including when installation was declined or revoked. Controlled runs disable
user skills and tools and use only the per-run tool server. No isolation or
action gate is relaxed to make the skill available. Assistant replies are the
report-back channel for Isoform-originated runs. Independently opened assistants
reply locally; the read-only MCP server has no completion or write tool.

## Verification

Installer tests exercise real files in disposable homes: fresh install, version
update, idempotence, revocation for both assistants, local edits, extra files,
conflicts and unsafe filesystem targets. Settings lifecycle tests cover explicit
consent, restart refresh, persisted revoke after a cleanup failure and config
write failures, shared consent across two app data roots, stale-revision rejection,
process locking and unsafe consent targets. Prompt tests cover both assistant formats, controlled runner
isolation, one-time work and playbook contracts. Frontend tests cover explicit
setup/save consent, read-only mounting, opt-out, retry, delayed-save and reopened
picker and overlapping-choice races, non-blocking preference/install failures
and both locales. The existing Quick
Chat stub coverage guard checks that both new settings bindings are exported.

No app launch or real-home installation is required for these checks. The hub
owns the running app and any final interactive check.
