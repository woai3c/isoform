# Isoform context for Claude Code and Codex

Isoform offers nine read-only MCP tools through its bundled `yansu` CLI:

```text
yansu mcp serve --client claude-code --data-dir /absolute/path/to/app-data
yansu mcp serve --client codex --data-dir /absolute/path/to/app-data
```

Open Settings > Assistant > Connect Isoform to Claude Code / Codex. Each row
shows the exact registration command for this installation. Press Connect to
issue that assistant's read grant and register the command. Viewing Settings
does neither. The assistant CLI and the bundled or installed `yansu` CLI must
be available. The desktop app must remain running for data reads.

The registration uses `claude mcp add --transport stdio --scope user isoform --`
or `codex mcp add isoform --`, followed by the executable and arguments above.
No token is placed in command arguments or assistant configuration. An existing
matching registration is reused on reconnect. Revoke removes the read token
immediately; it leaves the assistant's registration in place for reconnecting.
The card distinguishes registration status from the presence of a grant so a
grant can still be revoked after an assistant is removed. Clicking Connect can
replace a stale Claude Code entry written by Isoform (for example, after moving
the app). An unrelated Claude Code server named `isoform` is left intact and
shown as a name conflict. Missing assistant and missing Isoform commands have
different messages. Name conflicts disable Connect while Revoke stays available
for an existing grant; operation errors remain visible beside the conflict.
Registration, startup installation and install discovery share the same
executable-relative bundle lookup. They never copy or publish a bundle found
relative to the app working directory. Registration can also use the installed
CLI or an absolute PATH result. Both Isoform and assistant CLI discovery reject
relative PATH results, including `exec.ErrDot` candidates from the working
directory, even when that error is disabled through `GODEBUG`.

## Read contract

| Tool | Inputs | Returned data |
| --- | --- | --- |
| `activity_search` | `query`, optional `from`, `to`, `limit` | Short local OCR, window-title and accessibility snippets |
| `memory_search` | `query`, optional `limit` | Short memory snippets with revalidated provenance |
| `memory_list` | Optional `kind`, `limit` | Recent memory snippets by existing memory dimension |
| `people_lookup` | `name` | Matching names and source identifiers; no messages or notes |
| `calendar_today` | Optional `limit`, `offset` | Today's local calendar event titles and times |
| `calendar_range` | `from`, `to`, optional `limit`, `offset` | Local calendar event titles and times in the interval |
| `brief_today` | None | Current source snippets for items in today's stored brief |
| `playbook_status` | None | Playbook title, status and schedule-enabled state |
| `run_history` | Optional `limit` | Recent scheduled-run identifiers, times and status |

Results contain `items`, `truncated`, `withheld` and `bounded`. `bounded` is false
when a result or work cap can leave a partial answer; `truncated` is then true.
Search tools match first, then validate only bounded candidates. Searches and
kind-filtered memory lists suppress filter-dependent withheld and privacy-budget
omission counts so hidden text cannot be probed through those counts. Searches
conservatively mark source sets
that may exceed privacy work limits as partial using query-independent metadata,
even when the returned matches fit within the cap. All memory lists, including
unfiltered lists, use the same store-wide preflight as memory search. Repeated
references count once per distinct session, matching the request cache. Legacy
revision history can also produce a conservative partial indication without
reading unrelated revisions or consuming their source-check budgets.
Other reads count privacy rejections as `withheld` and work-budget omissions as
`omitted`. Code-owned `reason` messages explain unavailable sources, empty
answers, disabled calendar permission and truncation. Every item has a code-owned
source kind, source identifier and Unix millisecond time, plus RFC3339
`at_local`/`end_at_local` values with the local UTC offset. There is no ambiguous
`Local` timezone-name field; use the offsets in each timestamp. Stored strings, including
identifiers and statuses, use `{ "text": "...", "untrusted": true }`. The MCP
transport additionally labels the result `untrusted_observation`. These are
observations, never instructions, durable facts inferred from predictions, or
permission to act.

Results are capped at 20 records and text fields at 320 Unicode characters.
Date inputs accept RFC3339 or `YYYY-MM-DD`. RFC3339 ends are exclusive; a
date-only end includes that entire local day, including daylight-saving changes.
Same-day date ranges are valid. Intervals span at most 31 local calendar days.
Calendar results include the total readable events, omitted count and
`next_offset`; pass it as `offset` to continue while retaining the same range.
Each page is capped at 20. A next offset is provided only when another readable
page is known to exist. Requery if the calendar or privacy settings change
during paging. Activity search defaults to the past seven days. Bounded scans can
omit older matches, so an empty result is not proof that something never
happened. Activity and people search select at most 100 matching candidates;
privacy checks are shared within each call and capped at 1,000 sessions and
100,000 decoded events. There is no cross-call privacy cache. Run history uses a
metadata-only query, so large run summaries are never loaded for that tool.
Transcript bodies, recordings, screenshots, run prompts, logs and
result bodies are not returned. Reads never start generation, embedding,
memory synchronization, calendar polling, or a playbook run.

