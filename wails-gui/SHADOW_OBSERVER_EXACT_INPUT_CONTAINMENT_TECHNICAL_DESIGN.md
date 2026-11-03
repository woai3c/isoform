# Shadow Observer Exact-Input Containment Technical Design

## Problem

The activity pipeline can revisit the same retained session group after a
legacy triage timeout, cancellation, or process rebuild. Every visit currently
records a new observer root and runs the Shadow onset or lifecycle flow again.
In production this has produced dozens of model calls and observer traces for
one unchanged retained input.

Downstream Task Episode identity and lifecycle-source gates contain many of the
resulting writes, but they do not prevent the repeated observer work itself.
Repeated calls waste model capacity, obscure source coverage, and make a clean
natural task -> Shadow attempt -> user result -> comparison chain harder to
establish.

## Product Contract

Shadow observes production behavior; it does not repeatedly reinterpret one
unchanged production record.

```text
new retained input
  -> immutable observer root
  -> normal silent Shadow flow
  -> terminal result

same exact retained input again
  -> new source-linked immutable observer root
  -> terminal Already handled receipt
  -> immutable link to the original observer result
  -> no model or lifecycle flow
```

Every activity-pipeline decision keeps its own observer trace so source
coverage remains one-to-one. A duplicate never points two pipeline rows at one
trace and never erases either source decision.

## Exact Input Identity

`task-shadow-observer-input-v1` hashes the observer inputs that can change the
result:

- flow: `full_onset` or `lifecycle_continuation`;
- group primary ID;
- canonical session manifest hash;
- observation-window start and end;
- session count and declared event count;
- retained event count; and
- content-sensitive retained event manifest hash.

The retained event manifest includes ordinal, timestamp, kind, and a SHA-256
digest of the complete retained `ActivityEvent`. Raw activity content is never
written to the observer ledger. Admission labels, pipeline-run identity,
runtime identity, and feature-toggle state are intentionally not part of the
input identity: they explain why a source reached the boundary but do not alter
the retained activity supplied to that flow.

Full-onset and continuation identities cannot alias. A changed retained event,
window, session manifest, or count creates a different identity.

The derived identity is stored in the immutable observer root table and
indexed. Existing roots keep an empty identity and remain valid; they cannot
suppress a current check because their older event-manifest shape does not
prove exact semantic equality.

## Reusable Original Policy

An exact prior trace may suppress repeated work only when its complete hash
chain verifies and one of these states is true:

| Prior state | Reusable | Reason |
| --- | --- | --- |
| `completed` | yes | The exact input reached the normal terminal boundary. |
| `skipped` because Shadow was off | yes | Disabled production input is not backfilled later. |
| current-runtime `started` | yes | One identical check is already queued or running. |
| `completed_with_errors` with an emitted Task Episode, Shadow run, workflow decision, expired task, or recovered continuation effect | yes | Repeating the source could duplicate a partial effect. |
| `completed_with_errors` with errors only | no | A later natural retry may recover from a transient dependency failure. |
| `cancelled` or `failed` | no | The exact input did not reach a usable result. |
| prior-runtime `started` (derived `interrupted`) | no | The trace remains immutable and the normal production source may retry. |
| corrupt or unsupported | no | Invalid provenance is never consulted by routing. |
| an `Already handled` receipt | no | Duplicate links never form chains. |

This policy suppresses duplicate dispatch; it does not retry from a trace.
Task 194 continuation remains the only recovery planner for existing Shadow
runs.

## Append-Only Duplicate Receipt

The new source decision records a normal observer root with its own positive
pipeline-run ID. Its first and only event is `skipped` with bounded metadata:

- policy version;
- original trace ID;
- original root hash;
- original latest-event hash and state at duplicate admission; and
- the shared exact-input identity hash.

Developer Tools renders this state as `Already handled`, explains that no task
check was rerun, and provides a read-only link to the original trace. Exact IDs,
hashes, and policy identity remain under technical detail.

## Concurrency

Observer input admission is serialized on the `ActivityTriager` before a root
is recorded. The lock covers exact lookup plus original-or-duplicate root
commit. Together with the application's single-instance boundary, two
concurrent dispatches for one exact input cannot both become real executions.
The existing Shadow semaphore continues to serialize the accepted work itself.

## No-Effect Manifest

An `Already handled` receipt may write only:

1. one observer root linked to the new activity-pipeline source row; and
2. one terminal observer event linked to the original trace hashes.

It must not create or change:

- Task Episode evidence, status, or transitions;
- model attempts or Shadow runs;
- results, comparisons, evaluations, revisions, or materializations;
- Memory, Knowledge, Self, Purpose, or work-pattern state;
- Initiative, Handoff, Schedule, or chat state;
- permission requests, decisions, grants, or authority ledgers; or
- files, browser state, messages, external systems, or execution state.

Reading either trace or following the relation is side-effect free.

## Verification

1. A completed exact full-onset check suppresses the next identical dispatch.
2. The duplicate has its own source-linked, integrity-valid skipped trace and
   links directly to the original; no model runner is called.
3. A content, manifest, window, count, or flow change is not a duplicate.
4. Continuation never suppresses full onset, or the inverse.
5. Failed, canceled, interrupted, corrupt, and error-only traces allow retry.
6. Disabled, current in-flight, and partial-effect traces suppress duplicate
   work without creating an authority or execution effect.
7. Concurrent identical dispatches create one real execution and one duplicate
   receipt.
8. Coverage remains exact and healthy for both source rows.
9. Developer Tools labels the duplicate `Already handled` and opens the exact
   original trace without a write control.
10. Focused, race, full backend, frontend, build, live no-effect, and immutable
    ledger checks pass.
