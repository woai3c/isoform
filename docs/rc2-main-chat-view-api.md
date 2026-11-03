# RC 2 C1: main chat views

These bindings project the existing main conversation. They do not create a
session, send a message, move a row, or persist a fold. No schema-version change
or new tables; bootstrap repairs two additive chat access indexes.

- `GetMainChatOverview()` returns `sessionId`, computer-local `day`
  (`YYYY-MM-DD`), `conversations`, `waiting`, `finished`, `routine`,
  `waitingItemIds` and `hideRoutine`. An uninitialized main chat returns an empty
  ID and zero counts. Conversations counts user rows today; finished counts
  finished rows today. Routine counts each entry in today's status rows plus
  each routine card/report today. Waiting includes pending/opened non-routine
  cards of any age, excluding expiry, with at most 50 IDs, newest first. A digest
  waits only while a non-routine child still needs an answer.
- `ListMainChatWaitingItems(cursor, limit)` returns `sessionId`, `items`,
  `hasMore` and `nextCursor`. Empty cursor starts at the newest waiting cards;
  items within each page are oldest first. Prepend subsequent older pages.
  Cursors are opaque, exclusive time/ID pairs bound to the main session.
  The indexed scan reads only open candidates, at most limit + 1 per call.
  Expired candidates can yield a short/empty page with an advancing cursor;
  continue while `hasMore`. Default limit 50, maximum 200.
  Malformed/cross-session cursors return
  `MAIN_CHAT_WAITING_CURSOR_INVALID` (`*MainChatWaitingCursorError` in Go).
- `SetMainChatHideRoutine(hide)` persists only the computer's view preference,
  off by default, in `ui_state.main_chat_hide_routine`.

`StreamItemData` adds `rowClass`, `topicIds` and `localDay` only on main-session
rows in the ordinary page, waiting page and topic-page messages. Empty topic
lists are omitted. Row classes are `conversation`, `waiting`, `finished`,
`routine` or `other`; `mainChatRowClass` is the shared Go rule for phone C3.
All API timestamps and expiry values are Unix milliseconds. Saved cards without
an expiry field use their original engagement moment's deadline at read time.

Routine comprises status rows, brief-ready/capture-paused cards and schedule API
change reports. Both API reports and chat confirmations carry an explicit
`routine` marker; confirmations remain visible. An unmarked legacy creation
is unambiguously an API report. Other unmarked legacy statuses stay visible
because API changes and chat confirmations cannot be distinguished safely.
Fold by `localDay` in the UI, retain all constituent rows, and apply
`hideRoutine` in presentation.

System-row topic tags use only the row's own currently readable original
Activity/goal/person evidence or target. Matching is deterministic, with 1-2
work topics or none; status rows never receive tags. Writes and reads reuse
A3's source gates and publication/revision fences. One source collection is
shared across a page/backfill batch, cached only while the checked source
revision, withdrawal epoch and time window match. A failed optional tag
projection returns ordinary/waiting chat rows without system tags; it never
fails the main chat read or removes explicit user/assistant membership. A tag
remains stored across temporary denial but is withheld on read until its
evidence is readable again.
Closed Eyes withholds system tags; explicit user/assistant A3 tags remain.
Topic pages omit system messages with withdrawn membership; empty pages can
still have `hasMore` and an advancing continuation through hidden rows.

After startup, a cancellable local registry readiness check retries at most
14 times, one minute apart. A single-flight backfill scans at most 500 main-chat
proactive/report rows from the last 14 local calendar days. Successful completion
publishes the whole batch in one checked transaction and sets
`ui_state.main_chat_topic_backfill_v1`; cancellation/failure leaves it
retryable on a later startup. Logs contain counts only. No model or search runs.

Both locales repair the obsolete capture-paused action-label headline during
read projection. Waiting-on-reply cards gain `not_now`, labeled from the existing
English/Chinese catalogs, including saved cards. It changes the card to
`deferred`, with no send/open action, and removes it from waiting.
