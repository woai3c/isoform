# Shadow Production Continuation Technical Design

Status: Task 194 base, Task 198 v2 materialization, and Task 222 bounded history reads implemented and validated

## Problem

The production Shadow loop has correct evidence and authority boundaries, but
its orchestration is not yet independent or restart-safe:

1. eligible Shadow onset is dispatched only after the legacy activity-triage
   model succeeds;
2. activity groups rejected by the generic prefilter or held by its retry
   cooldown never reach Task Episode lifecycle observation, so a short later
   send or submit can be lost;
3. reconciliation treats any existing task-mirror run as handled, even when a
   process exit occurred before its current-runtime execution attempt was
   persisted;
4. a result-bearing episode that expires, a ready pair interrupted before
   evaluation, or an evaluation persisted before learning has no bounded
   recovery pass; and
5. when no activity session is pending, the five-minute triager tick returns
   without inspecting interrupted current Shadow work.

These are orchestration failures, not model-quality failures. They can prevent
one ordinary task from ever reaching the natural parity gate even though every
required immutable source record exists.

## Product Meaning

Shadow mode must behave like a production shadow deployment:

- it receives the same retained production activity independently of the
  legacy product router;
- it resumes incomplete internal processing after interruption;
- it never replays a production effect;
- it never converts readiness into authority; and
- it leaves durable failures visible instead of retrying them indefinitely.

Task 194 repairs that orchestration. It does not broaden what work is eligible
for Shadow or change what the user sees in the product.

## Three Independent Flows

```text
retained activity group
  |
  +-- onset eligible ----------------> detect new Task Episodes
  |                                      freeze context
  |                                      create current task-mirror run
  |
  +-- any retained activity ---------> observe existing Task Episodes
  |                                      append result/state evidence
  |                                      expire only at observed cutoffs
  |
  +-- startup / every triager tick ---> inspect immutable current ledgers
                                         recover missing execution
                                         recover ready evaluation
                                         recover missing learning
```

The first flow remains sampled by the existing generic group eligibility rule
for this task. The second flow is not: a short or single-app group can contain
the exact committed-text result for an already-open episode. The third flow is
model-free until it finds one exact recoverable action.

## Invariants

1. The Shadow feature toggle governs all three flows.
2. Eligible onset is dispatched before legacy activity triage runs. A legacy
   timeout, parse error, permanent rejection, cooldown, or disabled output
   cannot cancel an already-dispatched Shadow flow.
3. Generic-prefiltered and cooldown groups run lifecycle continuation only.
   They cannot create a new Task Episode or invoke onset detection.
4. Lifecycle expiry still uses a retained production activity cutoff. Wall
   clock passage, app lock, observer pause, or startup alone cannot claim that
   an observation horizon was covered.
5. Recovery considers only `task_mirror` runs in the closed production
   workflow registry with the current worker generation.
6. First Run exclusions are rechecked before every recovered action.
7. A missing current `shadow-runtime-v1` attempt may be executed once through
   the existing isolated policy. A persisted attempt of any terminal status is
   never automatically retried.
8. Task-mirror execution uses the model frozen on the immutable ShadowRun. A
   later settings change cannot silently change a recovered experiment.
9. Evaluation is recovered only for a terminal episode whose current pair is
   `pair_ready`, or for a current evaluation missing its linked v2 learning
   projection.
10. Existing execution, evaluation, and learning idempotency remains the write
    boundary. Recovery adds no synthetic TaskEpisode, result, comparison, or
    learning evidence.
11. Recovery is bounded, newest-first, singleflight/serialized by the existing
    Shadow runtime controls, and safe to invoke repeatedly.
12. No continuation state grants authority, permission, Initiative, Handoff,
    Schedule, external write, or product qualification.

## Continuation Contract

Task 194 adds the derived contract:

```text
task-shadow-continuation-v1
```

Each current task-mirror run derives one state from immutable storage:

- `execution_pending`: no attempt exists for the current runtime version;
- `awaiting_task_terminal`: isolated output exists while the episode remains
  candidate, active, or paused;
- `awaiting_user_result`: the episode is terminal but no valid current user
  result exists;
- `evaluation_pending`: the episode is terminal and the current pair is ready;
- `learning_pending`: a current evaluation exists without a linked current
  learning projection;
