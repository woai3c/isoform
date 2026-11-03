# Shadow Waiting-State Content

Status: Task 245 implementation contract on `vision/shadow-agent`.

## Goal

Shadow mode must explain a blocked or waiting production state without asking
the operator to decode a release-gate enum, a system process name, or a prompt
contract. The primary view should say what Shadow can do now, what it needs
next, and what other bounded learning remains available.

This milestone does not make a missing user result appear, replay old work, or
advance reliability, learning, permission, or authority.

## Live Problem

The current production gate correctly stops at `desktop_locked`, but the scan
path exposes `loginwindow`, `direct-text`, `compose.text`, and qualification
metric language before the operator understands the state. It also says only
to unlock the desktop even though Background learning is a separate bounded
capability that may continue from sources the user already allowed.

## Content Contract

The Shadow production gate follows five layers:

1. `Can Yansu observe your next task?`
2. one translated state, such as `Waiting for desktop unlock`;
3. one plain next step and a truthful description of separate Background
   learning;
4. readable task type and observation facts;
5. exact stage, foreground app, capability key, backend instruction, prompt
   versions, evidence IDs, and reasons under `Technical details`.

The primary scan path uses these task labels:

| Exact capability | Primary label |
| --- | --- |
| `compose.text` | `Writing` |
| `analyze.text` | `Reviewing text` |

Unknown capabilities use the existing readable fallback. The exact key always
remains available in technical detail.

## Waiting States

- `desktop_locked`: ask the user to unlock the desktop and work normally;
  explain that Shadow remains stopped while Background learning may continue
  separately when its own settings allow.
- `observer_paused`: ask the user to turn observation on before the next
  supported writing or review task.
- `frozen_context_insufficient`: explain that this task remains observation
  only and the next task may provide enough pre-start context.
- current task, attempt, result, comparison, and learning stages: name the
  missing lifecycle fact without using prompt-generation vocabulary.
- unknown stages: use the backend instruction as a fallback and retain the
  exact stage in technical detail.

The `Review background learning` command changes only the selected Developer
Tools view. It does not start a learning run, enable a source, change a daily
limit, or alter Shadow state.

## Background Learning Destination

The destination must preserve the same content hierarchy. Its primary scan
path shows a readable status, checks used and attempted, source readiness,
plain run outcomes, and counts of source records and learning items. A source
may be both allowed and not ready; those facts are stated separately so the UI
does not imply that permission and current availability are the same thing.

Raw scheduler reasons, policy versions, grant IDs, trigger reasons, failure
strings, and run summaries stay under `Technical details`. Hiding these values
from the scan path does not discard them or change the underlying receipt.

An opened run is organized as:

1. why the learning check started;
2. `What Yansu could access`;
3. `How Yansu accessed the source`, when Computer Use evidence exists;
4. `What happened`;
5. `Information reviewed`;
6. `Saved learning`.

Scope JSON, transport values, direct-connection flags, lifecycle enums and
payloads, hashes, source-item kinds, attribution, artifact kinds, and IDs remain
available in section-local technical disclosures. The readable historical
access summary is derived from that run's frozen scope, never from the current
source grant.

## Reliability Language

Qualification answers `Can Yansu match your writing reliably?` or the
equivalent task question. Metrics use:

- `Results compared`;
- `Different work contexts`;
- `Task understanding`;
- `Result match`;
- `Comparisons usable for learning`.

Counts distinguish current private attempts, older attempts not counted,
results still awaited from the user, ready comparisons, blocked attempts, and
safety concerns. Reliability remains explicitly separate from permission.

## Safety And Data Contract

This milestone must not change:

- observation, task onset, lifecycle, result capture, comparison, or learning;
- production rollout, quality thresholds, prompt selection, or model choice;
- Background learning settings, source grants, scheduling, or daily capacity;
- Memory, Knowledge, About you, priorities, permission, authority, or work;
- any RPC, database row, receipt, hash, or append-only ledger.

## Verification

- Tests cover locked, paused, active, and unknown gate instructions.
- The raw foreground app, stage, capability key, prompt versions, backend
  instruction, and reasons are absent from the primary scan path and visible
  after opening technical detail.
- The Background learning navigation command changes only the selected debug
  view.
- Background learning tests keep raw scheduler, grant, trigger, and failure
  values out of the primary scan path while preserving them in technical
  disclosures.
- Opened-run tests cover readable access, transport, lifecycle, reviewed-data,
  and saved-learning labels plus each exact technical record.
- Qualification tests cover the rewritten question, metrics, and counts.
- Full frontend tests and the production build pass.
- Live desktop and compact-width inspection verifies readable wrapping,
  functional disclosure and navigation, and no horizontal overflow.
