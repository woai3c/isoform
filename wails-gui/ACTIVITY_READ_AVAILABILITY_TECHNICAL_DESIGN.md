# Activity Read Availability Technical Design

Status: implemented and rebuilt-live validated on `vision/shadow-agent`

## Purpose

Yansu's Activity database is shared by observation, Shadow mode, Self, Agency,
History, and their read-only Developer tools. A background enrichment job must
not make those product surfaces unavailable.

This contract keeps reads responsive without widening database concurrency or
weakening append-only evidence behavior.

## Production Failure

The Activity store deliberately uses one SQLite connection. The embedding
backfill worker held query rows from that connection while calling code that
needed the same connection again:

```text
BackfillEmbeddings
  -> query candidate sessions
  -> keep rows open
  -> EmbedOCRFrames
       -> query frames
       -> keep rows open
       -> begin write transaction
```

Both nested transitions could wait forever. While the worker owned the only
connection, Activity-backed pages and `GetTaskShadowDebugSnapshot` also waited.

After removing that deadlock, the Shadow aggregate still decoded every frozen
TaskEpisode context and repeatedly loaded full episode details to derive simple
health counters. The live database held 82 episodes and about 22 MB of frozen
context, so a read-only index request could exceed 30 seconds.

The shared Shadow mode page then introduced a separate fanout problem. Opening
one embedded diagnostic always launched the legacy all-in-one Shadow snapshot,
and every relevant event launched another request. That snapshot expanded each
of 58 Shadow runs into its full observation, evaluation, and evidence graph.
Work patterns similarly decoded 55.42 MB of canonical profile JSON
across 83 snapshots merely to construct list-row counts. Because the Activity
store intentionally has one connection, these read-only requests serialized
every other Activity-backed page behind work the visible view did not need.

While a request was queued, Tasks Yansu noticed rendered an initialized empty object
as `0` tasks and `-` supported workflows. Those values looked authoritative even
though no backend response had arrived.

## Design

### 1. Release rows before nested database work

Every query that precedes another query or transaction must fully scan and
close its rows first. `EmbedOCRFrames` and `BackfillEmbeddings` therefore use
short ownership closures:

```text
query -> scan bounded values -> check rows error -> close rows
      -> embedding work -> transaction
```

Context cancellation propagates through query, transaction, prepare, and exec
operations. Shutdown cancellation is not logged as a production failure.

### 2. Probe the small indexed side

Backfill candidate selection begins with `activity_sessions` and probes
`activity_ocr_frames` through `idx_ocr_frames_session`. It does not scan every
OCR frame or read existing embedding payload pages to discover three missing
sessions.

The query remains deterministic and bounded to three oldest eligible sessions.
Protected sessions, empty OCR, and already embedded frames remain excluded.

### 3. Keep aggregate reads aggregate

The Shadow debugger index derives lifecycle health from narrow relational
facts:

- current status;
- inferred, detected, evidence, and transition timestamps;
- current task and desired-result hypotheses;
- result-role evidence only; and
- the latest linked task-mirror run.

It does not decode frozen context, hypothesis history, links, or raw evidence
payloads merely to compute global counters. Full immutable context remains
available through the selected episode's detail endpoint.

Pair readiness likewise loads current episode status and result-role evidence
instead of the complete TaskEpisode graph. This preserves comparison semantics
while keeping the list page interactive.

### 4. Load only the selected diagnostic

The Shadow mode container defaults to Tasks Yansu noticed and does not request the
legacy summary until a summary-backed view is selected. Event refreshes are
subscribed only for the active view, and concurrent refresh signals coalesce
onto one in-flight promise. Switching away prevents stale result selection or
detail refresh from issuing hidden work.

The Tasks Yansu noticed client has three explicit states:

```text
loading -> valid data (including a valid empty list)
        -> unavailable (timeout, null, malformed, or rejected response)
```

It never substitutes an empty snapshot for a missing response. Search, filters,
metrics, and empty-state claims remain unavailable until the response passes
its generated-at, limit, workflow, and episode-array contract.

### 5. Keep list rows narrow and details complete

The private-attempt list reads one bounded relational summary query. It decodes
only the prediction fields displayed in the row and derives observation and
evaluation status from grouped counts. Full immutable run graphs remain behind
the selected detail endpoint.

The observed-work-pattern list reads stored scalar columns and SQL JSON array
lengths for projection and exclusion counts. It does not transfer or decode the
55.42 MB canonical profile corpus. Evidence, correction, consumer, and ablation
counts are grouped in one bounded query; opening a profile still returns its
complete canonical snapshot, provenance, frozen consumers, paired evaluation,
and exact model attempts.

