# RC 1 A6: Activity thread API

`GetDayThreads(startMs, endMs)` retains its existing day selection, thread IDs,
`title`, `kind`, `userTitled`, `startMs`, `endMs`, `timeSpentMs`, `activityIds`,
and ordering. Membership and grouping rules are unchanged. The Activity UI can
continue to use its existing list for As recorded.

The following fields are additive on each `ActivityThreadView`:

| Field | Contract |
| --- | --- |
| `stretches` | Array in time order, always present. A maximal run of this thread's moments in the day's merged, chronological thread-moment sequence. Only another thread's moment breaks a stretch; elapsed time alone never does. |
| `stretches[].start`, `end` | Unix milliseconds, first start and maximum moment end in the stretch. |
| `stretches[].activityIds` | Member IDs in time order. |
| `stretches[].activeMs` | Sum of the existing per-moment active-time allocation. |
| `stretches[].resumePoint` | `null` or `{kind, label}` from the last moment of the stretch. Specificity: `pr`, `path`, `figure`, `repo`. For equal specificity, newest evidence wins, then label order. Source privacy, evidence exclusion and redaction checks apply on read. |
| `gaps` | Array between consecutive stretches, always present. |
| `gaps[].start`, `end` | Previous stretch's end and next stretch's start, Unix milliseconds. Overlapping bounds produce a zero-length gap at the next start. |
| `gaps[].minutes` | Nonnegative whole minutes, rounded down. |
| `gaps[].label`, `threadId` | Title and ID of the other readable thread occupying the most of the gap, or the dominant app and empty ID. Thread occupancy unions overlapping member spans. Ungrouped moments supply app labels, with their clipped duration apportioned by their app counters and overlapping app spans credited once at the maximum observed share. Ties use sorted thread IDs/app names, with threads winning equal app occupancy. Fallback: `label: "away"`, `threadId: ""`; the UI translates the `away` sentinel. |
| `activeMs` | Sum of `activityMomentTimeMs` for visible members; equals the existing `timeSpentMs`. No new time accounting or day-total changes. Unassigned time remains unassigned. |
| `linkReason.kind` | Latest visible member's source: `rule`, `model`, or `user`. User confirmation changes only the separate confirmation state, not model review or the original reason source. |
| `linkReason.keys` | Array, always present. For rule provenance, distinct stored rule keys in member order: `obj:` becomes `file:`; `pr:`, `person:`, `channel:`, `site:` retain their prefix and value. Unknown keys are omitted. Empty for model/user provenance. |
| `linkReason.text` | Redacted model reason as stored for the latest visible membership, while its complete readable day context still matches; empty for rule/user provenance, changed source context, Purpose-derived inputs, or legacy unprovenanced reasons. UI authors all explanation sentences. |
| `linkReason.resumedWithinMinutes` | Omitted for one stretch; otherwise the smallest gap in whole minutes. This explains resumption and never changes grouping. |
| `interruption` | All visible moments lie inside another thread's gap and summed active time is strictly below 30 minutes. |
| `reviewed` | User confirmation from `activity_threads.user_confirmed`, default false. Model review never sets it. Adding, removing or moving members clears it. The existing membership `reviewed` flag remains internal to model grouping. |
| `unfinished` | Latest visible moment has a currently readable outcome with an unfinished state, using the existing outcome classifier. Missing, stale or withheld outcomes yield false. |

