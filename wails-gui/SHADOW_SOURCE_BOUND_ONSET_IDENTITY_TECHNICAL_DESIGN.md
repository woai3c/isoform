# Source-Bound Shadow Task Identity

Status: Task 247 implementation contract on `vision/shadow-agent`.

## Goal

One real user task start must produce one Task Episode and at most one private
Shadow attempt, even when overlapping detector windows describe that start
with different words. The identity boundary must use immutable production
evidence rather than model wording, confidence, prompt version, or detector
window identity.

This milestone prevents future duplicate starts. It does not rewrite, merge,
delete, or relabel historical append-only records.

## Observed Defect

The same user-entered ChatGPT request was present in two overlapping onset
windows. Both predictions used the same keyboard event and timestamp, but the
detector returned different local keys and task wording. The current ID key
included the detector source ID and local key, so Yansu persisted two active
Task Episodes and launched two isolated attempts for one real start.

Run-level duplicate checks cannot correct this. Once the Task Episode forks,
each episode has a valid independent run identity and inflates task, attempt,
comparison, and learning counts.

## Exact Onset Anchor

A prediction has a source-bound onset anchor only when one of its primary
`evidenceRefs` resolves to evidence that:

- has a non-empty ref, source kind, and source ID;
- occurred exactly at `inferredStartedAt`;
- came from the normalized onset input.

Alternative and reasoning evidence do not establish task identity. If more
than one primary item qualifies, anchors are sorted by source kind, source ID,
timestamp, and ref, and the first exact item is canonical.

The canonical future episode ID hashes:

```text
task-onset-anchor-v1
  + source kind
  + source ID
  + observed timestamp
  + evidence ref
```

It deliberately excludes detector source ID, observation-window bounds,
prompt version, model local key, task wording, desired result, and confidence.

When no exact anchor exists, capture keeps the existing identity derivation.
This fail-closed fallback avoids merging two merely similar or overlapping
tasks without exact production evidence.

## Persistence Contract

Before assembling a new immutable episode, capture searches for the earliest
episode with the same exact source-bound anchor and inferred start. A match is
reused without:

- appending a hypothesis revision;
- changing frozen context;
- attaching later evidence;
- changing lifecycle status;
- creating a second private run.

The lookup validates the stored onset payload, including the original evidence
ref. Matching only source kind, source ID, and timestamp is insufficient.

Concurrent capture calls compute the same canonical ID. If one writer wins and
the other receives an immutable-payload conflict, the losing writer resolves
the exact anchor again and reuses the committed episode. Any error that cannot
be resolved to that exact anchor still fails.

The first private run is also immutable for one episode decision. A retry may
arrive milliseconds later with a different model label or horizon end, but it
reuses the committed run only when the episode, frozen context, prediction,
prompt, runner, and immutable decision fields still match. A real payload or
evidence conflict remains an error.

## Overlap And History

Different exact onset anchors remain different Task Episodes even when their
times overlap or their descriptions are similar. Predictions without an exact
anchor also remain independent under the legacy key.

Existing duplicate records remain immutable. Developer Tools derives a
read-only health projection that reports:

- the number of exact starts represented by multiple historical records;
- the number of affected Task Episode records;
- exact source and episode identity under `Technical details` only.

The primary diagnostic calls this `Repeated task starts` and explains that new
detections reuse one record while saved history remains unchanged. The
diagnostic cannot merge records, retry work, create evidence, or affect
permission and authority.

## Learning Boundary

Downstream comparison and learning already bind to Task Episode and Shadow run
identity. Preventing the fork therefore prevents one observed action from
artificially increasing sample count or supporting multiple learnings. This
milestone does not promote any evidence to Self, change qualification, or
grant authority.

## Verification

- Two detector windows with different wording and local keys but one exact
  source anchor return one episode; only one result reports `added=true`.
- Concurrent captures of that same anchor converge on one immutable episode.
- Millisecond-later retries for the same immutable decision converge on the
  first private run without weakening generic run-payload validation.
- A previously persisted legacy episode with the exact anchor is reused.
- Different exact anchors remain distinct, including overlapping tasks.
- Predictions without an exact start anchor are not coalesced.
- The debugger reports historical exact duplicate clusters while preserving
  every stored row.
- Product copy hides source IDs and episode IDs until technical detail opens.
- Focused Go tests, race coverage, frontend tests, production build, and a
  rebuilt live read-only snapshot verify the milestone.
