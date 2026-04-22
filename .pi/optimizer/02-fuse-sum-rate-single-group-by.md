# 02 — Fuse `sum by (...) (rate(m[r]))` into a single GROUP BY pass

Eliminate the per-series `time_series` groupArray whenever a range-function
output is consumed by an additive outer aggregation. Today the range pipeline
materializes a sorted `(timestamp, value)` array per series, then the outer
`sum by (g)` pass `arrayJoin`s those arrays and groups again by `g`. When the
downstream is additive across series per-timestamp, the groupArray step is
pure work: the rows that were just grouped then sorted are about to be flattened
and re-grouped. Fusing the passes reduces memory (no per-series arrays), cuts
one scalar sort per series, and shrinks the intermediate row count from
`num_series × steps` to `num_groups × steps`.

## Problem

Concrete query:

```
sum by (code) (rate(http_requests_total[5m]))
```

Current range-mode SQL envelope (paraphrased from `BuildRangeWindowSelectorQuerySQLWithFinalTags`
in `internal/promshim/storage/selector_sql.go:62-126` followed by
`BuildRangeAggregationQuerySQLWithBounds` in `internal/promshim/storage/sql.go:136-184`):

```sql
-- stage 1: selector -> per-series windows
SELECT grid.tags AS tags, grid.eval_ts AS eval_ts,
       arraySort(item -> item.1, groupArray((d.timestamp, d.value))) AS window_series,
       …
FROM grid INNER JOIN timeSeriesData(...) d ON d.id = grid.id
WHERE …
GROUP BY grid.id, grid.tags, grid.eval_ts    -- per-series, per-step

-- stage 2: per-step rate using the per-series window
SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS final_tags,
       eval_ts AS timestamp,
       (counter_delta(window_values)) / (window_duration) AS value   -- rate(...)
FROM (stage 1)

-- stage 3: per-series groupArray
SELECT final_tags AS tags,
       arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (stage 2) GROUP BY final_tags

-- stage 4: sum by(code) re-flatten and re-group
SELECT arrayFilter(tag -> has(['code'], tag.1), tags) AS grouping_tags,
       point.1 AS timestamp, sum(point.2) AS value
FROM (stage 3) ARRAY JOIN time_series AS point
GROUP BY grouping_tags, timestamp

-- stage 5: outer groupArray
SELECT grouping_tags, arraySort(..., groupArray((timestamp, value)))
FROM (stage 4) GROUP BY grouping_tags
```

Cost model: `S` series, `T` steps, `P` points per window. Stage 1 hashes
`S × T` groups and arraySorts `S × T` small arrays. Stage 3 does the
`(timestamp, value)` groupArray of size `T` per series, then arraySort — this
array is consumed exactly once. Stage 4 immediately unpacks it row-by-row.

## Current behavior

- `internal/promshim/native/renderer/range.go:92-124` — the direct
  window-join path and the windowed-arrays path both end in
  `buildRangeFunctionOverWindowedArraysSQL` / `BuildRangeWindowSelectorQuerySQLWithFinalTags`,
  which unconditionally produce a `(final_tags, time_series)` shape with
  `arraySort(item -> item.1, groupArray((timestamp, value)))`.
- `internal/promshim/native/renderer/join.go:62-96` — `renderAggregationFragment`
  always receives the rendered child as a `(tags, time_series)` range matrix
  and ARRAY JOINs it back out via `renderAggregationRangeSourceSubquery`
  (`internal/promshim/storage/sql.go:269-?`).
- `internal/promshim/native/optimizer.go:211-218` — the announced
  `PassFunctionPatternRewrites` currently just returns. The catalog in
  `functionRewriteCatalog` advertises `sum(rate(...))` and
  `sum by(...) (rate(...))` but there is no rewrite behind them.

## Proposed technique

Introduce a fragment-layer pass (named `PassAggregationRangeFunctionFusion`)
that matches the tree shape

```
Aggregation{Op in additiveOps, Grouping G, Without w}
  -> RangeFunction{Func F}
       -> LeafSource{RangeVector, selector S}
```