## Invariants

1. The Activity store remains single-connection unless a separate reviewed
   migration changes that policy.
2. No open `sql.Rows` may cross into code that queries or starts a transaction
   on the same store.
3. Background work is bounded, cancelable, and lower priority than interactive
   reads.
4. Aggregate Developer tools APIs return bounded summaries and never include
   raw frozen context.
5. Detail APIs remain read-only, provenance-complete, and available on demand.
6. Performance changes do not rewrite TaskEpisodes, Shadow runs, learning,
   authority grants, or authority decisions.
7. A timeout, null response, or malformed response is an unavailable state,
   never evidence that there are zero tasks, attempts, or results.
8. An inactive Developer tools view must not subscribe to or issue its heavy
   summary request.

## Verification Contract

- a production-like one-connection regression must prove candidate rows are
  released before embedding starts;
- protected, empty, and already embedded fixtures must remain untouched;
- lifecycle, pair-readiness, bounded-debugger, and side-effect-free debugger
  tests must pass;
- race, complete Go, vet, frontend, and native rebuild checks must pass;
- the rebuilt aggregate endpoint must return within an interactive budget on
  the retained production database; and
- authority table hashes must remain unchanged before and after live reads.

## Rebuilt Live Result

On the retained 2.5 GB Activity database, repeated HTTP 200 Browser RPC reads
completed in approximately:

- `GetTaskShadowDebugSnapshot(200)`: 0.70 seconds for 82 episodes;
- `GetShadowAgentDebugSnapshot(200)`: 0.04 seconds for 58 runs; and
- `GetWorkPatternProfileDebugSnapshot(200)`: 0.37 seconds for 83 profiles.

After the reads, pprof showed 68 goroutines and no queued stack for any of the
three diagnostic methods. The combined authority, ambient-authority, and source
grant ledger fingerprint remained byte-identical before and after all three
requests.

## Non-Goals

- Increasing SQLite connection count to hide row ownership defects.
- Deleting or truncating frozen evidence to make the debugger faster.
- Caching mutable health values without provenance or invalidation.
- Turning Developer tools into a replay or mutation surface.

## Task 222: Bounded Retained-History Reads

The first compact Task index removed full TaskEpisode detail loading, but a
repeatable rebuilt-live audit found three retained-history costs still hidden
inside `GetTaskShadowDebugSnapshot(200)`:

- every frozen context manifest was reparsed to count included and excluded
  entries and prove pre-onset activity context;
- every historical task-mirror run was decoded as a complete immutable graph
  merely to classify its prompt generation; and
- every historical latest run rebuilt pair readiness even though an old prompt
  generation is always retained history.

On the retained database, 82 manifests contained 55,006 entries and 17.17 MB
of JSON. The embedded SQLite JSON implementation repeatedly parsed those
objects, and the read took 20.46 to 21.30 seconds despite returning only narrow
summary fields.

Task 222 adds `task_context_manifest_stats_v1`, an immutable relational read
model containing:

- the Task Episode and frozen-context identities;
- a SHA-256 of the exact stored manifest JSON;
- total, included, and excluded entry counts; and
- the count of included activity snapshots observed no later than inferred
  task onset.

Existing manifests are decoded once during migration and backfilled in the
same transaction that installs immutability triggers. New Task Episodes write
the frozen context and its derived stats atomically. A missing stats row is an
error, not a synthetic zero. Aggregate diagnostics query only these scalar
rows; selected detail and current-generation recovery continue to load and
validate the original immutable context.

Continuation now scans only run ID, episode ID, workflow, prompt version, and
creation time. Historical generations are counted from those immutable
headers. A current-generation row must still load through `GetShadowRun`, pass
the complete immutable JSON/hash contract, and exactly match the scanned
header before any recovery action can be derived. Historical pair summaries
report the same deterministic `historical_generation` state without loading
observations, evidence, or evaluations.

The rebuilt production migration produced 82 stats rows for 82 contexts with
zero gaps. Repeated Browser RPC reads returned all 82 episodes, both enabled
workflows, the honest `desktop_locked` release stage, and all 51 historical
runs. The first warm read completed in 0.15 seconds; repeated reads completed
in 1.97, 0.33, and 0.23 seconds, where the 1.97-second sample included shared
startup/background contention. A post-change CPU profile under repeated reads
contained no Task snapshot manifest parsing or historical ShadowRun graph
loading. The combined authority and permission ledger fingerprint remained
`3ac84a4bbb0bcfc9f041180fff646667d186738fed8489b2e028ca94a08abf76`
before and after the reads.
