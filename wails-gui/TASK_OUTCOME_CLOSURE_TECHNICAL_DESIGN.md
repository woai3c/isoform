# Yansu Task Outcome Closure Technical Design

Status: canonical design for Tasks 152 through 155, amended by Tasks 192 and
216, on `vision/shadow-agent`

## Problem

Shadow learning is useful only when Yansu observes the user's independent
result and compares it with the isolated result. The live ledger currently has
many detected Task Episodes but very few terminal episodes and evaluated
results. Two production defects compound:

1. common text submissions performed with Return are retained as generic
   keyboard activity, so the lifecycle observer cannot distinguish a possible
   commit gesture from ordinary typing; and
2. unsupported candidate, active, and paused hypotheses remain open
   indefinitely, occupying the bounded lifecycle context and displacing newer
   work.

The missing loop is:

```text
observed onset
  -> exact later production evidence
  -> source-backed continuation, result, or terminal state
  -> independently materialized user result
  -> shadow comparison
  -> evaluated learning

or

observed onset
  -> no qualifying evidence before a declared horizon
  -> expired without a result
```

Expiry is honest negative capability evidence about Yansu's observation. It is
not evidence that the user abandoned or completed the task.

## Invariants

1. Later user evidence never enters the frozen task-start context or isolated
   shadow attempt.
2. A key press is not automatically a task completion, result, external send,
   or statement of user reasoning.
3. A Return-based result is eligible only for a deterministic direct-text
   workflow when the same retained event contains source-backed committed text
   and current structured destination provenance.
4. Shift+Return is ordinary editing evidence and never a commit candidate.
5. The lifecycle model may associate evidence with an episode, but the backend
   independently validates task type, evidence kind, retained content, state,
   timestamp, and source reference before persistence.
6. A result is materialized from retained production evidence, never copied
   from model prose.
7. Missing evidence becomes `expired`, never `completed`, `abandoned`, or a
   synthetic result.
8. Expired episodes and runs remain immutable and inspectable. A later task is
   a new episode; history is never rewritten to force a match.
9. An expired episode with no observed result cannot enter evaluation or
   learning. An expired episode with independently materialized result evidence
   may still be compared, with boundary confidence evaluated separately.
10. Closure, comparison, confidence, and qualification never grant authority,
    create an Initiative, or widen the production Shadow rollout.
11. Debugger reads never run lifecycle observation, expire an episode, retry a
    model, or manufacture evidence.

## Commit Evidence

The activity adapter emits `text_commit_candidate` only when a keyboard burst
contains exact `Return` or `Command+Return`. It preserves the redacted app and
window context and, when available, the committed text already recovered by
the accessibility differ.

The evidence is deliberately named a candidate. The lifecycle prompt must
treat it as ambiguous unless the surrounding task and application show that
Return submitted the composed text. The backend then applies a stricter gate:

```text
one episode
+ deterministic workflow is in the closed direct-text registry
+ evidence kind == text_commit_candidate
+ content kind == committed_text
+ non-empty retained content
+ capture method == ax_field_diff
+ current structured destination role == user_result_candidate
+ all accepted text references have one exact destination
+ event time inside the lifecycle suffix
+ destination is send-on-Return OR separate terminal evidence is present
= completion/result-capable evidence
```

An exact click on Send, Submit, Publish, Merge, or another existing strong
terminal control remains strong evidence. A generic save, focus change, app
switch, window close, idle interval, bare Return without retained text, or
Shift+Return does not satisfy the gate.

The current lifecycle prompt is `task-lifecycle-v7`; Tasks 192 and 216 add
backend destination and completion semantics without trusting model prose. The
isolated direct-text worker remains `shadow-task-v7` with a 24-hour observation
horizon. Historical attempts, runs, results, and evaluations retain their
original versions and horizons.

## Deterministic Observation Horizons

Open Task Episodes use status-specific horizons measured from their latest
independently observed signal:

| Status | Horizon | Meaning when elapsed |
| --- | --- | --- |
| `candidate` | 90 minutes | onset was not source-backed as continuing |
| `active` | 24 hours | no later evidence kept the observed task active |
| `paused` | 72 hours | no source-backed resume or result was observed |

The latest signal is the maximum of inferred start, detection time, immutable
evidence time, and immutable transition time. Model attempt time, shadow output,
debugger access, Memory, Self, and wall-clock reads are not signals.

At the next real production activity cutoff, the pipeline appends one
`expired` transition for every elapsed open episode before assembling the
lifecycle prompt. The transition records the policy version, prior status,
last signal, and horizon. Idempotent retries observe the terminal state and do
nothing. No model call is spent on deterministic expiry.

The lifecycle prompt receives only the remaining open set plus recent completed
episodes. This keeps its existing item and byte guards operational rather than
allowing stale hypotheses to consume them.

## Pair Readiness

Pair readiness adds an explicit `observation_expired` state.

- Expired with no independently materialized user result: not ready and never
  evaluated.
- Expired with a materialized result and a completed isolated result: pair
  ready; the comparator evaluates task boundary separately and may leave it
  unknown.
- Completed or abandoned direct-text work without a current destination-bound
  committed-text result remains `user_result_unavailable`.