and produces a new `FragmentKindFusedAggRange` with fields `{AggOp, Grouping,
Without, Func, Selector}`. The renderer lowers that fragment directly to:

```sql
WITH
  grid AS (grid_ts × matched_series),            -- unchanged
  window AS (
    SELECT grouping_tags, eval_ts, <F>(value_array, ts_array) AS per_series_value
    FROM grid INNER JOIN data ON data.id = grid.id
    WHERE <temporal bounds>
    GROUP BY grid.id, grouping_tags, grid.eval_ts
  )
SELECT grouping_tags AS tags,
       arraySort(item -> item.1, groupArray((eval_ts, <AggOp>(per_series_value)))) AS time_series
FROM (
  SELECT grouping_tags, eval_ts, <AggOp>(per_series_value) AS value
  FROM window
  GROUP BY grouping_tags, eval_ts
)
GROUP BY tags
```

Where `grouping_tags` is derived once via
`buildAggregationTagsExpr(series.tags, G, w)` and inlined into stage 1. The
series-id GROUP BY stays (we must keep counter lineage inside the rate
computation), but the per-series `groupArray` of `(timestamp, value)` pairs
disappears. The output of the window stage is a single float per `(series,
eval_ts)`; the next aggregation groups by `(grouping_tags, eval_ts)` directly.

**Which aggregations are safe to fuse?** Additive-per-timestamp in the sense
that "apply F to each series, then combine" matches "combine the series'
window inputs, then apply F":

- `sum`: yes — sum of rates == rate-of-sum *only when all series are observed
  over an identical grid and extrapolation factors agree*. We are NOT claiming
  that here; we keep the rate computation per series and then sum the
  per-series rate outputs. That is exactly what Prom does; no semantics change.
- `avg`: yes — `avg(per-series-rate) = sum(per-series-rate) / count(per-series-rate)`.
  Encode as `avg` in the outer GROUP BY.
- `count`: yes — `count(series with a defined per-series-rate at ts)`.
- `group`: yes — the operator discards values; only the grouping survives.
- `min` / `max`: yes per-timestamp.
- `stddev` / `stdvar` / `quantile`: yes per-timestamp. They only need the
  per-series scalar at `eval_ts`, not the full `(timestamp, value)` history.
- `topk` / `bottomk` / `limitk`: see doc 04 — selection aggregations need the
  per-series *identity* of the value and a different fusion shape.
- `count_values`: not a fusion target; it synthesizes label values from
  per-series values.

What we MUST keep per-series: the rate/irate/increase/delta/deriv window
computation itself. Each series has its own counter lineage; summing raw
counter values across series and then computing the rate is *wrong* because
resets on one series do not imply a reset on the joined counter. The fusion
preserves the per-series window pass and only collapses the terminal
`groupArray` and its re-flatten.

## Expected gain

- Rows materialized between stages drops from `Θ(S × T)` tuples
  (S = series, T = steps) in the per-series array payload to `Θ(G × T)`
  aggregated rows (G = output group count). Typical `sum by (code)` on a
  metric with 5k series and 3 codes: 5000×T → 3×T. ~1000× reduction.
- One fewer arraySort per series. For 5k series × 300 steps that is 5000
  sorts of length 300 each avoided.
- ClickHouse's aggregating query tree can parallelise the single fused
  aggregation; today it must serialise stages 3→4 because stage 3's output
  row grain is `(tags, time_series[])` with the array as a single large cell.
- Memory for the intermediate is bounded by distinct `grouping_tags` rather
  than distinct series.

## Risk / PromQL semantics caveats

- **Counter lineage**: keep the per-series window GROUP BY; never collapse it
  before `rate()` runs.
- **Extrapolation factor**: `extrapolationFactorSQL` (range.go:222-241) uses
  `arrayElement(ts, 1)` / `arrayElement(ts, length)`. Those come from the
  per-series window_series, not from the global series — unchanged by the
  fusion.
- **Stale NaN**: already filtered in the storage WHERE clause
  (`staleNaNFilterSQL`); no change needed.
