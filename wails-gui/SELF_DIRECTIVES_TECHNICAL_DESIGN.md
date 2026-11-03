# Explicit Self Directives Technical Design

Status: contract locked for Tasks 133-139

## Purpose

Observed behavior can show what a user did without revealing why. Yansu may
retain competing reasoning hypotheses, but unsupported reasoning remains
unknown. The user therefore needs a direct way to state durable principles and
hard boundaries in canonical Self.

An explicit Self directive is user-authored reasoning, not a behavioral
inference. It can guide judgment or narrow what Agency may do. It never grants
authority, approves an Initiative, qualifies Shadow, or proves a result.

## Product Contract

The first release supports two directive kinds:

- `principle`: free-form durable reasoning that is always placed ahead of
  inferred preferences and work patterns in Agency context;
- `boundary`: a free-form constraint plus an optional structured deny-only
  capability policy.

The current structured boundary policy may deny only the five capabilities in
the initial local Agency envelope:

- `tool.read`;
- `tool.glob`;
- `tool.grep`;
- `tool.write`;
- `tool.edit`.

A boundary with no structured capability list remains a binding planning
constraint, but Yansu must not claim deterministic runtime enforcement for
free-form text it cannot compile. A structured list can only subtract from an
existing operating envelope. It cannot add a capability, workspace, recipient,
budget, recurrence, or external effect.

## Immutable Model

`self_user_directives_v1` stores one immutable logical root:

- directive and idempotency IDs;
- stable Self `entry_key`;
- kind (`principle` or `boundary`);
- contract version;
- creation time;
- canonical payload JSON and SHA-256 hash.

`self_user_directive_events_v1` stores an append-only lifecycle:

- monotonic sequence;
- `created`, `revised`, or `retracted` event;
- exact `self_model_entries.id` produced by that event;
- statement, scope, structured denied capabilities, and retraction reason;
- occurred time;
- canonical payload JSON and SHA-256 hash.

Every event points to one immutable typed Self revision with the same logical
entry key. The current directive is the latest event. Revision never updates or
deletes a prior row. Retraction appends a retracted Self revision and event.
Reactivation requires a new directive so a retired constraint cannot silently
resume under an old identity.

The migration also adds a delete trigger to `self_model_entries`. Every typed
Self read reconstructs and verifies its existing source hash before the entry
can enter Self, prompts, origination, continuation, or policy evaluation.

## Write APIs

Native and browser product surfaces use three explicit user actions:

```text
CreateSelfDirective
ReviseSelfDirective
RetractSelfDirective
```

Each request requires an idempotency key. Revision and retraction also require
the expected current Self entry ID, so stale UI state fails instead of
overwriting a newer decision. Only roots created through this direct-user
ledger may be revised or retracted through these APIs.

The write transaction must atomically:

1. validate the root and current event chain;
2. normalize the statement, scope, and deny-only policy;
3. append the typed Self revision;
4. append the directive root when creating;
5. append the directive event;
6. commit all rows or none.

User-authored evidence is stored with confidence `1.0`, reasoning status
`explicit_user_statement`, and exact operation provenance. This means the
statement is explicit, not that it grants authority or is universally
applicable outside its stated scope.

## Read And Context Rules

The product snapshot marks direct directives as editable and retains their
directive ID and structured enforcement scope. Self continues to omit retracted
entries from the active projection, while History and directive detail preserve
the full chain.

Context assembly follows this order within a bounded character envelope:

1. active direct boundaries;
2. active direct principles;
3. other active evidence-backed boundaries and principles;
4. relevant decision policies and preferences;
5. provisional hypotheses.

There is no fixed top-N limit. If the complete set does not fit, the manifest
records every excluded entry and reason. Direct entries are not silently
downgraded to inferred observations.

## Deny-only Runtime Policy

At Initiative admission, required capabilities are compared with every active,
hash-valid structured boundary. A match rejects admission with the exact
directive ID and capability reason.

