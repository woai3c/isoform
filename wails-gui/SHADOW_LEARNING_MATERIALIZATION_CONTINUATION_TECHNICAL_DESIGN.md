# Shadow Learning Materialization Continuation Technical Design

Status: implemented and validated for Task 198 on `vision/shadow-agent`

## Purpose

The production Shadow loop currently persists a contamination-safe learning
projection and then materializes qualified or contained candidates into their
declared targets:

```text
comparison
  -> shadow_task_learning_runs
  -> shadow_task_learning_revisions
  -> Memory / Knowledge / typed Self / capability profile
  -> shadow_task_learning_materializations
```

Those writes are intentionally separated, but continuation currently treats
the projection as the end of learning. A process exit after the projection
commits, or a local target-write failure, can therefore leave Memory,
Knowledge, or typed Self unresolved forever while both Production continuation
and the natural parity gate report learning complete.

Task 198 makes that final local projection step restart-safe and inspectable.
It runs no model, changes no learning qualification, and grants no authority.

## Product Meaning

Shadow mode is useful only if evaluated differences become durable, bounded
context for later work. Persistence must distinguish:

- the immutable recommendation made by the learning policy;
- whether the recommendation was qualified, collecting, rejected, denied, or
  corrected;
- the exact local target write, if one was allowed; and
- the immutable receipt proving that the recommendation was resolved.

The product may say that one comparison completed its learning stage only when
every candidate in the linked projection has a terminal receipt, or when the
projection contains no candidates and explicitly excludes the evaluation.

## Demonstrated Failure Window

`BuildAndPersistShadowTaskLearning` commits the run and revisions before it
calls the target writers. The target writes and materialization receipts are
not in one transaction because Memory and Knowledge are local files and typed
Self is a separate append-only ledger.

Before Task 198:

1. a crash after projection persistence skips all target writes permanently;
2. a crash after a target write but before its receipt can cause an unnecessary
   repeated file snapshot on retry;
3. an error or receipt-persistence failure is swallowed by the materializer;
4. idempotent projection persistence incorrectly reports `added=true`;
5. continuation indexes evaluations by projection alone; and
6. the natural parity gate accepts the projection without checking resolution.

## Invariants

1. **Projection remains immutable.** Reconciliation never rebuilds, edits, or
   requalifies a persisted projection.
2. **One exact run is reconciled.** Recovery loads the immutable learning run
   and its exact candidate revisions by run ID.
3. **Terminal receipts are explicit.** `materialized`, `skipped`, and `denied`
   are terminal. Missing and error-only candidates remain pending. Historical
   `failed` receipts are treated as the prior error spelling, never as success.
4. **No-candidate projections resolve safely.** A current projection whose
   evaluation is explicitly excluded needs no invented candidate or target
   write.
5. **Retries are model-free.** Recovery invokes only deterministic local target
   writers and receipt persistence. It never reruns onset, Shadow execution,
   lifecycle observation, comparison, or learning qualification.
6. **Target writes are idempotent.** Memory and Knowledge compare existing
   bytes before snapshotting or rewriting. Typed Self uses its existing source
   hash idempotency. Capability profile candidates require no second target
   write.
7. **Receipts are append-only and retry-safe.** Receipt identity excludes retry
   time. Repeating the same terminal or error outcome returns the existing
   receipt rather than conflicting on `created_at`.
8. **Errors remain visible.** A failed target or receipt write is returned to
   the caller and leaves the candidate pending for a bounded future retry.
9. **Corrections and First Run remain containment boundaries.** A denied
   candidate records a terminal denial and cannot reach Memory, Knowledge,
   typed Self, or capability profile materialization.
10. **Completion is not authority.** Materialization readiness and natural
    parity never grant, expand, or imply execution permission.
11. **Debug reads are non-causal.** Dev Tools cannot invoke reconciliation,
    retry a target, edit a receipt, or manufacture a terminal state.
12. **Conflicting receipts fail closed.** A terminal status inconsistent with
    the immutable candidate decision, a mismatched target kind, a missing
    materialized target ID, or multiple terminal target IDs is invalid, blocks
    automatic retry, and remains inspectable instead of looping or being
    overwritten.

## Derived Resolution Contract

Each learning run has a metadata-only resolution:

```text
shadow-task-learning-materialization-v1

run_id
complete
candidate_count
terminal_count
materialized_count
skipped_count
denied_count
error_count
invalid_count
missing_count
pending_revision_ids
error_revision_ids
```

The contract contains no Memory body, Knowledge body, Self statement, evaluator
output, activity text, prompt, or user result.

Resolution rules:

```text
zero candidates                         -> complete
any materialized/skipped/denied receipt -> candidate terminal
error receipt only                      -> candidate pending
historical failed receipt only          -> candidate pending
inconsistent terminal receipt           -> invalid and pending
no receipt                              -> candidate pending
all candidates terminal                 -> complete
```

