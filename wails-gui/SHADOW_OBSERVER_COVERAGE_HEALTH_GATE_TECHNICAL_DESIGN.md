# Shadow Observer Coverage and Health Gate Technical Design

Status: implemented and validated for Task 196 on `vision/shadow-agent`

## Purpose

Task 195 records an immutable receipt after a retained activity group reaches
the production Shadow boundary. The live ledger can truthfully contain zero
rows, but a zero by itself cannot distinguish:

- no new activity group has reached the instrumented boundary;
- an activity group was classified but observer dispatch was never attempted;
- observer root persistence failed;
- a source diagnostic row or observer root was later pruned or lost; or
- the source and observer records refer to different production decisions.

Task 196 adds exact coverage provenance and a derived health gate. It answers:

> For every instrumented production activity-group decision, did exactly one
> immutable observer trace record the same source, admission, and flow?

The gate is operational observability. It does not decide whether a task began,
whether Shadow was correct, whether a capability is qualified, or whether Yansu
may act.

## Existing Production Flow

The activity triager currently performs:

```text
pending sessions
  -> merged activity group
  -> heuristic admission
       -> prefiltered continuation
       -> cooldown continuation
       -> eligible full onset
  -> observer root
  -> asynchronous Shadow flow
  -> observer terminal event
  -> activity pipeline row saved later
```

This order has three coverage gaps:

1. The observer root cannot identify the exact activity pipeline row because
   that row does not exist yet.
2. A retry can reuse the same group ID, so group ID plus timestamps is not an
   exact one-to-one key.
3. Cooldown decisions do not currently persist a group pipeline row.

Timestamp proximity, group-title similarity, and count matching are not
acceptable substitutes for provenance.

## Invariants

1. **One source decision, at most one observer trace.**
   A nonzero pipeline-run ID is unique in immutable observer roots.

2. **One observer trace, at most one source decision.**
   A nonempty observer trace ID is unique in retained source rows.

3. **Source identity is frozen before model work.**
   The group pipeline row is inserted before observer dispatch and its ID is
   part of the current observer root hash.

4. **Coverage is derived, never repaired.**
   Reading the gate cannot create a row, append an event, retry dispatch, run a
   model, or update either ledger.

5. **Historical absence is not failure.**
   Pipeline rows created before Task 196 have `observer_expected = false` and
   are counted as legacy. They are never labeled missing.

6. **A crash remains legible.**
   The source row begins in `pending`. If the process ends before dispatch, the
   retained row remains pending. If the observer root commits before source
   writeback, the immutable root still carries the exact source-row ID.

7. **Trace integrity remains authoritative.**
   A linked trace whose root or event chain fails verification is corrupt, not
   covered.

8. **Coverage has no product influence.**
   It is excluded from Task evidence, Memory, Knowledge, Self, Initiatives,
   qualification, readiness, authority, permission, recovery, and execution.

## Source Row Contract

`activity_pipeline_runs` gains bounded metadata fields:

```text
shadow_observer_expected         INTEGER NOT NULL DEFAULT 0
shadow_observer_admission        TEXT NOT NULL DEFAULT ''
shadow_observer_dispatch_status  TEXT NOT NULL DEFAULT ''
shadow_observer_dispatch_reason  TEXT NOT NULL DEFAULT ''
shadow_observer_trace_id         TEXT NOT NULL DEFAULT ''
```

Only `kind = group` rows created by the instrumented activity triager set
`shadow_observer_expected = true`.

Dispatch status is a closed enum:

```text
pending
recorded
store_unavailable
input_invalid
record_failed
```

Dispatch reason is a fixed safe category. Raw database, model, activity, panic,
or prompt text is never copied into these fields.

The existing pipeline table remains bounded to 200 rows and seven days. These
fields are operational metadata, not a new durable user model.

## Observer Root Contract

The current observer root contract advances to
`task-shadow-observer-trace-v2` and adds:

```text
source_pipeline_run_id INTEGER NOT NULL DEFAULT 0
```

For v2 roots this field is included in the canonical root payload and root
hash. A positive ID is required for a normally persisted production source
row. Zero is allowed only when source-row persistence failed before dispatch;
that creates an explicitly unlinked trace rather than hiding the observer work.

