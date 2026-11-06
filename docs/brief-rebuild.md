# Brief rebuild: sealed briefs, amend on privacy change, one latest record

Status: PR 1 (steps 1 and 3 of the plan approved 2026-11-05). Owner: brief rebuild.
Code: `wails-gui/mobile_brief_finished_read.go`, `wails-gui/mobile_brief_current.go`,
`wails-gui/mobile_brief_read_privacy.go`, `wails-gui/launch_brief.go`.

## Why

A brief that was built correctly could disappear from Today. On 2026-11-05 the 6:50
AM morning brief was built with text and audio, then Today withheld it on every read
with `reason=store_unavailable`. Every read of a brief re-ran the privacy and source
gates (`mobileBriefForDelivery`) against live stores (memory, activity, purposes, mail
subjects, ranked inputs). A store that was still opening, or a read at launch, hid the
brief. Each surface (Today, the menu bar, Chat, the phone) also chose "the latest
brief" with its own logic.

## What changes

1. Sealed brief. A brief is sealed once, when it is made or changed: the gates run
   once, and the display-ready result is saved beside the brief as `<id>.read`. A
   plain read returns that copy and calls no store. A store that is still opening
   cannot hide a sealed brief.
2. Privacy changes amend, they do not re-gate on read. A privacy change advances the
   privacy epoch. A sealed brief is valid only while its epoch is current. The next
   read of such a brief seals it again (the amend). Each amend is idempotent: a copy
   sealed under the current epoch is never sealed again.
3. One latest record. `latest-shown.json` names the brief every surface shows
   (id, slot, time). Today, the menu bar, the launch read and the phone all go through
   `finishedLatest()`, which reads `latestShown()`. Chat's card for one brief (opened from a turn
   by its id) still runs the gates in PR 1; see the gaps below. An empty or fully
   withdrawn brief never replaces the record's brief with words (R61).

## Data shapes

`<briefs>/<id>.read` (schema 2, written atomically):

```json
{ "schema": 2, "epoch": 7, "day": "2026-11-05", "cites": ["memory|m1", "handoff|memo"],
  "brief": { ...the display-ready MobileBriefV1, cards and audio included... } }
```

- `epoch`: the privacy epoch the copy was sealed under.
- `day`: the local date it was sealed on. Day words ("today", "yesterday") are
  relative, so a copy sealed on another day is sealed again when it is read. If the
  gates cannot decide that day, the saved copy stands, because nothing private changed.
- `cites`: source keys the words were checked against (`mobileBriefModelInputs`),
  sorted. Empty means the brief has no binding record. PR 1 does not use `cites` to
  narrow an amend; every privacy change re-seals the brief on its next read. PR 2
  uses it to amend only the briefs that cite the withdrawn source.
- `key`: retired. Schema 1 copies (with `key`) are not sealed; they are sealed once
  on first read (migration).

`<briefs>/latest-shown.json`: `{ "version": 1, "briefId": "...", "slot": "morning",
"at": "<asOf>" }`. It moves only when a brief with written words is sealed and is at
least as new as the record, or when the brief it names no longer has words.

`<briefs>/privacy-epoch`: the privacy epoch as decimal text. It is written before the
privacy event returns, so a restart keeps the epoch and does not seal every brief
again.

## Privacy-class and freshness-class events

- Privacy class (advance the epoch, amend on next read): every
  `publishPrivacyWithdrawal` (forget, private app or window, source or session
  withdrawal, account removal, observation holds), plus the app events
  `sensitive-apps:updated`, `closed-eyes:changed`, `background-listening:changed`,
  `topics:withdrawn` and `mail:synced` (the mail echo holdback reads mail subjects).
- Freshness class (read again by caches only, `mobileBriefReadEpoch`): `cron:state`,
  `calendar:synced`, `memory:updated`, `handoff:*`, `activity:summary-ready` and the
  rest of `mobileBriefReadInputEvents`. A sealed brief is not sealed again for these;
  the next brief made or refreshed carries them.

## Read rules

- `latestShown()`: the record's brief first, then the other stored briefs newest
  first. The first brief whose sealed words may be shown is the latest. A brief
  whose words are withheld for a transient reason (a store still opening, Closed Eyes,
  mail that cannot be read) ends the search: it is shown without words while the
  reason lasts, and an older brief is not shown in its place.
- With no brief that has words, the newest readable brief stands in (a template), as
  before.
- A transient withholding is shown but never saved, so the copy on disk survives until
  the gates can decide.

## Gaps that remain (later PRs)

- PR 2 (withdrawals amend, not re-gate): a background job amends every sealed brief
  that cites a withdrawn source, emits one `brief:amended` event, and writes
  atomically. It uses `cites` to limit the work. Today the amend runs on the next read
  of each brief, which covers every reader but is not eager.
