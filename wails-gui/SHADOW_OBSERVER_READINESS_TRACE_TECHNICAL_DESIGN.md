# Shadow Observer Readiness Trace Technical Design

Status: implemented and validated for Task 195 on `vision/shadow-agent`

## Problem

The production Shadow loop can now continue independently and recover safely,
but a cold start with no current task-mirror run is still opaque. Existing
records begin or end at the wrong boundaries:

1. `activity_pipeline_runs` describes generic activity triage and is retained
   only as a bounded operational log. It does not follow the asynchronous
   Shadow path to a terminal outcome.
2. `TaskAgentAttempt` begins after a model invocation starts. It cannot explain
   a disabled toggle, generic prefilter, cooldown, First Run exclusion, empty
   onset window, or a process exit before model work.
3. Task Episode diagnostics begin only after onset produced and persisted an
   episode. They cannot explain why an ordinary retained group produced none.
4. Logs are not a durable, queryable production receipt and cannot prove which
   exact source group, feature snapshot, or admission decision was involved.

Without this boundary, zero current runs may mean no eligible work, a disabled
feature, missing retained evidence, a model/runtime failure, a workflow outside
the closed registry, or an interrupted asynchronous dispatch. The product and
engineering implications are very different.

## Product Meaning

Shadow mode is a production shadow deployment for the user's agency. It should
receive real retained activity, attempt the same bounded work independently,
and make every admission and containment decision inspectable before Yansu is
trusted to act.

Task 195 adds observability, not new behavior. It answers:

- Did this retained activity group reach the Shadow boundary?
- Was it eligible for onset or continuation only, and why?
- Was Shadow enabled at admission and again at execution?
- Was guided First Run activity excluded from natural learning?
- How many source sessions, raw events, retained onset evidence items, and
  onset windows were available?
- Which model attempts, Task Episodes, workflow decisions, and ShadowRuns were
  produced?
- If no run was produced, which deterministic gate stopped it?
- Did the asynchronous path complete, fail, cancel, or end with no terminal
  event because the process exited?

The trace is not user evidence and must never improve a readiness score merely
because debugging became more detailed.

## Boundary

```text
retained session group
  |
  +-- generic admission
  |     eligible -----------> full onset plus lifecycle
  |     prefiltered --------> lifecycle continuation only
  |     cooldown -----------> lifecycle continuation only
  |
  +-- observer trace root
  |     source IDs and hash
  |     timing and counts
  |     admission and toggle snapshot
  |     started or skipped event
  |
  +-- asynchronous Shadow flow
        First Run check
        retained onset-window construction
        model attempts
        Task Episode capture
        closed-workflow readiness
        task-mirror run dispatch
        continuation recovery
        terminal trace event
```

The generic admission rule continues to sample onset. Prefiltered and cooldown
groups remain lifecycle-only. Task 195 does not widen onset eligibility.

## Immutable Contract

Task 195 introduces:

```text
task-shadow-observer-trace-v1
```

Each dispatch writes one immutable root to
`task_shadow_observer_traces_v1` and append-only events to
`task_shadow_observer_trace_events_v1`.

### Root

The root contains only metadata that exists before Shadow model work:

- random trace ID and contract version;
- random process-runtime instance ID so a reader can distinguish an active
  in-process `started` trace from a prior-process `interrupted` trace;
- group primary ID and sorted session IDs;
- canonical session-group digest;
- source `activity_triager`;
- flow `full_onset` or `lifecycle_continuation`;
- generic admission `eligible`, `prefiltered`, or `cooldown`;
- generic admission reason;
- group start, end, session count, declared event count, and retained event
  count;
- retained event-manifest digest, never raw activity content;
- Shadow-enabled snapshot at admission;
- creation time and canonical root payload hash.

Every retained group considered by the triager calls this boundary, including
when Shadow is disabled. A disabled trace contains metadata only and receives a
terminal `skipped` event without scheduling model work.

### Events

Events are append-only, monotonically sequenced, and hash-chained to the root:

- `started`: Shadow was enabled and asynchronous work was queued;
- `completed`: the flow reached its terminal boundary without an error;
- `completed_with_errors`: the bounded flow completed and retained one or more
  diagnosable errors;
