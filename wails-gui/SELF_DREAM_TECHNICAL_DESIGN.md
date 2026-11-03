# Self Dream Technical Design

Status: implementation contract for `vision/shadow-agent`

## Product Contract

Self Dream periodically reorganizes Yansu's evidence-backed understanding of
the user. It can discover that two entries describe the same thing, that a
global statement is actually contextual, that one entry should be split, or
that newer explicit evidence contradicts an older interpretation.

Self Dream does not invent a personality, grant authority, execute work, or
replace Memory and Knowledge. The relationship is:

```text
Memory + Knowledge + evaluated behavior + explicit correction
  -> frozen evidence set
  -> Self Dream shadow candidate
  -> deterministic validation and paired evaluation
  -> review or rollout gate
  -> append-only canonical Self revision
```

Memory and Knowledge remain durable evidence infrastructure. Canonical Self is
the inspectable, typed synthesis used by every Initiative.

## Non-Negotiable Invariants

1. Canonical Self remains the only product Self.
2. A Self Dream run freezes its cutoff, base Self revisions, selected source
   content, omissions, hashes, prompt, policy, and model before inference.
3. Candidate output is not evidence for that candidate or its evaluation.
4. A candidate is shadow-only until a deterministic validator and promotion
   policy allow compilation.
5. Self Dream never reads or writes authority grants, permission decisions,
   schedules, Handoffs, Initiatives, or external systems.
6. Model execution is ephemeral, local-context, no-tools, and denies external
   writes.
7. Behavior may support what happened. It does not support why it happened.
   Reasoning remains `unknown` unless explicit correction, stated rationale, or
   repeated source-backed decisions support it.
8. Corrections and contradictions take precedence over inferred synthesis.
9. Every merge, split, contextualization, contradiction, supersession,
   rejection, replay, evaluation, and compilation is append-only.
10. A replay may append a diagnostic replay record only. It cannot mutate
    product Self, Memory, Knowledge, learning, History, or authority.
11. Context is selected by source class, cutoff, relevance, privacy, and token
    budget. It is never truncated to an arbitrary item count.
12. Automatic promotion is disabled unless real paired shadow evidence clears
    the rollout gate. Confidence alone is never permission.

## Domain Model

### Frozen Run

`self_dream_runs_v1` stores one immutable analysis input:

- source cutoff;
- policy and assembler versions;
- base canonical Self revisions;
- assembled context and full selection manifest;
- context, input, and prompt hashes;
- exact prompt and requested model;
- trigger and creation time.

The run row is immutable. Lifecycle transitions are appended to
`self_dream_events_v1`.

### Attempt

`self_dream_attempts_v1` stores one production shadow attempt. It records:

- safety mode `local_no_tools`;
- `learning_eligible=0`;
- `product_mutation=0`;
- raw output hash and bounded raw output;
- model, duration, status, and error.

Deterministic duplicate detection can complete a run even when no model is
available. Broader synthesis fails closed when the configured model cannot be
run.

### Operation

A candidate operation is one of:

- `merge`
- `split`
- `contextualize`
- `contradict`
- `supersede`
- `no_change`

Every operation identifies target canonical Self entry IDs, output Self kind,
contextual scope, proposed statement or split statements, evidence references,
reasoning status, confidence, and uncertainty. Validation state is persisted
with the immutable candidate.

Scope is typed:

- `global`
- `app`
- `app_transition`
- `work_mode`
- `capability`
- `project`
- `relationship`

An empty scope key is legal only for `global`.

### Evaluation

The paired shadow evaluation compares:

1. the neutral projection: current canonical Self;
2. the Dream projection: the candidate applied in memory only.

It measures evidence coverage, duplicate reduction, information loss,
contradiction risk, unsupported reasoning, entry-count change, and aggregate
quality. The candidate output and evaluator prose are not added to evidence.

### Decision And Compilation

Promotion decisions are append-only:

- `accept`
- `keep_separate`
- `not_enough_evidence`
- `correct`
- `auto_promote`

A user may accept a qualified consequential synthesis. A correction appends
contradiction evidence and newer canonical Self revisions; it never edits the
original entries.

Compilation writes canonical Self revisions and a
`self_dream_compilations_v1` lineage record in one database transaction.
Targets are checked optimistically against the frozen revisions. If canonical
Self changed after the run, compilation fails closed as stale and a newer
Dream run must reconsider it.

## Evidence Assembly

Self Dream reuses the cutoff-safe task-context source scanner. It scans all
retained Memory and Knowledge versions and all eligible database evidence,
then packs content by token budget. The allowlist is:

- active, source-backed canonical Self;
- Memory and Knowledge that pass historical, privacy, and contamination
  checks;
- qualified deterministic Work Pattern Profile projections;
- explicit Work Pattern corrections;
- independently observed or evaluated user outcomes.

The following are excluded:

