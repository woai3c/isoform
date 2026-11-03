# Shadow Learning Influence Release Technical Design

Status: implemented and validated for Task 200 on `vision/shadow-agent`

## Purpose

Task 199 made evaluated Shadow learning eligible for later task context. Task
200 proves that one exact learning actually influenced both stages of a later
ordinary task and measures whether that influence improved the prediction.

The release argument is:

```text
source user task(s)
  -> contained Shadow result
  -> immutable user-result comparison
  -> qualified learning revision
  -> terminal materialization receipt
  -> strictly later independent user task
  -> exact production-onset prompt admission
  -> exact isolated-Shadow context admission
  -> contained Shadow execution
  -> immutable later user result
  -> current comparison contract
  -> paired neutral/profile ablation
  -> recomputed measured lift
```

This is a read-only proof over existing production ledgers. It does not run a
model, create evidence, repair history, promote a learning, change a prompt
policy, grant authority, or perform an external action.

## Product Meaning

Shadow mode should work like a production shadow deployment. A candidate model
receives real production-shaped context and independently attempts the same
work under containment. Its result is compared with what the user actually did
before any supported difference can influence later predictions.

There are two separate claims:

1. **Admission:** an evaluated learning was present in the exact model input.
2. **Benefit:** the profile-aware prediction outperformed the neutral branch
   against a later immutable user result.

Availability, semantic relevance, profile qualification, or a positive score
cannot substitute for exact admission. A measured benefit cannot grant
permission to act.

## Exact Prompt Admission

Task Episode selection evidence advances to
`work-pattern-selection-provenance-v3`.

The frozen provenance records two distinct stages:

```text
production onset
  source profile
  source selection manifest
  compact prompt profile
  prompt selection manifest

isolated Shadow context
  source profile
  source selection manifest
  enriched context profile
  context selection manifest
```

Each learning entry records:

- source eligibility and source-selection reason;
- `onsetPromptIncluded` and `onsetPromptReason`;
- `shadowContextIncluded` and `shadowContextReason`;
- exact immutable learning lineage from Task 199; and
- source, projection, selection-manifest, and compact/enriched profile hashes.

The production detector may deliberately use the neutral branch of an ablation.
In that case the episode records `onsetProfilePresent=false` and
`onset_profile_not_supplied`. It must never reconstruct the profile branch and
claim that learning influenced production onset.

Historical v1/v2 evidence remains readable, but it cannot prove exact Task 200
prompt admission. It resolves to `learning_omitted` with
`exact_prompt_admission_not_recorded`.

## Derived Proof Contract

`BuildShadowLearningInfluenceProof(now)` returns
`shadow-learning-influence-proof-v1`.

It scans complete immutable ledgers rather than bounded debugger lists and
selects the strongest current valid chain. The returned proof includes:

- current verified learning identity and terminal receipt;
- every source Task Episode, Shadow run, and evaluation ID;
- the strictly later Task Episode and frozen context/profile hashes;
- exact onset and Shadow admission decisions and reasons;
- current contained execution and evaluation IDs;
- paired ablation run and evaluation IDs;
- neutral score, profile score, lift, and winner;
- seven stable proof steps and explicit failure reasons; and
- `authorityEffect=none`.

The proof is derived on every read. It creates no new release ledger whose
existence could be mistaken for evidence.

## State Machine

```text
awaiting_source_learning
  no current verified, materialized learning exists

awaiting_later_task
  a source exists but no strictly later ordinary task has begun

learning_omitted
  a later task exists but the learning did not enter both exact model stages

awaiting_shadow_result
  both stages used the learning, but the later task/result comparison is not complete

awaiting_ablation
  the contained later result is valid, but paired onset measurement is absent

measured_no_lift
  the exact chain is complete and recomputes without positive profile lift

measured_improvement
  the exact chain is complete and the profile branch wins with positive lift

integrity_error
  a candidate chain contains contradictory, ambiguous, or non-recomputable evidence
```

An integrity failure in one unrelated historical chain does not hide a stronger
valid chain. It does prevent that candidate from advancing. Within equal states,
the most recent later task is shown.

## Source and Independence Rules

The source is loaded through `VerifiedShadowLearningSourcesAt`; Task 200 does
not introduce another learning adapter.

A later task is eligible only when:

- its inferred start is after the materialization receipt timestamp;
- it is not one of the source learning's Task Episodes;
- its profile cutoff includes the receipt and precedes task onset;
- the frozen snapshot identity and hash match the Task Episode;
- the current profile policy produced the snapshot; and
- v3 selection provenance validates without ambiguity.

The proof retains every source ID. Collapsing several source episodes into one
display ID would hide whether the later task reused its own evidence.

## Onset Integrity

The paired onset ablation must prove that the profile branch was the production
branch that created the exact Task Episode.

