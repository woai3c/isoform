# RC 1 A3: topic views of the main chat

The topic panel is a projection of the existing main conversation. These APIs never create a session. Opening a panel reads; only `SendMainChatWithTopic` sends. Ordinary chat reads and sends stay available.

## Wails bindings

```ts
SendMainChatWithTopic(text: string, topicId: string): Promise<MainChatTopicSendResult>
ListMainChatTopicItems(topicId: string, cursor: string, limit: number): Promise<MainChatTopicPage>
```

A successful send returns `{ sessionId, userItemId, sortOrder }`. Use the returned main-session ID with existing stream events. The stored user bubble contains only the redacted user words, with dates retained. Topic metadata is attached to the model input through the existing Today admission path, outside the bubble. The caller must not persist a second user row. A missing main session, missing registry/topic, unavailable original source, stale topic source fingerprint, or Closed Eyes rejects before publishing a user row. After admission, source identities and digests are rechecked before/after prompt composition, at dispatch, and before later history reuse. A withdrawal after admission keeps the accepted words in chat and stops model dispatch. Existing already-running/Stop admission behavior is retained.

The page read also validates the registry source fingerprint before using its thread keys.

A page returns:

```ts
{
    sessionId: string;
    topicId: string;
    items: Array<{
        id: string;              // "message:<stream id>" or "event:<Activity id>"
        kind: "message" | "event";
        at: number;              // Unix milliseconds
        actor?: "user";          // event role code, not display copy
        text?: string;           // current, redacted Activity title
        message?: StreamItemData;
    }>;
    hasMore: boolean;
    nextCursor: string;
}
```

An empty cursor reads the latest page. Each page is oldest first; prepend the next (older) page. Pass `nextCursor` unchanged and keep the same topic. A cursor is exclusive and uses time plus prefixed identity to retain ties across both event and message rows. Invalid or cross-topic cursors return `TOPIC_CURSOR_INVALID`. Default limit is 50; the cap is 200. Each call scans at most 200 Activity candidates before its thread join. If more candidates remain, `hasMore` is true and `nextCursor` advances through that scanned range. A page can therefore contain fewer than `limit` items, including zero; continue while `hasMore` is true. Older chat messages wait until the interleaved events have been scanned, so this continuation skips neither kind. Fetch again when privacy/source state changes: pages are live reads, not a retained snapshot.

`message` uses the same storage-to-renderer conversion and App projection as `LoadStreamItemsPage`, including attachment metadata and proactive/engagement policy payloads. Existing direct-inspection chat behavior remains under Closed Eyes; observed events disappear. The panel can use the existing main-chat page read for Show everything. No endpoint writes or prepares a separate chat.

## Registry adapter

`NewApp` installs the A1 registry adapter. `GetTodayTopics` derives source-bound metadata and keys together and commits them to `topics.json` under its publication barrier. The adapter supplies one combined metadata/key snapshot to admission and panel reads; the shared validator reuses one Today source collection and resolution cache. Individual TopicByID/TopicKeys callers also receive current source validation. The adapter only reads during admission or panel reads; it never refreshes or writes while admission owns the barriers. A missing, stale or unsupported snapshot returns an explicit error. Fixture integration tests use the actual adapter with temporary databases/files and the existing capture runner, alongside A3's seam tests.

The Go interface is:

```go
type TopicSource interface {
    TopicByID(ctx context.Context, id string) (ChatTopic, error)
    TopicKeys(ctx context.Context, id string) (ChatTopicKeys, error)
}
```

`ChatTopic` has typed JSON fields `id`, `name`, `status`, `latest`, `next`, `refs` and `sourceFingerprint`. Refs cover every original observed input used to derive metadata. Supported source kinds are memory, knowledge, activity, principal, purpose, find, calendar and interest. Calendar refs resolve through the existing EventKit/permission/source gates. An interest ref binds its stored naming record and user override; it grants no authority to its evidence, whose complete supported original closure is also included as separate refs. Activity fingerprints include original entity/member identity so a key correction invalidates old metadata even when the visible text is unchanged. Topic/brief wrappers cannot recursively grant provenance. An adapter captures `chatTopicSourceFingerprint` **when deriving** the metadata; it must never attach a new fingerprint to an old synthesis at read time. The source digest includes current item facts and detects edits/corrections as well as deletion and access withdrawal. Empty/unproven provenance is unavailable. The adapter refuses contextual sends for legacy phrase topics, unsupported hashed/chat/semantic interest evidence, incomplete original search lineage, and closures exceeding 64 refs. Such following cards keep their existing visibility; no closure is truncated or replaced by an invented reference. Refresh Today after a source change before using contextual sends again. Snapshots store derivation/expiry times and expire at the earliest local midnight, moving-status boundary or upcoming meeting start, so unchanged bytes cannot authorize clock-stale status/next metadata.

