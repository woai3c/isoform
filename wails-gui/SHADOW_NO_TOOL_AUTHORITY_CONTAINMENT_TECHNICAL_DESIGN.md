# Shadow No-Tool Authority Containment Technical Design

Status: implementation contract for Task 269 on `vision/shadow-agent`.

## Problem

Silent Shadow uses several pure inference calls to detect task onset, interpret
lifecycle changes, compare retained activity with earlier predictions, and
evaluate the user's result against Yansu's private result. Direct-text Shadow
workers also run without tools.

Those calls correctly set `NoTools: true`, but they also set
`AutoApproveTools: true`. The current runner omits every tool definition when
`NoTools` is active, so the approval flag cannot create an effect today. The
combination is still the wrong containment contract:

- `AutoApproveTools` marks a run as unattended and bypasses legacy permission
  prompts after semantic authority allows an action;
- a future refactor could remove or weaken `NoTools` while leaving the
  approval flag behind; and
- diagnostic evidence would describe a pure measurement pass as pre-approved
  unattended work even though measurement has no authority.

Shadow deployment should fail closed at every independent boundary, not rely
on two contradictory flags cancelling each other.

## Product Contract

Every pure Shadow measurement pass is both tool-free and unapproved:

```text
ordinary production activity
  -> task onset inference       NoTools=true, AutoApproveTools=false
  -> lifecycle inference        NoTools=true, AutoApproveTools=false
  -> private direct-text work   NoTools=true, AutoApproveTools=false
  -> result correlation         NoTools=true, AutoApproveTools=false
  -> parity evaluation          NoTools=true, AutoApproveTools=false
  -> evidence-backed learning
```

`AutoApproveTools: false` does not make these background calls interactive.
There is no tool definition, tool call, permission prompt, or permission wait
to approve. If a tool unexpectedly becomes available later, the run fails into
the normal permission and authority boundary instead of silently inheriting
unattended approval.

## Scope

This milestone changes five silent-Shadow runner boundaries:

1. task-onset detection;
2. task-lifecycle observation;
3. legacy Shadow-to-activity correlation;
4. direct-text task-mirror execution; and
5. direct-text parity evaluation.

The task-mirror runtime derives both flags from the same closed workflow
decision. Current `compose.text` and `analyze.text` work is no-tools and not
auto-approved. Historical tool-using Shadow candidate plans keep their current
unattended execution flag because the deterministic Shadow authority overlay
still denies every write and unknown effect while allowing explicitly
classified reads inside the isolated workspace.

## Unchanged Boundaries

This milestone does not change:

- production workflow eligibility or prompt generations;
- model selection, prompts, frozen context, or result parsing;
- task detection, lifecycle, correlation, evaluation, or learning policy;
- Shadow authority policy or the tool-using legacy runtime;
- permission requests, grants, Initiatives, Handoffs, Schedules, or execution;
- Memory, Knowledge, About you, Purpose, or prediction confidence; or
- the natural Task 216 parity gate.

No confidence, readiness, background status, or successful model result grants
authority.

## Verification

1. Onset, lifecycle, correlation, and evaluation calls set `NoTools: true`,
   `AutoApproveTools: false`, `Ephemeral: true`, and suppressed telemetry.
2. Current direct-text task mirrors set `NoTools: true`, disable programmatic
   tool calling, deny external writes, and set `AutoApproveTools: false`.
3. A historical tool-using Shadow candidate retains auto-approval only behind
   the immutable Shadow authority overlay and continues to record denied
   effects.
4. Direct-text task mirrors record no tool action or authority-decision rows.
5. Focused, race, full backend, vet, rebuilt live read-only, and immutable
   authority-ledger checks pass.
6. Task 216 remains open until a future ordinary production task completes the
   natural current-generation comparison chain.