The verifier continues to support v1 roots by hashing the v1 payload without
the new field. New writes always use v2.

Partial unique indexes enforce:

```text
observer roots: UNIQUE(source_pipeline_run_id) WHERE source_pipeline_run_id > 0
pipeline rows:  UNIQUE(shadow_observer_trace_id) WHERE shadow_observer_trace_id <> ''
```

The existing observer update and delete triggers protect the new root column.

## Dispatch Flow

Each admission path follows the same order:

```text
construct group source row
  -> expected = true
  -> admission = eligible | prefiltered | cooldown
  -> dispatch status = pending
insert source row and obtain pipeline_run_id
  -> build v2 observer input with pipeline_run_id
  -> atomically insert observer root and first event
  -> return bounded dispatch receipt
  -> update source row with receipt and trace ID
  -> continue existing asynchronous Shadow behavior
```

Eligible, prefiltered, and cooldown paths all use this order. A disabled Shadow
toggle still records the exact source-linked terminal skipped trace. Prefiltered
and cooldown paths remain lifecycle-only and cannot enter onset.

The source-row insert is observability-only and best effort. If it fails, the
production observer may still record a v2 root with source ID zero; the health
gate then reports an unlinked current trace. Observability failure never blocks
the existing product pipeline.

## Derived Coverage Contract

The read-only API returns `task-shadow-observer-coverage-v1`.

For each instrumented source row it joins by exact pipeline-run ID, then checks:

- source row observer expectation and closed admission;
- immutable root source pipeline-run ID;
- optional source-row trace ID;
- exact group ID, admission, and expected flow;
- exact ordered session IDs;
- observer root and event-chain integrity; and
- whether more than one root claims the same source ID.

Coverage states are:

```text
covered
covered_root_only
pending_dispatch
dispatch_failed
trace_missing
link_mismatch
trace_corrupt
duplicate_trace
```

Observer-side states are:

```text
source_pruned_or_cleared
unlinked_current_trace
legacy_trace
```

`covered_root_only` means the immutable root committed with the correct source
ID but the mutable source-row receipt was not written before interruption. It
is complete provenance with an operational writeback warning, not a missing
trace.

The aggregate health state is:

```text
awaiting_instrumented_activity
healthy
attention
```

- `awaiting_instrumented_activity`: no instrumented source row or current v2
  trace exists. Legacy rows may still be present.
- `healthy`: at least one instrumented decision exists and every retained
  source/trace pair is exact and integrity-valid.
- `attention`: any pending, failed, missing, mismatched, corrupt, duplicate,
  unlinked, or scan-truncated current item exists. A valid root whose source
  row aged out through bounded retention remains informational.

Terminal Shadow outcomes do not affect coverage health. A correctly linked
skipped, interrupted, or completed-with-errors trace is covered because the
gate measures observer delivery, not model success.

## Retention Semantics

Observer roots are durable while activity pipeline diagnostics are bounded.
When a linked pipeline row is pruned or cleared, the root retains its exact
positive source ID and the gate reports `source_pruned_or_cleared`.

This state is informational because the immutable root still proves the exact
positive source identity while source diagnostics are intentionally bounded by
both age and count. It remains visible and counted separately so operators do
not confuse a retention boundary with a source-ID-zero unlinked write. Pending,
missing, failed, mismatched, corrupt, and unlinked states remain actionable.

Task 196 does not lengthen activity pipeline retention or prevent the existing
Dev Tools clear operation.

## Read-Only API

The only new Wails and Browser RPC method is:

```text
GetTaskShadowObserverCoverageSnapshot(limit)
```

The limit is clamped. The response contains source IDs, trace IDs, fixed state,
bounded counts and timestamps, admission, flow, integrity state, and safe
reason categories. It contains no activity text, OCR, screenshots, prompts,
model output, email content, or user-result content.

No clear, repair, link, replay, retry, dispatch, or export method is added.

## Dev Tools

Shadow Agent -> Observer gains two functional views:

```text
Traces    immutable root and event detail from Task 195
Coverage  exact source-to-trace health from Task 196
```

Coverage provides:

