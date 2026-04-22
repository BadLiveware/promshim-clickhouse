# 04 — `topk`/`bottomk` early cutoff via `LIMIT n BY`

`topk(10, m)` today materialises every matching series, ranks them per-group
with `row_number() OVER (PARTITION BY grouping_tags ORDER BY value DESC)`,
and filters `rank <= 10`. The window function still has to consume all rows.
ClickHouse can do better: `ORDER BY value DESC LIMIT 10 BY grouping_tags`
short-circuits per partition, and the approximate `topK(k)(value)` aggregate
can answer the approximate form in one pass without sorting at all. Pick the
right tool per query semantics.

## Problem

Concrete queries:

- `topk(5, rate(http_requests_total[5m]))` — instant
- `topk(10, up)` — instant, cheap selector
- `topk(3, sum by (code) (rate(http_requests_total[5m])))` — topk of an
  aggregation result
- range-mode versions of all of the above

Current SQL envelope for instant `topk(k, expr)`
(`internal/promshim/storage/sql.go:556-621`, specifically
`buildInstantSelectionAggregationOverSubquerySQL`):

```sql
SELECT tags, timestamp, value FROM (
  SELECT tags, timestamp, value,
         row_number() OVER (PARTITION BY grouping_tags
                            ORDER BY isNaN(value) ASC, value DESC, tags ASC) AS rank
  FROM (<full source subquery with tags, timestamp, value>)
) WHERE rank <= k ORDER BY tags
```

Cost model: the window function reads every row (`num_input_series`) in the
partition, buffers at least k rows per partition, and emits all rows with a
rank. We then filter post-window. ClickHouse cannot fuse the `<= k` filter
back into the window; it's a second pass. For a selector that matches
100k series and `topk(5, up)` the current query scans and ranks all 100k
series.

## Current behavior

- `internal/promshim/storage/sql.go:556-621` — instant selection
  aggregation uses `row_number() OVER` and a post-filter.
- `internal/promshim/storage/sql.go:623-700` (same file, continuation) —
  range selection aggregation does the same shape but per-step.
- `internal/promshim/native/analysis_support.go:53-60` —
  `IsSelectionNativeAggregation` enumerates the four selection ops: TOPK,
  BOTTOMK, LIMITK, LIMIT_RATIO.
- `internal/promshim/native/renderer/join.go:67-72` —
  `renderAggregationFragment` already has a branch for selection
  aggregations that forces full-tag materialisation (`forceFragmentFullTags`),
  which stays necessary because the output must preserve the series identity.

## Proposed technique

### Exact path: `ORDER BY … LIMIT k BY group`

ClickHouse's `LIMIT k BY` clause returns up to k rows per distinct value of
the BY expression. Semantically identical to
`row_number() OVER (PARTITION BY …) … WHERE rn <= k` when the ORDER BY is
the same, and the engine implements it as a heap per group — it never
sorts the full partition. Emit:

```sql
SELECT tags, timestamp, value
FROM (<same source as today, projected with grouping_tags>)
ORDER BY grouping_tags ASC,
         isNaN(value) ASC, value DESC,      -- DESC for topk, ASC for bottomk
         tags ASC
LIMIT k BY grouping_tags
```

Note: `LIMIT k BY <expr>` requires the top-level statement; it does not
compose with outer projections the way the window function does. The
emitted query has to be the outermost SELECT; any outer wrapper (e.g. the
`zero_on_empty` wrapping or an outer `sort()` call) must be reorganised to
treat the topk result as a new leaf.

For `bottomk`: same shape with `ASC` on value.

For `limitk`: `LIMIT k BY grouping_tags` with ORDER BY only on tags (no
value-based ordering, matching the existing implementation).

### Approximate path: `topK(k)(value)` aggregate

ClickHouse's `topK(k)` is a Space-Saving sketch that returns an approximate
top-k array in a single pass. It is NOT Prom-equivalent: it returns the
top-k values but does not preserve the series-identity mapping (we get
values, not (tags, value) pairs). It is therefore unsuitable as a direct
replacement for Prom's `topk` semantics, which require returning the source
series.

We can still use `topK` as an aggregate-pushdown filter: compute the
approximate threshold, then use it to filter the exact query. But that's
two queries; not worth it for v1. Drop `topK`/`topKWeighted` from the
proposal; keep only the exact `LIMIT k BY` path.

### When is the rewrite applicable?

The `LIMIT k BY` path is safe when:

1. The `topk`/`bottomk`/`limitk` is the terminal fragment (no outer
   aggregation or transform requires an unlimited input).
2. `k` is a literal integer (the existing
   `selectionAggregationKValue` already enforces integer `paramNumber`).
3. The source subquery returns `(tags, timestamp, value, grouping_tags)` —
   which is already the shape produced by
   `renderAggregationInstantSourceSubquery` / the range variant.
4. No `LIMIT_RATIO`: that one uses a hash-threshold, not a rank cutoff. Keep
   the window-function path for LIMIT_RATIO.

Range mode needs an extra wrinkle: `topk` in range mode runs per-step. The
rewrite becomes `LIMIT k BY (grouping_tags, timestamp)`:

```sql
SELECT tags, timestamp, value FROM (
  SELECT grouping_tags, tags, point.1 AS timestamp, point.2 AS value
  FROM <range source> ARRAY JOIN time_series AS point
)
ORDER BY grouping_tags, timestamp, isNaN(value) ASC, value DESC, tags ASC
LIMIT k BY grouping_tags, timestamp
```

Then re-pack into the `(tags, time_series)` range matrix with the usual
`groupArray` + `arraySort` outer wrapper.

## Expected gain

- Instant `topk(5, m)` with 100k matched series: the window function path
  reads and sorts ~100k rows; `LIMIT 5 BY` keeps a 5-element heap per group
  and processes rows as they stream. For a single-group case (no `by`) that
  is a ~20000× reduction in intermediate rows buffered.
- Range mode: the per-step `LIMIT k BY (group, ts)` avoids re-sorting every
  step's partition; heap-per-group means the step count multiplies only
  the tiny per-step heap, not the per-step partition size.
- Memory on the ClickHouse side: sorted window buffer → bounded per-group
  heap of size k. For k=5, the memory footprint is effectively constant.

## Risk / PromQL semantics caveats

- **Tie breaking**: Prom's `topk` is unspecified on ties. The existing
  window path uses `ORDER BY isNaN(value) ASC, value DESC, tags ASC` — a
  stable total order. The `LIMIT k BY` rewrite uses the same ORDER BY, so
  the emitted output is byte-identical on tied values. Keep the `tags ASC`
  tiebreak; do not drop it.
- **NaN handling**: the `isNaN(value) ASC` clause keeps NaN rows last.
  `LIMIT k BY` respects ORDER BY, so NaN series never crowd out real series
  unless there are fewer than k non-NaN series in the group. Match
  Prometheus's behaviour: a non-NaN series beats a NaN series. Add a test
  that exercises this.
- **`by()` with empty grouping**: `topk(5, m)` with no `by` produces a
  single-group topk across the whole set. `LIMIT 5 BY constant()` works
  (use `LIMIT 5 BY 1`). Verify ClickHouse actually executes with a heap
  rather than a full sort when the BY expression is constant — if it
  reverts to a global sort, force a group expression anyway (e.g. hash
  zero).
- **Range-mode per-step independence**: `topk` semantics select per-step
  winners independently; a series can be in the top-5 at some steps and
  out at others. The `LIMIT k BY (grouping_tags, timestamp)` formulation
  preserves that exactly.
- **LIMIT_RATIO exclusion**: the ratio form deterministically hashes tags
  and keeps rows below a threshold. That's not a top-k cutoff; keep it on
  the current path.
- **`COUNT_VALUES` is not a selection aggregation** (it's handled
  separately by `forceFragmentFullTags`) — no interaction.
- **Outer wrappers**: if the optimizer folds
  `sort(topk(k, m))` into a selection + external sort, the `LIMIT k BY`
  shape has `ORDER BY` on the inside. The outer `sort()` then receives at
  most k×groups rows, which is fine; but the SortTransform lowering must
  be aware that the topk fragment is "already limited" and re-sort rather
  than assume unlimited input.

## Implementation sketch

1. Storage helper
   `BuildInstantSelectionAggregationOverSubquerySQLLimitBy(...)` mirroring
   the existing `buildInstantSelectionAggregationOverSubquerySQL`, but
   replacing the `ranked` CTE with a direct `ORDER BY … LIMIT k BY
   grouping_tags`. Range twin with `LIMIT k BY grouping_tags, timestamp`.
2. Renderer branch in `renderAggregationFragment`
   (`internal/promshim/native/renderer/join.go:67-96`): when
   `IsSelectionNativeAggregation(op) && op != parser.LIMIT_RATIO`, route
   to the new helper. Keep the existing helper for LIMIT_RATIO.
3. Optimizer report: add `AppliedRewrites =
   append(..., "topk_limit_by_cutoff")`.
4. Feature-flag the rewrite on `QueryConfig` for a week of soak time
   against the harness before removing the legacy window-function path.
5. Do not touch the fragment-shape model; the rewrite is pure-SQL
   within the existing aggregation fragment.

## Test coverage idea

- Unit (storage SQL): `TestBuildTopkInstantUsesLimitBy` — assert emitted
  SQL contains `LIMIT` and `BY grouping_tags`, does NOT contain
  `row_number()`.
- Unit: `TestBuildBottomkRangeUsesLimitByWithTimestamp` — BY clause
  includes `grouping_tags, timestamp`.
- Unit: `TestBuildLimitRatioStillUsesWindowPath` — regression pin for
  LIMIT_RATIO.
- Golden SQL: rendered output matches a checked-in fixture for each of
  (topk, bottomk, limitk) × (instant, range).
- Integration (harness): run `topk(5, up)`, `topk(3, sum by (code)
  (rate(...)))` and compare to Prometheus reference output via the
  conformance suite. Include a tie-breaking case where three series share
  the same value and k=2; assert deterministic selection matching the
  existing path.
- Integration: `topk(0, m)` — must return empty (current path does this
  via `rank <= 0` filter; verify `LIMIT 0 BY …` behaves the same on
  ClickHouse).
- Bench: topk(5) over a 100k-series fixture; expect ≥10× wall-clock
  improvement and significantly lower peak memory per query.
