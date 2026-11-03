# Yansu Background Purpose Understanding Technical Design

## Problem

The Purpose ledger, reconciliation rules, Agency integration, product states,
and diagnostics are complete, but normal application runtime never invokes the
extractor. Purpose can currently enter production only through a direct user
statement. A user may therefore have rich Memory, Knowledge, typed Self, and
Shadow learning while Agency still sees no current outcomes.

The missing product loop is:

```text
durable personal evidence changes
  -> freeze complete eligible evidence and current Purpose at one cutoff
  -> generate hypotheses with no tools
  -> reconcile deterministically into the append-only Purpose ledger
  -> emit Purpose change events
  -> let ordinary Agency origination reconsider independent work
```

This service understands current purpose. It does not assign work, execute an
Initiative, grant authority, or ask the user to review routine hypotheses.

## Invariants

1. Existing Purpose is context, never evidence.
2. Memory, Knowledge, and typed Self remain the only extraction evidence.
3. Every included evidence item retains exact source identity, version, hash,
   privacy decision, evidence class, independence key, and observed time.
4. The complete current Purpose map is frozen at the same cutoff. If it cannot
   fit the declared context budget, the cycle fails closed instead of operating
   with duplicate blindness.
5. Model output remains a hypothesis. Deterministic reconciliation alone may
   append a Purpose event or relation.
6. Behavior never proves hidden reasoning. Explicit rationale requires an
   explicit-user or correction source; otherwise reasoning remains unknown.
7. A verified local outcome is still required for Agency-driven resolution.
   Agent prose and worker output are not completion evidence.
8. One stable evidence fingerprint permits at most one model call. Failed runs
   are not silently retried against unchanged evidence.
9. Confidence, frequency, Self, Shadow qualification, or Purpose support never
   grants authority.
10. The service cannot call tools, mutate external systems, create a work item,
    or expose a browser replay endpoint.

## Frozen Cycle Input

Version 2 of the extraction contract adds a separate `currentPurpose` section.
Each entry includes:

- root ID and canonical key;
- exact cutoff-safe Purpose reference and projection hash;
- kind, title, desired outcome, owner, scope, due and waiting state;
- support, lifecycle, and reasoning status; and
- active duplicate targets.

The model may use this section to select a stable canonical key and distinguish
support, update, waiting, contradiction, resolution, dismissal, or duplicate
relations. It may not cite these refs as evidence. The output validator accepts
only refs from the frozen evidence source set.

Historical version-1 runs remain readable and hash-valid. Validation and prompt
reconstruction dispatch on the stored input, manifest, and prompt versions;
the current writer emits only version 2.

## Stable Evidence Fingerprint

The scheduler computes a canonical fingerprint from:

- extraction policy and prompt versions;
- selected model and query;
- every included and excluded source decision;
- source versions, content hashes, evidence classes, privacy decisions, and
  independence keys; and
- every cutoff-safe current Purpose reference and projection hash.

Wall-clock cutoff and generated timestamps are excluded. The fingerprint is
the scheduler idempotency identity. If it already has a run:

- a successful run is reconciled again only to recover a crash window;
- a failed run stays failed until durable evidence or policy changes; and
- a no-source run remains a truthful no-op until evidence changes.

This prevents periodic checks from becoming periodic model spending.

## Runtime Service

`PurposeUnderstandingService` is application-owned and starts only after the
activity store, model runner, Memory pipeline, and typed Self storage are ready.

Triggers are:

- startup warmup;
- `memory:updated`;
- `self_model:updated`; and
- a low-frequency periodic recovery tick.

Triggers are coalesced. One cycle may run at a time. A durable-context change
arriving during a cycle schedules one later check rather than cancelling the
frozen run. Shutdown cancels the active no-tools call and waits for the service
to settle.

After reconciliation, the service compares immutable receipts and emits
`purpose:updated` only for roots that received a new event or relation. It then
emits `agency:updated`; the existing Agency controller performs ordinary
origination under unchanged admission, operating-envelope, and action-policy
checks.

## Product Experience

Purpose understanding is ambient, not a review queue.

