# Shadow Analyze Text

Status: implemented and validated on `vision/shadow-agent` (Task 191)

## Purpose

Before Task 191, the production Shadow rollout recognized only `compose.text`.
That proved a narrow parity loop, but most observed knowledge work was assigned
a one-off capability key and could not create a current-generation Shadow run.

Task 191 adds one second deterministic workflow: `analyze.text`. It covers a
bounded case in which the user examines source material already present at task
onset and later commits their own textual analysis, assessment, comparison,
explanation, findings, or recommendation.

This is not a generic research, browsing, coding, configuration, or delegated
AI workflow. A user prompt sent to another AI is not the user's result, and an
assistant response is not adopted user judgment. Neither can satisfy parity
without separately observed user-authored result evidence.

## Admission

`analyze.text` is selected by deterministic high-precision classification.
Admission requires:

- an analysis action such as analyze, assess, audit, compare, evaluate,
  explain, identify, inspect, review, or synthesize;
- a desired textual result such as an analysis, assessment, comparison,
  explanation, findings, gaps, insights, issues, options, recommendation,
  report, risks, summary, synthesis, or takeaways;
- no coding, implementation, mutation, navigation, sending, publishing,
  purchasing, configuration, software-change review, or visual-media
  inspection action; and
- at least one exact included `activity_snapshot_context` entry captured no
  later than inferred task onset.

`compose.text` remains first in classifier precedence. `analyze.text` does not
relabel direct drafting, rewriting, translation, or text transformation.

The production workflow registry is a closed ordered list containing only:

1. `compose.text`;
2. `analyze.text`.

Confidence, model output, profile readiness, and capability qualification
cannot add a workflow to that list.

## Shadow Execution

Both production workflows run under `shadow-task-v7` with:

- `NoTools=true`;
- programmatic tool calling disabled;
- an isolated temporary directory with no artifact writes permitted;
- external-write denial and Shadow authority mode retained as defense in depth;
- only the immutable pre-onset context and profile revision; and
- complete assistant output discarded from production.

For these direct-text workflows, an attempt with a tool action or captured
artifact is a containment failure. An artifact digest cannot substitute for a
direct assistant result, and an already persisted comparison cannot mask the
violation.

The `analyze.text` prompt must produce the complete analysis directly. It must
separate supported findings from unknowns, cite no facts absent from frozen
context, and must not retrieve newer information or perform an external action.

## User Result

Parity requires a later, source-backed `committed_text` event after task onset.
The lifecycle observer may materialize it only when:

- the Task Episode's deterministic workflow is `analyze.text`;
- a Return or Command+Return commit candidate retains the exact user-authored
  text;
- the event was captured by the exact AX field-diff path and retains a current
  destination whose backend-recomputed role is `user_result_candidate`;
- every accepted committed-text reference names the same exact destination;
- terminal or result evidence cites that exact event; and
- the backend reconstructs and hashes the text from retained evidence rather
  than model output.

The onset prompt, other-party speech, system content, an AI response, screen
OCR, and a bare Return are not materialized user results. If no committed user
result appears before the observation horizon ends, the pair expires without
evaluation or learning.

The materialized result records its exact workflow, destination, content
digest, and destination digest. A `compose.text` result cannot satisfy an
`analyze.text` run or vice versa. The destination contract and historical
compatibility rules are defined in
`SHADOW_RESULT_DESTINATION_PROVENANCE_TECHNICAL_DESIGN.md`.

## Comparison And Learning

The current eight-dimension comparator remains shared, but `analyze.text`
receives workflow-specific quality criteria:

- answer the desired analytical outcome;
- distinguish evidence-backed findings from uncertainty;
- cover material risks, gaps, or tradeoffs supported by context;
- avoid invented facts and unsupported causal claims; and
- produce a complete, usable textual result.

Reasoning remains unknown unless supported by explicit rationale, correction,
or repeated-decision evidence. Result similarity cannot establish why the user
reached a conclusion.

Learning remains capability-scoped and contamination-safe. `analyze.text`
evaluations cannot enter the existing `compose.text` quality campaign or
release ledger. They may build their own generic Shadow learning projection
only under the existing repeated-evidence thresholds and never grant authority.

## Debug Contract

Task Shadow Dev Tools expose the backend-provided closed workflow registry,
the derived workflow, eligibility, exact source-context blocker, pair state,
and current-generation receipts. The UI must not hard-code `compose.text` as
the only scope.

Browser mode remains read-only. Debug reads create no Task Episode, ShadowRun,
attempt, evaluation, learning, Memory, Knowledge, Self, Initiative, authority,
or external effect.

## Compatibility

Existing Task Episodes and Shadow runs are immutable. The new deterministic
classifier may make a still-open recent episode eligible during the existing
bounded reconciliation pass, but it never rewrites historical capability keys
or retries a Task Episode that already has a Shadow run.

The `compose.text` release gate and quality campaign retain their exact meaning.
Task 191 changes neither qualification thresholds nor release status.

## Validation

Tests must prove:

- high-precision positive and negative classification;
- compose precedence and a closed two-workflow registry;
- exact frozen source-context requirement for `analyze.text`;
- tool-free workflow-specific worker prompts;
- direct assistant-result evidence and rejection of tool or artifact output;
- production runtime containment at direct, pipeline, and reconciliation paths;
- exact workflow-bound committed-text materialization;
- no materialization from prompts, AI responses, OCR, speech, or bare Return;
- pair readiness, evaluation input, and quality criteria for both workflows;
- no cross-workflow result reuse or compose-campaign contamination;
- generic capability-scoped learning with no authority effect;
- functional backend-provided scope in Dev Tools; and
- full backend, frontend, vet, race, build, and rebuilt live read-only gates.

No production episode, result, comparison, or learning receipt may be
fabricated to satisfy validation.

## Validation Result

Validated on 2026-08-04:

- the final uncached Wails suite passes in 116.462s and `go vet ./...` passes;
- the focused workflow, lifecycle, evaluation, debugger, and pipeline race
  suite, including direct execution containment, passes in 102.627s, with only
  the established macOS `LC_DYSYMTAB`
  linker warning;
- the complete `yansu-agent` suite and vet pass;
- all 44 frontend test files and 222 tests pass, including historical `null`
  reasoning-collection compatibility, and the production frontend build
  succeeds;
- `pnpm exec tsc --noEmit` retains the repository's pre-existing generated
  Wails alias, Session/Workspace, and Agency Runtime typing baseline and adds
  no Task 191 diagnostic;
- the rebuilt read-only Browser RPC reports exactly `compose.text` and
  `analyze.text` across 74 real historical Task Episodes, with every reasoning
  collection serialized as an array; and
- live interaction opens multiple historical Task Episode details, renders the
  backend production scope and Reasoning parity, and records no new browser
  error after the compatibility fix.

The final rebuilt read-only pass again reported 74 historical Task Episodes,
the exact two-workflow scope, `shadow-task-v7`, and intact reasoning arrays in
an opened detail after the no-tools artifact fallback was removed.

No `analyze.text` episode, result, evaluation, learning projection, Memory,
Knowledge, Self revision, Initiative, authority grant, or external effect was
created for validation. Native signed-in visual QA remains unavailable while
macOS is at `loginwindow`; the Wails and Vite development services remain
running.
