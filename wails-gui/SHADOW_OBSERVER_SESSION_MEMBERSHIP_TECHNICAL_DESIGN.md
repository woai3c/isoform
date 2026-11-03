# Shadow Observer Session Membership Verification

Status: implemented and validated on `vision/shadow-agent`

## Problem

Task 196 introduced an exact source-to-observer coverage gate. A production
pipeline row stores the session IDs in traversal order. The immutable observer
root canonicalizes the same IDs with `uniqueSortedStrings` before hashing and
persistence.

Coverage v1 compared those arrays positionally. Identical session membership
therefore became `link_mismatch` whenever traversal order differed from lexical
order. The live database exposed three such false failures across six current
instrumented sources. Every affected source and observer trace otherwise agreed
on pipeline row, group, admission, flow, trace identity, and integrity.

This is a verifier defect, not corrupt production evidence. Rewriting either
side would destroy the distinction between an immutable record and a derived
interpretation.

## Invariants

1. Existing pipeline rows, observer roots, event chains, payload hashes, and
   timestamps remain unchanged.
2. Session membership is exact: missing, additional, blank, duplicate, or
   truncated identities fail closed.
3. Session ordering is not treated as identity. Exact ordered session metadata
   remains protected by the observer root's frozen session-manifest hash.
4. Coverage is read-only metadata. It cannot create a Task Episode, Shadow run,
   evaluation, learning, permission, authority, or external action.
5. Browser RPC remains limited to read methods. There is no repair, replay, or
   relinking endpoint.
6. A healthy observer source is necessary but never sufficient for natural
   parity. The release gate still requires observer-bound onset, current
   contained execution, a later user result at a verified destination,
   comparison, and materialized contamination-safe learning.

## Design

### Canonical membership comparison

Coverage parses the source `session_ids` JSON, trims each value, rejects blank
values, canonicalizes with the same bounded unique-sort function used by the
observer root, and rejects the source if canonicalization changes cardinality.
The canonical source set is then compared positionally with the already
canonical v2 observer-root IDs.

This accepts only a permutation of the exact same unique set. It does not use a
fuzzy match, timestamp join, group-only fallback, or database repair.

### Versioning

- Observer Coverage advances to `task-shadow-observer-coverage-v2`.
- Derived release provenance advances to
  `task-shadow-release-observer-provenance-v2`.
- The natural parity projection advances to `task-shadow-release-gate-v7`.

The immutable observer trace contract does not change. No migration is needed.

### Diagnostics

The existing Observer Coverage list and detail views continue to show exact
pipeline and trace identities. After rebuilding, formerly order-only failures
must derive as `covered`; any real mismatch remains `link_mismatch` and keeps
health at `attention`.

## Verification

1. Reproduce reversed source order against a canonical observer root and prove
   it derives as covered.
2. Prove duplicate source identities remain a mismatch.
3. Preserve existing missing, extra, corrupt, duplicate-trace, unlinked,
   pruned, and scan-truncated coverage tests.
4. Run focused coverage and release-provenance tests, focused race coverage,
   the full Wails package, frontend tests, vet, and production build.
5. Rebuild the live app and verify the six real sources derive as six covered,
   zero mismatched, without changing any source or trace row.
6. Confirm the natural release gate remains incomplete unless a genuine
   current-generation task satisfies every proof step.

## Validation Result

- Focused coverage and release tests pass, including the exact-order regression
  and duplicate-ID fail-closed case.
- Focused race coverage, the complete Wails Go package, `go vet`, all frontend
  tests, and the production frontend build pass.
- The rebuilt Browser RPC reports Coverage v2 as `healthy`: six instrumented
  sources, six current traces, six covered, and zero mismatched.
- Three coverage reads and three release-gate reads left the source, root, and
  event fingerprint unchanged at
  `923479fc458222c7da5d1a7031acdbfa90b2abd703b14f9a4a66e8d231b1a334`.
  Task Episode, Shadow run, evaluation, and learning-ledger counts were also
  unchanged.
- The rebuilt natural parity projection reports release gate v7, provenance
  v2, stage `desktop_locked`, and `complete=false`. The correction removes a
  verifier false negative without promoting readiness or creating evidence.
- Browser QA confirmed the Source coverage view, health summary, row selection,
  and exact record detail remain functional in the renamed Developer Tools
  navigation.