- **Agency cold start:** show whether Yansu is waiting for durable evidence,
  checking changed context, or has current supported outcomes.
- **Self / What matters now:** continue to show only canonical supported
  Purpose, never raw hypotheses.
- **History:** show only immutable reconciled Purpose events and relations.
- **Dev Tools / Purpose Map:** add scheduler health, trigger, fingerprint,
  source counts, result, duration, and error. Keep raw bodies, prompts, model
  output, replay, repair, mutation, authority, and execution out of controls.

No progress percentage is invented. A cold user sees concrete states such as
`collecting durable context`, `checking 18 changed sources`, or `no supported
current outcome yet`.

## Failure And Recovery

- Missing runner or model records an unavailable cycle state without a fake
  extraction result.
- Shutdown cancellation or deadline expiry records transient runtime health but
  does not persist a failed hypothesis run for otherwise unchanged evidence.
- No eligible sources may persist one fingerprinted no-source run and makes no
  model call.
- Source drift after freeze fails reconciliation closed and waits for a new
  evidence fingerprint.
- Malformed output persists one failed run; unchanged evidence is not retried.
- Process failure after an event but before its receipt uses existing
  deterministic reconciliation recovery.
- Corrupt Purpose or extraction history prevents the cycle and emits no product
  or Agency update.

## Verification Gates

Before rollout, prove:

- complete cutoff-safe current Purpose context with no fixed root count;
- version-1 historical read compatibility and version-2 strict validation;
- current Purpose refs cannot satisfy evidence requirements;
- stable fingerprint dedup across changing wall-clock cutoffs;
- a changed source, Purpose projection, model, or policy creates a new cycle;
- single-flight, coalescing, cancellation, restart, and crash recovery;
- no tools, authority, work-item, external-write, Memory, Knowledge, Self,
  Shadow, Handoff, or Schedule mutation;
- exact Purpose and Agency events only after new deterministic reconciliation;
- functional cold, running, no-change, changed, unavailable, failed, and retry
  product states; and
- focused, full, race, build, live, desktop, and narrow-window checks.

## Implementation Status

Tasks 148 through 151 are complete. The current writer emits extraction input,
manifest, and prompt version 2 with a complete cutoff-safe `currentPurpose` projection.
Validation and prompt reconstruction retain exact version-1 compatibility.
`purposeUnderstandingEvidenceFingerprint` provides a stable scheduler identity
that changes with evidence, Purpose, model, or policy but not with the moving
wall clock. Dev Tools exposes the frozen context and accounting as read-only
provenance.

The production service now starts after the runner is ready, coalesces the
declared triggers, executes the exact fingerprinted frozen input, recovers
missing deterministic receipts, and emits updates only for reconciled Purpose
events or relations. Fresh and already-ready runner paths start it atomically;
idempotent `StartAgent` calls cannot retrigger it, and the internal startup
timer fires only when no first cycle or pending trigger exists.

Agency consumes a product-safe projection of the read-only runtime snapshot.
It shows concrete cold-start and active states, but suppresses a stable
`no_change` banner when canonical Purpose is already present. Dev Tools exposes
the complete scheduler health needed for diagnosis, including triggers,
fingerprint, source counts, cache state, duration, run identity, and bounded
errors. Neither surface adds replay, retry, repair, mutation, authority, or
execution controls.

The service fingerprints and reports the effective configured primary model,
not the caller's role alias. API BYOA uses its configured primary model,
external agents use their configured model, and the default native path pins
the alias to the same Anthropic API identity used by the runner. Changing the
effective model therefore creates a new cycle instead of reusing an
incompatible successful cache entry.

The release gate passed historical-compatibility, stable-cache, cancellation,
single-flight, containment, focused race, full Go, vet, agent race, live
restart, forbidden-RPC, responsive Browser, and console checks. A real live
cycle reconciled one supported current goal from two independent redacted
retained-memory sources, emitted one ordinary Agency wake, and left Shadow,
task episodes, Self directives, work items, permissions, Handoffs, and
Schedules unchanged. Native visual inspection remains pending only because
macOS is locked; the lock was not bypassed.
