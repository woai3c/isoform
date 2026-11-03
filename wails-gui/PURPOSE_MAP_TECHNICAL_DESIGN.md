# Yansu Purpose Map Technical Design

Status: canonical design for Tasks 140-146 on `vision/shadow-agent`

## Problem

An Initiative needs a durable answer to: **what outcome is worth moving forward
now?** Today, Agency Origination defines a commitments section, but normal
runtime gathering supplies no first-class commitment object. Relevant text may
exist in Memory or Knowledge, yet it has no stable identity, lifecycle,
recipient, due signal, contradiction state, or resolution evidence.

That leaves two unsafe choices: ask the user to assign work, which violates the
agency thesis, or let a planner reinterpret loose prose on every wake, which can
silently change purpose and duplicate work.

The Purpose Map closes that gap. It is an append-only, evidence-backed map of
current commitments, responsibilities, goals, concerns, and opportunities. It
supplies purpose to Initiatives without becoming authority or a task request.

## Product Boundary

Purpose and Self are related but different:

- **Self** describes how the person tends to reason and work: principles,
  boundaries, preferences, decision policies, and stable patterns.
- **Purpose** describes what currently deserves progress: commitments,
  responsibilities, goals, concerns, and opportunities.
- **Memory and Knowledge** preserve human-readable evidence for both.
- **Initiatives** are workers pursuing one selected purpose under independent
  authority and resource checks.

An open loop is not:

- proof that the user wants Yansu to act;
- an authority grant or operating envelope;
- a Task Episode, ShadowRun, Handoff, Schedule, or Initiative;
- a reminder or approval card;
- a durable fact about hidden reasoning;
- resolved merely because an agent says it finished.

## Non-Negotiable Invariants

1. Every open-loop state is derived from immutable roots, events, evidence
   links, and relations.
2. Model output may propose a hypothesis but is never supporting evidence.
3. One explicit user source or multiple independent observed sources may
   support a loop. Repeated summaries of the same source count once.
4. Unsupported or ambiguous hypotheses remain candidate-only and never enter
   ordinary Agency prompts or user-facing active-purpose counts.
5. Behavior can support that an outcome matters; it cannot establish hidden
   reasoning. Rationale remains unknown without an explicit source.
6. Purpose never grants authority, raises a budget, expands a capability, or
   changes a working boundary.
7. Initiative execution reports cannot resolve a loop. Resolution requires an
   independently verified outcome, explicit user evidence, or a source-backed
   external-state observation.
8. Duplicate reconciliation preserves every root and evidence link. It adds a
   canonical relation; it never deletes or rewrites history.
9. Every eligible current loop is scanned for origination. Prompt section
   budgets may bound encoded content, but there is no fixed top-N retrieval
   cap and every omission remains in the manifest.
10. Hidden Shadow hypotheses, evaluator prose, guided First Run output, and
    unsupported agent claims never enter the Purpose Map.
11. Corrections append contradiction, revision, dismissal, or reopening
    evidence. They never mutate the event that was wrong.
12. Purpose changes wake Agency Origination, but origination admission and
    every state-changing action still pass their existing deterministic gates.

## Domain Model

### OpenLoop Root

One stable identity for a desired outcome:

- `id`
- `kind`: `commitment`, `responsibility`, `goal`, `concern`, or `opportunity`
- `canonical_key`: normalized identity used only for deterministic retries
- `contract_version`
- `created_at`
- canonical payload and SHA-256 hash

The root contains no mutable current-state columns.

### OpenLoop Event

An ordered immutable state transition:

- `id`, `open_loop_id`, `sequence`, and `idempotency_key`
- `event_type`
- normalized title and desired outcome
- optional owner, counterparty, scope, due signal, and waiting condition
- lifecycle state
- support state and confidence
- explicit rationale or `unknown`
- predecessor event ID
- occurrence and persistence times
- canonical payload and hash

Lifecycle states:

```text
candidate -> active -> waiting -> active
                    \-> resolved -> reopened
candidate/active/waiting -> contradicted
candidate/active/waiting -> dismissed
candidate/active/waiting -> superseded
```

Support states are separate:

- `hypothesis`: insufficient independent evidence;
- `supported`: explicit-user or repeated independent support;
- `contradicted`: current evidence conflicts;
- `unknown`: integrity or source availability prevents a conclusion.

Only `active` or `waiting` plus `supported` is eligible for ordinary Agency
context.

### Evidence Link

Each link records exactly why an event exists:

- source kind, source ID, immutable source version, observed time, and hash;
- evidence class: explicit user, observed user, evaluated projection, verified
  external state, verified outcome, or correction;
- stance: support, update, wait, resolve, contradict, or dismiss;
- source independence key;
- optional exact excerpt hash, never an unbounded raw transcript copy;
- privacy decision and redaction version.

The link references a canonical source that can be revalidated. A stale,
missing, forbidden, or hash-mismatched source makes the affected projection
unavailable; it is not silently skipped.

