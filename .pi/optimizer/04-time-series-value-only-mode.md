# `time_series` value-only mode for range outputs without tags

## Problem

The native range-mode SQL always groups by `tags`, even when
`selector.NeedTags=false` and the output is destined to be a single
series (e.g., a scalar output or an aggregation that collapses to
one row). The empty-tags placeholder `CAST([], 'Array(Tuple(String,
String))')` is threaded through the query as the grouping key — we
scan, hash, and group by a constant.

Example where `NeedTags=false` still hits the grouped path:

```promql
sum(rate(http_requests_total[5m]))
```

The inner range-vector selector is lowered with `NeedTags=false` once
the outer `sum` (no grouping) determines that tags can be dropped.
But the current SQL emits:

```sql
SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
  SELECT CAST([], 'Array(...)') AS tags,
         d.timestamp, d.value
  FROM timeSeriesData(...) d INNER JOIN (... matched_series ...) series
  ON d.id = series.id
  WHERE ...
) inner
GROUP BY tags
ORDER BY tags
```

The `GROUP BY tags` with a constant key still forces ClickHouse to
hash-group all rows into a single bucket and maintain
`groupArray((timestamp, value))` on top. Because it's a single group,
it runs single-threaded in the final aggregation stage — defeating
the point of a columnar engine.

## Current behavior

Relevant anchors:

- **Selector SQL (range matrix)**: `selector_sql.go:290-329`
  `buildRangeMatrixSelectorSourceSQL`. Lines 295-300 substitute
  `EmptyTagsArrayExpr()` and drop the `ORDER BY` when
  `selector.NeedTags=false`, but lines 315-323 still build the outer
  query as `GROUP BY tags` (single constant group).
- **Selector SQL (range-instant)**: `selector_sql.go:226-288`
  `buildRangeInstantSelectorSourceSQL`. Same shape. Lines 243-248
  collapse the inner group-by to drop `grid.tags`, but the outer
  still does `GROUP BY tags`.
- **Range-window selector**: `selector_sql.go:62-126`
  `BuildRangeWindowSelectorQuerySQLWithFinalTags`. Lines 84-92
  collapse `groupByWindow` to `[grid.id, grid.eval_ts]` when
  `!NeedTags` and set `orderBy=nil`, but still do
  `GROUP BY final_tags` at the outer level (line 118).
- **`selectorNeedsTags`**: `renderer/sqlutil.go:132-137` — returns
  `false` when the selector's `RequireFullTags` is false and
  `RequiredTagLabels` is empty. Propagated to
  `storage.SelectorSource.NeedTags` (`source.go:279`).
- **`SortedTimeSeriesGroupArrayExpr`** is the shared expression used
  in every outer select that assembles the terminal `time_series`
  column (`schema.go:62-67`).

Concretely, when `NeedTags=false`, the outer query becomes:

```sql
SELECT tags, arraySort(...) AS time_series
FROM (...) inner
GROUP BY tags
```

where every row has identical `tags = CAST([], 'Array(…)')`. The
engine runs a group-by with a single hash bucket, then emits one
row.

## Proposed technique

When `NeedTags=false` at a range-mode selector **and** the upstream
operator does not distinguish per-series (i.e., the immediate parent
is also tag-agnostic — an aggregation with no grouping, a
`ScalarConvert`, or a `Synthetic` merge target), emit a **scalar
range path**:

```sql
SELECT
  CAST([], 'Array(Tuple(String,String))') AS tags,
  arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
  -- inner: no tags, no grouping key
  SELECT d.timestamp, d.value
  FROM timeSeriesData(...) d INNER JOIN (...) series
  ON d.id = series.id
  WHERE ...
)
-- no GROUP BY, no ORDER BY
```

Omitting `GROUP BY tags` when the grouping key is a constant turns
the query into a single-pass scan-and-collect. ClickHouse still
runs `groupArray` as an aggregate, but without a `GROUP BY` it's a
terminal no-group aggregation — the whole scan folds into one
aggregate result without the hash-group bucket dance.