## Transport and authorization

`yansu mcp serve` is a thin stdio process using `mark3labs/mcp-go`, the SDK
already used by the desktop app. It sends only the allowlisted read calls to
the running app's read handler at `/api/mcp/read` over a private Unix domain
socket. The host bridge owns its lifecycle; the route is not exposed on its TCP
listener. The CLI does not open Isoform databases. Initialization and tool discovery work
without the desktop; data calls then report that Isoform is not running.

The app publishes a per-launch socket basename in `mcp-read/endpoint.json` and
issues separate random 256-bit tokens in `mcp-read/claude-code.token` and
`mcp-read/codex.token` under its data folder. The directory is mode `0700`;
files are written atomically with mode `0600`; the socket is also mode `0600`.
Old discovery is removed at startup before the fresh listener is published.
Legacy TCP discovery is rejected, so a reused port cannot receive a token.
The bridge reloads credentials
for each call. The server validates the assistant identity and bearer token
on every data request, and again before returning data. Tokens cannot access
the host's action/media routes or browser RPC and are unrelated to cron auth.
Revoking one assistant leaves the other assistant's grant intact. Reconnecting
after revocation creates a fresh token.

Socket transport, Host, Origin, method and JSON checks apply to the route.
Browser origins are refused. The stdio process rejects socket paths outside the
private directory, symlinked credential/socket files and unsafe permissions.
It never dials TCP and rejects redirects and proxy routing.
Native Settings is the only registration/issuance surface; neither the browser
RPC allowlist nor MCP exposes those operations.

## Privacy and conservative omissions

The app checks current privacy state before each read and before returning it.
Closed eyes, an active observation pause, or disabled screen/accessibility
observation returns `paused`. Calendar permission also gates calendar data.
Every observation text field is PII/code-masked before truncation.
MCP composes the same read-time memory and activity privacy filters used by chat
with its stricter external-sharing checks and bounded source traversal. A local
Memory-page exemption never authorizes an MCP read. Explicit `read_blocked`
provenance and corrected learning sources remain excluded.

Messaging/mail sources (including RingCentral names and bundle identifiers),
protected sessions, built-in sensitive apps, user-hidden
apps, the user's
don't-listen list, private windows and historical pause intervals are checked
on the server. Derived memories, people, playbooks and brief items must have
source evidence that passes the same checks. Unknown, missing or malformed
capture provenance is withheld, including browser events whose private-window
title was stripped. User-entered/imported records are handled by their explicit
source type. Aggregate brief narratives and derived records lacking sufficient
source links are deliberately omitted. A retained memory or playbook can outlive
raw activity only when its retained privacy receipt proves the required capture
and snapshot checks and passes today's policy. Such receipts never authorize raw
activity or person-sighting reads. Retention and explicit deletion use feat's
single transactional deletion path; explicit deletion preserves a denial.

For today's brief, stored item references select current, revalidated source
records. Supported sources include activity/meetings, memory, people, calendar
events/day counts, one-time work, Purpose, learning, deliverables, schedule runs,
files, recap, news and topic finds. Aggregate prose remains withheld; activity
meeting labels are included after source checks. Unknown or unresolvable
provenance contributes to `withheld`. Retired Purpose product-command evidence
is unsupported and withheld; Purpose references with resolvable source evidence
retain their existing checks. News/finds require their retained research
record and current source permissions. Personalized news without source lineage,
legacy finds without frozen topic evidence, and learning that depends on an
unverifiable work profile stay private. Deliverables and runs expose minimal
labels/status, not
file bodies, prompts or result logs. Each brief item has its own reference and
depth budget; one complex item cannot spend another item's traversal budget.
Completed person and calendar-day reads are reused across items within the same
request. Definitive person denials are also reused. Composite references,
depth/budget failures, storage errors, canceled reads and partial aggregates are never shared,
and no memo survives into another request. Reusing a composite reference deeper
within one item revalidates its descendants against the remaining depth budget.
Tracked traversal and provenance work limits are reported as truncation. Existing
oversized source files remain unresolvable and withheld. Reads never generate a
replacement brief.

