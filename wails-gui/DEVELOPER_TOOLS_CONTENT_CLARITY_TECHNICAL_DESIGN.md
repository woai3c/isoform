# Developer Tools Content Clarity

Status: Task 244 implementation contract on `vision/shadow-agent`.

## Goal

Developer Tools must help an operator answer a question before it asks them to
interpret a subsystem. The exact runtime record remains available, but it must
not compete with the conclusion in the normal scan path.

This milestone applies the existing five-layer hierarchy:

1. destination;
2. debugging question;
3. current conclusion;
4. human-readable record;
5. exact technical detail.

## Content Rules

- A view switcher names a destination or record set. It uses a short noun
  phrase such as `Results`, `Start checks`, or `Corrections followed`.
- The opened view asks the full diagnostic question. Question-shaped labels do
  not fill the navigation row.
- A status is one conclusion. Do not render an internal state and decision as a
  pair such as `Permission setup needed - Allowed`.
- A label describes the meaning of a value. `Plans Yansu may consider` is not
  `Work types allowed`, because candidate admission is not execution authority.
- Commands name the object and effect: `Check pending activity` and `Clear
  activity-flow history`.
- Empty and error states say what could not be found or loaded. They do not
  lead with the implementation's record type.
- Contract versions, model names, hashes, UUIDs, raw enums, immutable payloads,
  and release machinery remain reachable in a closed technical disclosure.
- Advanced planning experiments are closed by default. Opening them is
  inspect-only and does not start work or change permission.

## Independent Work Views

| Group | View label | Question retained in the view |
| --- | --- | --- |
| Follow the work | Results | What happened? |
| Follow the work | Why this work? | Why did Yansu choose it? |
| Follow the work | Follow-up decisions | Should this work continue? |
| Follow the work | Reconsidered work | Can paused work resume? |
| Follow the work | Schedule changes | What was paused or resumed? |
| Permissions and limits | Task coordination | Which tasks can run together? |
| Permissions and limits | Work settings | What independent-work permission applied? |
| Permissions and limits | Start checks | Can Yansu start work now? |
| Permissions and limits | Rules Yansu follows | Which explicit personal rules apply? |
| Learn from corrections | Corrections | What did the user correct? |
| Learn from corrections | Corrections followed | Did later work follow the correction? |

## Work-Start Conclusions

The work-start view translates the activation gate into one conclusion while
preserving the exact state and decision in the title and technical record:

- `ready + allow`: `Independent work may start`;
- `activation_required + allow`: `May prepare work for approval`;
- `activation_required + suppress`: `Independent work needs setup`;
- other suppressed checks: `Automatic work search is paused`;
- invalid or denied checks: `Work search is blocked`.

The `activation_required + allow` case is intentionally not called independent
work. It allows Yansu to consider a Handoff or Schedule that still requires the
user's permission. It does not grant Initiative authority.

## Supporting Destinations

- Activity flow uses explicit command scope and readable time ranges. Frame
  diagnostics describe frames used and excluded rather than winners, siblings,
  or drops.
- Independent-work results summarize how many saved results can be checked
  again. Observer versions, raw status counts, IDs, and the last technical scan
  remain in `Technical result-check history`.
- Handoffs uses `Handoff activity`, `Live output`, and `Open handoffs` while
  retaining the technical status in the opened record.
- Notification delivery uses `Notification checks` beneath the page question.
- Independent-work settings show friendly status, access, and priority scope in
  their list. Exact setting IDs remain in the selected record's identity block.

## Safety And Data Contract

This is a presentation-only milestone. It must not change candidate generation,
activation decisions, background learning, task admission, execution,
permissions, authority, Self, memory, knowledge, prompts, model selection,
receipts, or append-only ledgers.

The plain-language conclusion is a projection of existing typed state. Unknown
values fall back to a readable enum label while the exact value remains
inspectable.

## Verification

- Interaction tests open every renamed view by its public label.
- Activation tests cover both independent-work admission and permission-only
  planning without a contradictory state pair.
- Tests prove planning versions, identity, immutable gate state, and advanced
  planning experiments remain behind closed disclosures and can be opened.
- English and Chinese content keep equivalent command, range, handoff, and
  notification meaning.
- The full frontend suite and production build pass.
- Live desktop and compact-width inspection confirm that labels wrap cleanly,
  disclosures open, details remain reachable, and no horizontal overflow is
  introduced.
