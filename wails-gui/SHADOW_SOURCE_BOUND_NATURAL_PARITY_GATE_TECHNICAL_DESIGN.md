# Source-Bound Natural Parity Gate Technical Design

Status: implemented and validated for Task 197 on `vision/shadow-agent`

## Purpose

The current `task-shadow-release-gate-v4` proves a persisted chain from a Task
Episode through an isolated Shadow result, independently observed user result,
comparison, and learning projection. It does not yet prove that the selected
Task Episode was created by the exact production activity-group decision now
covered by Tasks 195 and 196.

That leaves a provenance gap: a historical, manually seeded, continuation-only,
or otherwise untraceable Task Episode could satisfy every later gate step.

Task 197 advances the derived release gate to v5 and answers:

> Did one exact covered production observer source create this Task Episode and
> bind it to this current-generation Shadow run before any result comparison?

This is a release-proof constraint. It does not change task detection,
execution, comparison, learning, qualification, or authority.

## Existing Evidence

The production observer terminal receipt already freezes:

- the v2 trace root and exact activity pipeline-run ID;
- admission and flow;
- ordered source session identities;
- production onset attempt IDs;
- created Task Episode IDs;
- workflow readiness decisions with source and frozen context hash;
- created Shadow run IDs and dispatch state; and
- a complete immutable event hash chain.

The Task Episode debugger already resolves:

- the selected Task Episode and frozen context;
- current-generation Shadow runs;
- materialized user and Shadow results;
- destination provenance;
- current evaluation IDs; and
- evaluated-only learning projection receipts.

Task 197 joins these existing ledgers. It creates no new evidence table.

## Threat Model

The v5 proof must not complete from:

- a v1 observer trace or a pre-instrumentation source row;
- a v2 trace whose source row was cleared or pruned;
- source ID zero, root-only writeback, pending, failed, missing, mismatched,
  corrupt, duplicate, or scan-truncated coverage;
- a prefiltered or cooldown continuation trace that merely mentions an existing
  Task Episode;
- a reconciliation decision rather than the captured onset decision;
- multiple eligible roots claiming the same Task Episode origin;
- an onset decision bound to another workflow, frozen context, or Shadow run;
- an existing-run retry where the observer receipt did not record creation;
- a diagnostic onset attempt without a production onset attempt;
- historical Shadow prompt, comparison, or learning generations; or
- timestamps, task text, title similarity, or inferred causal ordering.

## Invariants

1. **Exact source before onset.**
   The qualifying observer root uses the current v2 contract, has a positive
   pipeline-run ID, and resolves to the retained source row by identity.

2. **Coverage must be fully covered.**
   The existing Task 196 exact matcher must return `covered`. The release proof
   does not accept `covered_root_only` or retention-only provenance.

3. **Origin is unique.**
   Exactly one integrity-valid candidate trace may contain a
   `captured_onset` decision for the selected Task Episode. A continuation or
   reconciliation receipt is never origin evidence.

4. **Origin is production onset.**
   The trace admission is `eligible`, flow is `full_onset`, First Run decision
   is `included`, terminal status is `completed` or
   `completed_with_errors`, and at least one production onset attempt ID is
   retained.

5. **Episode, context, workflow, and run agree exactly.**
   The terminal payload contains the Task Episode ID and current Shadow run ID.
   Its unique captured-onset decision matches the Task Episode ID, current
   production workflow, frozen context hash and manifest counts, Shadow run ID,
   `run_created` state, and dispatched execution.

6. **The onset attempt resolves exactly.**
   The captured-onset workflow decision freezes its production onset attempt
   ID. That ID must also appear in the terminal production-attempt set and
   resolve to the immutable attempt ledger with the current onset prompt, onset
   phase, and successful terminal status. A terminal receipt cannot qualify
   from an arbitrary sibling or orphaned ID.

7. **Bounded scans fail closed.**
   The resolver uses the same seven-day, 4,096-root observer corpus as coverage.
   If current roots exceed that bound, no release proof completes.

8. **Selection remains monotonic only among complete current proofs.**
   A newer incomplete episode cannot hide an older complete v5 proof. A v4-only
   historical chain is no longer complete and cannot remain selected forever.

9. **The gate is non-causal.**
   Reading it cannot insert, update, delete, retry, repair, replay, dispatch,
   compare, learn, qualify, approve, or execute anything.

## Derived Provenance Contract

The release gate exposes a bounded metadata-only object:

```text
version
state
reason
source_complete
origin_complete
pipeline_run_id
trace_id
trace_contract_version
coverage_state
admission
flow
terminal_status
production_onset_attempt_id
task_episode_id
workflow
workflow_decision_state
frozen_context_hash
shadow_run_id
candidate_trace_count
integrity_valid
```

It contains no activity text, OCR, screenshot, prompt, model output, result
content, email content, or raw diagnostic error.

Closed provenance states include:

```text
awaiting_task_episode
awaiting_current_run
scan_truncated
origin_missing
legacy_origin
duplicate_origin
source_pruned_or_cleared
source_not_covered
trace_corrupt
boundary_mismatch
terminal_incomplete
production_onset_missing
episode_binding_mismatch
context_binding_mismatch
workflow_binding_mismatch
run_binding_mismatch
covered_source
origin_verified
```

`covered_source` completes only the source step. `origin_verified` completes
both source and origin steps.

## Resolver

For a selected Task Episode and current Shadow run:

```text
load bounded source rows
load current observer roots and exact event chains
  -> if root scan truncated: fail closed
find traces that claim the Task Episode in terminal structured payload
find captured_onset decisions for the exact Task Episode
  -> none: origin_missing or legacy_origin
  -> more than one: duplicate_origin
verify selected trace root and complete event chain
resolve exact source row by source_pipeline_run_id
reuse Task 196 exact source-to-trace matcher
  -> require coverage state covered
require eligible + full_onset + included + terminal
require production onset attempt
require exact episode + frozen context + manifest + workflow + run bindings
  -> origin_verified
```

The resolver parses structured payload fields. It does not search serialized
JSON with substring or timestamp matching.

## Release Gate v5

The gate gains two leading steps:

```text
Real activity source found
Task start verified
Your task completed
Yansu's private attempt completed
Your result captured
Your result source verified
Yansu's result captured
Results compared across 8 dimensions
Safe learning update recorded
```

The first incomplete step determines the derived stage. Source-retention,
legacy, and duplicate-origin states direct the operator to wait for the next
ordinary instrumented task rather than repair or relabel history. Integrity and
binding failures remain inspectable diagnostics.

## Functional Dev Tools

The existing Task Episodes natural parity section shows:

- source and origin state;
- exact source row, trace, workflow, run, and production attempt identities;
- coverage, admission, flow, terminal, and integrity state; and
- all nine ordered proof steps.

The trace identity is a functional navigation control that opens the existing
Observer trace detail. It does not add a duplicate provenance page.

No execute, retry, repair, replay, link, approve, learn, or clear control is
added.

## Retention

Task 196 treats an integrity-valid root whose bounded source diagnostic aged out
as healthy operational history. Task 197 is stricter: that root cannot prove a
current natural parity release because the exact two-ledger source join can no
longer be independently checked.

The retained root remains visible in Observer Dev Tools. The release gate waits
for a later ordinary task; it does not extend source retention or repair the
old chain.

## Validation

Backend tests must cover:

- one exact v2 eligible/full-onset source, origin decision, episode, context,
  current run, production attempt, and complete downstream proof;
- prefiltered, cooldown, reconciliation, existing-run, and historical v1
  exclusion;
- missing and pruned source rows, root-only writeback, failed coverage, corrupt
  chains, and scan truncation;
- duplicate captured-onset traces;
- wrong episode, workflow, frozen context hash, manifest counts, run ID,
  dispatch bit, First Run state, and terminal state;
- missing production onset attempt;
- monotonic selection across complete and incomplete workflows;
- read-only database counts before and after snapshot generation; and
- no Task, run, result, evaluation, learning, Self, authority, or execution side
  effect.

Frontend tests must cover:

- all nine ordered steps;
- cold, legacy, source-covered, origin-verified, mismatch, and complete states;
- compact and desktop-safe provenance layout;
- functional Observer trace navigation; and
- absence of mutation or execution controls.

Release validation includes focused tests, race coverage, uncached full Wails
tests, Wails vet, all frontend tests and production build, complete agent tests
and vet, rebuilt live read-only Browser RPC, visual and interaction QA, clean
browser diagnostics, and native signed-in QA only after macOS is naturally
unlocked.

No source, trace, Task Episode, run, result, evaluation, or learning record may
be fabricated solely to make the live v5 gate complete.

## Implementation Result

Task 197 shipped the derived `task-shadow-release-gate-v5` and
`task-shadow-release-observer-provenance-v1` contracts. The resolver loads the
same seven-day, 4,096-root corpus as Observer Coverage, requires one exact
current v2 `captured_onset` origin, and binds the retained source row, complete
event chain, exact decision-bound production onset attempt, Task Episode,
frozen context manifest,
workflow decision, created and dispatched Shadow run, and current prompt
generation before either leading gate step can complete.

Containment tests cover scan truncation, aged or cleared source rows,
root-only coverage, continuation and reconciliation receipts, legacy and
duplicate origins, corrupt chains, orphaned or historical onset attempts,
First Run exclusion, terminal incompleteness, and episode, context, workflow,
and run mismatches. Snapshot reads remain count-stable and downstream-only v4
evidence cannot satisfy the v5 gate.

Task Episode Dev Tools exposes the metadata-only production-origin state and
opens the exact immutable Observer trace when a real trace identity exists.
The navigation is covered by interaction tests and exposes no execute, replay,
repair, compare, learn, approval, or authority control.

Validation completed with the uncached Wails tree, Wails vet, focused race
coverage, the complete agent test and vet suites, all 45 frontend files and 228
tests, and the production frontend build. The rebuilt live app reports v5 with
all nine steps incomplete at `desktop_locked`, `origin_missing`, 95 legacy
source rows, and zero current instrumented traces. A plausible mutation RPC is
rejected with HTTP 403. Browser interaction and a 1280 by 720 visual check are
clean, and the browser log contains no warning or error. Native signed-in proof
remains unavailable while macOS is naturally locked; no lock was bypassed and
no production evidence was manufactured.

## Non-Goals

Task 197 does not:

- add or widen a workflow or destination registry;
- change onset or lifecycle prompts;
- migrate historical evidence into current provenance;
- lengthen source retention;
- retry or repair observer work;
- run a model or execute a task;
- create a result, comparison, or learning projection;
- influence Memory, Knowledge, Self, Purpose, or Initiatives;
- qualify an agent or grant authority; or
- turn silent Shadow into a user review queue.