Immediately before every Initiative tool action, the dynamic authority
evaluator reloads current directives. A matching boundary returns `deny` before
the existing operating envelope could return `allow`. This makes a newly added
boundary effective for already-running work. A retraction removes only this
extra denial; the Initiative still needs its original unchanged operating
envelope and admitted capability snapshot.

The precedence is:

```text
invalid directive state -> deny
active explicit boundary deny -> deny
operating envelope deny -> deny
operating envelope allow -> allow
```

Principles never participate in deterministic authority evaluation. Free-form
boundary text that has no structured capability mapping is never presented as
runtime-enforced.

## Product Experience

Self provides:

- `Add to Self` with Principle and Boundary modes;
- optional scope;
- structured local capability restrictions for Boundary mode;
- clear direct-user provenance;
- functional edit and retract controls only for direct directives;
- optimistic-concurrency errors that preserve the user's draft;
- an immutable timeline in detail.

History distinguishes directive creation, revision, and retraction. Quick Chat
remains available for discussion, but chat text does not become a directive
until the user explicitly records it through the Self control.

Agency Runtime Dev Tools exposes read-only roots, events, Self revision hashes,
compiled denied capabilities, and policy-match diagnostics. Debugger reads do
not create or revise Self.

## Read-only Diagnostics

Agency Runtime Dev Tools uses two dedicated read methods:

```text
GetSelfDirectiveDebugSnapshot
GetSelfDirectiveDebugDetail
```

The snapshot validates the complete explicit-user ledger before returning a
bounded searchable root index. Its counts cover every validated root and every
validated policy match. The current compiled deny set is complete and has no
presentation limit. A truncation flag states when only the root or receipt
index has been shortened for the interface.

Detail returns one complete immutable root, every directive event, every typed
Self revision, all current compiled denials for that root, and every observed
admission or action match. It exposes exact root, event, Self source, candidate,
and action payload hashes so a developer can follow one statement through the
entire policy path.

An admission match is accepted only when the selected candidate payload and
stored payload JSON recompute exactly, the authority decision is a denial, the
capability is missing, and the referenced directive revision was active at the
candidate timestamp. An action match additionally requires an exact action
payload, blocked status, persisted `deny` decision, matching capability, and
the historical directive revision active at action time. A stale, malformed,
inactive, or corrupt relation fails the diagnostic read instead of displaying
plausible but unsupported provenance.

The debugger exposes no create, revise, retract, replay, repair, approval, or
authority control. Browser RPC allowlists only the two read methods; canonical
Self mutations remain in the product Self surface.

## Threat Model

- Model output cannot call a private helper to create a directive.
- Shadow, Memory, Knowledge, Self Dream, and inferred corrections cannot
  impersonate the explicit-user ledger.
- A directive cannot contain wildcard, unknown, external, or grant semantics.
- Corrupt root, event, Self revision, hash, sequence, or relation fails closed.
- Stale revision and retraction requests fail without appending a row.
- Idempotent replay returns the existing exact result; conflicting replay fails.
- Retraction never deletes historical reasoning.
- Direct boundaries cannot broaden an operating envelope or re-admit an
  Initiative.
- Product and debugger reads are inert.

## Verification

Required coverage:

- create, revise, retract, idempotent replay, stale edit, and conflict behavior;
- immutable update/delete rejection for roots, events, and typed Self rows;
- root, event, Self revision, source-hash, sequence, and relation tamper checks;
- automatic systems cannot create a direct principle;
- structured boundary capability normalization rejects wildcard and unknown
  values;
- admission and per-action policy deny only matched capabilities;
- principles and unstructured boundaries never grant or claim enforcement;
- retraction removes only the additional deny and creates no authority;
- active direct boundaries and principles precede inferred context without a
  fixed top-N;
- Self, History, detail, and Dev Tools controls are functional in every state;
- repeated reads create no directive, Self, Memory, Knowledge, Shadow,
  Initiative, permission, or authority rows;
- focused and full Go, vet, frontend, build, live read-only, and native visual
  checks pass before release.
