# 03 — `count by (...)` direct from series-ID cardinality

`count(m)` and `count by (label) (m)` ask a question that is answered by the
`time_series_tags` subtable alone. The value column, the `time_series_data`
subtable, the grid join, the per-step per-series arrayJoin — none of it is
necessary. Route those queries to a series-cardinality query on
`time_series_tags` and avoid the value pipeline entirely.

## Problem

Concrete queries:

- instant: `count(up)`
- instant with by: `count by (job) (up)`
- range: `count by (namespace) (kube_pod_info)`

Current SQL envelope (for the range case) today threads through:

1. `buildMatchedSeriesSQL` — `SELECT src.id, tags FROM timeSeriesTags(...)
   WHERE metric_name = 'kube_pod_info' AND src.max_time >= … AND
   src.min_time <= …`
2. A grid `CROSS JOIN` per series per step
   (`buildRangeInstantSelectorSourceSQL` in `selector_sql.go:226-288`)
3. `INNER JOIN timeSeriesData d ON d.id = grid.id` with the step-window
   predicate and `argMax(d.value, d.timestamp)` per `(id, eval_ts)`
4. `GROUP BY tags` + `arraySort(item->item.1, groupArray(...))` into the
   range matrix
5. Outer `renderAggregationRangeSourceSubquery` ARRAY JOINs the matrix back
   out
6. Outer `GROUP BY grouping_tags, timestamp` with
   `toFloat64(count(point.2))` from `buildAggregationValueExpr` (`sql.go:730`)

Cost model: every value row in the lookback envelope is read and
`argMax`-reduced, even though the final answer is "did this series have a
non-stale sample in this step?" ClickHouse scans the data table proportional
to (series × samples_in_envelope). For high-cardinality metrics (Kubernetes
kube_pod_info at a few hundred pods × a few dozen labels × dashboards
sweeping a multi-hour range) this is a large fraction of the total read.

For the instant case the waste is the same with T=1: we still scan
`time_series_data` when we only need to know which series are live in the
selector window.

## Current behavior

- `internal/promshim/native/renderer/join.go:62-124` — `renderAggregationFragment`
  dispatches by `Aggregation.Op`. `parser.COUNT` flows through
  `buildAggregationValueExpr` (`internal/promshim/storage/sql.go:730-731`) which
  emits `toFloat64(count(value))`. There is no short-circuit that inspects
  whether the outer op is COUNT before building the full value pipeline.
- `internal/promshim/storage/selector_sql.go:290-329` — `buildRangeMatrixSelectorSourceSQL`
  still joins on `timeSeriesData` even when the caller only needs presence.
- `internal/promshim/native/optimizer.go:211-218` —
  `applyFunctionPatternRewrites` body is a no-op.

## Proposed technique

A new fragment-layer pass `PassCountCardinalityPushdown` that matches
`Aggregation{Op=COUNT, Grouping=G, Without=w} -> LeafSource{InstantVector,
selector}` (plus the range-mode twin where the leaf is still an InstantVector
source, i.e. no `rate`/`sum_over_time` wrapper) and rewrites it into a
`FragmentKindCardinalityCount` fragment carrying `{Grouping, Without,
Selector, Mode}`.

The renderer lowers the fragment directly against `timeSeriesTags` as
follows.

**Instant mode:**

```sql
SELECT
  grouping_tags AS tags,
  fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp,
  toFloat64(count()) AS value
FROM (
  SELECT arrayFilter(tag -> has([<g1>,<g2>,…], tag.1),
                     arrayConcat([tuple('__name__', metric_name)], tagsAsTuples))
         AS grouping_tags
  FROM timeSeriesTags(db, table) AS src
  WHERE <matchers> AND src.max_time >= fromUnixTimestamp64Milli({eval_minus_lookback_ms:Int64})
                  AND src.min_time <= fromUnixTimestamp64Milli({evaluation_ms:Int64})
) AS series
GROUP BY grouping_tags
ORDER BY grouping_tags
```

**`count(m)` (no grouping labels):** collapses to `count(DISTINCT id)` /
`count()` (whichever ClickHouse costs cheaper on the matched rows) with
`grouping_tags = []`. Since each row of `timeSeriesTags` already represents
one distinct series, `count()` is sufficient.

**Range mode:** when a series is live only for part of the outer `[start,
end]` envelope it should contribute to the count at exactly the steps where
it has a non-stale sample. We can answer this from `time_series_tags` if the
subtable carries `min_time` / `max_time` (it does — already used in the
time-overlap filter at `selector_sql.go:383`). For each step `eval_ts`, a
series is live if
`max_time >= eval_ts - lookback` AND `min_time <= eval_ts`. That maps to

```sql
SELECT grouping_tags AS tags,
       arraySort(item -> item.1, groupArray((eval_ts, toFloat64(count())))) AS time_series
FROM (
  SELECT grouping_tags, eval_ts
  FROM series CROSS JOIN grid
  WHERE series.max_time >= grid.eval_ts - toIntervalMillisecond({lookback_ms:Int64})
    AND series.min_time <= grid.eval_ts
  -- no join on time_series_data
) GROUP BY grouping_tags, eval_ts
```

This is still a `series × steps` cross product, but *without* the
`time_series_data` join. The join with the data table was the expensive part;
the grid join against the (small) tags projection is cheap.

Stale-NaN wrinkle for range mode: `time_series_tags` tracks observed-time
extents, not the "last non-stale timestamp before stale marker". For most
series the distinction is immaterial (Prom's stale markers are explicit and
happen seldom). For workloads where it matters we can gate the rewrite
behind a safety flag that disables range-mode cardinality pushdown but keeps
the instant form, which is exact.