- `skipped`: the feature was disabled at admission or after queueing;
- `cancelled`: the parent context ended before work acquired the serialized
  Shadow slot; or
- `failed`: the flow could not run or its terminal result could not be formed.

A latest `started` event with no terminal successor is derived as
`interrupted`. No startup write is needed to manufacture that fact.

Each terminal event contains a canonical, metadata-only result:

- enabled snapshot at execution;
- First Run decision `included`, `excluded`, or `error`;
- onset-window count and retained onset-evidence count;
- production onset attempt ID and any diagnostic ablation attempt IDs;
- lifecycle attempt IDs, Task Episode IDs, expired episode IDs, and ShadowRun
  IDs;
- workflow readiness decisions;
- continuation summary;
- bounded diagnostics and errors; and
- canonical payload hash plus previous-event hash.

Raw prompts, raw model output, raw activity, committed user result text,
screenshots, OCR, email content, Memory content, Knowledge content, and Self
statements are excluded. Existing ledgers remain the source of truth for those
records.

## Workflow Readiness Decision

Every captured or reconciled Task Episode considered for a production
task-mirror run contributes a decision:

- source `captured_onset` or `reconciliation`;
- Task Episode ID and derived workflow key;
- state:
  - `outside_registry`;
  - `frozen_context_not_ready`;
  - `first_run_excluded`;
  - `existing_run`;
  - `run_created`;
  - `run_create_failed`;
  - `candidate_limit_deferred`; or
  - `readiness_error`;
- normalized reason;
- frozen-context manifest hash and counts;
- whether exact pre-onset activity snapshot context exists;
- linked ShadowRun ID, when one exists; and
- whether isolated execution was dispatched.

This record explains production readiness. It cannot qualify a capability,
grant authority, or execute a task.

## Toggle And Interruption Semantics

1. The admission trace is recorded regardless of the Shadow toggle so a
   disabled production observer is visible.
2. When disabled at admission, the trace receives `skipped` and no goroutine,
   model attempt, Task Episode, ShadowRun, evaluation, or learning write occurs.
3. When enabled, `started` is persisted before queueing.
4. The toggle is checked again after the serialized Shadow slot is acquired.
   If it changed to disabled, a terminal `skipped` event is appended and no
   model work occurs.
5. Parent cancellation before slot acquisition appends `cancelled` when the
   store remains available.
6. A process exit after `started` leaves the trace immutable. Readers derive
   `interrupted`; they do not retry from the trace.
7. Task 194 continuation remains the only recovery planner. The observer trace
   cannot invoke or authorize recovery.

## Persistence And Integrity

- Roots and events reject updates and deletes through SQLite triggers.
- Root IDs are random so repeated real dispatches remain separate historical
  facts. Event IDs are deterministic from trace ID, sequence, and payload hash.
- Events require the next sequence and the previous event hash.
- A terminal event forbids later events.
- Inputs are normalized, bounded, and encoded canonically before hashing.
- Debug reads recompute root, payload, and chain integrity. Invalid data is
  shown as corrupt and never consulted by production routing.
- Queries are bounded newest-first. Persistence is append-only; UI limits do
  not silently delete production diagnostics.

## Dev Tools

Shadow Agent Dev Tools gains an `Observer` tab with a functional master/detail
view.

The list shows:

- derived state and integrity;
- admission, flow, and source group;
- group time and source counts;
- onset windows, produced episodes, and produced runs; and
- a concise terminal reason.

The detail shows:

- exact source session IDs and digests;
- root admission metadata;
- the complete bounded event chain;
- linked attempt, episode, and run IDs;
- First Run and workflow decisions;
- continuation counts; and
- errors and diagnostics.

The APIs are read-only:

```text
GetTaskShadowObserverDebugSnapshot(limit)
GetTaskShadowObserverDebugDetail(traceID)
```

They are browser-RPC allowlisted for local preview. There is no record, replay,
recover, execute, approve, edit, delete, or authority method.

## Invariants

1. One real retained group produces one trace per actual dispatch path.
2. Trace writes never create or modify Task Episodes, ShadowRuns, evaluations,
   learning, Memory, Knowledge, Self, Purpose, Initiatives, authority grants,
   permissions, or external effects.