Alternatively: keep `GROUP BY tuple()` (grouping by empty tuple is
an explicit no-group-key form), which ClickHouse optimizes to a
single-pass reduction.

For `buildRangeMatrixSelectorSourceSQL` specifically: when
`NeedTags=false`, the inner query can also drop the `tags` column
entirely (don't even emit `EmptyTagsArrayExpr()` inside the inner
select). The outer synthesizes it as a literal alongside the
aggregate.

For range-window and range-instant variants: the inner
`GROUP BY grid.id, grid.eval_ts` already collapses per-step; the
outer then just needs to re-collect timestamps. When `NeedTags=false`
and the anchor grouping is "scalar" (aggregation with empty
grouping), the outer reduces to a single `groupArray` without
`GROUP BY`.

## Expected gain

- **No hash-group stage**: the terminal aggregation runs as a single
  streaming reduction instead of hash-group-then-emit. For large
  scans this is the single biggest win — ClickHouse's `GROUP BY`
  with one key is still hashed.
- **Parallelism**: without a `GROUP BY` key the final stage can run
  multi-threaded and then perform a final merge. With a single-key
  `GROUP BY tags` it serializes on the single hash bucket.
- **Tags column elimination inside the inner select**: removes one
  column from the intermediate materialization. `EmptyTagsArrayExpr`
  evaluates cheap, but repeating it per row still writes a constant
  tuple-array into the intermediate block.
- **Ordering**: today's `ORDER BY tags` on a single-row result is a
  no-op but still costs plan setup. Already dropped when
  `!NeedTags` in the matrix variant (line 299) but not in the
  range-instant outer (`selector_sql.go:281`).

## Risk / semantics caveats

- **Range-instant `GROUP BY grid.id, grid.eval_ts`**: this collapses
  per-step across multiple raw samples, picking `argMax(value,
  timestamp)`. If `NeedTags=false`, the inner `GROUP BY` must still
  happen per series per step (otherwise samples from different
  series would collide on their step timestamp). So the *inner*
  group-by stays; only the *outer* `GROUP BY tags` is removed.
- **Multi-series collapse**: `sum(rate(http_requests_total[5m]))`
  has `NeedTags=false` because the outer `sum` drops all labels.
  The correct semantics is: compute `rate` per series, then sum
  across series at each step. The current empty-tags path in
  range-instant silently merges all series at the inner group-by
  (`grid.id, grid.eval_ts`): `argMax(value, timestamp)` over rows
  that now span multiple `grid.id` values would yield the wrong
  answer. **Critical**: the inner group-by must include `grid.id`
  to preserve per-series aggregation boundaries. Today this is the
  case (`selector_sql.go:246`). The fast path must not change this.
- **Matrix selector (range-vector used as `[5m]`)**: the outer
  `GROUP BY tags` collects all (timestamp, value) pairs per series.
  Collapsing without `GROUP BY` would merge all series into a
  single time_series array. That's semantically *wrong* unless the
  upstream operator explicitly wants a per-series-blind collapse.
- **When the outer *is* an aggregation with empty grouping**: this
  is the right time to collapse. The aggregation already uses
  `EmptyTagsArrayExpr` at its own level and groups the child output
  into a single-series result. Pushing the collapse into the
  selector is semantically equivalent.

So the fast path is only safe when **the immediate parent operator
also emits a single series**. The optimizer has this info through
`fragment.Aggregation.Grouping` being empty and
`fragment.Aggregation.Without == false`, plus an assertion that no
intervening operator preserves per-series identity.