- **Empty series at a step**: when a series contributes <= 1 sample to the
  window, the current pipeline filters it via
  `length(window_series) > minimumSeriesLength`. The fused shape must apply
  the same predicate to the per-series `per_series_value` before the outer
  GROUP BY (NaN-gate on the scalar), otherwise `sum` would include phantom
  NaNs.
- **`sum` + NaN handling**: the existing `buildAggregationValueExpr` emits
  `if(countNaN > 0, NULL, sum(value))` for Prom's "NaN poisons the group"
  semantics. That predicate lives at the outer aggregation — unchanged.
- **`without` grouping**: when the aggregation is `without(label_x)`,
  `grouping_tags` depends on *all* other labels. The selector must project
  full tags for the series (`selector.NeedTags=true`, `RequireFullTags=true`).
  Fusion is still valid; only the tag projection shape changes. The optimizer
  already handles this via `applySelectorProjection`.
- **`rate(m[r]) / rate(n[r])` not eligible**: fusion requires an additive
  outer aggregation, not a binary op.

## Implementation sketch

1. New fragment kind `FragmentKindFusedAggRange`, added to `native/fragment.go`
   alongside an `AggRangeFragment` struct `{AggOp, Grouping, Without, Fn,
   ParamNumber, Selector}`.
2. New optimizer pass `PassAggregationRangeFunctionFusion`, inserted between
   `PassFunctionPatternRewrites` and `PassJoinNormalizationDuplicateDetection`.
   The pass pattern-matches `Aggregation -> RangeFunction -> LeafSource` where
   `Aggregation.Op` is in the additive set above and produces the fused
   fragment. Other shapes pass through untouched.
3. `OptimizationReport.AppliedRewrites` gains `"sum_rate_fused_group_by"` so
   planner tests can assert fusion happened.
4. New storage helper
   `BuildRangeFusedAggRangeWindowSelectorQuerySQL(cfg, selector, aggOp,
   grouping, without, fn, paramNumber, startMS, endMS, stepMS, requiredStartMS,
   requiredEndMS)`. Internally it reuses `buildMatchedSeriesSQL` and the grid
   builder; the key change is replacing the `perStep` subquery's output with
   the per-series scalar and adding a single outer `GROUP BY grouping_tags,
   eval_ts` stage.
5. Renderer: new `renderFusedAggRangeFragment` that routes to the new storage
   helper. Instant mode can use a simpler variant that computes the scalar at
   a single `eval_ts` and skips the grid entirely — fall back to the existing
   path if we don't want two implementations for v1.
6. Projection-pushdown pass: when the fused fragment exists, require only
   `grouping` labels from `series.tags`, identical to the non-range
   aggregation case (already handled by `applySelectorProjection`).

## Test coverage idea

- Unit (optimizer): `TestFuseSumRateRewrite` — build a plan for
  `sum by (code) (rate(http_requests_total[5m]))`, assert the resulting
  fragment is `FragmentKindFusedAggRange` with the expected `{AggOp=SUM,
  Grouping=[code], Fn=rate}` and that `Report.AppliedRewrites` contains
  `"sum_rate_fused_group_by"`.
- Unit (optimizer): `TestFuseAvgRate`, `TestFuseCountRate` mirror.
- Unit (negative): `TestFuseDoesNotApplyToTopkRate`,
  `TestFuseDoesNotApplyToBinaryOp`, `TestFuseDoesNotApplyToSubqueryChild`.
- Golden-SQL: `TestBuildFusedSumRateSQL` — rendered SQL contains a single
  outer `GROUP BY grouping_tags, eval_ts` and does NOT contain a per-series
  `arraySort(…groupArray((timestamp, value)))` in the intermediate stage.
- Integration (harness): run `sum by (code) (rate(http_requests_total[5m]))`
  through the existing native harness under both the legacy path (feature
  flag off) and the fused path; assert bitwise-equal (or per-point tolerant)
  output.
- Bench: hyperfine against the harness for a workload with S=5000 series /
  G=3 groups / T=300 steps; expect wall-clock and peak memory to drop.