If both an older error and a later terminal receipt exist, the candidate is
terminal while the historical error remains inspectable.

## Continuation v2

The continuation contract advances to `task-shadow-continuation-v2` and adds:

```text
action: materialize_learning
state: learning_materialization_pending
item.learningMaterialization
snapshot.materializationPendingCount
recovery.materializationRecoveredRunIds
```

An evaluated run with no learning projection still uses `learning_pending` and
the existing evaluation continuation. A linked projection with unresolved
materialization uses `learning_materialization_pending` and the new model-free
action. A run is `complete` only when its linked resolution is complete.

Recovery remains bounded by the existing newest-first 100-run continuation
scan and Shadow feature toggle.

## Natural Parity Gate v6

The gate keeps the same nine semantic steps but advances to
`task-shadow-release-gate-v6`. Its learning step completes only when:

- a current `shadow-task-learning-v2` run includes or explicitly excludes one
  of the current evaluation IDs; and
- that run's materialization resolution is complete.

The learning step evidence ID remains the immutable learning run ID. Dev Tools
shows its candidate, terminal, pending, error, and target outcome counts.

## Functional Dev Tools

Production continuation and the natural parity section show the linked
learning run's resolution state and counts. Existing Task Episode learning
detail remains the drill-down for revisions and receipts.

There is no retry, repair, materialize, promote, approve, execute, or clear
button. The reconciler is an unexported Go method, and Browser RPC remains
read-only.

## Validation

Backend tests must prove:

- missing and error-only receipts remain pending;
- `materialized`, `skipped`, and `denied` each resolve a candidate;
- an error followed by a terminal receipt resolves without deleting history;
- no-candidate projections resolve without a target write;
- retrying identical receipts ignores retry time and remains idempotent;
- identical Memory and Knowledge bytes are not snapshotted or rewritten;
- typed Self and capability profile retries do not duplicate state;
- projection persistence returns its real `added` value;
- a persisted projection with missing receipts is recovered without a model
  call, and repeated recovery is idempotent;
- continuation v2 and release gate v6 remain incomplete until resolution;
- First Run, correction, and authority containment still produce terminal
  denial without target mutation;
- read-only snapshots do not change database or file counts; and
- browser RPC exposes no reconciliation method.

Frontend tests must prove:

- continuation and natural parity show pending and complete resolution counts;
- compact and desktop layouts remain stable;
- existing learning detail remains reachable; and
- no materialize, retry, repair, approval, or execution control appears.

Release validation includes focused tests, race coverage, uncached full Wails
tests, Wails vet, all frontend tests and production build, complete agent tests
and vet, rebuilt live read-only RPC, visual and interaction QA, and native QA
only after macOS is naturally unlocked.

## Validation Outcome

Validation on 2026-08-05 passed:

- focused backend lifecycle, resolution, reconciliation, release, observer,
  idempotency, and containment tests in 14.643 seconds;
- focused race coverage in 398.347 seconds with only the established malformed
  macOS `LC_DYSYMTAB` linker warning;
- the uncached complete Wails tree in 132.074 seconds and Wails vet;
- the complete `yansu-agent` tree in 6.139 seconds and agent vet;
- all 45 frontend files and 228 tests in 14.38 seconds; and
- the production frontend build in 7.07 seconds with only the established
  large-chunk warning.

Standalone TypeScript retains established unrelated application and generated
alias failures; no Task Shadow component or Task 198 shim error is present.

The rebuilt live debugger reports `task-shadow-continuation-v2`,
`task-shadow-release-gate-v6`, 51 scanned historical runs, zero current runs,
zero pending materializations, and `desktop_locked` at `loginwindow`. The live
learning ledger remained stable across repeated debugger reads at one
historical run, two revisions, two skipped receipts, and zero typed Self rows.
Browser RPC rejects the unexported reconciliation method with HTTP 403.

Profile -> Setting -> Dev Tools -> Shadow Agent -> Task episodes verified the
functional continuation panel and its `materialize` count at desktop and
compact widths. No source, trace, Task Episode, run, attempt, result,
evaluation, learning, Memory, Knowledge, Self, Initiative, authority,
permission, external action, or production effect was fabricated. Native
signed-in QA remains deferred while macOS is naturally locked; Wails and Vite
remain running.

## Non-Goals

Task 198 does not:

- change learning thresholds, evaluator prompts, or candidate statements;
- promote collecting or rejected candidates;
- add a new Memory, Knowledge, or Self schema;
- feed Shadow output or evaluator prose into durable user context;
- retry model work or production activity;
- create a Task Episode, Shadow run, user result, comparison, or learning run;
- add a workflow or destination;
- grant authority or create an Initiative; or
- expose silent Shadow as a user review queue.
