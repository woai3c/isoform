# Shadow Natural Parity Proof Technical Design

Status: implemented and validated for Task 193 on `vision/shadow-agent`

## Problem

The Natural parity gate predates the production `analyze.text` workflow and
structured result destination provenance. It currently searches only
`compose.text` runs and treats a materialized user-result digest as the entire
user-result proof. This creates two false diagnostics:

1. a complete current-generation `analyze.text` loop is invisible even though
   it uses the same no-tools, direct-text parity boundary; and
2. the gate cannot show whether that text came from a current, verified output
   destination rather than historical or malformed evidence.

Task 193 repairs the proof surface. It does not create Shadow evidence, run a
model, broaden production eligibility, or change qualification.

## Product Meaning

The gate answers one question:

> Has one ordinary production task completed the current silent Shadow loop
> from onset through destination-bound user result, comparison, and safe
> learning?

It is a proof of measurement integrity, not proof that a capability is ready
for autonomous execution. The separate `compose.text` quality campaign and
release ledger continue to answer reliability questions.

## Invariants

1. The gate considers only workflows already returned by the closed backend
   production registry.
2. Every considered workflow must also use the direct-text result contract and
   current `shadow-task-v7` generation.
3. `compose.text` and `analyze.text` may each satisfy the parity-loop proof;
   neither can qualify the other workflow or enter the other's campaign.
4. User result content and destination are separate required steps with
   separate canonical digests.
5. A destination step is complete only when pair readiness was reconstructed
   from current `task-result-destination-v1` evidence and digest validation.
6. One comparison receives one canonical user result. Repeated current results
   at the same destination select the latest immutable observation; current
   results across different destinations fail closed rather than concatenate.
7. Historical and superseded result summaries remain inspectable but cannot
   enter comparator evidence as user-result text.
8. Historical worker, lifecycle, destination, evaluation, or learning
   generations cannot complete a current step.
9. Invalid result provenance takes precedence over an older comparison receipt
   and leaves the proof incomplete.
10. A complete persisted proof is monotonic in the debugger: newer incomplete
   work may be inspected but cannot hide it.
11. Debug reads remain read-only and expose no committed text.
12. Gate completion grants no authority, permission, Initiative, Handoff,
    Schedule, execution, or external effect.

## Versioned Contract

Task 193 advances the derived debugger contract to:

```text
task-shadow-release-gate-v4
```

`ShadowTaskPairReadiness` adds the canonical
`userResultDestinationSha256`. It is populated only beside a materialized
current user result after the persistence validator has rechecked workflow,
content digest, destination version, semantic role, capture method, and
destination digest.

The proof gate adds `selectedWorkflow` and the following ordered steps:

1. completed Task Episode;
2. completed isolated Shadow attempt;
3. materialized user result;
4. verified user-result destination;
5. materialized Shadow result;
6. current eight-dimension evaluation; and
7. contamination-safe learning projection.

The result-content step uses the content digest as its evidence receipt. The
destination step uses the destination digest. Neither digest is interpreted as
user reasoning or authority.

## Workflow Selection

The debugger loads Task Episode details only when the derived workflow is both:

- in `taskWorkflowProductionShadowWorkflows()`; and
- accepted by the direct-text result registry.

Selection is deterministic:

1. retain the first complete current proof in the existing newest-first detail
   order;
2. otherwise retain the first detail with a current-generation direct-text
   run; and
3. otherwise retain the newest eligible detail.

Historical run IDs from every eligible direct-text workflow remain visible as
diagnostic context. The gate's required action uses generic direct-text copy so
it does not tell an analysis user to send a message or tell a composition user
to produce an analysis.

## Compatibility

The release gate is derived read-only state and has no persistence migration.
Historical runs, results, evaluations, and learning remain immutable. Existing
frontend consumers tolerate the new optional selected-workflow and destination
digest fields while the backend always emits them under v4 where available.

The compose quality campaign, qualification thresholds, release events, Self,
Memory, Knowledge, and authority policy are unchanged.

## Validation

Tests must prove:

- both closed direct-text workflows can satisfy v4 with complete persisted
  current evidence;
- unsupported workflows and historical generations cannot enter the gate;
- result content without a destination digest leaves the destination step
  incomplete;
- repeated same-destination result revisions select only the latest result,
  while cross-destination current results and malformed provenance fail closed;
- historical or superseded result summary text cannot enter comparator input;
- malformed later provenance overrides an older current evaluation;
- complete proof remains selected over newer incomplete work across workflows;
- compose quality campaign state is unchanged;
- frontend rendering shows workflow and the new proof step without a write
  control; and
- focused, full, race, vet, frontend, production-build, rebuilt live read-only,
  and no-fabricated-evidence gates pass.

## Non-Goals

- No new production workflow or destination classifier entry.
- No synthetic Task Episode, ShadowRun, result, evaluation, or learning row.
- No user-facing Shadow prediction or review request.
- No change to worker tools, model selection, context packing, or authority.

## Validation Result

Validated on 2026-08-04:

- focused backend tests prove canonical latest-result selection, same-destination
  revision behavior, cross-destination failure, both eligible workflows,
  unsupported-workflow containment, current-generation lineage, monotonic proof
  selection, and unchanged compose quality-campaign scope;
- the uncached complete Wails suite passes in 143.767s, Wails vet passes, and
  focused race coverage passes in 17.168s with only the established malformed
  macOS `LC_DYSYMTAB` linker warning;
- all 44 frontend test files and 222 tests pass, the production frontend build
  succeeds, and the complete agent test and vet suites pass;
- the rebuilt live Task Episode debugger shows the exact
  `compose.text, analyze.text` production scope, `task-lifecycle-v6`, seven
  natural-proof steps including `Your result source verified`, selected
  workflow `compose.text`, zero current observed results, and
  `desktop_locked` while the frontmost system surface is `loginwindow`;
- the historical result remains readable as
  `historical_missing_destination`, with a content digest and no destination
  digest, and the corresponding historical pair remains not materialized; and
- a separate signed-in clean reload renders successfully with no browser
  warning or error.

No current Task Episode, ShadowRun, result, comparison, learning, Memory,
Knowledge, Self, Initiative, authority, execution permission, or external
effect was fabricated. The natural parity milestone still requires one future
ordinary current-generation direct-text task after the desktop is unlocked.
