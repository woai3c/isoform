# Shadow Debug Index Technical Design

Status: implemented and rebuilt-live validated on `vision/shadow-agent`

## Purpose

Shadow mode must preserve enough production context to compare Yansu's private
attempt with the user's real result. The Developer tools list does not need to
decode that complete evidence graph for every detected task.

This design separates a responsive aggregate index from provenance-complete
detail while preserving the existing quality, release, and containment rules.
It changes no Memory, Knowledge, About-you, task-detection, execution, learning,
or permission policy.

## Production Problem

`GetTaskShadowDebugSnapshot` previously loaded complete `TaskEpisode` graphs for
the bounded list. Each graph decoded frozen canonical context, the complete
manifest, hypotheses, transitions, links, and evidence before the debugger
could show simple counts and current state.

The retained production database had already reached tens of megabytes of
frozen context. Even after removing Activity-store connection deadlocks, an
aggregate debugger read could spend seconds decoding evidence that the user had
not opened.

The aggregate and detail responsibilities were therefore coupled:

```text
list request
  -> decode every selected evidence graph
  -> derive small summaries
  -> optionally open one selected detail
```

## Index Contract

### Episode projection

The aggregate reads one relational row per episode containing:

- current status and current hypothesis;
- confidence and task-boundary timestamps;
- frozen-context ID and hash;
- included and excluded manifest totals; and
- whether an exact included pre-onset Activity snapshot exists.

Each selected manifest is streamed from SQLite as one bounded row and decoded
once through the typed Go manifest representation. This avoids the repeated
JSON-object parsing cost of relational JSON extraction while keeping raw
context, excerpts, and evidence payloads out of the list response.

The existing 500-episode corpus cap and requested 200-row UI cap remain
unchanged. The quality campaign and release-proof selection receive the
complete 500-episode corpus even when the visible debugger list requests fewer
rows.

### Run projection

For each selected episode, the index returns at most the newest 100 task-mirror
run headers in chronological order. A windowed count records the complete
retained run total, so bounding the response does not falsify the displayed
count.

The visible summary returns only the newest 20 run IDs. Latest pair readiness
always uses the genuinely newest retained run, never the oldest row in a
bounded prefix.

The compose quality campaign also inspects the newest 100-run window. This
prevents old prompt generations from displacing the current generation after a
long retained history. Historical rows remain immutable and available through
detail; they simply cannot hide newer qualification evidence.

### Aggregate counts

Persisted evaluation and task-agent-attempt counts are grouped in SQLite across
the complete retained history. Attempt links through an episode, run, or
evaluation are unioned and counted once per episode, matching the existing
detail semantics without transferring all linked IDs into Go.

### Lifecycle health

Lifecycle status and result counters continue to scan the complete lifecycle
fact corpus independently of the visible list limit. For visible rows, the
already-derived latest pair state is reused. Rows outside the visible list and
readiness errors load only bounded run headers and the latest pair.

## Detail Contract

`GetTaskShadowDebugDetail` remains the only path that decodes a complete
selected Task Episode and its bounded evidence, attempts, comparisons,
reasoning, learning, and correlations. Included manifest entries retain
priority over exclusions in bounded detail responses, and aggregate manifest
totals always disclose truncation.

No aggregate optimization may weaken the detail's hashes, provenance checks,
redaction, or immutable-history access.

## Invariants

1. Aggregate reads are read-only and cannot append lifecycle, Shadow, learning,
   About-you, permission, or authority records.
2. UI limits may bound displayed rows and payload size, but they cannot truncate
   lifecycle totals or the existing quality-campaign episode corpus.
3. Latest state is derived from the newest immutable row by timestamp and stable
   ID ordering.
4. Current prompt evidence cannot be hidden behind older prompt generations.
5. Raw frozen context and private evidence payloads remain detail-only.
6. Prediction reliability remains separate from permission to act.
7. The index does not change how much context Memory or Knowledge can retrieve;
   it only avoids sending unopened debugger evidence graphs.

## Failure Behavior

- Missing required storage fails the read explicitly.
- Invalid manifest JSON fails closed instead of inventing context totals.
- Pair-readiness failures appear as `readiness_error` with the exact reason.
- A release-gate diagnostic failure remains visible without mutating or
  manufacturing milestone evidence.
- Unsupported workflows remain observation-only under the closed registry.

## Verification Contract

- compact no-run and complete-graph summaries must equal the detailed summary;
- a 101-run episode must report the complete total, expose the newest 20 IDs,
  and derive readiness from the newest run;
- the quality campaign must retain the newest current-generation run in its
  100-run window;
- a small visible-list limit must not truncate quality-campaign episode counts;
- repeated aggregate and detail reads must leave evidence and authority ledgers
  unchanged;
- focused, race, full Go, vet, formatting, rebuilt Browser RPC, and functional
  UI checks must pass; and
- rebuilt production latency must remain inside an interactive budget under
  ordinary background load.

## Rebuilt-Live Results

The production Browser RPC previously took 45.48 seconds on its first measured
read and 27.88 seconds on the next read while decoding complete graphs. After
the indexed projection and single-pass manifest decoding, three settled reads
completed in 4.23, 4.99, and 4.76 seconds with stable HTTP 200 responses and an
85,793-byte payload.

The final rebuilt read after the release-index audit completed in 4.16 seconds
with the same response size and release/quality state.

A CPU profile attributes about 90 milliseconds to
`GetTaskShadowDebugSnapshot` itself. The remaining wall time is queueing behind
concurrent Agency and Activity reads on the intentionally single-connection
Activity store. Widening that storage invariant is outside this milestone.

The rebuilt UI opened Settings, Developer tools, Shadow mode, the detected-task
list, and a complete compared-task detail without console errors. Focused and
race regressions, the complete Go suite, `go vet`, formatting, and diff checks
pass. Repeated live reads left all six authority and permission-ledger table
fingerprints byte-identical.

The release diagnostic remains `desktop_locked` and the quality campaign
remains `evidence_exhausted`. Task 216 therefore still requires a future
ordinary direct-text task; this optimization did not replay, backfill, or
manufacture proof.

## Non-Goals

- Deleting or compacting immutable Shadow evidence.
- Increasing permissions or releasing a capability.
- Replaying, backfilling, or synthesizing the ordinary Task 216 parity proof.
- Replacing full-context Memory or Knowledge retrieval with a fixed top-N
  debugger limit.