- aggregate health and instrumentation state;
- instrumented, covered, pending, failed, mismatch, corrupt, and retention
  counts;
- bounded search and state filtering;
- source-row and trace identity without raw activity content;
- a clear legacy explanation during cold start; and
- refresh plus event-driven updates.

There is no action button other than Refresh and view/filter controls.

## Failure Containment

- Source-row persistence failure cannot prevent observer dispatch.
- Observer root failure cannot prevent legacy triage.
- Source writeback failure cannot mutate an immutable root.
- Coverage query failure returns an error and does not produce a guessed state.
- Malformed source metadata is classified as mismatch or invalid; it is not
  used to repair a trace.
- Browser RPC remains localhost-only and read-only.

## Validation

Backend coverage must include:

- migration replay, bootstrap repair, and partial-index checks;
- eligible, prefiltered, cooldown, and disabled exact linkage;
- pre-dispatch crash/pending source;
- source insert failure with unlinked v2 trace;
- observer record failure with bounded failed receipt;
- trace committed before source writeback;
- missing, mismatched, corrupt, duplicate, legacy, pruned, and cleared source
  states;
- repeated group ID retries linked by distinct pipeline-run IDs;
- v1 and v2 root verification;
- no onset widening for prefiltered/cooldown work;
- no Memory, Knowledge, Self, Initiative, qualification, authority,
  permission, recovery, or execution side effect; and
- Browser RPC allowlist rejection for plausible mutation names.

Frontend coverage must include:

- cold start with legacy rows but no instrumented activity;
- healthy exact coverage;
- every attention state;
- search, filter, view switching, Refresh, and event refresh;
- constrained desktop and compact layouts; and
- no replay, repair, retry, approval, permission, or execute control.

Release validation includes focused tests, race coverage, uncached full Wails
tests, Wails vet, complete frontend tests and build, complete agent tests and
vet, live read-only Browser RPC, visual QA, clean browser diagnostics, and
native signed-in QA only when macOS is naturally unlocked.

No production trace or source row may be fabricated solely to make the live
gate nonempty.

## Implementation Result

Task 196 is complete. The production triager now persists an instrumented
group source row before observer dispatch on eligible, prefiltered, and
cooldown paths. The current v2 observer root freezes that exact row ID into its
canonical hash, while v1 verification remains backward compatible. Source and
root partial unique indexes enforce the one-to-one link.

The derived coverage query is read-only and fail-closed. It bounds roots to
4,096 rows and scans events from the same bounded root set, so a truncated gate
cannot defeat its own memory bound. Valid roots whose bounded source rows have
aged out are reported separately as retention information and do not create a
false health incident.

Focused migration, hash, dispatch-path, integrity, retention, truncation,
privacy, Browser RPC, and frontend interaction tests pass. The uncached full
Wails suite passes in 137.630 seconds, focused race coverage passes in 129.272
seconds with only the established malformed macOS `LC_DYSYMTAB` warning, and
Wails vet passes. The complete agent tests and vet pass. All 45 frontend files
and 227 tests plus the production frontend build pass; the build retains only
the established large-chunk warning. The standalone TypeScript check retains
only the established unrelated application and generated-alias failures.

After a cold rebuild, live Browser RPC reported 95 explicit legacy source rows,
zero instrumented sources, zero current traces, and
`awaiting_instrumented_activity`. A plausible repair method was rejected with
HTTP 403. The functional Coverage view, Refresh path, cold-state explanation,
and all metrics were verified at 1280 by 720 with clean browser diagnostics.
No source, trace, Task Episode, result, learning, permission, or external effect
was fabricated. Native signed-in QA remains unavailable while macOS is locked;
Wails and Vite remain running.

## Non-Goals

Task 196 does not:

- add a workflow or destination classifier;
- replay or synthesize retained activity;
- create a Task Episode or Shadow run;
- retry an interrupted observer flow;
- change First Run containment;
- change Memory, Knowledge, Self, or learning prompts;
- change qualification or readiness thresholds;
- grant authority or permission;
- create an Initiative, Handoff, Schedule, message, browser write, or external
  action; or
- turn silent Shadow into a user review queue.
