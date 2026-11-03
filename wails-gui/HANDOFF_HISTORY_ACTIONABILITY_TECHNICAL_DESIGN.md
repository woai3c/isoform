# Handoff History Actionability Technical Design

Status: implementation contract for Task 255 on `vision/shadow-agent`.

## Problem

The classic Handoff library displays every active or stale Handoff with `Pick
this up`, `Do it later`, and delete affordances. That is valid for legacy
Handoffs, whose stale state is intentionally reclaimable, but invalid for
Agency Handoffs.

An Agency Handoff is controlled by its Desk permission request. The backend
correctly rejects direct accept, decline, and delete calls for active or stale
Agency files. The classic reader nevertheless exposes those calls, so a user
can reach an error that says the permission decision could not be recorded.
The UI is promising an action that the authority boundary must refuse.

## Product Contract

The Handoff library is a work library and historical record. It is not a
second permission surface.

- An active Agency Handoff routes to Desk, where its current evidence, scope,
  access, and permission state are revalidated before a decision is shown.
- A stale Agency Handoff remains readable but has no accept, defer, decline, or
  delete control. It explains that its supporting work is no longer current.
- An active or stale legacy Handoff keeps its existing pick-up behavior.
- An in-progress, completed, or aborted Handoff with a chat session keeps its
  existing chat-recovery behavior.
- Authority and permission state remain backend-owned. UI routing never grants
  permission or changes Handoff state.

## State Matrix

| Source | Status | Primary behavior |
| --- | --- | --- |
| Agency | active | Open Desk for the permission decision |
| Agency | stale | Read-only historical detail |
| Legacy | active | Pick up or cancel |
| Legacy | stale | Pick up again |
| Any | in progress | Open execution chat when available |
| Any | completed or aborted | Open historical chat when available |

Active and stale Agency rows also omit delete because the backend reserves
their lifecycle for the Agency permission owner.

## Implementation

`HandoffsPage` derives the protected state from the same persisted source and
status fields enforced by `AcceptHandoff`, `AbortHandoff`, and `DeleteHandoff`.
The application supplies a direct callback to switch to Yansu Desk. Stale
Agency details render a plain-language status band and a return command instead
of decision controls. The Agency-only `No longer available` status is distinct
from the reclaimable legacy stale status.

The backend gate is unchanged and receives an explicit stale-state regression.
This preserves defense in depth if another client invokes the legacy APIs.

## Containment And Effects

Opening a Handoff, returning to the list, or routing to Desk is read-only. This
milestone creates no permission event, authority grant, Handoff transition,
Schedule, Initiative, execution, Memory, Knowledge, Purpose, Self, profile, or
Shadow-learning record.

## Verification Gates

1. Clicking an active Agency Handoff opens Desk and does not call
   `AcceptHandoff`.
2. A stale Agency Handoff opens readable detail with no pick-up, defer,
   decline, or delete command.
3. The backend rejects direct accept, decline, and delete calls for stale
   Agency Handoffs without changing their state.
4. A stale legacy Handoff remains reclaimable through the normal execution
   path.
5. Completed and aborted Handoffs with sessions still open their chats.
6. English and Chinese surfaces contain the same information architecture.
7. Desk and Handoff navigation produce no console error or authority effect.
