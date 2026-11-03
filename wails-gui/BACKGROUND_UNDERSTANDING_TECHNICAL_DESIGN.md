# Background Understanding Technical Design

Status: implementation contract for `vision/shadow-agent`

## Product Role

Background Understanding is the low-priority fallback used when Yansu cannot
advance its primary production work or cannot observe a user task safely.

It is not:

- an Initiative, because it does not independently pursue a user outcome;
- a ShadowRun, because it does not mirror a currently observed user task;
- an authority grant, because permission to inspect a source never authorizes
  mutation of that source;
- canonical Self, because newly derived understanding remains provisional until
  the existing evidence and Self Dream qualification gates accept it.

The product flow is:

```text
primary work unavailable, blocked, or explicitly deferred
  -> select one eligible background mission
  -> freeze source scope and policy
  -> inspect only the granted read surface
  -> record an immutable source manifest
  -> derive provisional Memory / Knowledge or a deterministic work profile
  -> let existing deduplication, correction, attribution, and Self gates run
  -> yield immediately when primary work becomes available
```

## Mission Types

### Retained Activity

This mission uses data Yansu has already been allowed to retain locally. It
refreshes the deterministic Work Pattern Profile and may later schedule bounded
Memory/Self synthesis over new retained evidence.

It requires no new source grant. It must not reread protected or excluded
activity and it must preserve the existing profile evidence manifest.

### Gmail Understanding

This mission is disabled until the user issues an explicit Gmail source grant.
Version one:

- uses the shared Computer Use driver and an existing signed-in browser session;
- never calls a provider API, managed MCP server, or OAuth flow, including as a
  fallback;
- compiles the frozen query and lookback into one bounded Gmail search URL;
- allows only `list_apps`, a non-activating browser launch, `open_url`,
  `list_windows`, one screenshot-free accessibility snapshot, and closure of a
  positively confirmed target tab;
- refuses to parse rows until the target is Gmail and the exact frozen query is
  visible in the accessibility snapshot;
- extracts only visible row metadata and snippets from the accessibility tree,
  not message bodies or attachments;
- excludes spam and trash;
- applies the exact Gmail query stored in the grant;
- reads at most the grant's `maxItemsPerRun`;
- hashes external message and thread identifiers in the background receipt;
- sends a bounded, attributed summary through the existing Memory/Knowledge
  pipeline;
- never exposes Gmail mutation operations to the mission;
- evaluates every Computer Use read or reversible local transport step through
  the shared authority gateway using the exact source grant and run ID;
- fails closed when Computer Use is unavailable, the Mac is locked, no
  supported browser is available, Gmail is signed out, or the target/query
  cannot be confirmed.

Future source readers must register an exact Computer Use action compiler and
receive a separate source grant. Generic MCP trust and Computer Use permission
are not source grants.

## Source Grants

A source grant is a privacy and inspection boundary. It is distinct from
execution authority.

Each immutable grant freezes:

- source;
- scope label;
- exact source query;
- lookback window;
- maximum items per run;
- exact allowed operations;
- issue and optional expiry time;
- policy version.

Grant state is derived from append-only `issued` and `revoked` events. Updating
scope creates a new grant and revokes the prior one. A Gmail grant can authorize
only `gmail.computer_use.read`; it can never authorize send, reply, draft,
label, move, delete, download, or attachment access. Grants created under the
retired metadata/MCP policy do not carry forward and must be reissued.

## Scheduling

The coordinator is intentionally conservative:

- one mission at a time;
- daily run cap;
- minimum interval between automatic runs;
- automatic runs while the screen is locked, observation is paused, or no
  foreground user input has occurred for the two-minute quiet window;
- an active meeting is never treated as a quiet window;
- manual runs are allowed for product and diagnostic use;
- foreground user input, privacy pause, source revocation, or shutdown cancels
  the active mission and records the terminal state;
- cancellation propagates through source reads, model work, and every
  materialization boundary;
- production Initiative work always has priority;
- a recent open Shadow episode that is outside rollout or lacks exact frozen
  source context may offer a content-free fallback reason;
- startup may recover that reason from recent append-only Task Episodes, but a
  valid production Shadow episode clears it and retains priority;
- a fallback reason only changes the visible waiting explanation and wakes the
  coordinator to re-check eligibility. It never bypasses the quiet window,
  source grants, daily cap, minimum interval, privacy state, or preemption.

Mission selection prefers a granted external source with unread evidence, then
falls back to retained local activity. No external grant is created
automatically.

## Evidence And Persistence

The activity database stores append-only:

- setting events;
- source grants and grant events;
- mission roots and lifecycle events;
- source item receipts;
- Memory/Knowledge/Profile materialization links.

Raw email bodies, attachments, OAuth data, complete accessibility snapshots,
screenshots, window titles, and complete raw Computer Use results are never
stored in this ledger. Source item receipts contain opaque identifier hashes,
content hashes, bounded attribution metadata, and timestamps. Lifecycle
receipts retain only the selected browser identity, target-window identity,
snapshot ID/hash, action names, cleanup status, counts, and bounded summaries.

Memory and Knowledge files remain the human-readable provisional products.
Canonical Self remains in the typed Self subsystem and may change only through
its existing evidence-backed revision flow.

## User Experience

Agency shows a functional **Learning in background** section with:

- current status and reason;
- last completed mission;
- today's bounded run usage;
- retained-activity availability;
- Computer Use readiness and Gmail source-grant state;
- Learn now, pause/resume, allow, and revoke controls;
- a user-safe detail view backed by the immutable run receipt;
- terminal mission outcomes in the Learning filter on History.

The product receipt contains source name, lifecycle state, bounded reason,
timestamps, source-receipt count, and materialization count. Exact source
queries, identifier hashes, raw event payloads, and materialization linkage
remain in read-only Dev Tools.

The UI must distinguish:

- available: Computer Use can reach a supported local browser;
- allowed: the user granted this learning source;
- running: a mission is currently inspecting it;
- learned: provisional Memory/Knowledge/Profile materialized;
- incorporated: later Self qualification accepted a revision.

These states must never be collapsed into one success label.

## Release Invariants

1. Source readiness and model confidence never imply permission.
2. Read permission never implies mutation authority.
3. Gmail is off by default.
4. Automatic work yields to primary Agency work.
5. No raw email payload is persisted in the background ledger.
6. Every materialized item is linked to a run and source receipt.
7. Third-party statements cannot become personal Self claims without
   attributable user evidence.
8. Corrections create new evidence and revisions; history is never rewritten.
9. The feature remains useful with no external connector through retained local
   activity.
10. Every user-facing control has a real backend transition and receipt.
11. First-party app access has no provider OAuth or managed-MCP fallback.
12. Computer Use permission never implies source consent or execution authority.
13. A source grant may authorize only the compiler's read perception and
    reversible local transport; it never materializes external-write authority.
