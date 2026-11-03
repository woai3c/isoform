# Shadow Learning Influence Technical Design

Status: implemented and validated for Task 199 on `vision/shadow-agent`

## Purpose

Shadow mode is a production-style shadow deployment for user agency. It
observes an ordinary user task, independently attempts the same task without
causing a production effect, compares the two results, and learns only from the
evaluated difference.

Task 198 made qualified learning materialization restart-safe. Task 199 closes
the next loop:

```text
prior ordinary user task
  -> contained Shadow attempt
  -> observed user result
  -> comparison
  -> qualified append-only learning revision
  -> exact materialization receipt
  -> later task cutoff
  -> verified learning admission
  -> frozen task context / work-pattern profile
  -> profile-versus-neutral onset ablation
  -> later contained Shadow prediction
```

The implementation must prove which earlier learning was available to a later
prediction, why it was selected or omitted, and whether the existing ablation
measured a difference. It cannot convert learning confidence into authority.

## Product Meaning

The product thesis is not that Yansu accumulates generic AI output. It is that
Yansu becomes more accurate by comparing its contained work with what the user
actually did and then carrying only supported differences into later work.

A later prediction may use a Shadow-derived learning only when Yansu can prove:

- the learning came from supported observed-user and verified-result evidence;
- the immutable learning policy qualified it;
- the exact target was materialized successfully;
- the target still matches the qualified revision;
- the receipt existed before the later task began;
- no correction existed at that task cutoff;
- no source episode belonged to the guided First Run; and
- the learning has no authority effect.

The frozen Task Episode records that proof. A debugger read does not recreate
or repair it.

## Audited Consumer Breaks

Before Task 199, the writers and consumers did not form one verified chain:

1. Memory and Knowledge materializations used the source
   `shadow_task_evaluation`, while generic context gathering correctly rejected
   any Shadow or evaluator source.
2. Typed Self used `source_backed_shadow_task_evaluation`, which the generic
   typed-Self reader also rejected. A recovered entry could retain the old run
   timestamp even though its receipt was written later.
3. capability learning rebuilt a separate raw profile from evaluations instead
   of consuming the immutable learning revision and receipt.
4. work-pattern context read capability snapshots directly, without proving
   the linked candidate, correction state, materialization, readiness, or First
   Run containment.
5. the capability scan stopped at 200 rows, so older eligible evidence could be
   silently absent.
6. the profile ablation froze profile-versus-neutral attempts but not the exact
   learning revisions and receipts that contributed to the profile.

The existing downstream Task Episode freeze, Shadow worker context hash, and
release provenance were already strong. Task 199 changes the admission
boundary rather than the worker or execution path.

## Single Verified Adapter

`VerifiedShadowLearningSourcesAt(store, cutoffAt, query)` is the only adapter
that turns a contained Shadow learning into later production context.

Its output contract is `verified-shadow-learning-source-v1`:

```text
cutoffAt
sources[]
  candidate
  materialization receipt
  exact learning provenance
  context candidate OR capability signal
exclusions[]
  revision identity
  observed time
  reason
  optional exact learning provenance
```

The adapter reads all immutable revisions and their append-only receipts. It
does not use a row-count cap. It emits one explicit manifest result for every
revision that was considered as-of the cutoff.

## Admission Algorithm

For each immutable revision:

1. Reconcile database columns with the canonical candidate JSON.
2. Select the newest revision for each target-kind and projection-key pair as
   of the task cutoff.
3. Mark every older revision as `learning_revision_superseded` even when the
   newest revision later fails validation. This prevents fallback to stale
   knowledge.
4. Recompute the evidence hash, projection key, and deterministic revision ID.
5. Require the current learning policy, append-only revision mode, supported
   target kind, safe evidence origins, and `authority_effect=none`.
6. Re-evaluate the frozen qualification counters, thresholds, confidence,
   result quality, task reliability, and capability safety evidence.
7. Recheck guided First Run containment against the original evidence chain.
8. Check user corrections as-of the later task cutoff, not merely at the time
   of the debugger read.
