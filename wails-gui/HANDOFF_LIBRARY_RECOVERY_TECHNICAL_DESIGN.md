# Truthful And Recoverable Handoff Library

Status: Task 265 completed on `vision/shadow-agent`.

## Problem

Before this task, the Handoff library collapsed every `ListHandoffs` failure
into an empty array and rendered the same empty state before the first read had
settled. The product therefore said `No handoffs yet` while the store was still
loading or when a browser binding failed. In the production profile used for
verification, the bridge returned 21 product-safe Handoffs after a multi-second
read while the old live library immediately rendered the empty state.

The action flow also treats several distinct failures as if `AcceptHandoff`
itself failed:

```text
accept Handoff
-> reload list
-> start or open execution chat
-> one shared catch handler
```

If acceptance succeeds and either later step fails, the user sees `Couldn't
start handoff` and can retry an action that has already changed state. Abort
and delete rely entirely on an asynchronous update event and provide no direct
read recovery.

## Product Contract

The library has four honest read states:

- `loading`: the first read has not settled;
- `ready with rows`: current product-safe Handoffs are available;
- `ready empty`: a successful read proved that there are no Handoffs;
- `failed`: the read did not prove anything and must offer Retry.

A failed refresh after a successful read retains the last confirmed rows and
shows that they may be out of date. It never replaces known data with a false
empty state.

## Command Boundaries

Each state-changing command has an explicit point of no return:

- `AcceptHandoff` succeeds when the backend returns its exact execution
  reference. A later list refresh or chat-opening failure cannot relabel that
  acceptance as failed or call `AcceptHandoff` again.
- `AbortHandoff` succeeds when the backend returns. The library then performs
  a read refresh; a refresh failure reports stale display state, not a failed
  abort.
- `DeleteHandoff` succeeds when the backend returns. The deleted row is removed
  locally and the library reconciles with a read refresh. A refresh failure
  cannot restore the row or repeat deletion.

Expected string or JavaScript `Error` rejections receive the same bounded
product message. Unexpected internal text is not interpreted as authority.

## Agency Ownership

The Handoff library remains a work library and historical record, not a second
permission surface.

- Active Agency Handoffs route to Desk for their current permission decision.
- Stale Agency Handoffs remain readable and have no accept, reject, defer, or
  delete command.
- Legacy Handoffs retain their established actions.
- Loading, Retry, search, sort, filtering, opening, and refresh are read-only.

## Browser And Native Compatibility

Both environments use the same `ListHandoffs` product API. Browser preview
must call the allowlisted read through the shared shim; native Wails continues
to use the generated binding. A binding or bridge failure is shown as a failed
read, never an empty library.

## Safety Invariants

- A library read or Retry creates no Handoff transition, permission decision,
  authority grant, Schedule, Initiative, action, Memory, Knowledge, Purpose,
  Self, profile, or Shadow-learning record.
- A successful state change is never retried automatically.
- Refresh failure cannot widen authority or make an Agency Handoff actionable.
- Product rows continue to use the verified Handoff projection; persisted
  Agency execution instructions remain hidden.
- Event-driven refresh and explicit refresh share one generation-aware loader,
  so an older response cannot replace a newer one.

## Implementation

- The first read now renders a stable loading state. Only a successful empty
  response may render `No handoffs yet`.
- Initial failures render a bounded localized reason and Retry. Failures after
  a successful read retain the confirmed rows and mark the list as possibly
  out of date.
- Acceptance updates the exact row from the returned execution reference
  before chat adoption. A chat-opening failure is reported as a follow-up
  problem and cannot expose a second acceptance attempt.
- Cancellation and deletion reconcile local state immediately after backend
  success, then perform a generation-aware read. A failed read cannot undo or
  relabel the completed command.
- Technical rejection details remain in console diagnostics; the product uses
  localized, bounded recovery copy for both JavaScript `Error` and string
  rejections.

## Validation

Frontend coverage must prove:

- initial loading is distinct from successful emptiness;
- initial read failure renders Retry and not `No handoffs yet`;
- Retry can recover real rows;
- a later refresh failure retains confirmed rows and marks them out of date;
- successful acceptance followed by refresh failure still starts exactly one
  execution and never reports acceptance failure;
- successful acceptance followed by chat-opening failure reports the follow-up
  failure without exposing another accept action;
- abort and delete reconcile directly without waiting for an event;
- active and stale Agency ownership remains unchanged;
- controls and status remain usable at narrow widths.

Live verification must compare the browser bridge count, rendered row count,
fresh diagnostics, Handoff-file manifest, and protected Agency ledger counts
before and after read-only inspection.
