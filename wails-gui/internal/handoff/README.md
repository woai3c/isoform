# One-time work store

This standard-library-only package persists Isoform one-time work under the
configured handoff directory. The app supplies the directory; this package never
resolves HOME or touches a global app-data location on its own.

## Persistence

Each record is `<id>.json` with `schemaVersion: 3`. The brief remains markdown in
`body`; criteria, lifecycle state, attempts, structured results and verification
are separate fields. Files are private (`0600`) and written through a unique
private temporary file followed by an atomic rename. The directory is created
with `0700` permissions. IDs cannot address files outside that directory.

The reader accepts older JSON with absent optional fields and ignores unknown
fields. Unknown statuses remain readable but cannot be started or mutated.
Records from a newer schema remain readable and cannot be overwritten or aged
out by an older app version. Malformed JSON returns an error from `Get`, remains
on disk for repair, and is skipped by `List` without hiding healthy records.

Legacy `<id>.md` frontmatter records are read without rewriting them. Their first
explicit mutation writes JSON and retains the original markdown as a recovery
copy. JSON always takes precedence, including when damaged, so an older active
record cannot replace a newer state. Listing deduplicates the two formats, and
`Delete` removes both. Files with missing IDs use their filename as identity.

Success criteria are read from the brief's acceptance/success criteria section
when the explicit criteria list is empty. The store never invents criteria.

## Lifecycle

| State | Product label | Allowed next states |
| --- | --- | --- |
| `active` | Waiting to start | in_progress, aborted, stale |
| `in_progress` | Working | completed, needs_review, blocked, aborted |
| `needs_review` | Needs review | in_progress, aborted |
| `blocked` | Blocked | in_progress, aborted |
| `completed` | Done | terminal |
| `aborted` | Stopped | in_progress (Start again / Start over) |
| `stale` | Expired | in_progress, aborted |

A task never disappears (Tasks K1): an `aborted` entry records `stoppedBy`
(`person` or `system`) and, when the system stopped it, `stopCause`
(`no_assistant`, `signed_out`, `restarted`, `timeout`, `assistant_error`).
`Placement` (board.go) is the one mapping every surface reads: stopped by the
person goes to Done; stopped by the system, `blocked`, `needs_review` and
`active` go to Needs you; `stale` and aborted entries recorded before stops were
tracked go nowhere.

The store also permits `in_progress` to return to `active` or `stale` for an
accept rollback before execution starts. Metadata updates within the same known
state are allowed. Both `Save` replacements and conditional mutations enforce
the transition table. Initial `Save` accepts historical terminal records without
rewriting their outcomes.

Only active records expire at the 24-hour TTL or when a newer record with the
same source ID supersedes them. Needs-review and blocked records wait for the
user. The app owns live-run recovery, activity tracking and turn cancellation;
it uses `blockedReason` values `interrupted` and `timeout`.

`attemptId` identifies one execution attempt. `MutateIfAttempt` checks this ID
and the current state under the store lock, preventing a late result from an old
attempt from completing a newer run. `lastActivityAt` records actual turn
activity separately from metadata changes to `updatedAt`.

`result` retains the final turn's structured claim: status, criteria and evidence
references (paths, URLs or artifact IDs). Criterion evidence indexes are
zero-based. A result is a claim, never authority or independently verified proof.
`verification` records the separate check's met/total counts and review note.
The app's verifier alone decides whether an execution becomes completed or
needs_review.

## API

| API | Purpose |
| --- | --- |
| `NewStore(dir)` | Open/create a private store directory |
| `Save(entry)` | Atomically save and normalize timestamps/criteria |
| `Get(id)` / `List()` | Read one record / all readable records, newest first |
| `UpdateStatus(id, status)` | Apply a permitted lifecycle transition |
| `MutateIfStatus(id, allowed, fn)` | Atomic conditional mutation |
| `MutateIfAttempt(id, attemptID, allowed, fn)` | Atomic attempt-sensitive mutation |
| `Delete(id)` | Delete both formats, idempotently |
| `ActiveForChip()` / `ActiveForChips(limit)` | Query active, blocked and needs-review records |
| `MarkStale(ttl)` | Expire only active records |
| `CanTransition(from, to)` | Inspect the transition table |
| `CriteriaFromBody(body)` | Parse the brief's existing success criteria |
| `Parse(id, markdown)` | Parse legacy/raw markdown without claiming authority |

The Wails integration lives in `handoff_api.go`, `handoff_detect.go` and
`handoff_verify.go`. Main-chat and Upcoming surfaces consume product projections
rather than the raw persistence record.

## Tests

Run `go test ./internal/handoff` from `wails-gui`. Tests use disposable store
directories. Coverage includes all lifecycle state pairs, stale-at-TTL behavior,
legacy migration and deduplication, malformed/future JSON, private file modes,
structured-result round trips, unsafe IDs and attempt-sensitive settlement.