- hidden ShadowRun hypotheses and output;
- TaskAgent output;
- First Run guided/tutorial evidence;
- evaluator prose;
- unsupported agent claims;
- sources newer than the cutoff;
- retracted or superseded sources;
- authority and permission records;
- active Initiative plans;
- Self Dream attempts, operations, evaluations, and replays.

Every discovered source receives an included or excluded manifest entry. The
manifest is part of the frozen input hash and exact replay fixture.

## Deterministic Pass

Before model inference, Self Dream detects low-risk duplicates using a strict
canonical statement form. An automatic merge candidate requires:

- the same Self kind;
- the same typed scope;
- identical canonical statement content;
- active source entries;
- no unresolved contradiction;
- complete evidence union;
- no information loss;
- no unsupported reasoning.

Near matches are never automatically merged by the deterministic pass.

## Synthesis Prompt

The primary model is the configured primary work model, currently expected to
resolve to `gpt-5.6-sol`. The prompt:

- describes the immutable contract;
- includes target entry IDs and frozen evidence references;
- forbids authority, execution, and unsupported reasoning;
- requires strict JSON;
- asks for the smallest useful operation set;
- requires `no_change` when evidence is insufficient;
- requires explicit uncertainty.

Lightweight deterministic bookkeeping and scheduling do not invoke a model.

## Validation

The validator rejects an operation when:

- its operation, Self kind, or scope is unsupported;
- a target is absent from the frozen base Self;
- an evidence reference was not included in the frozen manifest;
- a statement or split is empty or oversized;
- a merge combines incompatible kinds or contexts without explicit support;
- a correction predating the run conflicts with the proposal;
- reasoning is claimed without explicit or repeated source-backed evidence;
- the operation mentions execution or attempts to modify authority;
- it removes source facts or drops evidence coverage;
- it duplicates another operation in the same attempt.

Validated broad synthesis remains `review_required`. Only deterministic exact
duplicates can become `auto_eligible`, and only the rollout gate may activate
that path.

## Rollout Gate

Collection, debugging, replay, and user review are available while automatic
promotion is disabled. Automatic promotion requires:

- at least 20 completed real shadow evaluations;
- zero evidence-coverage loss;
- zero unsupported-reasoning promotions;
- mean Dream improvement of at least 0.03;
- correction or rejection rate no greater than 0.10;
- no unresolved stale-compilation or containment error.

Synthetic tests, manual debug replays, and model confidence do not count.
Rollout state is append-only and cannot create authority.

## Scheduler And Cache

`SelfDreamService` is single-flight. It checks periodically after startup and
runs only when:

- the evidence input hash differs from the last completed run;
- enough durable input exists to make synthesis meaningful;
- no recent scheduled run already evaluated that input.

The cache key includes policy version, assembler version, source cutoff
projection, base Self hash, selected content hashes, exclusions, corrections,
and prompt version. Any source, correction, policy, or prompt change therefore
invalidates the cache without mutating old runs.

## Product UI

Self contains two product-safe surfaces:

1. **Canonical Self**: current typed entries with scope, confidence, revision,
   reasoning status, evidence summary, contradictions, and Dream lineage.
2. **Self updates**: qualified proposed changes and completed reviewed changes.

Consequential updates open a detail dialog with before/after statements,
scope, evidence count, uncertainty, and deterministic evaluation. Available
commands are Accept, Keep separate, Need more evidence, and Correct.

Raw prompts, hashes, omitted sources, model output, internal scores, and shadow
attempts remain in Dev Tools.

## Developer Debugger

Settings > Dev Tools > Shadow Agent > Self Dream provides:

- run list and exact run detail;
- frozen base Self and source manifest;
- selected and excluded evidence;
- prompt/model and bounded raw output;
- parsed operations and validation reasons;
- neutral-versus-Dream evaluation;
- decisions and compilations;
- rollout metrics;
- exact frozen replay with editable prompt/model.

The debugger cannot accept, reject, correct, compile, enable rollout, grant
authority, or execute work. Replay appends a diagnostic record with
`learning_eligible=0` and `product_mutation=0`.

## Failure And Recovery

- Model, parse, validation, or persistence failure appends a failed event.
- A crash leaves immutable inputs and prior events intact.
- Missing or changed frozen evidence makes replay visibly non-exact; the
  stored frozen context remains replayable.
- A stale target blocks compilation without partial retraction.
- Reprocessing the same frozen input returns the prior completed run.
- Scheduler cancellation is bounded by application shutdown context.

## Verification

Required coverage:

- migration idempotency and immutable triggers;
- cutoff, privacy, contamination, and complete manifests;
- deterministic duplicate behavior;
- strict parser and validator failure cases;
- correction precedence and stale-target rejection;
- transactional compilation and lineage;
- shadow-only default and rollout gate;
- neutral-versus-Dream ablation;
- scheduler single-flight and cache invalidation;
- product Self Updates and canonical detail usability;
- compact/wide debugger master-detail and Escape behavior;
- exact replay hash, no-tools input, and no product mutation;
- full Go, frontend, build, and live desktop regression.
