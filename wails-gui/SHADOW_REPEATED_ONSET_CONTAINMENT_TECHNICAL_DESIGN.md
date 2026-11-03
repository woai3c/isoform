# Repeated-Onset Shadow Containment

Status: Task 248 implementation contract on `vision/shadow-agent`.

## Goal

Task 247 prevents a future exact user task start from forking into multiple
Task Episodes. Task 248 makes preserved historical forks incapable of creating
or inflating Shadow evidence.

One exact observed onset may contribute:

- once to the product's observed-task total;
- at most once to ordinary lifecycle processing when its identity is unique;
- zero times to comparison, learning, Self, qualification, calibration, or
  release proof when more than one immutable Task Episode already claims it.

No historical row is updated, merged, deleted, retracted, or relabeled.

## Primary Onset Identity

An episode's source-bound identity is reconstructed only from revision 1 of
its immutable hypothesis and the evidence IDs that revision names as primary.
For each named onset evidence row, the payload must agree exactly with its
stored source kind, source ID, timestamp, and ref, and the timestamp must equal
the episode's inferred start.

If more than one primary item qualifies, the same deterministic ordering used
by Task 247 selects the canonical anchor. Alternative, contradiction, later
hypothesis, lifecycle, and result evidence cannot establish onset identity.

This also tightens historical episode reuse: an exact source fact that appeared
only in an alternative cannot alias a new prediction whose primary anchor is
that fact.

## Repeated-Onset State

Two or more immutable Task Episodes with the same canonical primary anchor and
inferred start form a repeated-onset cluster. The cluster is an integrity
defect, not evidence of multiple user tasks and not a repair instruction.

The derived index provides:

- one stable observation key per exact onset;
- the ordered immutable episode IDs in each cluster;
- a complete set of episodes excluded from Shadow evidence;
- no write, replay, model call, permission, or authority effect.

Episodes without a valid exact primary anchor retain independent legacy
identity. Similar text, nearby timestamps, overlapping windows, or shared apps
never merge them.

## Runtime Boundary

A repeated-onset episode must not:

1. enter a later lifecycle model input or receive new result evidence;
2. create or recover a private task-mirror run;
3. execute a pending private run;
4. enter pair readiness as usable work;
5. create or recover an evaluation;
6. create profile ablation or profile-learning evidence;
7. enter a Shadow learning candidate;
8. materialize or re-enter Memory, Knowledge, or Self context;
9. count as an independent source or later task in reliability measurement;
10. satisfy natural-parity or production-influence release proof.

Every gate derives the decision from immutable onset evidence. Confidence,
quality, completion, model output, and user-facing review cannot override it.

## Existing Evidence

Existing runs, attempts, results, evaluations, learning revisions, profile
rows, and materializations remain append-only history. Read models exclude
their repeated-onset lineage from active evidence and expose the exclusion
reason. They do not compensate by creating a new record.

The learning projection retains an explicit `repeated_task_start` exclusion
for any persisted evaluation in the cluster. Verified-learning retrieval
rechecks every candidate source episode, so an old materialization cannot
silently re-enter prompt context.

## Product And Developer Tools

Desk and About you count exact repeated starts once in `tasks observed`, but do
not show their private attempts as current learning progress. Evaluated counts
remain based only on admissible comparisons.

Developer Tools keeps the raw task, run, attempt, and evaluation rows. The
existing `Repeated task starts` diagnostic explains that the affected records
are preserved and excluded from comparison and learning. Exact source refs and
Task Episode IDs remain under `Technical details`.

No Merge, Repair, Retry, Use anyway, Approve, or Delete command is added.

## Failure Behavior

- A malformed onset payload or revision-1 evidence list does not become an
  exact identity; it fails back to independent legacy identity for grouping.
- A database read error fails the runtime path closed rather than admitting an
  episode without the repeated-onset check.
- Generic task/run immutability and payload validation remain unchanged.
- First Run exclusion, current-generation checks, frozen-context checks,
  destination provenance, learning qualification, and authority remain
  separate required gates.

## Verification

- Primary revision evidence groups exact historical forks; alternative-only
  evidence does not.
- Different refs at one timestamp and unanchored episodes remain independent.
- Lifecycle, reconciliation, continuation, execution recovery, evaluation,
  profile learning, and ablation all fail closed for repeated starts.
- Persisted evaluations remain visible but enter learning as explicit
  exclusions and cannot become verified Memory, Knowledge, or Self context.
- Product observation count deduplicates the exact start while progress count
  excludes it.
- Release selection cannot use a repeated-onset episode.
- Debug reads preserve row counts and authority-ledger fingerprints.
- Focused, race, full backend, frontend, production build, rebuilt live, and
  formatting checks complete before the milestone commit.
