# Shadow Result Destination Provenance Technical Design

Status: implemented and validated for Task 192 on `vision/shadow-agent`;
completion semantics tightened by Task 216

Task 216's successor boundary is documented in
`SHADOW_TEXT_COMPLETION_BOUNDARY_TECHNICAL_DESIGN.md`. It retains this v1
destination schema while preventing Return-only completion in multiline editor
surfaces.

## Problem

Shadow parity is valid only when Yansu compares its isolated result with the
user's independently produced result. The current committed-text boundary
retains exact user text, but it decides whether that text was entered into a
delegated AI surface by parsing a generated activity summary. Summary wording
is presentation data, not durable source provenance. A changed summary can
therefore admit an AI prompt as the user's result or reject legitimate work.

The raw activity event already retains the source fields needed for a stricter
boundary:

- destination application bundle ID;
- destination application name;
- destination window title; and
- committed text recovered from focused-field accessibility snapshots.

Task 192 carries those fields into lifecycle evidence, records how the text was
captured, classifies the destination deterministically, and binds the accepted
destination to the materialized result. It does not broaden production Shadow,
observe new data, or grant execution authority.

## Product Loop

```text
focused-field AX snapshots
  -> exact field-diff committed text
  -> structured destination provenance
  -> deterministic semantic role
  -> lifecycle association with one Task Episode
  -> backend result admission
  -> destination-bound materialized result
  -> current-generation parity evaluation
  -> evidence-backed learning
```

The lifecycle model may associate evidence with a task. It cannot choose or
rewrite the capture method, destination identity, semantic role, result body,
digest, or workflow.

## Invariants

1. Activity summaries never determine whether committed text is a user result.
2. Only text recovered by the focused-field AX before/after differ is eligible.
   Raw key tokens, OCR, speech, model output, clipboard inference, and legacy
   committed text with no capture method remain ineligible.
3. Every eligible event retains a versioned destination with bundle ID, app
   name, window title, capture method, and semantic role.
4. The backend recomputes semantic role from the structured raw fields before
   accepting a result. A stored or model-provided role is not trusted.
5. Native delegated AI applications and browser windows explicitly identifying
   an AI product are `delegated_ai_prompt`, never user results.
6. Explicit writing and communication destinations are
   `user_result_candidate`. Task identity, exact workflow, actor, commit
   gesture, content, and lifecycle evidence must still pass independently.
7. Generic browsers and unfamiliar or incomplete destinations are `unknown`.
   Unknown means fail closed, not delegated and not a user result.
8. Multiple committed-text references may form one result only when every
   accepted reference has the same canonical destination identity.
   Conflicting destinations cannot close a task from text alone; a separate
   terminal event may close the task but cannot manufacture one combined
   result.
9. The materialized result carries the exact destination and a canonical
   destination digest. Persistence revalidates both before comparison.
10. Historical evidence without Task 192 provenance remains immutable and
    inspectable, but cannot satisfy a current comparison.
11. Current lifecycle and evaluation generations change. Earlier comparisons
    remain historical and cannot mask a missing or malformed current result.
12. Dev Tools exposes provenance and hashes without exposing committed text or
    lifecycle payloads.
13. Destination confidence, result similarity, qualification, and learning
    never grant authority, create an Initiative, or cause a production action.

## Capture Provenance

`ActivityEvent.TypedTextCaptureMethod` is empty for historical events and all
text not produced by an accepted capture path. The initial and only admitted
method is:

```text
ax_field_diff
```

The typed-text segment records the application identity and window at segment
open. A bundle or window change invalidates that segment and starts a new one;
text from two destinations must never be attached to one keyboard burst. When
the AX differ attaches committed text, it marks that exact event with
`ax_field_diff`. Protected-app persistence clears both text and capture method.

## Destination Contract

Lifecycle evidence carries:

```json
{
  "version": "task-result-destination-v1",
  "appBundleId": "com.apple.mail",
  "appName": "Mail",
  "windowTitle": "Customer update",
  "semanticRole": "user_result_candidate",
  "captureMethod": "ax_field_diff"
}
```

The semantic-role classifier is a closed, deterministic registry:

| Role | Admitted examples | Result behavior |
| --- | --- | --- |
| `delegated_ai_prompt` | native ChatGPT, Claude, Codex, Yansu; browser title explicitly naming an AI product | always rejected |
| `user_result_candidate` | Mail, Messages, Slack, Teams, Word, Pages, TextEdit, Notes, Notion; browser title explicitly naming Gmail or Google Docs | may pass the remaining result gates |
| `unknown` | generic Safari/Chrome, terminals, editors, unfamiliar apps, missing identity | rejected |

The registry prefers false negatives over contaminated learning. Browser URL or
focused-control semantics can later extend this versioned contract; Task 192
does not infer them from a title that lacks an explicit product marker.

Normalization trims and bounds display fields. Validation then recomputes the
role from bundle, app, and window. Summary text has no input to the classifier.

## Result Admission

A committed-text event can materialize a direct-text result only when all of
the following are true:

```text
one exact Task Episode
+ workflow is in the closed direct-text registry
+ source kind is activity_event
+ event kind is text_commit_candidate
+ actor is user
+ content kind is committed_text
+ retained content is non-empty
+ destination contract version is current
+ capture method is ax_field_diff
+ recomputed role is user_result_candidate
+ stored semantic role matches the recomputed role
+ every accepted result ref names the same destination
= result-capable evidence
```

The materialized result stores the exact workflow, content digest, evidence
refs, destination object, and destination digest. The result body remains
materialized from production evidence only. A later evaluator read verifies
workflow, content digest, destination digest, destination role, and evidence
shape before adding the user result to comparison input.

## Generation Boundary

Task 192 advances the lifecycle prompt because destination semantics enter the
model input and rules. It also advances evaluation input, output, comparator,
and evaluator contracts so a prior comparison cannot stand in for a current
destination-bound pair. The isolated `shadow-task-v7` attempt generation does
not change: the frozen independent attempt remains valid and may wait for a
later current-provenance user result.

Legacy lifecycle observations and evaluations are append-only history. They
are neither rewritten nor silently upgraded.

## Canonical Result Selection

Task 193 applies the same provenance validator before pair readiness and
evaluation select the user result. Repeated current observations at one exact
destination are immutable revisions: only the newest valid observation enters
the comparator. Current observations at different destination digests fail
closed and identify every conflicting receipt. Historical and superseded
summary text is replaced with a generic excluded-evidence marker in comparator
context so an older result cannot contaminate the current comparison.

The derived `task-shadow-release-gate-v4` exposes content and destination as
separate proof receipts. This changes no persisted result, destination role,
quality campaign, qualification event, or authority policy.

## Dev Tools

Shadow Agent Task Episode detail gains a read-only Result destination
provenance section. For each retained result evidence item it displays:

- evidence ID and observed time;
- current, historical, or invalid provenance status;
- semantic role;
- app name, bundle ID, and window title;
- capture method and contract version; and
- destination digest.

The provenance section suppresses committed text and lifecycle payload JSON.
Other pre-existing raw diagnostic sections retain their existing local-only
contract. Browser preview remains inspect-only and never runs lifecycle
observation, evaluation, or learning.

## Validation Matrix

1. AX field-diff attachment marks the capture method; other typed text does not.
2. Protected-app persistence clears text and capture method together.
3. Native and explicit browser AI destinations classify as delegated.
4. Explicit writing destinations classify as candidates.
5. Generic browser, terminal, missing, and unfamiliar destinations are unknown.
6. Summary spoofing cannot change a structured role.
7. Delegated, unknown, legacy, wrong-method, and malformed evidence fail closed.
8. Mixed destinations cannot be concatenated into one result.
9. Destination and content digest tampering fail comparison input construction.
10. Prior evaluation generations remain historical and cannot qualify learning.
11. Dev Tools renders complete redacted provenance and performs no write.
12. Focused tests, full Go tests, vet, race coverage, frontend tests, production
    build, rebuilt read-only RPC, and live browser interaction all pass before
    release verification.

## Non-Goals

- No URL capture or browser-history access.
- No semantic reading of committed text to guess its destination.
- No widening beyond `compose.text` and `analyze.text`.
- No OAuth, connector, Computer Use, or external-write change.
- No retrospective migration that labels historical summaries as provenance.
- No user-facing Shadow prediction, approval flow, Initiative, or authority.

## Validation Result

Validated on 2026-08-04:

- activity capture marks only exact AX field differences as
  `ax_field_diff`, binds the source app and window at segment open, resets on
  destination changes, and clears text plus provenance for protected apps;
- lifecycle `task-lifecycle-v6` deterministically revalidates
  `task-result-destination-v1`, admits one candidate destination, and rejects
  summary spoofing, delegated AI, generic browsers, legacy or wrong-method
  evidence, mixed destinations, and malformed provenance;
- comparison uses the v4 input, output, comparator, and evaluator lineage, and
  persistence verifies both content and destination digests before a result
  can enter evaluation or learning;
- Task Shadow Dev Tools aggregates redacted provenance receipts and contains
  malformed current evidence as `user_result_unavailable` rather than
  blanking the detail view;
- the uncached complete Wails suite passes in 131.294s, both Go vet suites and
  the complete agent suite pass, focused race coverage passes in 31.733s with
  only the established macOS `LC_DYSYMTAB` warning, all 44 frontend files and
  222 tests pass, and the production frontend build succeeds; and
- the rebuilt live debugger reports lifecycle v6, the exact
  `compose.text, analyze.text` registry, 74 historical Task Episodes, zero
  current-provenance observed results, and one historical result explicitly
  labeled `historical_missing_destination` with source identity and hashes but
  no result body in the provenance section. A separate signed-in clean reload
  rendered successfully with no browser warning or error.

No current result, comparison, learning, Memory, Knowledge, Self, Initiative,
authority, execution permission, or external effect was fabricated for
validation. A natural current-generation parity result remains gated on future
ordinary user work; native signed-in QA remains unavailable while macOS is at
`loginwindow`.