The cache signature reads only database revision metadata, privacy revision,
observation pause state and sensitive-app settings. Activity metadata is read
through the read-only pool, including the time-share and mail-repair lookups.
Its SQLite `data_version` is paired with the physical reader identity: a change
or reopened reader invalidates the stamp, including source edits and outcome
writes without timestamp updates. The main-store revision remains a separate
stamp. Same-connection `total_changes` also covers the in-memory/migration
reader fallback. Reader leases are released before assembling the day, and never consume
capture's writer slot. A warm read uses one Activity revision/metadata query and the existing day-state
query, plus constant main-store revision/pause queries. It loads no capture or
mail content. The metadata query also detects day sessions with transcript or
audio paths, including denied sources. Such days bypass cache reuse: file
headers and recording sidecars can withdraw access without a database write.
The existing 60-second TTL still bounds reuse for other days. The signature
includes the full pause log and current outcome pause state. Any open pause
withholds derived outcomes immediately, even if clock skew places its recorded
start in the future; ended intervals retain their normal time bounds. Closing
the pause rechecks eligibility immediately.

On a cache miss, the original thread/list read and time allocation supply the
base view. Two day-wide queries load run evidence and session revision metadata;
privacy decisions reuse cached source facts only after checking their current
revision, declared metadata and the current Closed Eyes, private-source,
sensitive-app and pause policies. Transcript headers/sidecars are checked once
per batched session; dependencies outside the batch conservatively withhold
resume/outcome/model evidence while preserving the base view. Gap app counters
are parsed once per day. When outcomes exist, two additional day-wide queries
rebuild their bounded snapshot/typed-event tails with exact OCR matching and the
ordinary outcome validator. Both tail queries use the exact captured run
bounds/session IDs already authorized, so a concurrent live-run change cannot
expand their scope. Mail holdbacks are loaded once for that operation.
The added SQL count is independent of the number of moments, runs or stretches.
A stretch projection read failure is returned through `GetDayThreads` as an
error. No partial or empty successful day is published or cached. A subsequent
read retries immediately; recovery does not wait for the cache TTL.

Named Activity migration `activity_thread_link_state` adds `user_confirmed`
and `reason_basis` to the existing thread table. It preserves all legacy titles,
membership and model review flags. Three triggers clear user confirmation when
membership is inserted, deleted or moved. Migration replay and partial upgrades
are idempotent; there is no numbered storage-schema change.

New model reasons use a version-2 envelope in the membership reason column:
bounded redacted text and one complete context digest. The provider's title
snapshot hashes, source-context digest and auxiliary input revision are stored
once per thread in `reason_basis`. Old member reasons cannot borrow a newer
thread basis because their complete digest must match it. Auxiliary revisions
cover correction history and main-store inputs conservatively without fetching
them on each read. Purpose can also depend on Memory/Knowledge files, for which
there is no cheap complete revision. Passes containing Purpose goals still use
the same provider grouping/naming inputs, but withhold their model explanation
text. Every generated title, including a retitle of a kept rule join, retains an
unsafe-input marker so a later pass cannot launder those inputs into a new
explanation. Empty threads used by correction examples are checked too. Legacy generated titles
without a proven basis also withhold new explanation text. Changed inputs,
database-connection lifetimes, or unrelated main-store writes can withhold an
old explanation. Legacy version-1/plain reasons remain loadable but
their model text is withheld. No source text is copied into the metadata.

`ConfirmActivityThreadLink(threadId) -> Promise<void>` sets only the thread's
`user_confirmed` flag. It leaves membership `reviewed`, source, reason and all
model grouping decisions untouched. Repeating confirmation does not rewrite
timestamps or emit another update. A changed confirmation invalidates the day
cache and emits `activity:threads-updated`. Missing threads and unavailable
stores return errors through the existing Wails error path.

`RenameActivityThread` keeps its existing contract. Naming examples now include
the current user title from rename corrections, deduplicated by thread and only
when the thread still has a readable member in the naming pass. Clearing a title
removes it from rename examples. Rename examples appear only in a naming block, explicitly excluded from membership, kind and merge decisions. Move/own examples retain their grouping block. No renamed title is copied into a new store.

Bindings are generated into the repository's ignored `frontend/wailsjs/` tree.
The checked-in runtime contract is the explicit `ConfirmActivityThreadLink`
export and typed read model in `frontend/src/lib/wails-shim-app.ts`, with the
matching Quick Chat Spotlight stub.
