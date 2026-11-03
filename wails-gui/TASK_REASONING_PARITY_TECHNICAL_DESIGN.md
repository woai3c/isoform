# Task Reasoning Parity

Status: implemented; natural production and unlocked-native visual proof remain

## Purpose

Task Shadow currently measures whether Yansu detected the same task, respected
its boundary, performed comparable work, and produced a comparable result. The
current Task Episode path always freezes an empty reasoning-hypothesis list,
however, and its comparator never receives a prediction of why the user might
have chosen the observed approach.

Behavior is strong evidence of what happened and weak evidence of why. The
reasoning-parity milestone lets Yansu test competing explanations without
turning one model inference into a fact about the user.

## Product Contract

Reasoning parity is silent Shadow learning. It does not create a user review
queue, an Initiative, a Handoff, a Schedule, a Memory fact, an authority grant,
or an execution permission.

At inferred task onset, Yansu may retain zero to three competing explanations:

- an explicit user statement available before the cutoff;
- a repeated, source-backed choice already present in the frozen profile; or
- a low-confidence contextual inference.

Zero hypotheses is valid. When the available evidence does not support why,
the durable answer is `unknown`.

## Evidence Separation

Three objects remain distinct:

1. **Predicted reasoning hypothesis**: what Yansu thought might explain the
   user's choice at onset. This is model-performance evidence only.
2. **Observed reasoning evidence**: a retained user-authored rationale,
   correction, or repeated-decision projection with exact source provenance.
3. **Reasoning evaluation**: a comparison between the prediction and the
   independently supported observation.

A model rationale, worker explanation, lifecycle `reason`, app content written
by another actor, or similar task outcome cannot become observed reasoning.

## Onset Contract

`task-onset-v5` adds `reasoningHypotheses` to each detected episode. Every item
contains:

- `statement`: one possible explanation, not an asserted user fact;
- `confidence`: dimension-specific confidence;
- `basis`: `explicit_statement`, `repeated_pattern`, or
  `contextual_inference`;
- exact current evidence references;
- exact profile references when repeated behavior is material;
- optional contradiction references; and
- `sourceQuote` only for an explicit statement.

The backend, not the model, enforces:

- at most three unique hypotheses;
- all evidence and profile references are in the supplied allowlists;
- contextual inference remains below `0.5`;
- repeated-pattern confidence is capped when the current task conflicts with
  the profile;
- an explicit statement requires an exact quote from user-authored keyboard or
  verified microphone evidence and an explicit rationale cue; and
- an unsupported explicit claim is retained only as a capped contextual
  hypothesis, never as observed reasoning evidence.

The normalized hypotheses are frozen in the append-only Task Episode
hypothesis revision. Historical revisions read as an empty list and are not
backfilled.

## Shadow Contract

`shadow-task-v7` copies the exact onset hypotheses into the immutable
`ShadowRun.prediction`. Evidence references are translated only to stable Task
Episode evidence identities. Reconciliation after restart must produce the
same prediction payload as immediate execution.

The shadow worker receives the hypotheses in a clearly unverified block. It
may use them to preserve uncertainty and choose among plausible approaches,
but it must not claim that any hypothesis is true. The result remains isolated
and tool-free for `compose.text`.

Changing the prompt generation starts a new quality campaign. Historical v6
attempts and evaluations remain inspectable and do not qualify v7.

## Observed Reasoning Contract

Current `task-lifecycle-v7` may return a separate `reasoningObservations`
collection.
It does not share lifecycle transition semantics and cannot complete, abandon,
merge, split, or otherwise change a Task Episode.

Each observation names one episode, one of `stated_rationale` or `correction`,
an exact source quote, a cautious normalized statement, confidence, and exact
suffix evidence references. The backend accepts it only when:

- a referenced source is user-authored keyboard content or verified
  microphone speech;
- the quote is a literal normalized substring of that source;
- the source and quote contain the required rationale or correction signal;
- the event occurs after the previous observation cutoff; and
- the target episode exists and was present in the lifecycle input.

Accepted evidence is appended with role `reasoning`. Its immutable payload
retains the reasoning kind, quote, statement, source references, model attempt,
and prompt version. A rejected item remains visible only in the raw model
attempt and creates no Task Episode evidence.

Repeated-decision evidence is not declared from one lifecycle pass. It is
derived only after distinct source-backed evaluated episodes repeat the same
choice in comparable contexts.

## Comparison And Learning

`shadow-task-evaluation-input-v3` gives the comparator the exact predicted
hypotheses and the evidence allowlist. `shadow-task-comparator-v3` may score the
reasoning dimension only when observed evidence is classified as explicit
reasoning, explicit correction, or repeated decision.

