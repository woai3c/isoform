# Editing Assistant text

Settings > Assistant lets the user edit the persona, their own context, standing
instructions, and each learned Self statement and scope. Save and Cancel apply
per field. Reset restores that field's learned text; new profile notes reset to
empty. For a directive, Reset restores its wording from before the first edit.
The persona's learned version is the current SOUL.md file. An active user edit
is stored separately and takes precedence over changes to that file.

Account availability, usage, and connected tools are reported facts. The page
explains their source and keeps their existing setup controls. Directive text
and scope can change, while the existing deny-only tool policy stays intact.
These prose fields never grant execution authority.

## Persistence and review

The named activity migration `assistant_text_edits` adds an immutable edit ledger
with field identity, value, learned value, action and timestamp. No numbered
storage schema version changes. Canonical Self text edits and provenance commit
in one SQLite transaction. Directive edits also append their validated lifecycle
event in that transaction. Each save and reset checks the revision displayed by
the editor, preserving stale drafts instead of overwriting a newer revision.

Open drafts live independently of the current field list. If Self Dream replaces
a field, its compilation links attach the draft to a unique surviving successor,
including across successive replacements. The current text is shown and the user
must choose to use that field before saving against its revision. Multiple
successors or competing drafts keep the unmatched text in a recovery editor for
copying or explicit discard. Stale Save does not refresh the list; the user can
load current fields, and later background refreshes still retain their draft.
Drafts last while the Assistant page is open; they are not persisted to disk.

Self edits retain their original source evidence and privacy gates. Forgetting
a source memory also erases its derived edit history through the existing
confirmed erasure transaction, including learned baselines. A new Self
Dream proposal targeting edited text requires user review, including exact
duplicate merges. Compilation checks the live targets again before automatic
promotion. Background requalification also preserves edited revisions. An older proposal becomes stale after an edit. Accepting a replacement
through the existing review flow ends that revision's live edit protection and
retains history. Reset releases protection for its own field only. User
directives remain in their dedicated lifecycle ledger and cannot be rewritten
by Self Dream.

The persona and explicit profile notes use the private-owner prompt path on the
computer and phone. Shared and unpaired connector conversations do not receive
them. `GetMainChatSetup` continues to report onboarding history and permissions;
Assistant text editing does not rewrite those facts or permission decisions.
The browser preview can read these fields and explains that editing requires the
computer app. Edit and Reset are disabled there; mutations and Self Dream review
remain native-only under the existing browser RPC policy.

## Preview

The headless component preview uses synthetic text and demonstrates an open
inline editor, persisted provenance, per-field resets and the existing review
card. No desktop app or Simulator was launched.

![Assistant inline editor with Save, Cancel, provenance and resets](hub-reviews/assets/assistant-editable.png)

The recovery preview replaces a synthetic Self field while its correction is
open. The text stays in the editor alongside the successor's current text.

![Assistant correction retained after a Self Dream replacement](hub-reviews/assets/assistant-editable-recovery.png)

## Rollback

Revert the feature commit. The additive activity table and immutable edit history
can remain; older binaries ignore them. Existing Self and directive revisions
remain valid. Reverting the code stops applying the persona/profile overrides;
it does not erase edits from the audit ledger or undo canonical Self revisions.
To undo a text edit, use its Reset control before reverting the feature.