- PR 2: the handoff delete path is not a privacy change. A removed work record leaves
  the sealed copy until the next seal (Today's refresh does that). This is a change
  from the old behavior, which re-gated on read; the pinned-memory test is adjusted.
- PR 4 (durable outbox): delivery to Chat, desktop notification and the phone stays
  as it is. `#569` catch-up is not replaced yet.
- PR 5/6: fallbacks in the build steps and the end-to-end test are not done.
- Chat's per-turn brief card (`todayChatDeliveredBriefFrom`) still runs the gates when
  it opens a brief by id. Its cards are built with the turn's sources, and applying them
  to a sealed copy needs checking first.
- A brief that was built before this change is sealed on its first read. If the stores
  are still opening at that moment, it is shown without words until they open.

## Migration

No data is moved. A brief with a schema 1 copy, or with no copy, is sealed on its first
read with the current gates. Briefs made after this change are sealed when they are
made, so the first read is plain.

## Open questions for Design

- The copy for a brief whose words are hidden for a transient reason (today: the
  brief shows its cards and no words). Design to say whether to show a line.
- Whether an amended brief with no words but with cards should read as the template.

## Screen rules (Claude Design, brief-rules v1.0.0, final)

Source: `handoff-2026-11-05-1443-brief-rules-v1.0.0.md`.
They add to R61, R64, R25, R55, chat-review v1.0.12 and R34 (polish-walk v1.0.7). Copy
goes through i18n in en and zh-CN; no code appears on screen.

| Rule | What the screen shows | Where it lands |
|---|---|---|
| 1 One latest brief | Every surface reads the one record and shows the made-at time ("6:50 AM"), not the slot. Today shows the brief in full; "Make my brief now" is never the main action while today's brief exists. An empty panel is never shown. | PR 3 (surfaces); the record is PR 1 |
| 2 Never called missed | "Didn't come" only when no brief was made for the slot; one cause line. Applies to Today's subline, Activity's Your day, Tasks' last three, the Missed brief notification and the phone. An early brief is not missed. | PR 3 |
| 3 Amended brief | Keeps its time and place; one quiet line "Updated 2:14 PM · 1 item was taken out because its source is gone."; Hear it hidden until the audio is remade; the phone copy is replaced, not duplicated; no notification unless already opened (then one quiet notification counted in the day's cap). All items taken out: the brief stays with its one line and "Make my brief now". | PR 2 (amend and audio) and PR 3 (copy and notification) |
| 4 Can't be shown | Today keeps the last brief it could show under its own time; one plain line says why. Storage still opening: it shows on its own when ready. Still unreadable after retries: "Make my brief now" writes a new one. Same line on menu bar, Chat card and phone. | PR 3 (copy); the background retry is PR 2/4 |
| 5 Why | Opens one or two sentences about this brief (what happened, when, what happens next). Never the settings in its place. | PR 3 |
| 6 Phone | "On your phone when it's back online" until sent; delivered once; retries never resend. | PR 4 (outbox) |
| 7 Fallbacks | A fallback is invisible unless it removes something the person would look for; then one quiet line ("No recording this time"). A fallback never changes the brief's time or title and never leaves an empty brief. | PR 5 |
| R34 Chat card | A brief's Chat card always shows the brief's first line, or the can't-be-shown line. | PR 3 |
| ST82 | The Missed brief notification follows rule 2. | PR 3 |

Plan change: PR 1 stays as built (sealed copy, latest record, privacy print, event hooks).
PR 2 (amend) must carry the amended-brief state (rule 3) so the surfaces can read it.
PR 3 is new: surface copy, made-at time, the can't-be-shown lines, Why, and R34, all in i18n.
Each screen change ships with its own tests in the surface it changes.

## Status at the end of this session (PR 1 not opened)

Built and tested: the sealed copy (privacy print, source digest, day), the one latest
record, the privacy hook and the source-changed hook (background, coalesced), the
read that never seals, the handoff and week-answer checks.

Not yet green. Five focused tests still fail on the no-copy gap and the withdrawn
card. The hub ruling below is not implemented yet.

### Hub ruling (2026-11-05, pending implementation)

- Storing a brief seals it in the same call; the stored brief and its copy appear together.
- Privacy and source hooks re-seal synchronously inside the action that fired them,
  before it returns. Background re-seal stays only for the print/day safety net.
- Write paths read the stored brief directly, not the display copy.
- Legacy briefs (no copy): a one-time startup migration seals them in the background;
  until sealed, a display read may seal once (migration only).

Implementation risk found while planning: the handoff `ChangedHook` fires inside the
handoff store's lock, and `mobileBriefStore.save` fires the source hook while its own
lock is held. A synchronous seal there would deadlock. The hooks must move outside the
locks first. This is the next step.

Intended behavior changes in this PR (to list in the PR body): a sealed brief keeps
showing handoffs when a source read fails; a closed policy store does not hide a sealed
brief; a memory write no longer seals a brief again; reads wait for a background seal
in tests that check the write-then-read order.