The following remain mandatory:

- no supported reasoning evidence means assessment `unknown`, score `null`,
  and low confidence;
- prediction evidence references explain why Yansu made the prediction but do
  not prove it correct;
- an explicit correction can contradict every predicted hypothesis;
- action and result similarity cannot substitute for reasoning agreement; and
- every non-unknown evaluator claim cites exact allowed evidence identities.

Existing learning policy remains conservative. One supported result can enter
the append-only learning ledger, but active Self decision policy still requires
repeated qualified evidence. Unsupported or inferred rationale never enters
Memory, Knowledge, Work Pattern Profile semantic learning, or typed Self.

The v4 comparison contract validates canonical lifecycle reasoning again at
the evaluation boundary. A reasoning row is support only when its payload is
valid current `task-lifecycle-v7` JSON, its rationale/correction kind is
allowlisted,
its literal quote matches the cited user source, its source identity matches
the payload, and its deterministic source identity is intact. Legacy untyped,
malformed, wrong-version, mismatched, or forged rows remain inspectable but are
classified as ordinary action evidence.

Evaluation persistence records input, output, comparator, and evaluator
versions. Pair readiness retains all evaluation IDs but separates current from
historical IDs. A historical-only pair remains eligible for a current
re-evaluation; it is never considered evaluated by the current campaign.
`shadow-task-learning-v2`, quality aggregation, and
`task-shadow-release-gate-v4` admit only current-generation evaluation IDs,
and a learning receipt must cite the selected current evaluation.

Worker generation is part of the same boundary. Pair readiness classifies any
non-`shadow-task-v7` run as `historical_generation`, and all evaluations on
that run remain historical even when their comparison payload uses the current
schema. Evaluation construction, runtime, and persistence reject historical
runs before creating observation or evaluation state. Release steps are
derived from one selected current run; no episode, attempt, result, evaluation,
or learning evidence may be assembled across generations. The v4 natural
parity projection additionally requires a separately verified current
user-result destination digest. It may prove the loop for either closed
direct-text workflow, while the quality campaign remains compose-only.

## Context And Authority Boundaries

Reasoning hypotheses and observations:

- are selected without a fixed top-N Memory or Knowledge count, within the
  existing byte-budget and provenance manifest;
- never enter a tool prompt as authority;
- never activate or widen an operating envelope;
- never satisfy Handoff or Schedule permission;
- never change capability release by themselves; and
- never become production outcome evidence merely because the shadow worker
  used them.

## Diagnostics

Task Episodes Dev Tools exposes `task-reasoning-debug-v2`, read-only:

- onset hypotheses with basis, confidence, quote validation, evidence, profile,
  and contradiction references;
- user-authored reasoning evidence with exact provenance and kind;
- comparator reasoning input, assessment, score, confidence, and citations;
- current versus historical onset, lifecycle, worker, comparator, and evaluator
  versions; and
- explicit `unknown` coverage rather than a false zero.

No debugger control may create, edit, replay, label, approve, or promote
reasoning evidence.

The projection independently validates its own lineage. It reports whether the
comparator input is byte-equivalent to the immutable Shadow prediction, whether
every non-unknown result cites an allowed reasoning source, and whether each
hypothesis resolves against evidence available at the task-start cutoff. A
later observation may support evaluation, but it cannot retroactively validate
an onset prediction.

## Release Gates

1. A Task Episode with no supported reason freezes an empty hypothesis list and
   later evaluates reasoning as `unknown`.
2. A contextual hypothesis is capped below `0.5` and never becomes observed
   reasoning or Self evidence.
3. An explicit hypothesis without a literal user-authored quote is downgraded
   or rejected before persistence.
4. Immediate Shadow creation and restart reconciliation produce the same
   hypothesis payload and hash.
5. Lifecycle reasoning capture appends evidence without changing episode state.
6. Assistant-authored, other-party, stale, post-cutoff, invented, or unmatched
   quotes create no reasoning evidence.
7. A supported rationale or correction reaches the comparator with exact
   provenance; absent support cannot receive a numeric score.
8. Learning preserves unknown reasoning and requires repeated source-backed
   evidence before active Self promotion.
9. Prompt-generation changes exclude historical evidence without rewriting it.
10. Focused, full, race, frontend, build, whitespace, live read-only, and
    containment checks pass. Natural production proof is observed, never
    manufactured.

The automated and live read-only gates pass for the implemented generations.
The retained production database currently contains no natural v7 Shadow run
or v2 reasoning evaluation, so release proof remains explicitly pending rather
than being synthesized from a fixture. Native desktop inspection also remains
pending while the Mac is locked.