- `complete`: execution, current evaluation, and linked learning all exist;
- `shadow_failed`: a current execution attempt exists but did not complete;
- `blocked`: current provenance, pair, context, or workflow validation failed;
- `historical`: the run is retained history and cannot be recovered; or
- `first_run_excluded`: guided evidence remains outside natural learning.

The recovery planner returns exact run, episode, workflow, attempt, pair,
evaluation, and learning identities plus one of four internal actions:

```text
execute_shadow
evaluate_pair
materialize_learning
none
```

There is no approval or execution action in Dev Tools. The action names
describe silent internal continuation only.

## Trigger Semantics

Recovery runs:

- once when the activity triager starts;
- on every five-minute triager evaluation, including zero-pending-session
  ticks; and
- after a retained group finishes lifecycle continuation.

Onset runs only once on the normal candidate-group path before legacy triage.
Prefiltered or cooldown groups may carry lifecycle evidence but never trigger
onset in Task 194.

## Failure And Retry Policy

- No persisted current-runtime attempt: recoverable because no model result was
  durably recorded and task-mirror execution has no tools or effects.
- Persisted `error` or `unsupported_runner`: durable blocker; inspect, do not
  retry automatically.
- Ready pair with no evaluation: recoverable through the existing comparator
  singleflight and immutable comparator hash.
- Current evaluation with no learning projection: recoverable through the
  existing deterministic learning projection.
- Current learning projection with missing or error-only target receipts:
  recoverable through deterministic local materialization without a model.
- Missing result, nonterminal task, invalid provenance, historical generation,
  or First Run exclusion: observe or inspect only.

## Task 198 Materialization Extension

Task 198 advances the derived contract to:

```text
task-shadow-continuation-v2
```

It preserves every v1 state and adds
`learning_materialization_pending` after a current evaluation has a linked
learning projection but one or more candidate revisions lack a valid terminal
receipt. Its internal `materialize_learning` action loads that exact immutable
learning run and retries only deterministic local target writes. It does not
rerun the comparator, learning policy, lifecycle observer, Shadow worker, or
onset model.

`complete` now means the linked projection has a complete
`shadow-task-learning-materialization-v1` resolution. `materialized`,
`skipped`, and `denied` are terminal; missing, `error`, and historical `failed`
receipts remain pending. A terminal receipt inconsistent with the immutable
candidate decision, target kind, or terminal target identity is `invalid`,
blocks automatic reconciliation, and remains inspectable. A no-candidate
projection resolves only when it explicitly links the current evaluation
through its exclusion ledger.

Memory and Knowledge compare exact bytes before creating history. Canonical
Self preserves nil empty-evidence identity so retrying a target cannot create a
new revision. Capability profile candidates use the immutable learning
revision itself as the target. Receipt identity excludes retry time, and the
reconciler is unexported from the Wails frontend surface.

## Dev Tools

Task Episode diagnostics gain a read-only Production continuation section with:

- contract version and bounded scan count;
- execution, evaluation, learning-projection, and learning-materialization
  pending counts;
- waiting, blocked, historical, excluded, and complete counts;
- exact current item state, workflow, episode/run IDs, attempt status, pair
  state, current evaluation IDs, linked learning ID, terminal/missing/error
  resolution counts, and reason; and
- the derived internal action, never a clickable command.

Browser preview remains inspect-only. Debug reads cannot invoke recovery.

## Validation

Tests must prove:

- a legacy triage failure cannot prevent eligible onset dispatch;
- filtered and cooldown groups can advance existing lifecycle/result evidence
  without running onset;
- startup/tick recovery executes a current run with no current-runtime attempt;
- repeated recovery creates one execution attempt, one current evaluation, and
  deterministic learning only;
- a durable failed attempt is not retried;
- result-bearing expired work reaches evaluation, while expired work without a
  result does not;
- a current evaluation missing learning is repaired without rerunning the
  comparator;
- historical, unsupported, First Run, invalid-provenance, and Shadow-disabled
  work cannot dispatch;
- the frozen run model is used during recovered task-mirror execution;
- Dev Tools is bounded, functional, and read-only; and
- focused, full, race, vet, frontend, production-build, rebuilt live read-only,
  and no-fabricated-evidence gates pass.

## Non-Goals