9. Resolve one valid `materialized` receipt at or before the cutoff. A skipped,
   denied, missing, conflicting, malformed, or future-only receipt cannot enter
   context.
10. Validate that the exact materialized target still represents the qualified
    candidate.
11. Emit a semantic context candidate for Memory, Knowledge, or typed Self, or
    a capability signal for the Work Pattern Profile.

Any failed check produces an exclusion reason. It never falls back to raw
Shadow output, evaluator prose, or a prior superseded revision.

## Time Semantics

The materialization receipt timestamp is the earliest usable time.

```text
revision created at T1
target recovered or materialized at T2
later task starts at T3

eligible only when T2 <= T3
```

The target's own timestamp is not sufficient. This closes the typed-Self
backdating risk and applies the same rule to every target kind.

Corrections use the same as-of rule. A correction created after a historical
Task Episode does not rewrite that episode's frozen context, while every later
assembly excludes the corrected learning.

## Target Integrity

Materialization status alone is not enough.

- Memory must have the deterministic target ID and exact expected OKF Markdown
  bytes under the local memory directory.
- Knowledge must have the deterministic target ID and exact expected OKF
  Markdown bytes under the local knowledge directory.
- typed Self must match the candidate key, kind, scope, statement, active
  status, evaluated reasoning status, and all source evidence. It must remain
  the current active revision for that key.
- capability profile learning uses the immutable learning revision itself as
  the target identity and rechecks the stricter current readiness thresholds.

A missing or changed target is excluded rather than reconstructed by a read.

## Frozen Context Provenance

Task context advances to `task-context-v6`. Included evidence and exclusion
manifest entries can carry:

```text
version
runId
revisionId
materializationReceiptId
targetKind
targetId
projectionKey
capabilityKey
learningKind
evidenceHash
availableAt
authorityEffect
```

The verified source kind requires this provenance, and the candidate must match
its receipt ID, availability time, and evaluated-projection evidence class.
Merely spelling a source `verified_shadow_learning` does not bypass the generic
Shadow/evaluator denylist.

The assembler freezes the exact lineage into the Task Episode manifest and
included evidence. The Shadow worker still receives only the episode's frozen
canonical context.

## Work Pattern Profile and Ablation

The Work Pattern Profile advances to `work-pattern-profile-v4`.

- capability inputs come only from the verified adapter;
- capability status is qualified only when the source is explicitly ready and
  currently qualified;
- profile evidence points to the exact materialization receipt;
- the selection manifest contains every selected and omitted capability signal
  with its learning lineage and reason;
- the frozen episode selection evidence advances to
  `work-pattern-selection-provenance-v2`; and
- v1 evidence remains readable for historical episodes.

The compact model-facing profile intentionally removes revision IDs, receipt
IDs, and other internal ledger metadata. Those identifiers do not improve task
semantics and would consume prompt budget. The immutable source context and
episode selection evidence retain the complete lineage for attribution.

The existing profile-versus-neutral ablation remains the measurement path. Its
attempts and scores are paired with the frozen episode and profile snapshot, so
Dev Tools can show both the exact contributing learning and the measured score
lift without adding another evaluator or profile.

## Complete Context Without a Blind Count Limit

"Give the most context" means no arbitrary source-count truncation and no
silent disappearance. It does not mean sending unbounded bytes to a model.

Task 199 therefore separates discovery from prompt selection:

- every learning revision is scanned;
- every current capability signal receives a selected or omitted manifest
  entry;
- semantic and capability selection remain relevance-ranked and token-bounded;
- omission reasons distinguish relevance from token budget; and
- Dev Tools exposes totals and exact lineage outside the model prompt.

This preserves complete inspectability while protecting prompt stability.

## Functional Dev Tools

Profile -> Settings -> Dev Tools -> Shadow Agent -> Work Profile adds an
`Evaluated learning influence` section.

For each later Task Episode with verified learning provenance, it shows:

- capability and target or learning kind;
- selected or omitted status and exact reason;
- immutable revision ID;
- materialization receipt ID;
- availability timestamp and `authority=none`;
- paired profile and neutral scores, lift, and winner when evaluated; and
- functional navigation to the existing Task Episode detail.