Validation requires:

- current onset prompt version and successful profile attempt;
- profile primary mode, snapshot ID, and snapshot hash;
- matching source kind, source ID, window, and cutoff;
- deterministic Task Episode identity plus exact task, desired result, and
  inferred start from the stored profile response;
- immutable attempt prompt and normalized-input hashes;
- canonical normalized input;
- exact compact profile bytes and selection-manifest hash; and
- v3 admission evidence matching those production bytes.

The neutral production branch cannot satisfy these rules even if a sibling
profile attempt predicted the same task.

## Shadow Containment and Result Integrity

The later Shadow result must use the current task-mirror contract and the exact
frozen Task Episode context.

Validation requires:

- current Shadow run contract, Task Episode ID, context hash, profile ID, and
  profile hash;
- prompt context byte-for-byte equal to the frozen canonical task context;
- latest completed execution attempt under the current Shadow runtime and
  `authority.ShadowPolicyVersion`;
- no allowed non-read side effect in the authority decisions;
- current immutable evaluation/comparator contract; and
- stored evaluator input byte-for-byte equal to a reconstruction from the
  immutable user and Shadow evidence.

The proof does not trust a stored aggregate score without reconstructing its
input.

## Ablation Recalculation

The neutral/profile measurement is recalculated through the same pure metric
function used when persisting the ablation evaluation.

The proof recomputes and compares:

- observed task hash;
- observed result/action hash;
- every neutral branch dimension;
- every profile branch dimension;
- neutral and profile composite scores;
- neutral and profile boundary errors;
- winner; and
- canonical evidence JSON.

Any mismatch yields `integrity_error`. The database also rejects updates and
deletes through immutable-ledger triggers; read-time recalculation remains a
second independent defense against storage corruption.

## Functional Dev Tools

The existing route remains:

```text
Profile -> Setting -> Dev Tools -> Shadow Agent -> Work profile
```

The Work Profile debugger adds one expandable `Learning influence proof` band.
It shows:

- current proof state and summary;
- source learning, later-task, and measured-chain counts;
- all seven proof steps and exact stage reasons;
- neutral score, profile score, lift, and `authority none`;
- required next evidence; and
- functional navigation to the exact later Task Episode and Shadow run when
  those records exist.

The existing per-profile `Evaluated learning influence` section now shows
onset admission and Shadow admission separately. Source-level selection is not
displayed as a proxy for model use.

There is no review queue, approval, retry, repair, replay, materialize, execute,
or authority control. Silent Shadow remains an observer and contained worker,
not user-assigned work.

## Read and Authority Boundaries

1. The proof reads existing ledgers only.
2. Browser RPC exposes the existing bounded debug snapshot/detail methods only.
3. A debugger read cannot create or continue a task.
4. A measured improvement cannot qualify a capability by itself.
5. A qualified capability cannot grant authority by itself.
6. Learning admission changes context only; it never changes permission.
7. User corrections remain append-only and cutoff-aware.
8. First Run evidence remains excluded from ordinary production learning.

## Validation

Task 200 covers:

- no-source and no-later-task cold states;
- neutral production onset recorded as omitted;
- a complete independent two-episode improvement chain;
- exact source, onset, Shadow, evaluation, and ablation linkage;
- corruption after deliberately disabling the test database's immutable update
  trigger, which still fails closed at read time;
- frontend state, expansion, exact stage labels, and functional navigation;
- focused backend and frontend tests;
- focused race coverage;
- full Wails and agent tests plus vet;
- frontend production build; and
- rebuilt live Browser RPC and visual interaction at 1280 by 720.

Standalone TypeScript still reports the established unrelated application type
and generated Wails-alias failures. The production build and complete frontend
test suite compile the Task 200 surface successfully.

The live production database currently resolves to
`awaiting_source_learning`. It contains real qualified profile snapshots and
paired historical profile ablations, but no current evaluated and materialized
Shadow learning that can satisfy this exact chain. The debugger reports that
absence rather than converting historical evidence into a success.

Native signed-in validation remains deferred while macOS is locked. The lock
must not be bypassed and no synthetic production evidence may be created to
complete the proof.

## Next Milestone

One successful chain proves possibility, not reliability. The next milestone
must derive a cohort of independent Task 200 chains per learning/capability and
measure repeated lift, regressions, unknown outcomes, corrections, and source
diversity.

The cohort must:

- count every eligible later task without a debugger row cap;
- deduplicate by immutable learning and later Task Episode;
- retain no-lift and integrity failures rather than selecting only wins;
- separate early evidence from a reliable band;
- require repeated independent outcomes before user-facing promotion;
- remain read-only and authority-neutral; and
- leave production influence and autonomy behind separate explicit release
  boundaries.
