# RC 2 local search API (C4)

`SearchEverything(query, limit)` is a read over current local stores. It does
not index content, create directories, persist queries or recents, or call an
embedding provider. The frontend retains actions, Settings, Library pages,
automations, handoffs and projects as its existing sources.

The query is trimmed, case-insensitive and split on whitespace; every word
must match. It accepts 2 to 200 Unicode characters, or one Han, Hiragana,
Katakana or Hangul character. Invalid/empty queries return empty groups. `%`,
`_` and backslash are literal characters. The limit is per group, defaults to
5 and caps at 20. Each source selects at most 50 matching candidates before
ranking, with its own 400 ms work budget. Results rank by exact title, title
prefix, title substring, then body; ties use newest time, then identity. Chat
title destinations are selected before message candidates within that cap, so
newer message hits cannot displace an old side chat title.

The response has `query`, `groups: [{place, results}]` and `failedPlaces`.
Successful groups may be empty. Failed or timed-out groups are omitted and
named in `failedPlaces`; a caller can retain its other results. A concurrent
visibility/privacy/database change can also withdraw affected groups. A
caller should run a fresh search after such a change.

Each result has `id`, `place`, `title`, a redacted `snippet` of at most 140
characters, `at` in Unix milliseconds, optional `topicId`/`topicName`, and a
discriminated `target`. The shim declares the TypeScript destination union.
Go sends stored facts and stable kind codes; the frontend writes localized
place labels, dates and meta lines in English and Simplified Chinese.

| Place | Target kind | Destination fields |
| --- | --- | --- |
| chat | chat | sessionId |
| chat | chat_item | sessionId, itemId |
| activity | activity_moment | activityId, sessionId, at |
| activity | activity_thread | threadId, startMs, endMs |
| person | person | id |
| goal | goal | id |
| know_how | knowledge | id |
| about_you | memory | id |
| topic | topic | id |

`FindStreamItemCursor(sessionLocalID, itemID)` returns an exclusive sort-order
bound. Call `LoadStreamItemsPage(sessionLocalID, cursor, limit)` with a positive
limit; its page contains that item, including with sparse sort orders and a
limit of one. The item is the newest entry of that page. A missing item,
wrong session, or hidden destination returns `SEARCH_STREAM_ITEM_NOT_FOUND`
(`*StreamItemNotFoundError` in Go). The read never prepares or sends a turn.

Privacy and source rules:

- Chat results retain direct inspection under Closed Eyes. Hidden sessions,
  projects and workspaces are excluded. User, assistant and system rows with
  an empty `tool_name` are matched; tool/result/thinking rows and rows carrying
  a tool name do not participate. A visible system command string without a
  tool name can match, subject to redaction.
- Activity matches corrected display titles/summaries and generated summaries,
  plus thread titles. Original wording overridden by a correction is excluded.
  All source runs/sessions must remain readable through the shared MCP gates,
  including protected sessions, sensitive apps, private windows, missing
  provenance and observation pauses. A generated thread title requires every
  recorded title input and current member to remain readable. No Activity
  results appear under Closed Eyes. Raw OCR, accessibility, typed/clipboard
  text and event JSON never participate in matching.
- People match display names, aliases, relationship and display handles.
  User-entered/imported names, aliases, relationships and direct handles retain
  the page's direct-inspection policy, including under Closed Eyes. Import
  ownership in `contact_handle_provenance` stays direct even when linked into
  an observed profile. A handle on a curated contact without same-app capture
  evidence is also direct. Capture evidence joins app sightings to overlapping
  session lineage; neither a missing import row nor a sessionless counter
  proves capture. Non-imported handles backed by this evidence retain Closed
  Eyes, pause, app and sighting/session gates even after a merge into a contact,
  plus observed-data
  publication checks. Direct-field snippets omit observed handles. People
  matches its permitted raw identity fields so a real email address remains
  findable, then redacts returned titles and snippets. Other places still
  redact before matching. Captured identities retain the shared person/session
  gates. Message previews, notes and research never participate in matching.
- Goals use the Goals page's projection and visibility rule. Hidden/dismissed
  goals are excluded. Search includes title, outcome, waiting condition and
  due date.
- Memory and Knowledge use the page file parsers without creating their
  directories. Titles and bodies are matched without frontmatter. Memory
  tombstones and learning-correction suppression apply. Files are checked
  again before returning; a changed, deleted or newly forgotten entry is
  omitted. These owner-managed pages retain direct inspection under Closed
  Eyes. Symlinks and files larger than 1 MiB are omitted.

A1 and C1 are absent at the implementation base. `SearchTopicNames` is the
small registry seam: an A1 adapter on `App.topicSource` must return only current
readable names and mark followed topics. Production therefore returns an empty
topic group and no Activity topic metadata. Under Closed Eyes only followed
names pass the seam. A3's explicit user/assistant topic IDs are returned on chat
items; system-row IDs are withheld until C1 supplies its evidence read rule.
No historical topic assignment is inferred.

Bindings/models are generated into the existing ignored `frontend/wailsjs`
directory. The committed shim exports, destination types, Spotlight stubs and
read-only browser bridge entries cover the new methods. No screen behavior or
locale text changes in C4.
