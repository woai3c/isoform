# Yansu UI Content Design

Status: active product-language contract for `vision/shadow-agent`

## Goal

Yansu should sound like a capable coworker, not an internal architecture
diagram. A user should understand a destination, state, or action without
knowing the persistence model, prompt generation, or release machinery behind
it. Developers still need exact contracts, but those belong in technical
details rather than primary navigation and instructions.

This contract follows established UI-writing guidance: use common words,
sentence-style capitalization, specific labels, active voice, and concise
controls. A name must belong in Yansu, set the right expectation about what is
behind it, and remain understandable wherever it appears. Useful references
include the
[Apple Human Interface Guidelines for writing](https://developer.apple.com/design/human-interface-guidelines/writing),
[Apple's feature and label naming guidance](https://developer.apple.com/videos/play/wwdc2026/290/),
[Microsoft Writing Style Guide](https://learn.microsoft.com/en-us/style-guide/),
[Microsoft guidance for UI text](https://learn.microsoft.com/en-us/windows/win32/uxguide/text-ui),
and [Microsoft guidance for technical terms](https://learn.microsoft.com/en-us/style-guide/word-choice/use-technical-terms-carefully).

## Voice And Tone

Yansu speaks like a calm, capable coworker:

- **Direct:** lead with the conclusion, current state, or next action.
- **Specific:** name the task, result, source, or limit instead of saying
  `item`, `process`, or `configuration`.
- **Transparent:** distinguish what Yansu observed, inferred, compared, and was
  explicitly told.
- **Humble about evidence:** never turn repeated behavior into an unsupported
  claim about motive or preference.
- **Clear about control:** say whether a control changes data, starts work,
  grants future permission, or is read-only.
- **Quiet:** avoid congratulatory copy, personality filler, urgency, and
  repeated explanations.

Use these content patterns:

- destination: familiar noun or job, such as `Detected tasks`;
- command: verb plus object, such as `Turn off independent work`;
- state: plain state plus consequence or recovery when needed, such as
  `Independent work is paused. Update the selected priorities to resume.`;
- comparison: name both actors in the explanation, such as `Compare your
  result with Yansu's private result`;
- diagnostic detail: readable summary first, exact IDs and contract values
  second.

## Writing Rules

1. Name the thing the person is looking for or the question the tool answers,
   not the subsystem that stores it.
2. Use one product term for one concept across navigation, headings, filters,
   dialogs, empty states, and accessibility labels.
3. Use sentence case. Reserve capitalization for Yansu, product names, and
   acronyms that a developer is expected to know.
4. Use nouns for destinations and verbs for commands. Prefer `Retry` over
   `Submit`, and `Turn on independent work` over `Activate boundary`.
5. Describe a state and, when useful, the next action. Do not expose a raw enum
   as the only explanation.
6. Keep implementation terms in expandable technical details. IDs, hashes,
   policy versions, database states, and canonical enum values must remain
   exact there.
7. Do not claim reasoning from observed behavior. Say `Yansu observed...` or
   `Yansu is testing...`, not `You prefer...`, unless supported reasoning exists.
8. Do not imply authority. `Ready`, `reliable`, or `learned` never means Yansu
   has permission to act.
9. Empty states explain what is absent in ordinary language. Add a next action
   only when the user or developer can actually take one.
10. Avoid duplicate explanation. A heading, description, and control should
    each add different information.
11. Prefer recognition over recall. Group related destinations by the work
    being inspected, and keep labels distinct enough to scan without opening
    every page.
12. Do not make language friendlier by making it vague. `What Yansu can read`
    is clearer than `Screen understanding`; `Chat context` is clearer than
    either `Chat details` or the broad label `App diagnostics`.
13. Give diagnostics a three-level information hierarchy. Navigation names the
    familiar result (`Activity flow`), the page heading names the question or
    task (`What happened to observed activity?`), and drill-down detail
    preserves exact implementation state (`dispatched`, hashes, IDs, and raw
    JSON).
14. A compact metric must name both its value and its unit. Do not show `0/0 ·
    68%` when the interface can say `0 results · 0 comparisons · 68%
    confidence`.
15. Test names in context, not in a vocabulary spreadsheet. A destination name
    should sound natural in `Open ...`, set an accurate expectation before it
    opens, and still make sense in search, accessibility labels, and narrow
    navigation.
16. Prefer the obvious word when it carries the full meaning. Keep a qualifier
    when it prevents a false expectation: `Private attempts` distinguishes
    contained work from production actions, and `Result comparisons`
    distinguishes measurement from either actor's result.
17. Primary diagnostic language follows three levels:
    - navigation names the familiar object or capability;
    - the page heading states what can be inspected or answered;
    - opened detail preserves exact implementation vocabulary and evidence.
18. Use parallel labels inside a group. A list of destinations should not mix
    subsystem names, commands, unexplained state names, and data-store nouns.
19. A primary label must make sense without its description. It should identify
    the record, result, or diagnostic job being inspected, not merely name the
    subsystem that owns it. Descriptions may clarify scope or safety, but they
    must not rescue an opaque label.
20. Prefer the person's observable object over Yansu's storage object. Use
    `Detected tasks`, `Private attempts`, and `Result comparisons` before
    `Task episodes`, `Runs`, and `Correlation`.
21. Say whose work or result is shown when two actors are involved. `Yansu's
    attempt` and `Your result` are clearer than two adjacent items both called
    `Run` or `Outcome`.
22. Use a question as a naming test: `What task did Yansu detect?`, `What did
    Yansu produce?`, `How did it compare with the user's result?`, and `What
    evidence now supports an insight?` Each primary diagnostic view should
    answer one question.
23. Put the conclusion before the mechanism. A person scanning a failed state
    needs `No matching result was found` before an observer version, policy
    generation, or correlation reason.
24. Shadow diagnostics must preserve the actor and lifecycle in their first
    layer: what task Yansu detected, what Yansu produced privately, what result
    the user produced, how the results compared, and which insight the evidence
    supports. `Private attempt` must remain visible anywhere a user could
    otherwise mistake contained work for a production action. Do not collapse
    these into generic `runs`, `outcomes`, or `quality` views.
25. A Developer Tools destination names the capability, its page heading asks
    the debugging question, and its primary status states the conclusion.
    Technical nouns belong in the opened record, not in all three layers.
26. Treat a destination label as a promise about what opens. Prefer the
    observable answer (`Audio status`, `App health`) to a process noun
    (`Audio capture`, `Performance analysis`).
27. Read every label aloud in context: `Open Activity flow`, `Check App
    health`, `Replay onboarding`. Rewrite noun stacks and awkward fragments
    even when each individual word is familiar.
28. Translate codes in summaries, but never discard them. Show `Task type:
    Writing` and `Tasks Yansu can safely practice: Writing, Reviewing text` in
    the scan path; keep `compose.text`, `analyze.text`, prompt versions, and
    exact policy values in an expandable `Technical details` layer.
29. Treat diagnostic content as five layers: destination, question, conclusion,
    human-readable record, and exact technical detail. A value belongs in the
    earliest layer where it helps someone choose what to inspect; UUIDs,
    hashes, model names, prompt versions, capability keys, and raw enum reasons
    normally begin in technical detail.
30. A list row must identify the familiar object and its current consequence.
    Do not make the operator decode an ID or capability key before deciding
    whether to open the record.
31. Desk leads with consequential state, not background context. After its
    overview, show nonempty `Needs your input`, `In progress`, and `Watching
    for changes` sections before settings, priorities, or learning. Omit empty
    operational panels so the next useful cold-start or setup action remains
    visible.
32. `About you` leads with canonical understanding. Show explicit rules and
    supported work patterns before learning progress, and show current
    priorities last. Learning is not promoted Self, and a current priority is
    context for work rather than durable identity.
33. History is a receipt surface, not a notification feed. Every event must
    open to the recorded explanation, result-check state, time, related record,
    and exact technical identity. Opening a receipt never means discussing,
    accepting, correcting, or authorizing it.
34. Work begins only after the applicable permission already exists. Group it
    by the permission a user recognizes: `Independent`, `One-time`, and
    `Scheduled`. Pending one-time and recurring permission decisions stay on
    Desk, and private Shadow attempts never appear as started work.
35. Pausing scheduled work describes run state, not permission state. Say
    `Active`, `Paused`, or `Permission needs review`; explain whether Yansu can
    reuse the same permission. Keep `cron`, scope hashes, approval receipts,
    and storage surfaces out of the product scan path.
36. A waiting Shadow state must name what is missing without implying the whole
    product is idle. Shadow can be stopped while separate Background learning
    continues under its own source grants and capacity. Keep system process
    names such as `loginwindow`, release-gate stages, and backend instructions
    in technical detail.
37. An opened Developer Tools record still follows product language before
    storage language. Use questions such as `What Yansu could access`, `How
    Yansu accessed the source`, `What happened`, `Information reviewed`, and
    `Saved learning`; place scope JSON, transport codes, event enums, hashes,
    source-item kinds, and artifact IDs in local technical disclosures.
38. Visible Shadow progress is inspectable, not reviewable. A progress detail
    names the current stage, evidence still needed, and private containment;
    it never asks the user to approve a private attempt or treats comparison as
    permission. Correction belongs only to an evaluated understanding with a
    concrete statement.

## Naming Scorecard

Before adding or changing a product name, evaluate it against all five checks:

1. **Belongs:** does it sound like language a Yansu user or developer would
   naturally use?
2. **Sets expectation:** can someone predict what opens without reading help
   text?
3. **Works everywhere:** does it remain clear in navigation, search, a button,
   an accessibility label, and translation?
4. **Distinguishes:** is it meaningfully different from adjacent choices?
5. **Preserves trust:** does it avoid claiming reasoning, readiness, authority,
   or success that the evidence does not establish?

If a short familiar name passes these checks, use it. Put the diagnostic
question in the page heading and the contract name in technical detail instead
of making the navigation label carry all three jobs.

## Product Vocabulary

Internal names remain unchanged in Go, TypeScript types, RPC methods, database
records, logs, and exact developer details.

| Internal concept | Product label | Meaning in the interface |
| --- | --- | --- |
| Agency | Desk (iPhone only) | What Isoform noticed, is doing, and needs from the user; the desktop Desk was removed on 2026-10-12 and the classic interface is the only desktop interface |
| Initiative | Independent work / Work | Work Yansu starts and owns without waiting for a request |
| Handoff | One-time work | Work Yansu offered to take over and the user allowed for this task only |
| Self | About you | Yansu's evidence-backed understanding and rules to follow |
| Purpose | Priorities | Outcomes, commitments, and responsibilities worth advancing |
| Self directive | Rule | An explicit instruction or boundary Yansu must follow |
| Working boundary | Independent work settings | The priorities, folder, access level, limits, and duration for independent work |
| Portfolio usage | Working now | How many independent tasks are active, ready, or waiting within the current settings |
| First Run | Replay first task / Guided task | Use `Replay first task` in Developer Tools and `Guided task` in onboarding for the task that proves observation and a useful outcome |
| Shadow Agent | Shadow mode | Silent prediction, contained parallel work, comparison with the user's result, and evidence-backed learning |
| Shadow run | Private attempt | Work Yansu performs in containment so it can compare results without changing the user's apps |
| Task Episode | Detected task | A detected span of real user work; use `Detected tasks` for its diagnostic list |
| Background Understanding | Background learning | Low-priority learning from already allowed sources |
| Receipt | History entry / Record | An inspectable account of what happened; use `receipt` only in exact technical detail |
| Automation / Cron job | Scheduled work / Schedule | Recurring work the user has approved for future runs |
| SOP | Playbook | A reusable, editable process learned from prior work |

Short navigation labels may use `Work`, `About you`, and `Priorities`. Page
headings should name the visible object and actor before describing the
mechanism. The Work page uses `Started by Yansu` / `Work`; supporting copy may
use `Independent work` when it must distinguish an Initiative from a Handoff or
Schedule. Retire `Works without a prompt`: it is grammatically awkward and
describes a trigger instead of what the user will find. Yansu may proactively
suggest all three forms of work, so `Proactive work` is too broad for
Initiative. `Independent` never means unbounded: the interface must show the
current settings and continue to separate prediction confidence from
permission.

When all three forms are present, Work groups them as `Independent`,
`One-time`, and `Scheduled`. These labels describe how Yansu is allowed to
continue, not who first noticed the work: Yansu proactively originates or
offers all three. A permission request is not work in progress and remains on
Desk until the user allows it.

The Work view says `Working now`, `Ready to start`, or `Waiting for a task
slot`. It may explain that another task is using the same files or that a daily
limit has been reached. `Portfolio`, `admission`, `concurrency`, `lock`,
`lease`, raw budget names, operating-envelope IDs, and hashes belong in
Developer Tools, not in the primary Work scan path.

An empty Work view names the verified reason before offering an action. Use
plain states such as `Yansu is learning what to work on`, `Choose what Yansu can
work on`, `Work settings need attention`, or `Yansu has not started anything
yet`. Never collapse unavailable, invalid, still-learning, and valid-idle states
into a generic reassuring empty message. Open the real settings dialog when
settings are the next step; otherwise return to Desk for learning progress.

Background learning uses one typed product vocabulary everywhere on Desk. Name
the user's current situation first: learning now, waiting for context, waiting
for an allowed source, paused, unavailable, or finished for today. Phrase daily
usage as `N of M checks today`. A source failure must explain what the user can
do next, such as opening Gmail in a signed-in browser window. Raw scheduler
reasons, source errors, run summaries, trigger reasons, mission kinds, and state
codes remain available in Developer Tools and technical records; they are not
finished product copy. Source permission and current readiness are separate
facts: `Learning allowed - Not ready` is possible and must include a useful
next step. Opened learning records lead with access, events, reviewed
information, and saved learning before showing immutable transport and storage
details.

## Developer Tool Vocabulary

Developer tools should still be precise, but the first layer names the familiar
capability. The page then asks a concrete debugging question. Exact subsystem
names remain searchable in technical details.

| Internal destination | Navigation label | Page question or task |
| --- | --- | --- |
| Browser Access | Browser preview | Open Yansu in a browser |
| Triage Log | Activity decisions | Why did Yansu use or skip this activity? |
| Activity Pipeline | Activity flow | What happened to observed activity? |
| Shadow Agent | Shadow mode | What is Yansu learning in Shadow mode? |
| Agency Runtime | Independent work | Why did Yansu start, wait, or stop work? |
| Purpose Map | Goals and priorities | How did Yansu learn these priorities? |
| Perf Analysis | App health | Is Yansu running smoothly? |
| Handoff Monitor | Handoffs | What is happening with handoffs? |
| Audio Capture | Audio status | Is audio recording active, paused, or stopped? |
| AX Parser and OCR | What Yansu can read | What can Yansu read from this app? |
| Notifications | Notification delivery | Do notifications work on this Mac? |
| Onboarding | Replay onboarding | Replay the first-run tutorial |
| Debug | Chat context | What context will chat receive? |

The navigation groups destinations by the operator's debugging job:

| Group | Destinations |
| --- | --- |
| What Yansu observes | Activity flow, Audio status, What Yansu can read |
| How Yansu learns | Activity decisions, Shadow mode, Goals and priorities |
| How Yansu works | Independent work, Handoffs |
| Test the experience | Browser preview, Notification delivery, Replay onboarding |
| Troubleshoot Yansu | App health, Chat context |

Complex tools use grouped views rather than a flat wall of tabs. `Shadow mode`
uses `Follow one task`, `Improve predictions`, and `Learning setup`.
`Independent work` uses `Follow the work`, `Permissions and limits`, and
`Learn from corrections`.

Within a complex tool, the switcher uses short destination names rather than a
row of full questions. Put the complete question in the opened view, where its
answer and evidence can follow it. Prefer one to four words when that remains
unambiguous: `Results`, `Work settings`, `Start checks`, and `Corrections
followed`.

Never present two backend fields as though they were one product conclusion.
For example, `activation_required + allow` means Yansu may prepare a Handoff or
Schedule that still needs permission; it does not mean independent work is
allowed. Translate the combination into one truthful conclusion and retain both
exact values in technical detail.

Advanced experiments, rollout controls, prompt versions, model names, hashes,
and record identity are closed by default. They remain fully functional after
the operator opens a clearly named technical or advanced disclosure.

The primary-to-technical translation for Shadow mode is explicit:

| Primary label | Exact technical concept retained in detail |
| --- | --- |
| Private attempts | Shadow runs and contained execution attempts |
| Detected tasks | Task episodes |
| How tasks were detected | Observer traces and coverage gates |
| Result comparisons | Correlation attempts and evaluations |
| Observed work patterns | Work-pattern profile revisions and cohorts |
| Prediction accuracy | Capability profiles |
| When to suggest work | Qualifications |
| What Yansu learned | Evaluated Self-model projections |
| Suggested profile updates | Self Dream proposals |
| Replay first task | First Run journey and replay records |
| Background learning | Background Understanding runs and grants |
| App access | Computer Use app-access checks and source outcomes |

The same rule applies to Independent work:

| Primary label | Exact technical concept retained in detail |
| --- | --- |
| Results | Outcome reports and evaluations |
| Why this work? | Initiative origination runs |
| Follow-up decisions | Continuation records |
| Reconsidered work | Re-admission records |
| Which tasks can run together? | Portfolio policy, resource locks, and scheduling decisions |
| Permission requests | Operating envelopes and events |
| Can work start? | Activation-gate decisions |
| Rules Yansu follows | Self directives and adherence records |
| Corrections | Feedback-application receipts |
| Did Yansu follow them? | Feedback-adherence attempts and evaluations |
| Scheduled work | Pause and resume command receipts and exact permission scope |

## Status Language

Primary status text is human-readable and sentence case:

| Internal state | Primary label |
| --- | --- |
| awaiting_observation | Waiting for your result |
| dispatched | Processing |
| shadowed | Saved for comparison |
| outside_production_rollout | Not available with the current method |
| pair_ready | Both results are ready |
| not_worth | Not useful |
| fresh_initiative_created | Work started |
| duplicate_suppressed | Already covered |
| conflict_suppressed | Conflicting work found |
| authority_denied | Outside current limits |
| resource_deferred | Waiting for capacity |
| integrity_error | Evidence could not be verified |

The exact state remains available beside its ID or in raw JSON. A status label
must not conceal an actionable blocker.

## Acceptance Criteria

1. The desktop has one interface, the classic one; it has no `Desk` (removed 2026-10-12). On the iPhone the tabs read `Brief`, `Desk`, `Chat`, `History`, and `Settings`.
2. User-facing History does not expose `Purpose`, `Self directive`, `First Run`,
   `source receipt`, or `learning artifact` as primary labels.
3. Settings and developer-tool navigation use sentence case and the vocabulary
   above. Group labels explain which part of Yansu is being inspected.
4. Shadow mode and Independent work diagnostics use grouped, understandable
   view names and remain usable at narrow widths.
5. Search placeholders, empty states, buttons, tooltips, and accessibility
   labels use the same vocabulary as visible headings.
6. Exact internal enum values, IDs, hashes, policy versions, and JSON are not
   rewritten or hidden from technical details.
7. Copy-contract tests cover the canonical navigation and both complex
   developer-tool view systems.
8. Desktop and narrow visual QA show no clipped labels, overlapping controls,
   or ambiguous duplicate tabs.
9. Empty Work copy comes from verified product state, never raw activation
   reasons or the absence of visible rows alone.
10. Independent-work setting changes use plain action summaries in the scan
    path. Each change opens to the exact historical folder, access, priorities,
    limits, and time; setting IDs, policy versions, and hashes stay inside
    technical details.
11. Work groups active records as `Independent`, `One-time`, and `Scheduled`;
    pending permissions stay on Desk, completed records stay in History, and
    private Shadow attempts are absent from Work.
12. Paused approved schedules remain in Work, do not return as permission
    requests, and explain whether Resume can reuse the exact permission.
    Developer Tools shows their read-only `Pause and resume history` with
    technical identity disclosed only after a record is opened.