## Expected gain

- Instant `count(m)`: the rewritten query touches only `timeSeriesTags`
  rows whose matchers match, which is typically a few hundred KB even for
  broad matchers. The legacy path reads `time_series_data` over the full
  instant lookback (default 5m). Typical I/O reduction: 50–500× on the data
  table, which dominates scan cost.
- Range `count by (label) (m)`: the number of bytes scanned against
  `time_series_data` goes to zero. Bytes scanned against `time_series_tags`
  grows linearly with matched series × 1 (not × samples_in_window). Expected
  10–100× reduction in total bytes read depending on sample density.
- Memory: the peak intermediate size drops from `matched_series ×
  samples_in_envelope` floats to `matched_series × 1` tuples.
- Planner savings: ClickHouse's `time_series_tags` subtable is typically
  small enough to sit in the filesystem page cache; the value table is not.

## Risk / PromQL semantics caveats

- **Stale NaN propagation (range mode)**: `min_time` / `max_time` in
  `time_series_tags` reflect sample timestamps including any stale-marker
  sample. That is actually correct for `count`: Prometheus counts a series
  at step `t` if it has any sample in `(t - lookback, t]`. If the last
  sample is a stale marker, Prom does NOT count the series — but PromQL's
  `count(m{})` at step `t` *does* count a series whose newest sample inside
  the lookback is a stale NaN, because the stale logic applies to value
  reads, not to presence. Verify against
  `promql-engine`/`pkg/promql/engine.go`'s staleness handling — if we're
  wrong, we must keep a cheaper value-probe (e.g. `count(value) > 0`) that
  still avoids argMax.
- **Matchers with value-level predicates**: `count(m == 5)` is *not*
  `count(m)`. The rewrite applies only when the outer COUNT wraps a pure
  instant-vector selector with no value-side filtering between it and the
  selector (no `ValueTransform` fragment wrapping the leaf).
- **`without` grouping**: `count without (foo) (m)` requires reading every
  tag except `foo`. Selector projection pass must set
  `RequireFullTags=true`; the rewrite is still valid.
- **`__name__` lineage**: `count by (__name__) (m)` is rare but legal; the
  grouping-tags expression must include `__name__` in the allowed list.
- **Empty matchers**: `count({job="x"})` with no metric name — the
  `time_series_tags` path handles this; the existing `buildMatchedSeriesSQL`
  already emits clauses for tag-only matchers.
- **COUNT_VALUES exclusion**: `count_values` is not the same operator —
  keep the existing pipeline for `parser.COUNT_VALUES`.

## Implementation sketch

1. New fragment kind `FragmentKindCardinalityCount` with fields
   `{Grouping, Without, Selector, IsRange}`.
2. Optimizer pass `PassCountCardinalityPushdown` placed after
   `PassFunctionPatternRewrites` and before
   `PassFinalSQLShapingLateMaterialization`. Match on:
   - Root fragment is `FragmentKindAggregation` with `Op == parser.COUNT`.
   - `Aggregation.Source.Kind == FragmentKindLeafSource` with a selector of
     kind `SelectorKindInstantVector`.
   - Source wrapper is the identity (`sourceWrapperIsIdentity`).
   - No `ValueTransform` / `ClampTransform` / `LabelTransform` between the
     aggregation and the leaf (inspect the same helper set used by
     `containsAggregationBoundary`).
3. Storage helper `BuildCountCardinalityQuerySQL(cfg, selector, grouping,
   without, instantMS, startMS, endMS, stepMS, requiredStartMS, requiredEndMS)`
   returning a query that reads only `timeSeriesTags`. Reuse
   `buildMatchedSeriesSQL` with `addTimeOverlap=true` (already does
   max_time/min_time bounding).
4. Renderer case in `renderFragment` (`renderer.go:45-78`) for the new kind,
   dispatching to the storage helper. Both `RenderModeInstant` and
   `RenderModeRange` are handled in a single renderer function.
5. Report: add
   `AppliedRewrites = append(..., "count_cardinality_pushdown")`.
6. Feature-flag path (`QueryConfig`) to disable the rewrite if a regression
   is found in the field; this matches the existing `preferDirectSelectorWindowJoin`
   knob.

## Test coverage idea

- Unit (optimizer): `TestPushdownCountInstant` — plan for `count(up)`
  produces `FragmentKindCardinalityCount`.
- Unit (optimizer): `TestPushdownCountBy` — `count by (job) (up)` lowers
  correctly with `Grouping=[job]`.
- Unit (optimizer): `TestPushdownCountWithoutValueFilterSkipped` — if the
  AST is `count(up > 0)` (a COUNT wrapping a binary-comparison fragment),
  the rewrite does NOT fire.
- Golden SQL: assert the emitted query contains no `timeSeriesData(`
  reference and no `argMax(d.value,`.
- Integration (harness): run `count(up)`, `count by (job) (up)`, and
  `count(kube_pod_info)` against the native harness; assert identical
  results to the legacy pipeline (both on a live metric and on a metric
  with no matching series — should return `0` for instant / empty range
  matrix for range with the existing `EmitZeroOnEmpty` wrapper).
- Integration (harness): run `count(absent(m))` to ensure the absent
  fragment (doc 05) is not accidentally captured by this rewrite.
- Bench: hyperfine `count by (namespace) (kube_pod_info)` with a harness
  fixture of ~2000 series × 1h range @ 15s step, measure wall-clock and
  bytes-read reported by ClickHouse `system.query_log`.