`ChatTopicKeys` returns `threadKeys`, `goalIds`, and optional `interestId`. A3 matches canonical `threadSureKeys` (for example `obj:atlas`) and `activity_threads.purpose_key`; goal and interest IDs are retained for the registry/phone integration. Day-local thread IDs are not durable topic keys. A readable member among the bounded 64-member thread lookup lends its keys to its thread; each returned event must separately pass the source gates. Matching is not inferred execution authority.

The adapter participates in `topicsSvc().store.publication`; admission also fences the interest store when reading topic/find/brief sources. The existing topic-store file state is included in the final admission digest. The adapter must keep all file-backed metadata and keys in that protected store. The existing SQLite revision fence covers registry data if it is stored in the main database, and the Activity fence covers thread changes. The final file-validator callback deliberately does not call the interface while the main SQLite transaction owns the connection. The registry fingerprints the exact inputs used for derivation, rechecks source revisions before publication and preserves that fingerprint on adapter reads. Registry collection occurs outside global privacy/memory barriers, with a five-second deadline. Chat enrichment shares the full five-second registry budget and caches the privacy-filtered source collection by checked source revision, for at most one minute or the next clock transition. Incomplete chat derivation returns an error before publication and preserves the complete prior file. Withdrawal clears that cache and invalidates late fills. Unchanged sidebar/list/pin reads share the source collection. Short snapshot/publication phases have a 150 ms budget, branch on the captured Closed Eyes mode before collecting, release all partial locks while waiting, and conservatively return `topic_registry_changed` rather than publishing partial data.

## Privacy and persistence

For an explicit topic turn, the hub has authorized the topic name, status, latest and next as context for the user's chosen model, under the same gates as Today chat. Isoform does not add registry project names or keys to its own news, interest or other outside queries. Explicit topic turns keep the same web tools as Today chat; the chosen model may search what the user asks about. Topic membership is context and grants no execution authority. Closed Eyes refuses the adapter and topic sends. Application Activity Delete (including composer purge/merge), Markdown Delete, Memory Forget and common goal Forget paths synchronously scrub affected derived names, keys, timestamps and saved chat metadata before returning, including under Closed Eyes; independent sidebar choices and content-free reservations remain. Original members independent of the withdrawal can remain as opaque IDs for a later checked rebuild, with no saved name or claim keys. The withdrawal protocol saves a sanitized reservation before irreversible source mutation and excludes registry publication through the operation. Initial save failure prevents deletion; mutation failure restores the prior projection. If finalization/restoration cannot be written, the sanitized file retains only opaque original memberships for a later checked rebuild. A retired reservation never claims new work; a surviving readable original member can preserve an existing project.

Events are derived on every read, with no event-text cache or new durable derivative. They use Today's current model/source gates: Closed Eyes, observation pauses, sensitive apps, private captures, protected/missing source sessions and run provenance. Topic events and their thread titles withhold Mail and Slack text, including mixed-source runs. The general Today context packer retains its existing gates; its ordinary `<activities>` section holds Mail/transcripts but does not add the topic event reader’s Slack holdback. User deletion/Forget of an original capture removes the event even if a derived Activity moment survives. Context uses current readable thread titles; a generated title requires its whole stored title basis to remain readable, otherwise the current moment title is used.

The main read holds Closed Eyes, sensitive-app and source-file publication barriers and verifies both main-store privacy/registry settings and the Activity database revision before returning events. Context admission also verifies the topic source fingerprint and its source files/database revisions before the history transaction commits.

Schema v63 adds `stream_item_topics(stream_item_id, session_id, topic_id, created_at)`, with primary key `(stream_item_id, topic_id)` and cascading row/session foreign keys. The user tag commits in the admission transaction. Before each `run_created` is published, its run ID is attached to the tagged user row. A routed fallback updates that current run ID; previous attempt rows retain their tags, and subsequent fallback rows inherit the same topic. Insert/update triggers inherit the topic for assistant, thinking and tool rows with that run ID in the same transaction as those rows. Ordinary sends have no topic seed, and unrelated runs do not inherit one. The existing chat table stays in place, with no historical topic inference. Run proof inheritance updates `automation_context` on existing same-run rows when needed. Existing bulk saves update rows in place so they preserve topic tags, the admitted run ID and source proof. `automation_context.topicContext` carries versioned source identities and digests for the actual packed context, including related records; it contains no contextual prose. The same run triggers copy that proof to response rows, so deleting the user bubble cannot erase a surviving response's dependency.