### Relation

Relations preserve non-destructive reconciliation:

- `duplicate_of`
- `supersedes`
- `depends_on`
- `blocks`
- `related_to`

Initiative fulfillment is represented by independently verified outcome
evidence on a resolution event, not by an agent-authored relation.

Duplicate roots remain inspectable. Exactly one supported current root may be
canonical for a duplicate set. Cycles and ambiguous canonical relations fail
closed.

## Evidence And Qualification

Qualification is deterministic over validated links:

- one current explicit-user source can support a loop;
- one verified outcome can resolve a loop it exactly references;
- observed-user evidence needs at least two independent source identities,
  unless the source contains a validated first-person commitment utterance;
- evaluated projections need at least two independent underlying source
  identities and may not support hidden rationale;
- verified external state can update or resolve an existing supported loop but
  cannot invent user intent by itself;
- agent-generated claims, hidden Shadow material, and evaluator prose count
  zero.

Confidence is diagnostic. It does not decide authority or override the support
rules.

## Extraction And Reconciliation

One bounded no-tools extractor receives a frozen manifest of eligible sources.
It emits candidate hypotheses with exact evidence refs and no free-form source
IDs. The backend rejects unsupported refs, future evidence, malformed kinds,
unbounded text, unsupported lifecycle transitions, and output that claims
authority or completion.

The deterministic reconciler then:

1. validates every referenced canonical source and content hash;
2. groups exact retries by idempotency and semantic identity;
3. compares candidates against every current loop, not a top-N subset;
4. appends reinforcement, revision, contradiction, resolution, or duplicate
   relations;
5. recalculates support from independent source identities;
6. emits one meaningful-state-change wake only after commit.

Model-proposed duplicate links remain hypotheses until deterministic identity
or source overlap supports them. Ambiguity creates no merge.

## Agency Integration

`GatherTaskContextSources` adds `purpose_open_loop` as a canonical source kind. At an
origination or continuation cutoff it scans every validated current root and
emits:

- active supported commitments, responsibilities, goals, concerns, and
  opportunities into the commitments/purpose section;
- waiting conditions and due signals;
- exact event, evidence-set, and relation hashes;
- resolved, contradicted, duplicate, and future rows as manifest exclusions.

Each reference has the form
`purpose_open_loop:<root-id>:<current-event-id>:<projection-hash>`. The
projection hash binds the immutable root plus every event, evidence, relation,
and relation event visible at the planning cutoff. A current inferred revision
does not inherit explicit-user trust from an older event.

The planner may propose an Initiative, Handoff, Schedule, or no useful work.
Purpose is context for that choice, never permission to execute it. The backend
derives `purposeEvidenceRefs` only after ordinary manifest allowlisting and
rejects model-authored values. Exact refs and the normalized desired-outcome
hash survive:

1. fresh origination or re-admission planning;
2. continuation planning;
3. the Initiative run;
4. the concrete worker plan, including `no_useful_work` receipts; and
5. the deterministic outcome-resolution receipt.

Creating or planning work does not change Purpose.

### Verified Outcome Resolution Gate

An Initiative outcome can append `resolved` only when all of these conditions
hold:

- the persisted outcome, Initiative, run, source plan, and worker plan pass
  immutable payload validation and belong to the same lifecycle;
- the outcome is completed local work evaluated by the exact deterministic
  local evaluator version;
- every claimed artifact is contained, exists, and matches its retained hash;
- every verified effect is the bounded `filesystem.write` / `local_write`
  capability and no failed, blocked, or disallowed action exists;
- source and worker planning retain the same exact Purpose refs and desired
  outcome hash;
- worker-declared expected artifacts match independently verified artifacts in
  both directions;
- the cited Purpose event is still current and eligible; and
- exactly one cited root has the same normalized desired outcome.

The agent-authored summary, next action, lifecycle suggestion, prompt, and raw
tool output are never resolution evidence. Stale, mismatched, ambiguous, and
ineligible results leave Purpose open. Missing or inconsistent worker
provenance creates an immutable `provenance_failed` receipt so the failure is
inspectable while unrelated Agency work can continue. The resolution event and
receipt use deterministic idempotency keys, allowing recovery when a process
stops after the event commit but before the receipt commit.

## Product Experience

The Purpose Map is integrated rather than added as another primary workspace:

- **Agency:** show the supported outcomes Yansu is watching or advancing, plus
  honest blocked/waiting state.
- **Self:** add **What matters now** beside stable Self, clearly labeled as
  current purpose rather than identity.
- **Initiative detail:** show the exact purpose roots and evidence versions the
  Initiative was created to move forward.
- **History:** show discovered, reinforced, revised, related, resolved,
  contradicted, dismissed, and reopened events.
- **Dev Tools:** provide complete read-only roots, events, evidence hashes,
  duplicate relations, eligibility decisions, and prompt-manifest provenance.

