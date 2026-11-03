# Task Lifecycle State-Bound Lease Technical Design

Status: implementation contract for Task 254 on `vision/shadow-agent`.

## Problem

Task Episodes are bounded observations of user work. An open episode currently
remains eligible until its status-specific observation horizon has elapsed
since the latest timestamp attached to the episode. That timestamp includes
every model-associated action, contradiction, and result evidence row.

This lets weak lifecycle associations renew old work indefinitely. A generic
activity event can be attached to an old active episode, move its expiry
deadline forward by another 24 hours, and place that episode back into later
lifecycle prompts. Repeated associations then create self-renewing stale tasks,
pollute task-boundary reasoning, and prevent Shadow from reaching enough real
terminal outcomes to compare and learn.

The production store demonstrates this failure: tasks first observed several
days earlier remain active because recent generic action evidence was attached
to them, while only a small minority of Task Episodes reach completion and
evaluation.

## Product Contract

An open Task Episode receives a finite **state-bound observation lease**.

- A candidate lease begins at the latest of inferred start, detection, and the
  persisted transition that established the current candidate state.
- An active lease begins at the latest of inferred start, detection, and the
  persisted transition that established the current active state.
- A paused lease begins at the latest of inferred start, detection, and the
  persisted transition that established the current paused state.
- Action, contradiction, reasoning, and result evidence remain append-only and
  inspectable, but never renew the lease.
- A later valid state transition, such as pause or resume, establishes a new
  lease for the new state.
- Completed, abandoned, merged, split, and expired episodes have no open lease.

The current 90-minute candidate, 24-hour active, and 72-hour paused horizons
remain unchanged. Task Episodes describe bounded work sessions, not durable
projects; a long-running project should produce later observed task episodes
rather than keep one episode alive through unrelated activity.

## Deterministic Lease

For an open episode:

```text
anchor = max(inferred_start_at, detected_at,
             occurred_at of the highest-sequence transition
             whose to_status equals current_status)
deadline = anchor + horizon(current_status)
due = cutoff_at >= deadline
```

Sequence, rather than the largest transition timestamp, selects the current
state boundary. This matches the append-only state derivation and avoids using
an out-of-order timestamp from an older state. A future anchor fails closed as
not due and remains visible in Developer Tools for diagnosis.

The lease calculation is shared by expiry, lifecycle-input admission, and the
read-only debugger. There is no second UI-only definition.

## Source Replay Containment

A finite lease is insufficient if the same raw activity can be interpreted
again as a new pause, resume, or switch. Each activity event therefore receives
a deterministic source identity derived from its captured timestamp, app,
interaction, text, speaker, and destination-relevant fields. Its identity does
not depend on its position in a later session-group slice.

Before lifecycle input is built, Yansu excludes a source already persisted as
Task Episode evidence anywhere in the append-only ledger. Historical rows from
before stable source identities are recognized conservatively by exact observed
timestamp and summary. One raw activity source may support one normalized
lifecycle observation, including a multi-episode switch, but it cannot be
replayed in later model calls to create another state boundary.

An unconsumed source remains eligible for a later interpretation. A consumed
source is preserved in history and omitted from later prompts; it is never
deleted or rewritten.

## Fail-Closed Prompt Admission

Normal activity processing attempts to persist expiry transitions before it
builds a lifecycle prompt. Persistence failure must not make stale work visible
to the model. `BuildTaskLifecycleInputFromActivity` therefore loads each open
episode's immutable detail and independently excludes any episode whose lease
is already due at the prefix boundary.

This filter is read-only. It does not repair, rewrite, or delete history, and it
does not replace the normal expiry transition. A later successful expiry pass
still appends the existing `expired` transition.

## Developer Tools

Shadow learning diagnostics expose the lease without adding a mutation control.
For an observed task, the detail view shows:

- whether its observation window is open, due, or closed;
- the current-state boundary that anchored the window;
- the deadline and status-specific duration; and
- the policy version and explicit fact that attached activity does not extend
  the window.

The aggregate lifecycle panel uses the same state-bound anchor when counting
overdue open tasks. Historical action evidence remains available below the
lease explanation, so an incorrect association can still be diagnosed.

## Containment And Effects

This milestone may append only the already-defined `expired` Task Episode
transition. It has no permission, authority, Handoff, Schedule, Initiative,
execution, Memory, Knowledge, Purpose, Self, profile, Shadow-learning, or model
tool effect. It does not delete or rewrite historical evidence.

The lifecycle and Shadow model calls remain no-tools, ephemeral, and
telemetry-suppressed, with tool auto-approval disabled.

## Verification Gates

1. New action, contradiction, reasoning, or result evidence cannot extend an
   open episode's lease.
2. A valid pause or resume transition establishes the lease for the new state.
3. An episode is due exactly at its state-bound deadline and not before it.
4. A due episode is excluded from lifecycle input even when no expiry mutation
   has run.
5. A production-shaped old active episode with recent polluted action evidence
   expires and cannot enter the next lifecycle prompt.
6. A raw activity event has one stable source identity regardless of slice
   position and cannot be consumed again in a later lifecycle prompt.
7. Legacy exact timestamp-and-summary evidence suppresses one historical replay
   without rewriting the old row.
8. Completed and other terminal episodes retain their existing read behavior.
9. Aggregate and task-detail diagnostics report the same anchor and deadline as
   backend admission.
10. Debugger reads remain read-only and historical evidence remains unchanged.
11. No permission, authority, execution, derived-understanding, or learning
   records are created by expiry or diagnostic reads.