The recent window applies only to topic exchanges: at most the last 30 text rows within the last 256 raw rows select the recent topic runs. A selected run keeps its exchange together, including earlier rows of that same run. Older topic exchanges stay visible in chat but are left out of model history without checking their sources. Recent stale or unproven topic exchanges, their run rows and linked context-status records are also omitted. Every ordinary row remains available to the native history loader, including older tools and the compaction summary. The ordinary deferred text-history sample retains feat's last-30-text-row contract. A chat with no topic rows retains the same prompt/history bytes and session identity as feat. Native dispatch is required only for proven topic context in the current prompt or selected history; otherwise the user's assistant route and model remain available.

A valid topic exchange does not clear or change `SessionID`. Ordinary later turns retain the caller's session, so the native runner can keep its existing todo list and plan state. If a recent topic run becomes stale or withdrawn, sends atomically store a replacement executor session identity and a receipt for that run in the existing `ui_state` table. Receipts contain hashed run identities, not source IDs/text. The same withdrawal rotates once, even after restart or when an older renderer/phone still passes the old ID. A new withdrawal can rotate again; merely aging out of the topic window does not rotate. Read-only history projection never rotates a session. A withdrawal detected after runner initialization stops that in-flight request and persists the replacement for the next turn, rather than using the already-selected old todo/plan state. Schema remains 63.

Proof results are cached for one turn and revision pair, shared by pre/post composition, dispatch and native history loading; source files, main/Activity revisions and live privacy policy invalidate the cache. Each validation batch collects sources once and resolves shared references once. A newly admitted topic prompt, or proof-bearing history already handed to an in-flight runner, is still refused if its sources change before dispatch; this does not poison future turns.

Moving a row between sessions removes its tags.

The tag's original creation time is the stable message cursor time, even when an older renderer bulk-saves the row. Deleting/rewinding a stream row cascades its tags. The schema target and compatibility ceiling are both 63. A schema-62 binary refuses this database. Revert by reverting the merge and restoring a verified pre-migration snapshot; never lower the schema stamp manually. Restoring the snapshot discards chat activity written after that snapshot, including topic tags.

Startup compares topic trigger definitions even on a database already stamped v63. Missing/older definitions are replaced transactionally; an existing recorded proof is copied to surviving run rows. Tagged rows with no recoverable original proof get an unproven version marker, so model history omits those exchanges instead of inventing provenance. Healthy reopen does not rescan historical chat.

## Deferred provenance follow-ups

A background shell continuation currently starts a new run without inheriting the originating topic’s tag/proof; the shared continuation-lineage gap is tracked in the backlog. `TopicKeys` are protected by the registry publication barrier but are not themselves part of `sourceFingerprint`; the A1 adapter must associate them with the same derivation revision. Forget withdraws model history and events, but source identities and SHA-256 digests remain in `automation_context` until the chat rows are deleted. This metadata retention is distinct from source-text retention and still needs an explicit cleanup policy.


## Bounded list and page reads

The registry keeps the newest 1024 recent Activities plus the newest 256 existing original historical members, and the newest 64 readable visible goals. Excess data reduces the projection instead of failing the whole topic/sidebar surface. Affected work views carry optional `truncated: true`; following views and Closed Eyes do not gain observed truncation fields. Deferred original membership retains opaque IDs for later validation, without a name or keys that could claim new work. Only checked members enter public keys or contextual proof. Outcome enrichment is also bounded without dropping the topic list.

Topic-panel source collection uses the same bounded historical project closure. Topic-panel reads take a short policy/file/database revision snapshot, release every global barrier, collect bounded sources and scan events, then reacquire the barriers for a short revision recheck and database fence, retaining the fences through bounded response assembly and return. A source change rejects the page rather than publishing old observed content. File revisions are checked again after waiting for database fences. Direct inspection of the user's chat under Closed Eyes remains available, with observed events withheld.


## Final registry privacy fences

Registry and topic-page content hashes are captured outside privacy/memory barriers. Short locked phases compare metadata manifests (device/inode, nanosecond change time, mtime, size and mode), policy/interests and source database revisions; after database waits they recheck metadata. Unsupported stat layouts refuse publication. Direct Markdown chat contributors also retain private derivation-time metadata digests, so a source correction cannot preserve an older cached chat derivative.

Adding or loading a sensitive-app policy synchronously scrubs denied project names/claim keys/timestamps and saved chat metadata, including during Closed Eyes. Cleanup uses the stored-derivative source policy, independent of the live pause. Goal evidence checks original source hashes. Independent readable projects remain. Saved IDs and aliases are unsalted key-derived hashes; opaque memberships and separate pin choices may remain. Scrubbing readable text/claim keys is not cryptographic erasure of these local identifiers.