Routine user review is not required. The user may correct, dismiss, reopen, or
state an open loop directly. Those actions append explicit-user evidence and
never approve an Initiative or grant authority.

### Product Projection And Commands

One canonical product projection serves Agency, Self, Initiative detail, and
History. It derives current supported Purpose, active Initiative links, and
append-only history from the complete validated ledger. It does not maintain a
second mutable product-state table.

Initiative detail resolves every retained `purpose_open_loop` planning ref at
the original planning cutoff. It shows the exact cited title, desired outcome,
event, and projection hash beside current state, so a later correction is
visible as changed history rather than silently rewriting why the Initiative
started.

The narrow product command surface supports:

- direct statement or exact live reinforcement;
- correction through a successor revision;
- dismissal with an explicit correction reason; and
- explicit reopening of a terminal root.

Every command is optimistic-concurrency safe and retry-idempotent. The backend
stamps source identity, immutable hashes, privacy metadata, event time, and
evidence class. A direct statement without rationale records reasoning as
unknown. One exact live duplicate reinforces the canonical root; multiple live
matches fail closed for inspection; a terminal match requires explicit reopen.
Raw ledger mutation is not exposed as a product or debugger operation.

All Purpose rows and History events deep-link to the same canonical detail.
Loading, empty, stale, duplicate, conflict, error, and retry states remain
functional without placeholder Purpose data.

## Cold Start

When there is no supported purpose, Agency explains the real progress path:

- observation is collecting current outcomes;
- Yansu has not yet found enough evidence to treat one as active;
- direct user statements can seed purpose but are optional;
- background understanding can gather bounded read-only context only under its
  existing source consent.

The UI never invents sample commitments, progress percentages, or a fake first
Initiative.

## Security And Privacy

- Purpose stores normalized operational facts and hashes, not unbounded raw
  email bodies, transcripts, screenshots, or credentials.
- Existing protected-app, redaction, source-consent, and local-retention rules
  apply before extraction.
- Browser RPC exposes only read-only diagnostics and the narrow product command
  surface above. It does not expose raw ledger mutation, extraction, replay,
  repair, authority, or execution.
- No Purpose API can call a tool, create authority, execute an Initiative, or
  mutate an external system.

## Read-Only Diagnostics

Settings > Dev Tools > Purpose Map is a separate query-only inspection surface.
Its index validates the complete ledger before returning all current roots and
bounded operational histories. When a bounded history is truncated, the API
returns the aggregate total and an explicit truncation flag rather than implying
that the visible rows are complete.

Root detail exposes the complete immutable event, evidence, and relation trail;
eligibility reasons; independent evidence keys; duplicate targets; and the exact
current projection ref and hash. Extraction detail exposes frozen source
metadata, the complete inclusion/exclusion manifest, hypotheses, and
reconciliation receipts. It deliberately omits raw source bodies, model prompt
text, and raw model output. Their immutable hashes preserve integrity without
turning the debugger into an unrestricted private-data export.

Agency detail exposes every Purpose context ref, dependency, inclusion decision,
and both cutoff-time and current projection validation. Outcome detail exposes
the immutable resolution receipt and deterministic evidence payload. Prompt and
raw worker output remain hash-only.

The debugger has no replay, repair, extraction, reconciliation, mutation,
authority, execution, or policy control. Repeated reads must preserve every
Purpose, Self, Agency, Shadow, Memory, and Knowledge count exactly. Browser RPC
allowlists only the five query methods, so a guessed mutation or replay method
fails closed before reaching application code.

## Verification Gates

Before product use, prove:

- append-only root/event/evidence/relation integrity and SQL immutability;
- exact source hash and cutoff validation;
- support independence and hidden-source exclusion;
- duplicate idempotency, cycle rejection, and non-destructive reconciliation;
- verified-outcome-only resolution;
- zero authority, Shadow, Initiative, Handoff, Schedule, Memory, Knowledge, or
  Self mutation from read/debug operations;
- complete context scanning with explicit budget exclusions;
- functional desktop and narrow product states, correction paths, relation
  navigation, and read-only debugger detail.

### Release Gate Result

Task 146 passed the complete executable Purpose gate on 2026-08-02:

- full Wails tests and vet;
- all Purpose tests under the Go race detector;
- full agent tests, vet, and race detector;
- all 180 frontend tests and the production frontend build;
- clean whitespace and worktree boundaries;
- repeated live read-only calls with unchanged Purpose, Shadow, Agency,
  authority, and Self-policy counts;
- Browser RPC rejection of a non-allowlisted Purpose replay method; and
- desktop, 700px, and 520px browser product checks without console warnings or
  document overflow.

The direct TypeScript command still reports pre-existing unrelated application
and generated-alias errors. No error references the Purpose implementation or
Purpose diagnostics. Native Wails visual inspection is deferred until macOS is
unlocked; the release gate does not permit bypassing the lock or manufacturing
production Purpose evidence.