Current comparison also verifies the canonical destination digest and uses the
v4 input, output, comparator, and evaluator lineage. Historical materialized
results without destination provenance remain inspectable but cannot enter a
current comparison.

When one episode has repeated current result observations, comparison selects
one canonical result rather than concatenating history. Revisions at the same
destination select the latest immutable observation by observed time and
stable receipt ordering. Current observations at different destinations fail
closed as `conflicting_user_result_destinations`. Historical and superseded
result summaries stay inspectable but their text cannot enter comparator
evidence.

The quality campaign reports expired observations as unavailable evidence. It
does not count them toward reliability, result quality, safety, or release.

## Model Identity

Task onset, lifecycle observation, and comparison use the effective configured
lightweight model. Isolated shadow work uses the effective configured primary
model. Persisted attempts and runs retain those effective identities rather
than `haiku` or `sonnet` role aliases. For the current configuration this means
`gpt-5.6-terra` for lightweight work and `gpt-5.6-sol` for primary work.

Changing the effective model is visible in immutable provenance and cannot be
mistaken for the previous generation.

## Developer Experience

Dev Tools / Task Episodes adds read-only lifecycle health:

- complete status counts;
- open and overdue counts by status;
- completed, abandoned, and expired counts;
- episodes with materialized observed results;
- latest lifecycle attempt and prompt version;
- exact observation-horizon policy and durations; and
- pair-readiness counts, including `observation_expired`.

Episode detail continues to expose immutable transitions, evidence hashes,
attempts, pair readiness, and model versions. It adds no replay, repair,
manual-completion, expiry, evidence-upload, or evaluation control.

## Failure And Recovery

- A malformed or corrupt episode fails closed for that episode and remains
  visible in diagnostics.
- An expiry transition conflict is re-read; an already-expired episode is an
  idempotent success, while any other drift is reported.
- Model failure leaves production evidence and episode state unchanged except
  for deterministic expiries already due at the activity cutoff.
- A commit candidate rejected by backend validation degrades to `unknown`; its
  content does not become result evidence.
- Process failure after result evidence or a transition relies on existing
  deterministic append-only identities and retries.
- No outcome is reconstructed from current screen state after the fact.

## Verification Gates

Before rollout, prove:

- exact Return and Command+Return classification, with Shift+Return exclusion;
- non-compose, empty-content, post-cutoff, and model-spoofed commit rejection;
- source-backed text result materialization and digest integrity;
- candidate, active, and paused horizon boundaries;
- append-only, idempotent expiry and corruption containment;
- stale episodes cannot consume lifecycle prompt slots;
- cross-group continuation and late-result capture;
- expired-without-result exclusion and expired-with-result pair readiness;
- effective primary/light model provenance;
- zero Initiative, authority, Memory, Knowledge, Self, Purpose, Handoff,
  Schedule, or external side effects;
- read-only debugger behavior and functional responsive navigation; and
- focused, full, race, build, live, and native checks without manufactured
  production evidence.

## Implementation Status

Tasks 153 and 154 are implemented. Lifecycle input version 4 recognizes an
ambiguous text commit candidate while deterministic validation remains the
only path to result materialization. Composition mirrors now use
`shadow-task-v6`, `task-mirror-runtime-v2`, the effective configured primary
model, and the bounded 24-hour observation horizon. Onset, lifecycle, and
comparison use the effective configured lightweight model.

Before every eligible production lifecycle pass, status-specific expiry closes
elapsed open episodes append-only. First Run activity is excluded before that
step. Pair readiness distinguishes `observation_expired` from a usable observed
result, and no-result expiry cannot enter evaluation or learning.

Task Episodes Dev Tools reconstructs complete lifecycle health independently of
the bounded episode index. It reports status, overdue, materialized-result,
pair-state, model, prompt, and horizon-policy provenance. Repeated reads do not
expire work or write any ledger.

## Release Gate Status

The automated and read-only live portions of Task 155 pass. Full Wails and
agent tests, both vet suites, all 182 frontend tests, the production frontend
build, focused Wails race coverage, the complete agent race suite, formatting,
and whitespace checks are clean. The frontend build retains its existing large
chunk warning, and the Wails race link retains the existing macOS
`LC_DYSYMTAB` warning.

The rebuilt live service reports 68 complete Task Episodes: 53 candidate, 13
active, and 2 completed. All 66 open episodes are past their declared wall-clock
horizons, but repeated snapshot and detail reads leave them open. They can only
expire at the next eligible production activity cutoff. The latest retained
lifecycle attempt is historical `haiku` / `task-lifecycle-v3`; no ordinary
post-upgrade activity has yet produced a lifecycle-v4 attempt. The current
`shadow-task-v6` quality campaign therefore correctly reports zero current
prompt runs and no processable pairs.

Repeated live reads preserved exact Task Episode, Shadow, Work Profile, Self,
Purpose, First Run, Memory, Knowledge, Initiative, authority, Handoff, Schedule,
deliverable, and session ledger fingerprints. The completed production episode
still resolves its frozen context, transition/evidence graph, isolated result,
observed result, evaluation, learning revisions, materializations, and
correlation IDs.

Two gates remain deliberately open: visual desktop/narrow inspection while the
Mac is locked and localhost browser navigation is unavailable, and one
naturally occurring post-upgrade task/result pair. Neither is replaced with
manufactured evidence.