Memory discovery stats all files and scans newest-first, with bounded content
reads and explicit truncation. List, search, brief references and generation
comparison context recheck the active memory store's 30-day forget suppression,
including same-ID restored files, renamed observed copies and directional
`merged_from` descendants. Feat's scoped-forgetting contract exempts independent
user-authored aliases from content-only matching. Unreadable suppression state
fails closed. Memory searches match before spending revision
history or source-check budgets; exhausted budgets are explicitly partial.
Generator, Memory-page, Dream and learning
writers retain code-owned provenance. Chat and activity generation retain their
actual trigger source; updates also preserve the selected target's lineage and
app dependencies. GUI saves retain `source=user_edit`, previous and explicitly
claimed session/app dependencies independently of `read_provenance`. Following
the shared memory-edit contract, an explicit replacement defines its own
`source_ids` observation attribution and `merged_from`/`merged_from_refs`
directional dependencies; earlier versions retain their original forgetting
links in history. Retaining a directional link in the replacement retains that
forgetting dependency. Observed writers and Dream cannot overwrite or archive a user
edit. User ownership never clears a withheld source.

Dedup selection scores each bounded snapshot's full title and body against the
current source before privacy validation and prompt admission, with recency as
the stable fallback. Prompt formatting still keeps only the first three points;
a relevant later point can select its memory without expanding prompt text.
Every admitted title, identifier and point passes current privacy checks and
retains its captured dependencies. Comparison context admits at most 256 unique
session/app dependencies and a 32 KiB provenance envelope; an entry that would
exceed either bound is skipped whole. Writers never discard an admitted source's
dependencies. The current observation can add its own dependencies; an oversized
result is ineligible as comparison context for the next generation. Every
provenance merge deduplicates its kind, session and app lists.

For upgrade compatibility, valid direct-source legacy memories may enter dedup
without a `prompt_safe` marker. All kinds in a historical union must be recognized
direct sources: manual, native conversation version 2, or activity/learning with
session references. Unknown, connector, withheld and version 1 conversation
lineage remain excluded. This compatibility rule does not certify historical
comparison inputs. Activity/learning lineage without any nonblank session ID
is invalid before merging, so it cannot borrow another source's evidence.
The completeness marker is retained only when every input
had complete lineage or genuine manual-only provenance. Model JSON cannot grant
it, and an edit, update or Dream merge cannot repair missing historical lineage.
A failed or paused privacy read admits no comparison text and logs one
content-free reason for that load.

If an update target disappears or cannot be read, existing fallback creation
keeps the merged output private because that selected source cannot be verified.
Correction logs remain available to the local deterministic rejection checks.
Generation prompts receive fixed attribution guidance instead of logged titles,
reasons or principal names, and initiative guidance contains no stored prose.
An old correction therefore does not withhold all future native memories.
Unused raw-initiative and auxiliary-provenance helpers are removed; model-authored
body metadata cannot override provenance. Corrected native-conversation writers use
provenance version 2; explicit manual memories remain readable. Connector triggers retain their real
transport source, including Telegram and WeChat. Old version 1 conversation
markers and unmarked conversation memories cannot distinguish those connector
threads from native chats, so they remain withheld, including through later edits
or Dream synthesis. No automatic upgrade grants them access. There is no
connector-shareability control in this version; pairing or model-authored flags
do not grant MCP sharing. Activity dependencies must still pass every gate, and
connector/unknown inputs remain withheld. Activity updates preserve earlier dependencies. Dream captures
dependencies from all inputs before model execution; archive/restore retains
that frozen provenance rather than depending on history files that were removed.
Frequent people are checked across paged sightings rather than dropped merely
because they appear in more than 200 sessions.

No write tools or assistant-first execution routing ship in this change.
This is the context boundary those later changes can use.

## Verification

Backend tests use real SQLite stores in short temporary homes and the real
HTTP route. A child process runs the production stdio implementation for
`initialize`, `tools/list`, `tools/call`, revocation, and closed-app checks.
Privacy tests seed actual stores and cover each gate, downstream provenance,
PII masking, caps and read-only behavior. The ten-million-event sparse-search
fixture skips under `go test -short`; full runs assert bounded session/event
checks and use a generous context timeout only as a hang guard, without a
wall-clock performance threshold. Installed Claude Code and Codex CLIs
are exercised using temporary home/config directories only; tests skip when a
CLI is absent. Settings tests cover click-only registration, stale-entry replacement, conflicts,
status, revocation, missing-command reasons, simultaneous conflict/action errors,
same-tick reentrant registration, standalone visibility and Chinese copy. Validation errors use HTTP 422 and preserve code-owned
messages in MCP tool errors; paused responses explain which controls to check.

Registration syntax was checked against the installed CLIs and the official
[Codex MCP documentation](https://developers.openai.com/codex/mcp/) and
[Claude Code MCP documentation](https://code.claude.com/docs/en/mcp).