- **range-vector that feeds `rate()` then aggregation**: rate
  preserves per-series identity (it's a `RangeFunction`), then the
  outer aggregation collapses. In this case the range-matrix
  selector does still need per-series rows because `rate` operates
  per series. `NeedTags=false` is fine because `rate` doesn't read
  tags, but the selector still needs to group by `id` (which it
  does via the join). The outer collapse fires only when the
  aggregation is directly above the selector, not above `rate`.

## Implementation sketch

1. Introduce a notion of "scalar-output selector" at the optimizer
   level: selector produces a single semantic series when
   `!NeedTags` *and* the immediate parent in the fragment tree is
   an aggregation with empty grouping, a `ScalarConvert`, or a
   top-level render that will collapse further. Capture as
   `SelectorSource.ScalarOutput bool` (separate from `NeedTags`).
2. Gate the SQL rewrite in `selector_sql.go` on `ScalarOutput`:
   - `buildRangeMatrixSelectorSourceSQL` (lines 290-329): when
     `ScalarOutput`, drop the outer `GROUP BY tags / ORDER BY
     tags`. The outer becomes a naked
     `SELECT emptyTags, arraySort(item->item.1,
     groupArray((timestamp, value))) AS time_series FROM (inner)`.
   - `buildRangeInstantSelectorSourceSQL` (lines 226-288): the
     *inner* group-by stays as-is (it's per-series per-step); the
     *outer* group-by drops. Outer becomes
     `SELECT emptyTags, arraySort(...) AS time_series
     FROM (inner_with_grid_id_grid_evalts_group)`.
   - `BuildRangeWindowSelectorQuerySQLWithFinalTags` (lines
     62-126): same treatment; the `perStep` stage stays, the
     `outer` drops `GROUP BY final_tags`.
3. Update `selectorSourceFromMatchers` and the renderer's
   `source.go:275-284` to propagate `ScalarOutput`.
4. Decide detection timing. Safest: in
   `applyFinalSQLShapingLateMaterialization`, after projection
   pushdown, walk fragments and set `Selector.ScalarOutput=true`
   when the ancestor chain up to the aggregation boundary has no
   per-series operator that would be lost by the collapse. Since
   the analysis already tracks lineage, the condition is
   `!NeedTags && directAncestorAggregation.Grouping is empty &&
   !directAncestorAggregation.Without && chain has no RangeFunction
   / Subquery / InfoJoin between selector and aggregation`.
5. Add a corresponding optimization report entry:
   `MaterializedColumns: ["time_series"]` plus a semantic-barrier
   tag like `scalar_collapse_fast_path`.

## Test coverage idea

- `storage/selector_sql_test.go`: add
  `TestBuildRangeMatrixSelectorSourceSQL_ScalarOutputOmitsGroupBy`.
  Build a selector with `NeedTags=false, ScalarOutput=true`, call
  `buildRangeMatrixSelectorSourceSQL`, and assert the output SQL:
  - Contains `CAST([], 'Array(Tuple(String, String))') AS tags`.
  - Does **not** contain `GROUP BY tags` at the outer level.
  - Does **not** contain `ORDER BY tags`.
  - Still contains `arraySort(item -> item.1, groupArray((timestamp,
     value)))` for the `time_series` column.
- `TestBuildRangeInstantSelectorSourceSQL_ScalarOutputKeepsInnerGroup`:
  assert the inner group-by still has `grid.id, grid.eval_ts` (per-
  series-per-step), but the outer group-by is gone.
- `optimizer_test.go`: `TestOptimizer_ScalarOutputDetection`. Plan
  `sum(rate(http_requests_total[5m]))` — the inner selector's
  `ScalarOutput` should be **true**. Plan
  `sum by (job) (rate(...))` — selector's `ScalarOutput` should be
  **false** (grouping is non-empty). Plan
  `sum(count_over_time(foo[5m]))` — `ScalarOutput` should be
  **true** for the absent/count_over_time range-window variant.
- Harness: include a micro-benchmark via `hyperfine` comparing the
  full-harness runtime before and after enabling this path, on a
  query set dominated by scalar aggregations like
  `sum(rate(metric[5m]))`. Harness runs in ~25s; the scalar-
  aggregation subset should measurably shrink when the hash-group
  is removed.
- Differential correctness: run the PromQL compliance suite
  against the optimized path; confirm results are byte-identical
  to the Prometheus engine for every scalar-collapsing query.
