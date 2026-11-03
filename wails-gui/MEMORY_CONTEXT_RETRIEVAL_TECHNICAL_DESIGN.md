# Memory Context Retrieval Technical Design

Status: implemented and rebuilt-live validated on `vision/shadow-agent`

## Purpose

Yansu's canonical Self is supported by Memory and Knowledge, but a model can
use that evidence only when relevant context reaches the prompt. Prompt
selection must therefore be budgeted without silently treating an arbitrary
number of search results as the complete digital self.

This design removes the hidden 200-candidate prefix from chat, Handoff, and
Schedule prompt injection. It does not make prompts unbounded, change Memory or
Knowledge files, promote anything into Self, or grant execution authority.

## Existing Problem

The general Memory injection path currently performs:

```text
hybrid search
  -> keep at most 200 returned candidates
  -> remove corrected Agency learning
  -> keep results above the relevance threshold
  -> fill a 48,000-character prompt budget
```

The character budget is the correct product boundary. The preceding 200-result
cap is not. When more than 200 short relevant chunks exist, later chunks
disappear before suppression and budgeting. A large corrected prefix can also
leave unused context space because omitted candidates were never returned.

Calling the cap an operational guard does not make the omission observable or
complete. It is still a fixed top-N discovery policy.

## Contract

### Separate ordinary search from prompt discovery

`MemoryStore.Search` remains the bounded product/search API. A caller asking
for 20 results still receives at most 20 results with the current hybrid,
temporal-decay, and MMR behavior.

Prompt assembly receives a separate complete-candidate API:

```text
SearchContextCandidates(query)
  -> synchronize the local index when dirty
  -> evaluate every indexed vector candidate
  -> retrieve every matching FTS candidate
  -> merge and score each unique chunk once
  -> apply temporal decay
  -> return the complete relevance-ordered candidate set
```

There is no result-count argument and no hidden candidate-count constant in
this path. The existing caller deadline remains the operational time boundary.
Cancellation fails the injection read explicitly and falls back to the prompt
without claiming that context was complete.

### Budget-driven selection

After complete discovery, prompt assembly:

1. removes explicitly corrected Agency-learning targets;
2. excludes empty and below-threshold chunks;
3. selects globally by relevance;
4. fills at most 48,000 characters;
5. truncates only the final included chunk when it partially fits; and
6. records every aggregate omission reason.

The selection result includes:

- discovered candidates;
- corrected candidates excluded;
- empty candidates excluded;
- below-relevance candidates excluded;
- candidates included;
- included characters;
- a final truncated candidate count; and
- relevant candidates omitted by the character budget.

Memory and Knowledge share one relevance and character budget so one source
cannot receive an implicit item quota. Their selected chunks remain grouped
separately in the final prompt for readability.

### Prompt receipt

The injected context contains one compact, model-visible selection receipt
before the selected chunks. It reports counts and the explicit character
budget, but no omitted private content. This lets the model and Chat details
distinguish complete discovery from complete prompt inclusion and use the
existing Memory CLI when more source context is needed.

## Developer Experience

Settings -> Developer tools -> Chat details gains a read-only Memory context
inspector. A developer can enter the same text a user would send and inspect:

- total indexed candidates considered;
- selected Memory and Knowledge counts;
- selected characters versus budget;
- corrected, below-relevance, empty, truncated, and budget-omitted counts; and
- selected source paths, line ranges, scores, and bounded previews.

The inspector does not send a message, append a session, write Memory or
Knowledge, update Self, create Shadow evidence, start work, or alter authority.
It reuses the exact production discovery and selection functions rather than a
debug-only approximation.

## Privacy And Authority Invariants

1. Complete discovery reads only the existing local Memory index.
2. The browser RPC remains localhost-only and returns only the same bounded
   snippets already available through `SearchMemory`.
3. Corrected Agency-learning targets remain excluded before prompt budgeting.
4. Hidden Shadow output remains excluded by its existing materialization and
   context-admission boundaries; this change does not admit a new source.
5. Search score, context inclusion, and Self evidence never imply permission.
6. Repeated production and debugger reads must leave Memory, Self, Shadow,
   Initiative, permission, and authority ledgers unchanged.

## Failure Behavior

- Missing Memory storage returns an empty read-only snapshot.
- An empty debug query returns an explicit validation error.
- Sync, embedding, FTS, or cancellation errors fail the context read without
  returning a partial set as complete.
- If no candidate meets the relevance threshold, the prompt remains unchanged
  except for canonical Self context already admitted by its own subsystem.
- Invalid paths or files are never opened through the debug API; previews come
  only from indexed chunks.

## Verification

- More than 200 matching short chunks must remain discoverable and selectable
  when they fit the character budget.
- Corrected candidates at the front of the ranking must not prevent later
  relevant candidates from filling the budget.
- Generic bounded Memory search must preserve its requested result limit and
  ranking behavior.
- Selection counts must reconcile exactly:

```text
discovered - corrected
  = selected + empty + below relevance + omitted by budget
```

  A truncated final result is selected and reported separately.
- English and Chinese Chat details copy, loading, empty, error, successful,
  narrow-width, and repeated-query states must be functional.
- Focused, race, complete Go, frontend, production build, vet, formatting,
  rebuilt Browser RPC, UI interaction, and unchanged-ledger checks must pass.

## Non-Goals

- Sending every Memory or Knowledge byte to a model.
- Removing the 48,000-character prompt budget or caller timeout.
- Changing the user-facing Memory search result limit.
- Rewriting, merging, deleting, or promoting Memory, Knowledge, or Self.
- Using hidden Shadow output, confidence, or context selection as authority.
