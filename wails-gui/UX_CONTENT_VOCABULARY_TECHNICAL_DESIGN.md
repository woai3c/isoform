# UX Content Vocabulary

Status: Task 249 implementation contract on `vision/shadow-agent`.

## Goal

Yansu should name one concept one way. A person scanning Settings or Developer
Tools should understand where a destination leads without knowing the backend
domain model. Exact schema and runtime terms remain available when an engineer
opens technical details.

This milestone changes presentation only. It does not change observation,
prompts, execution, learning, Self, permission, authority, receipts, or
append-only records.

## Writing Rules

1. Use familiar words before product or engineering terminology.
2. Use short, parallel noun phrases for destinations and categories.
3. Use a direct question for a diagnostic page's main heading when the page
   answers that question.
4. Use verb-object labels for commands. The label must name the effect.
5. Describe a state as what happened or what comes next, not as an internal
   lifecycle code.
6. Lead with the conclusion. Put provenance and exact identity later.
7. Use sentence case in English. Keep English and Chinese information
   architecture parallel rather than translating individual words literally.
8. Do not repeat a heading in its description. Use the description to add the
   purpose, boundary, or next step.
9. Keep IDs, hashes, versions, model names, prompt names, and raw enum values
   inside `Technical details`, copy actions, or explicit debug metadata.

These rules follow established UI-content guidance: design for scanning, use
simple and concise language, keep parallel controls grammatically parallel,
and make commands specific and self-explanatory.

## Product Vocabulary

The Agency product surface keeps these canonical destinations:

| Concept | Product label |
| --- | --- |
| daily agency overview | Desk |
| active agent-owned work | Work |
| canonical typed Self | About you |
| immutable outcomes and decisions | History |
| current desired outcomes | Priorities |
| one-time permission to take over | Handoff |
| recurring permission | Schedule |
| bounded self-started work | Independent work |
| conversational escape path | Quick chat |

Internal terms such as Initiative, Self, authority grant, receipt, mission,
planner, envelope, projection, and materialization are not primary product
labels.

## Developer Tools Navigation

Developer Tools uses five short categories:

- `Observation`
- `Learning`
- `Work`
- `Testing`
- `Troubleshooting`

Destinations use the operator's object of inspection:

| Internal topic | Navigation label |
| --- | --- |
| activity pipeline | Activity processing |
| audio capture | Audio capture |
| AX parser and OCR | App reading |
| activity triage | Learning decisions |
| shadow agent | Shadow learning |
| purpose map | Priorities |
| agency runtime | Independent work |
| handoff monitor | Handoffs |
| browser bridge | Browser access |
| notification tests | Notifications |
| onboarding debugger | Onboarding |
| performance analysis | App health |
| chat debug mode | Chat context |

The navigation label identifies a destination. The opened page may use a
longer question to explain what it answers.

## Shadow Vocabulary

`Shadow learning` is the Developer Tools destination. The page heading asks
`How is Yansu learning from your work?` and then describes the contained
observe, practice, compare, and learn loop.

The primary hierarchy is:

| Internal concept | Primary label |
| --- | --- |
| task episode | Observed task |
| observer trace | Task detection |
| shadow run or attempt | Private practice |
| evaluation or correlation attempt | Comparison |
| work profile | Work pattern |
| capability profile | Prediction accuracy |
| qualification | Suggestion readiness |
| self-model entry | Supported insight |
| self-dream candidate | Profile suggestion |
| first-run debugger | First task |
| background understanding | Background learning |
| computer-use access | App access |

Group labels are `Task timeline`, `Learning quality`, and `Learning sources`.
Internal names remain valid in code, APIs, stored records, and technical detail.

## Status And Error Pattern

- Completed state: `Comparison complete`, not `evaluated`.
- Waiting state: `Waiting for your result`, not `awaiting_observation`.
- Exclusion: `Not used for learning`, followed by the reason.
- Load error: say what could not load and retain the raw error in technical
  detail or the error body.
- Empty state: say what evidence does not exist yet and what ordinary event can
  create it. Never imply that a replay can substitute for production evidence.

## Accessibility And Localization

- Accessible names use the same visible concept as the control.
- Master-detail regions derive their labels from the visible destination.
- English and Chinese tests assert the same group and view topology.
- Labels must wrap at compact widths; navigation never depends on a tooltip to
  reveal the full destination.

## Verification

- Content-contract tests assert the complete English and Chinese navigation and
  Shadow hierarchy.
- Primary labels reject internal terms such as episode, observer, runtime,
  qualification, self model, correlation, pipeline, parser, and monitor.
- Interaction tests open every renamed Shadow view and verify accessible
  master-detail labels.
- Full frontend tests, production build, strict TypeScript baseline comparison,
  live desktop and compact inspection, formatting, and `git diff --check`
  complete before the milestone commit.

## References

- Microsoft, [Windows Admin Center UI text and terminology](https://learn.microsoft.com/en-us/windows-server/manage/windows-admin-center/extend/guides/ui-text-style-guide): use familiar, concise, specific language; make parallel controls grammatically parallel; use clear verb-object commands.
- Microsoft, [Writing style for Windows apps](https://learn.microsoft.com/en-us/windows/apps/design/style/writing-style): write for scanning, use sentence case, and put the most important information first.
