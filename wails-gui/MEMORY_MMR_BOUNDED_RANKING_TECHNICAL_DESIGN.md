# Bounded Memory Diversity Ranking Technical Design

Status: implemented for Task 258 on `vision/shadow-agent`.

## Problem

Activity triage recalls Memory and Knowledge before asking the model whether a
retained activity group contains useful work. The recall path intentionally
scans a broad candidate set and fills a character budget so accumulated user
context is not reduced to an arbitrary small number of records.

The bounded search path previously applied Maximal Marginal Relevance (MMR) by
recomputing every candidate's similarity to every previously selected result
on every selection. With 400 hybrid candidates, this repeats millions of
Jaccard comparisons and ignores context cancellation while it runs. Production
retrieval can therefore consume several minutes even though vector and keyword
candidate collection complete in seconds. Activity triage then reaches its
model call only after its deadline has expired.

The search path also sorted the MMR output by relevance immediately afterward.
That discards the diversity order, so the expensive computation cannot improve
the budget-filled context.

## Product Contract

- Memory and Knowledge recall remains broad and character-budget-driven. This
  task does not replace that policy with a small product top-N limit.
- Diversity ranking must be deterministic and preserve relevance while moving
  redundant records later in the candidate stream.
- Ranking must honor the caller's context during tokenization, selection, and
  similarity updates.
- A bounded, non-strict search whose diversity pass is canceled may return the
  already collected candidates in deterministic relevance order. It must not
  block useful context behind optional diversity work.
- A strict search returns the cancellation or ranking error instead of hiding
  incomplete work.
- Search and fallback are read-only context operations. Memory, Knowledge,
  Purpose, Self, profile, Task Episode, Shadow, Agency, permission, authority,
  and execution behavior are unchanged.
- Retrieved prose remains context only. Relevance, diversity, confidence, and
  successful retrieval never grant authority.

## Ranking Algorithm

The implementation preserves greedy MMR semantics while reducing repeated
work:

1. Tokenize every candidate once and normalize relevance scores once.
2. Keep one `maxSimilarity` value for each unselected candidate.
3. Select the candidate with the highest MMR score after establishing a stable
   relevance, path, source, and line ordering for ties.
4. Compare every remaining candidate only with the newly selected candidate
   and update its cached maximum.
5. Repeat until every candidate is ordered.

For `n` candidates this performs at most `n * (n - 1) / 2` Jaccard
comparisons, uses linear auxiliary state, and avoids the previous extra scan of
all selected candidates. Context checks occur before tokenization, throughout
selection scans, and throughout similarity updates.

## Search Ordering

When vector search is active and MMR completes, filtering retains the MMR
order and result limiting takes the diversified prefix. The final relevance
sort is skipped because it would erase the reranking.

When MMR is disabled, unnecessary, or canceled in a bounded non-strict search,
the established stable relevance sort remains the ordering rule. A canceled
fallback does not perform another database query and does not increase the
candidate or prompt budget.

## Failure And Cancellation Semantics

| Search state | Result |
| --- | --- |
| MMR completes | Return diversified candidates |
| MMR context canceled, bounded search | Log fallback and return relevance order |
| MMR context canceled, strict search | Return a wrapped error |
| No vector results | Skip MMR and return relevance order |
| Zero or one candidate | Return deterministic relevance order |

The fallback changes only candidate ordering. It never changes files, appends
evidence, retries activity, invokes a model, or starts a tool.

## Verification Gates

1. The optimized implementation matches a simple reference MMR implementation
   on a non-tied fixture.
2. Equal-score and equal-similarity candidates produce stable ordering across
   repeated runs.
3. A canceled context exits before ranking and a mid-computation deadline is
   observed promptly.
4. A production-sized 400-candidate fixture completes inside the five-second
   triage recall budget.
5. MMR order survives filtering and result limiting; the relevance-only path
   remains stable.
6. Dedup recall still scans the established broad candidate window and fills a
   character budget rather than an arbitrary entry-count product cap.
7. Focused, race, complete Go, frontend, production-build, and natural live
   activity-pipeline checks pass with unchanged authority and effect counts.