3. Debugger reads are side-effect free and never enter activity capture as user
   evidence.
4. The trace records existing production decisions; it does not add a second
   classifier or reinterpret workflow eligibility.
5. First Run exclusion stays fail closed.
6. No raw private content is copied into the trace or its debug contract.
7. Onset remains limited by generic eligibility. Continuation remains allowed
   for prefiltered and cooldown groups only to observe existing episodes.
8. Confidence and readiness remain measurements, never execution permission.

## Validation

Tests must prove:

- eligible, prefiltered, and cooldown groups record the correct flow and
  admission without widening onset;
- disabled-at-admission and disabled-after-queue produce terminal skipped
  traces with no model or evidence write;
- context cancellation and process-like unmatched `started` records derive
  cancelled and interrupted states correctly;
- First Run exclusion, empty onset windows, runner unavailability, onset
  failure, no predictions, unsupported workflow, missing frozen context,
  existing run, new run, and create failure are distinguishable;
- the exact production onset attempt is retained separately from optional
  ablation attempts;
- root and event update/delete, bad sequence, terminal append, hash tampering,
  oversized metadata, and malformed JSON fail closed;
- snapshot and detail reads are bounded, content-safe, and side-effect free;
- every list row opens a complete detail view and refresh works;
- Browser RPC exposes only the two reads; and
- focused, race, full backend, vet, agent, frontend, build, rebuilt live RPC,
  visual, interruption, and no-fabricated-evidence gates pass.

## Non-Goals

- No activity replay or synthetic production data.
- No new task, workflow, destination, or result classifier.
- No raw-prompt or raw-content duplication.
- No automatic retry based on a trace.
- No user review card for silent Shadow.
- No authority, permission, Initiative, Handoff, Schedule, external write, or
  proactive production action.

## Implemented Validation

Task 195 is implemented at the production dispatch boundary.

- Eligible, prefiltered, and cooldown admissions produce immutable roots before
  asynchronous work. Disabled admission produces one terminal metadata-only
  event and never queues model work.
- Enabled work appends exactly one terminal event for completed,
  completed-with-errors, skipped-after-queue, cancelled, or panic-contained
  failure. An exact terminal retry returns the original event; a conflicting
  terminal append fails closed.
- Guided First Run exclusion, empty retained evidence, missing runner, onset
  failure, no prediction, unsupported workflow, missing frozen context,
  existing run, new run, and run-creation failure are separately testable and
  inspectable.
- Production onset attempt IDs remain distinct from diagnostic work-profile
  ablation attempt IDs. Observer receipts contain only IDs, counts, fixed
  categories, and hashes; a closed diagnostic category registry prevents raw
  or forged already-redacted text from entering the ledger.
- Migration bootstrap detects and repairs a partially applied schema. SQLite
  constraints and triggers reject update, delete, bad sequence, bad hash link,
  malformed JSON, oversized identity, restart, and post-terminal append.
- The complete uncached Wails suite passed in 148.895 seconds. Wails vet, the
  complete agent test and vet suites, focused race coverage in 82.339 seconds,
  all 45 frontend files / 225 tests, and the production frontend build passed.
  Race linking emitted only the established macOS `LC_DYSYMTAB` warning. The
  standalone TypeScript check retains established application and generated
  alias failures and reports no Observer component error.
- Rebuilt Browser RPC returned `task-shadow-observer-debug-v1` twice with a
  real zero-row ledger and identical zero counts. It rejected the nonexistent
  replay method with HTTP 403. No trace, Task Episode, run, attempt, result,
  evaluation, learning, Memory, Knowledge, Self, Initiative, authority,
  permission, or external effect was fabricated for validation.
- Live profile -> Setting -> Dev Tools -> Shadow Agent -> Observer interaction
  verified the real zero state, refresh, search, status control, complete state
  labels, and a stable two-row count layout at 1280 by 720. A clean signed-in
  reload and navigation produced no browser warning or error.
- Native signed-in interaction remains unavailable while macOS is locked.
  Wails, Vite, and Browser RPC remain running; the lock was not bypassed.
