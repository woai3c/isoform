# Developer Tools Content Hierarchy

Status: active contract for Task 237 on `vision/shadow-agent`

## Problem

Yansu's Developer Tools navigation uses familiar product language, but several
high-traffic views still expose internal codes in their scan path. A developer
can encounter capability keys, record IDs, prompt versions, model names, raw
reasons, and persistence terms before understanding what happened.

That makes the interface harder to scan without improving diagnostic power.
The exact values are necessary, but they are most useful after the operator has
identified the record or state they want to investigate.

## Content Hierarchy

Every Developer Tools destination follows five layers:

1. **Destination** names the familiar capability, such as `Shadow mode`.
2. **Question** states what the page helps diagnose, such as `What is Yansu
   learning in Shadow mode?`.
3. **Conclusion** states the current result and next useful step in ordinary
   language.
4. **Record** identifies the user's task, Yansu's result, or the observed app
   with a translated status and units.
5. **Technical details** preserves exact IDs, enum values, versions, hashes,
   prompts, models, and raw reasons.

The first four layers must be understandable without knowing the Go types,
database schema, prompt contract, or release machinery. The fifth layer must be
complete enough to trace the exact backend record.

## Naming Rules

- Prefer the observable object: `Detected task`, `Private attempt`, `Your
  result`, and `Result comparison`.
- Name both value and unit: `2 private attempts`, not `2 runs` or `2`.
- Translate lifecycle codes into a conclusion. Keep the exact code in
  technical detail and, where useful, in a title attribute.
- Do not put UUIDs, hashes, model names, prompt versions, or capability keys in
  list-row summaries.
- Do not use a verb that suggests mutation for a read-only destination.
- Keep confidence scoped to what it measures. `Prediction confidence` is not
  readiness, permission, or authority.
- Preserve unknown reasoning as unknown. Friendly wording must not invent a
  motive or preference.
- Use sentence-style capitalization for headings, labels, and controls.

## Task 237 Scope

### Shadow mode

- Keep the task-comparison sequence visible: detected task, private attempt,
  user result, comparison, and supported learning.
- Show friendly task-type labels in lists. Exact capability keys remain in the
  selected record.
- Move continuation versions, record IDs, raw actions, pair states, prompt
  versions, model names, and raw blocker reasons into technical disclosure.
- Show a plain next step for each continuation item.
- Label confidence specifically as prediction confidence.

### Activity decisions

- Add the page's debugging question above the list.
- Keep time, source, decision, contribution, and plain reason in each summary.
- Move the exact activity ID, decision code, and raw reason into an expandable
  row detail.
- Translate structured legacy reason strings into their saved title and
  description instead of displaying the serialized `title=...` contract.
- Scroll the decision list itself when new records arrive. Do not move the
  settings page away from its title, question, filters, or navigation context.

### App reading

- Describe what text Yansu can read before naming Accessibility or OCR.
- Use familiar control names such as `Current app`, `Choose app by ID`, `Time
  to switch apps`, and `Saved check name`.
- Preserve bundle IDs, Accessibility, OCR, tree sizes, and saved paths where
  they are required for diagnosis.

### Goals and priorities

- Use `Priority checks` instead of `Learning runs` in the summary.
- Describe unavailable supporting data as `Supporting sources are unavailable`
  rather than referring to evidence records.

### Settings navigation

- Prefer familiar destinations: `Keyboard shortcuts`, `Action cursor`,
  `Observation`, `Personal context`, `AI model`, `Tool servers`, and `Use in
  other AI apps`.
- Keep technical terms such as MCP in the destination description and page
  detail where they help configuration.

## Safety And Data Contract

This milestone is presentation-only. It must not:

- change an RPC, database row, prompt, model, task-detection rule, comparison,
  learning admission, Self promotion, permission, or authority decision;
- hide or rewrite the exact value in the opened technical record;
- turn a read-only debugger into a replay or mutation path;
- treat prediction confidence or learning readiness as permission.

## Verification

- Copy-contract tests cover canonical English and Chinese navigation labels.
- Shadow mode tests prove raw capability keys and continuation IDs are absent
  from the scan path and present after technical disclosure or record opening.
- Activity-decision tests or source assertions prove exact IDs are disclosed,
  not removed.
- Activity-decision live inspection proves ordinary summaries stay readable,
  legacy structured reasons are translated, and raw values remain available
  after opening `Technical details`.
- Existing interaction tests continue to open every list record and view.
- The full frontend suite and production build pass.
- Live desktop and compact-width inspection confirms readable labels, no text
  overlap, functional disclosure, and no horizontal overflow.