- No new workflow or destination classifier.
- No onset expansion to every short or single-app group.
- No retry of a persisted failed model attempt.
- No wall-clock-only lifecycle expiry.
- No user-facing Shadow review, replay, approval, or prediction card.
- No authority, permission, Initiative, Handoff, Schedule, or external effect.

## Implementation Outcome

Task 194 implements the contract above without adding a new persistence table.
Continuation state is derived from the existing immutable Task Episode,
ShadowRun, execution-attempt, evaluation, and learning ledgers. The activity
triager now invokes recovery before reading pending sessions, feeds retained
prefiltered and cooldown groups through lifecycle-only observation, and starts
the full Shadow path before generic triage. Recovered task-mirror execution
uses the model frozen on the ShadowRun.

The read-only Task Episode debugger exposes the complete bounded continuation
snapshot and listens for internal continuation events. It contains no replay,
execute, evaluate, approval, or authority control. A direct browser-RPC attempt
to call a recovery method is rejected by the allowlist.

Validation on 2026-08-05 passed:

- focused backend lifecycle, recovery, containment, and routing tests;
- focused race coverage in 88.464 seconds, with only the established malformed
  macOS `LC_DYSYMTAB` linker warning;
- the uncached complete Wails suite in 139.413 seconds and Wails vet;
- the complete `yansu-agent` suite and vet;
- all 44 frontend files and 222 tests; and
- the frontend production build, with only the established large-chunk
  warning.

The rebuilt live debugger reports `task-shadow-continuation-v1`, 51 scanned
runs, zero current runs, and 51 historical runs. All execute, evaluate, learn,
wait, complete, blocked, and excluded counts are zero. The retained dataset
still contains 74 Task Episodes, 51 linked runs, one historical evaluation,
and zero current observed results. Refresh and Task Episode detail navigation
work at 1280 by 720 with no browser warning or error. Native signed-in QA is
unavailable while macOS remains locked at `loginwindow`; no production evidence
or external effect was fabricated to bypass that gate.

## Task 198 Validation Outcome

Task 198 advances the live contract to `task-shadow-continuation-v2`. Focused
backend coverage passed in 14.643 seconds, focused race coverage in 398.347
seconds, and the uncached complete Wails tree in 132.074 seconds. Wails vet,
the complete agent tree and vet, all 45 frontend files / 228 tests, and the
frontend production build pass. Standalone TypeScript retains only established
unrelated application and generated-alias failures.

The rebuilt live debugger reports 51 scanned historical runs, zero current
runs, zero pending materializations, and the v6 natural parity gate remains
`desktop_locked` at `loginwindow`. Repeated read-only RPC and UI refreshes left
the exact learning, revision, materialization, and Self ledger fingerprints
unchanged. Browser RPC rejects the unexported materialization reconciler with
HTTP 403. Desktop and compact Dev Tools QA verified the continuation panel,
including the new materialization count, without overlap or a mutation action.
No evidence or product effect was fabricated, and native signed-in QA remains
deferred until macOS is naturally unlocked.

## Task 222 Retained-History Continuation Index

Continuation recovery and continuation diagnostics have different read needs.
A recoverable current-generation run can lead to contained model execution,
comparison, or local learning materialization, so it must load and validate the
complete immutable ShadowRun graph. A historical prompt generation can never
produce an action; loading its context, prediction, observations, and evidence
only delays the interactive read.

`BuildTaskShadowContinuationSnapshot` therefore begins from a bounded immutable
header projection:

```text
id, task_episode_id, capability_key, prompt_version, created_at
```

If `prompt_version` is historical, the row contributes only to the historical
count and no graph is decoded. If it is current, continuation loads the full
run through `GetShadowRun`, validates its JSON and hashes, and then requires the
full run to match every projected identity before deriving state or action. A
malformed current prediction remains a hard error before recovery planning.

The Task list applies the same separation to latest-pair state. Historical
latest runs deterministically expose `historical_generation` and
`historical_shadow_prompt_generation`; current latest runs continue through
complete pair-readiness validation. Selected detail remains provenance-complete
for both generations.

This optimization changes neither the continuation state machine nor its
authority boundary. It cannot create an attempt, evaluation, learning row,
permission, Initiative, Handoff, Schedule, or external effect. Task 216 remains
open until a future natural current-generation direct-text task completes the
production parity chain.