The empty state says that no verified learning has reached a prediction. The
screen has no repair, retry, materialize, edit, approve, execute, or authority
control.

## Invariants

1. Generic hidden Shadow output and evaluator prose remain forbidden sources.
2. A verified source requires a qualified immutable revision and exact terminal
   materialization receipt.
3. Receipt time, not target time, controls availability.
4. Newer revisions supersede older revisions without unsafe fallback.
5. Corrections and First Run exclusion are rechecked at admission.
6. Missing, changed, malformed, conflicting, or unsupported targets fail
   closed.
7. Full discovery has no count cap; model prompts remain token-bounded and
   omission-complete.
8. Frozen episode evidence retains exact lineage even though model-facing
   semantic content strips internal IDs.
9. Read-only assembly and Dev Tools do not create or mutate learning, Memory,
   Knowledge, Self, profiles, episodes, runs, evaluations, initiatives,
   authority, permissions, or external state.
10. Learning readiness never grants authority or enables execution.

## Validation Plan

Backend coverage proves:

- delayed materialization remains unavailable before its receipt;
- generic Shadow/evaluator sources remain denied;
- a forged verified source kind without provenance is rejected;
- corrections obey historical cutoffs;
- a newer revision suppresses stale fallback;
- changed or missing targets are excluded;
- typed Self uses receipt time even when its target is backdated;
- First Run evidence remains excluded;
- more than 200 capability signals are discovered with a complete selection
  manifest;
- the next immutable Work Pattern Profile records receipt provenance;
- model prompts omit internal lineage while frozen selection evidence keeps it;
- Work Profile detail returns the exact frozen revision and receipt IDs; and
- no authority decision or Initiative is created.

Frontend coverage proves the new influence section, empty state, selected and
omitted states, ablation scores, Task Episode navigation, and absence of action
controls.

Release validation includes focused and complete Wails tests, race coverage,
Wails vet, complete agent tests and vet, all frontend tests, production build,
standalone TypeScript baseline comparison, rebuilt live Browser RPC, responsive
interaction QA, and native signed-in QA only after macOS is naturally unlocked.

## Validation Result

Task 199 passed the following release checks:

- focused Work Pattern, task-context, verified-source, and exact debugger-link
  backend tests;
- focused race coverage in 47.871 seconds, with only the established malformed
  macOS `LC_DYSYMTAB` linker warning;
- the complete uncached Wails test tree in 138.685 seconds and Wails vet;
- the complete agent test tree and agent vet;
- all 45 frontend test files and 228 tests;
- the frontend production build, with only the established chunk-size warning;
  and
- standalone TypeScript baseline comparison, whose failures remain limited to
  established unrelated application types and missing generated aliases.

The rebuilt live Browser RPC returned 70 real Work Pattern profiles. The
functional Work Profile screen loaded and refreshed those records and showed
the honest empty state, `No verified evaluated learning has reached a frozen
prediction yet.` No eligible learning, ablation, or authority was fabricated.
A plausible `RepairVerifiedShadowLearningSources` mutation was rejected with
HTTP 403.

The influence section was visually verified at 1280 by 720 without overlap. A
fresh browser load produced zero warnings or errors; earlier ThemeProvider
messages in the retained diagnostic buffer were stale hot-reload artifacts.
Native signed-in QA remains deferred because the live release gate reports
`desktop_locked` with `foregroundApp=loginwindow`. The lock was not bypassed,
and Wails, Vite, Yansu, and Quick Chat remain running.

## Non-Goals

Task 199 does not:

- change Shadow onset, lifecycle observation, worker execution, comparison, or
  evaluator prompts;
- weaken qualification or lower readiness thresholds;
- expose hidden Shadow work as a user review queue;
- create a second Self or Work Pattern Profile;
- infer a user's reasoning from behavior without supported evidence;
- materialize or repair a target during a read;
- create production evidence or fabricate a successful ablation;
- add OAuth or a connector;
- grant authority, permission, or execution capability; or
- perform any external action.
