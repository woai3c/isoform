# Background Understanding Source Fairness Technical Design

Status: implemented and release-validated for Task 214 on
`vision/shadow-agent`.

## Purpose

Background Understanding uses otherwise idle capacity to learn from retained
local activity or from a separately allowed Gmail Computer Use scope. The live
profile showed four retained-activity refreshes consuming all four daily runs
while Gmail was temporarily unavailable on the locked desktop.

That behavior was individually valid but globally wrong: a lower-cost source
could consume the complete daily capacity before a richer consented source
became available. The product History also rendered every periodic profile
refresh as a separate identical completion row.

Task 214 adds source-fair scheduling and a compact product projection without
changing source access, evidence, authority, or immutable run receipts.

## Product Rule

When a user has explicitly allowed a bounded Gmail source, Yansu keeps one
automatic daily learning run available for that source while it is temporarily
blocked by desktop lock or Computer Use readiness.

This reservation:

- does not grant, renew, broaden, or execute Gmail access;
- does not bypass the exact Computer Use compiler or source grant;
- does not delete or reclassify a retained-activity run;
- does not affect Shadow, Memory, Knowledge, Self, Purpose, or authority; and
- disappears after Gmail uses the reserved capacity or the grant is revoked.

## Scheduling Contract

The versioned scheduler contract is
`background-understanding-scheduling-v2-source-fair`.

For a daily limit `L`:

```text
no active Gmail grant:
  retained-activity capacity = L

active exact Gmail Computer Use grant:
  retained-activity capacity = max(0, L - 1)
  Gmail reservation = 1 until one Gmail run exists that day

consumed daily capacity:
  min(retained-activity attempts, retained-activity capacity)
  + Gmail attempts
  + future non-retained mission attempts
```

All roots and terminal states remain attempts. The consumed-capacity projection
may therefore be lower than the immutable attempt count when older local runs
already exceeded the newly reserved share. This derived correction makes the
reservation effective immediately without rewriting historical rows.

Manual runs retain their existing explicit path. They do not change a source
grant and may still exceed automatic capacity because the user invoked them
directly.

## Per-Source Interval

The minimum interval now applies to the mission selected for the next run, not
to the latest background run from any source.

```text
select the oldest currently ready mission
  -> calculate source-fair daily capacity
  -> reject when that mission's daily share is exhausted
  -> compare now with the latest run for that same mission
  -> run only when the source-specific interval elapsed
```

A retained-activity refresh immediately before unlock cannot delay the first
eligible Gmail run. Repeated Gmail scans and repeated retained-activity scans
still respect their own bounded interval.

## Read Model

The product and Developer Tools snapshot exposes:

- scheduling policy version;
- consumed daily run count;
- immutable attempt count;
- reserved run count and source;
- `waiting_for_source` when the retained share is exhausted and the reserved
  Gmail source is not currently usable; and
- the existing complete source and run receipt lists.

Product copy says that capacity is held for Gmail and names the exact user
action required, such as unlocking the Mac or enabling Computer Use. It never
implies that Gmail was read while unavailable.

## History Projection

Product History combines equivalent automatic retained-activity completions or
skips only when source, terminal state, summary, and UTC day all match. The
combined row reports the number of refreshes and aggregate source receipts and
learning artifacts.

Manual runs, Gmail runs, different days, different summaries, and different
terminal states remain separate. The Background Learning debugger and run
detail API continue to expose every immutable root, event, source item,
materialization, hash, and timestamp.

## Validation

Required coverage includes:

- four retained-activity attempts with a current Gmail grant consume only
  three units of a four-run daily limit and reserve one for Gmail;
- the same attempts without a Gmail grant consume all four units;
- a newly ready Gmail source is not delayed by a recent local run;
- a reserved but locked source produces a truthful waiting state;
- reads do not create, update, delete, execute, grant, revoke, or learn;
- equivalent automatic local History rows combine while manual, Gmail,
  cross-day, and non-equivalent rows remain distinct;
- product and Developer Tools expose consumed, attempted, and reserved counts;
- all focused, race, full, frontend, build, vet, live read-only, and browser
  interaction checks pass.

No desktop unlock, Gmail read, Shadow result, Initiative, or source evidence may
be fabricated to validate Task 214.

## Release Evidence

- Focused backend coverage and focused race coverage pass, including reserved
  capacity, full local capacity without a grant, source-specific intervals,
  truthful waiting states, and History grouping boundaries.
- The complete Wails Go suite passes in 147.591 seconds and `go vet ./...`
  passes.
- All 48 frontend test files and 252 tests pass. The production frontend build
  passes with only the established large-chunk advisory.
- The rebuilt live contract reports scheduler
  `background-understanding-scheduling-v2-source-fair`, `3/4` consumed runs,
  four immutable attempts, and one run held for Gmail while the desktop is
  locked.
- Three repeated live snapshot reads left the complete background-learning
  SQLite dump fingerprint unchanged at
  `9139a5b60e88a745abc3acee6c9cc16972452d4fdaae0d199b049eae5faebac5`.
- Browser interaction QA opened the latest product learning record, confirmed
  one combined same-day four-refresh History row, and inspected the raw
  scheduler policy, lifecycle, source receipt, and materialization in Developer
  tools.
- Standalone `tsc --noEmit` remains blocked by the repository's established
  generated-binding and application type drift. The production build and test
  compiler both pass, and the new source-fair fields add no reported compiler
  error.
