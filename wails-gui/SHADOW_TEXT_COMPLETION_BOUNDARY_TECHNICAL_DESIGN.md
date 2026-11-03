# Shadow Text Completion Boundary Technical Design

Status: implemented and rebuilt-live validated for Task 216 on
`vision/shadow-agent`

## Problem

Shadow parity must compare Yansu with the user's finished result. The retained
activity stream marks a keyboard burst ending in Return or Command+Return as a
`text_commit_candidate`, but Return has different meanings across production
surfaces:

- in Messages, Slack, Teams, and Discord it normally sends the message; and
- in Mail, Gmail, Docs, Notes, Word, Notion, and other editors it normally
  inserts a newline.

Lifecycle v6 admitted any current `user_result_candidate` destination as
terminal when that event also carried accessibility field-diff text. A model
could therefore close an editor task after its first paragraph, dispatch a
comparison, and project learning from a partial draft.

## Product Decision

Task lifecycle v7 separates **captured result content** from **terminal task
evidence**.

```text
AX field difference
  -> destination-bound committed text
  -> usable result content
  -> destination completion policy
       messaging surface + Return -> terminal candidate
       editor surface + Return    -> content only
  -> separate send / submit / publish / explicit completion when required
  -> materialized user result
  -> comparison and learning
```

The lifecycle model may associate supplied evidence with one Task Episode. It
cannot choose the destination role or completion policy; the backend derives
both from retained source fields.

## Versioned Contract

The lifecycle prompt and attempt lineage advance from `task-lifecycle-v6` to:

```text
task-lifecycle-v7
```

`task-result-destination-v1` remains the immutable destination schema. The
completion policy is a stricter backend interpretation of its already retained
bundle ID, app name, semantic role, and capture method, so no evidence row or
database migration is required.

## Completion Matrix

| Surface | Return alone may be terminal | Separate terminal evidence |
| --- | --- | --- |
| Messages | Yes | Optional |
| Slack | Yes | Optional |
| Microsoft Teams | Yes | Optional |
| Discord | Yes | Optional |
| Mail and Outlook | No | Send or explicit completion |
| Gmail | No | Send or explicit completion |
| Google Docs | No | Submit, publish, or explicit completion |
| Notes, TextEdit, Pages, Word, Notion | No | Submit, publish, or explicit completion |
| ChatGPT, Codex, Claude, Yansu, or another delegated-AI prompt | Never a user result | Not admissible |
| Unknown browser or application | Never a user result | Not admissible |

The matrix is deliberately closed and high precision. New messaging surfaces
must be added explicitly with source-backed tests; confidence, model prose, or
window-summary wording cannot widen it. A known app-name fallback applies only
when bundle identity is absent; it cannot override a conflicting bundle ID.

## Invariants

1. `ax_field_diff` remains the only admitted committed-text capture method.
2. A text commit remains useful content evidence even when it is not terminal.
3. Return can close a task by itself only when the current structured
   destination is an allowlisted send-on-Return messaging surface.
4. Editor text materializes only when the same lifecycle observation also
   cites independent send, submission, publish, or explicit-completion
   evidence.
5. Multiple destinations cannot be concatenated into one user result.
6. Delegated-AI and unknown destinations never become user results.
7. Historical lifecycle-v6 attempts and evidence remain immutable and
   inspectable. They are not rewritten or promoted into v7 proof.
8. This policy creates no Shadow run, comparison, learning, Initiative,
   permission, authority grant, or external effect by itself.

## Validation Requirements

- prove Return-only completion for every allowlisted messaging identity;
- prove Return-only abstention for Mail, Notes, Gmail, Google Docs, delegated
  AI, and unknown browser surfaces;
- prove Mail/editor text still materializes with separate `sent` evidence;
- preserve destination digest, cross-destination, actor, capture-method, and
  direct-text workflow containment tests;
- run focused and race tests, the complete Wails suite, vet, native rebuild,
  live read-only RPC, observer coverage, and unchanged authority-ledger checks;
  and
- accept the first natural parity result only when its lifecycle attempt uses
  `task-lifecycle-v7`.

## Non-Goals

- No inference from raw text content or generated summaries.
- No broad browser-title allowlist expansion.
- No replay, backfill, or synthetic parity evidence.
- No change to `shadow-task-v7`, the closed `compose.text` / `analyze.text`
  registry, learning qualification, or authority policy.
