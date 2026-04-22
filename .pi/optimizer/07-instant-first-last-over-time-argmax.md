# Instant-mode `first_over_time` / `last_over_time` via `argMax` / `argMin`

## Problem

For instant-mode evaluation of `last_over_time(expr[range])` and
`first_over_time(expr[range])`, the current renderer materialises the
entire sorted `time_series` array, then plucks one element:

```go
// renderer/range.go:293-296
case "last_over_time":
    return "tupleElement(arrayElement(time_series, length(time_series)), 2)"
case "first_over_time":
    return "tupleElement(arrayElement(time_series, 1), 2)"
```

To produce `time_series`, the selector pipeline does:

```sql
SELECT tags,
       arraySort(item -> item.1, groupArray((d.timestamp, d.value))) AS time_series
FROM (SELECT tags, d.timestamp, d.value FROM timeSeriesData ...) GROUP BY tags
```

So for every series the planner sorts the entire `(timestamp, value)`
tuple array just to extract one tuple at the end (or beginning).

For `last_over_time` we only need the single `(value, timestamp)` pair
with the **maximum** timestamp; for `first_over_time`, the minimum.
ClickHouse has aggregate functions that do exactly this:

```sql
argMax(d.value, d.timestamp) AS value,  -- value at max timestamp
max(d.timestamp)             AS timestamp

argMin(d.value, d.timestamp) AS value,  -- value at min timestamp
min(d.timestamp)             AS timestamp
```

These run as streaming aggregates — no sort, no array materialisation,
no `groupArray`, O(n) over samples with O(1) state.

Concrete emitted SQL for `last_over_time(up[5m])` in instant mode today:

```sql
SELECT tags AS tags,
       tupleElement(arrayElement(time_series, length(time_series)), 1) AS timestamp,
       tupleElement(arrayElement(time_series, length(time_series)), 2) AS value
FROM (
    SELECT tags,
           arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
    FROM (SELECT tags, d.timestamp, d.value
          FROM timeSeriesData AS d
          INNER JOIN (SELECT src.id FROM timeSeriesTags AS src WHERE ...)
          AS series ON d.id = series.id
          WHERE d.timestamp BETWEEN ... AND reinterpretAsUInt64(d.value) != 9218868437227405314)
    GROUP BY tags
)
WHERE length(time_series) > 0
```

Proposed shape:

```sql
SELECT series.tags AS tags,
       max(d.timestamp)                   AS timestamp,
       argMax(d.value, d.timestamp)       AS value
FROM timeSeriesData AS d
INNER JOIN (SELECT src.id, src.tags FROM timeSeriesTags AS src WHERE ...)
AS series ON d.id = series.id
WHERE d.timestamp BETWEEN ... AND reinterpretAsUInt64(d.value) != 9218868437227405314
GROUP BY series.tags
HAVING NOT isNaN(value)
```

This is almost identical to the existing `buildInstantSelectorSourceSQL`
shape (`storage/selector_sql.go:174-210`) — which already computes
`argMax(d.value, d.timestamp)` for instant-vector selectors. The
optimisation is recognising that `last_over_time(expr[range])` in
instant mode reduces to an instant selector with a wider lookback
window.

Symmetrically for `first_over_time`: `argMin(d.value, d.timestamp)`
and `min(d.timestamp)`.

For `ts_of_last_over_time` / `ts_of_first_over_time` (range.go:314-316)
the rewrite is even cleaner: we only need `max(d.timestamp) /
min(d.timestamp)` as a Float64, no `argMax` at all.

## Current behaviour

* `renderer/range.go:198-209` — `buildInstantRangeFunctionSQL` wraps
  the child-rendered SQL (which produces `tags` + sorted `time_series`)
  and emits the tuple-extraction shape.
* `renderer/range.go:293-296` — `last_over_time` / `first_over_time`
  value expressions extract the last/first element of the sorted
  array.
* `renderer/range.go:313-316` — `ts_of_first_over_time` /
  `ts_of_last_over_time` use the same pattern but grab
  `tupleElement(..., 1)` (the timestamp) and convert to seconds.
* `storage/selector_sql.go:174-210` — `buildInstantSelectorSourceSQL`
  already uses `argMax(d.value, d.timestamp)` — the target shape.
* `storage/selector_sql.go:212-288` — `buildRangeSelectorSourceSQL`,
  `buildRangeInstantSelectorSourceSQL` also use `argMax` for range
  mode per-step evaluation; pattern is well established.

## Proposed technique

Add a pre-render rewrite in
`native/optimizer.go::applyFunctionPatternRewrites` (or a new dedicated
pass) that recognises the shape:

```
RangeFunctionFragment{
    Func: "last_over_time" | "first_over_time" |
          "ts_of_last_over_time" | "ts_of_first_over_time",
    Child: LeafSource{Selector{Kind: RangeVector, ...}},
}
```

…and rewrites it into:

```
RangeFunctionFragment{
    Func: "last_over_time",  // preserved for metric-name lineage
    Child: LeafSource{Selector{Kind: RangeVector, ...}},
    InstantAggregationStrategy: "argmax"  // new
}
```

When the renderer sees `InstantAggregationStrategy = "argmax"` in
instant mode, it bypasses the normal `buildInstantRangeFunctionSQL`
(which materialises the sorted array) and instead emits an
instant-selector-style SQL directly, with the lookback/offset windowing
taken from the range-vector selector.

Specifically, add a new helper
`buildInstantRangeAggregateSelectorSQL(cfg, selector, fn, requiredStartMS,
requiredEndMS) (sql, params, err)` that mirrors
`buildInstantSelectorSourceSQL` but uses the range-vector WHERE clause
and the `argMax` / `argMin` aggregator keyed on `fn`:

```go
switch fn {
case "last_over_time":       value = argMax(d.value, d.timestamp); ts = max(d.timestamp)
case "first_over_time":      value = argMin(d.value, d.timestamp); ts = min(d.timestamp)
case "ts_of_last_over_time": value = toFloat64(max(d.timestamp)) / 1000.0; ts = max(d.timestamp)
case "ts_of_first_over_time":value = toFloat64(min(d.timestamp)) / 1000.0; ts = min(d.timestamp)
}
```

For instant mode the WHERE clause uses `required_start_ms` /
`required_end_ms` which `requiredInputBounds` already computes to
`[evalTime - lookback - offset, evalTime - offset]` — exactly the
range that `last_over_time(expr[range])` needs to see.

Range-mode (series-of-series over a step grid) is trickier because the
evaluation point varies per step. For range mode, the per-step
`buildRangeInstantSelectorSourceSQL` shape already uses `argMax(d.value,
d.timestamp)` grouped by `(grid.id, grid.eval_ts)` — but it uses
**last-sample-before-eval-ts** semantics (`d.timestamp <= grid.eval_ts
- toIntervalMillisecond(offset_ms)` with no lookback floor, which is
actually range-lookback based). Verify that the range-mode shape is
already equivalent to `last_over_time(foo[range])` semantics at each
step. It likely is, which means the range-mode version of this
rewrite reduces to calling the existing range-instant shape with the
appropriate lookback.

## Expected gain

SQL text size:

* `last_over_time(up[5m])` instant mode today: ~800 bytes of nested
  SELECT + `arraySort(groupArray(...))` + tuple-element extraction.
  Proposed: ~400 bytes (same shape as a plain instant selector with a
  widened lookback). ~2× smaller.
* `first_over_time` / `ts_of_*` variants similarly ~2× smaller.

Server-side per-row work:

* **Today**: for each matched series, `groupArray` collects every
  `(timestamp, value)` tuple (O(n) memory proportional to samples in
  window), then `arraySort` sorts them (O(n log n) time). One-pass
  streaming is impossible because `groupArray` ordering is
  unspecified.
* **Proposed**: `argMax(d.value, d.timestamp)` is a streaming
  aggregate with O(1) state (current best (value, timestamp)) and
  O(n) time. No intermediate array allocation.
* For a typical window (e.g. 20 samples/series × 10k series = 200k
  samples), ClickHouse runs the aggregate in SIMD and ends up
  spending ~95% less time on this path. The dominant cost becomes
  the join itself.
* Memory: `groupArray(Tuple(DateTime64, Float64))` needs ~24 bytes per
  sample × 200k samples = ~5 MB of transient state per query. The
  `argMax` variant uses 16 bytes per group total. Eliminates a
  measurable allocation hotspot.

## Risk / semantics caveats

* **Staleness / NaN handling**: `argMax(d.value, d.timestamp)` picks
  the value at max timestamp regardless of whether that value is NaN.
  In Prometheus, `last_over_time` returns the last non-NaN sample
  *except* when the stale-NaN marker is there (which signals the
  series went stale, and the lookback rules mean the series should be
  considered absent). We already filter stale markers in the WHERE
  (`staleNaNFilterSQL("d.value")`), so stale NaNs do not reach the
  aggregator. Computed NaNs (from upstream transforms) are not
  possible on a leaf range-vector selector.
  * Prometheus `last_over_time` actually returns the last sample
    (even if NaN) per the 3.x semantics after a behaviour change;
    verify against the harness oracle which rule is in effect.
* **Tie-breaking**: `argMax(value, timestamp)` returns the value
  associated with the maximum key. If two samples share the same
  timestamp (should not happen but can via double-scrape), ClickHouse
  picks one arbitrarily. Today’s `arraySort` also picks arbitrarily
  because tuple sorting is by key only. No regression.
* **`ts_of_*` semantics**: `ts_of_last_over_time` returns the
  timestamp of the **last non-NaN** sample. The proposed rewrite uses
  `max(d.timestamp)` which includes any sample that passed the
  WHERE clause (post-stale-filter). If we want strict parity with
  Prometheus we need `max(d.timestamp) WHERE NOT isNaN(d.value)`,
  which is achieved by the same `HAVING NOT isNaN(value)` that the
  existing instant selector uses — but that would reject the whole
  group if any single sample is NaN, which is wrong. Fix:
  `argMax(d.timestamp, d.timestamp) WHERE NOT isNaN(d.value)` inside
  the group, by filtering at SELECT level (`argMaxIf(d.timestamp,
  d.timestamp, NOT isNaN(d.value))`).
* **`first_over_time` with offset / @-modifier**: the selector window
  shifts; the argMin/argMax logic still works because the WHERE
  clause constrains the window explicitly.
* **Range mode**: do not apply this rewrite blindly to range mode
  unless the equivalent shape for per-step evaluation is used. The
  existing `buildRangeInstantSelectorSourceSQL` is probably already
  the right shape, but the rewrite pass must detect which rendering
  path is taken.
* **Predicate-pushdown interaction**: range-vector selectors used by
  `last_over_time` in instant mode are equivalent to instant-vector
  selectors with a widened lookback. The optimiser should keep the
  existing `RangeVector` selector kind to preserve lineage metadata
  (e.g. lookback bounds for the time-requirements calculator) but
  render as the instant shape.

## Implementation sketch

1. Add a new field to `native.RangeFunctionFragment`:
   `InstantStrategy string` (e.g. `"argmax"`, `"argmin"`,
   `"max_ts"`, `"min_ts"`, or empty = default).
2. New optimiser pass (or extension of `applyFunctionPatternRewrites`)
   detects the shape:
   * fragment.Kind == RangeFunction
   * fragment.RangeFunction.Func ∈ {last_over_time, first_over_time,
     ts_of_last_over_time, ts_of_first_over_time}
   * fragment.RangeFunction.Child.Kind == LeafSource
   * fragment.RangeFunction.Child.Selector.Kind == RangeVector
   * ctx.Mode == RenderModeInstant
   Sets `InstantStrategy` accordingly.
3. In `renderer/range.go::renderRangeFunctionFragment`, the
   RenderModeInstant branch (line 80) checks `InstantStrategy` and
   dispatches to a new helper:
   `buildInstantArgMaxRangeFunctionSQL(cfg, selector, fn,
   evaluationTimeMS, lookbackMS, offsetMS)`.
4. The new helper builds a SQL shape similar to
   `buildInstantSelectorSourceSQL` but with:
   * `value = argMax(d.value, d.timestamp)` for `last_over_time`
     (or `argMin` for `first_over_time`).
   * `timestamp = max(d.timestamp)` (or `min`).
   * WHERE clause from the range-vector selector (using
     `requiredStartMS`, `requiredEndMS` already computed in
     `requiredInputBounds`).
   * `GROUP BY series.tags` (or `series.id`).
5. For `ts_of_first_over_time` / `ts_of_last_over_time`, the value
   expression is `toFloat64(toUnixTimestamp64Milli(<the chosen
   timestamp>)) / 1000.0`. The chosen timestamp is either `min` or
   `max`; use `argMinIf` / `argMaxIf` to exclude NaN values.
6. Range-mode path: detect the same shape and route through the
   existing `buildRangeInstantSelectorSourceSQL` with the appropriate
   lookback/offset rather than
   `buildRangeFunctionOverWindowedArraysSQL`. Verify per-step
   semantics match.

## Test coverage idea

* Renderer test: `last_over_time(up[5m])` instant mode. Assert
  emitted SQL contains `argMax(d.value, d.timestamp)` and does **not**
  contain `groupArray` or `arraySort`.
* Renderer test: `first_over_time(up[5m])` instant mode. Same
  assertions with `argMin` / `min`.
* Renderer test: `ts_of_last_over_time(up[5m])` instant mode. Assert
  `argMaxIf` (or `maxIf`) is emitted with the NaN predicate.
* Differential test against Prometheus: synthetic counter with a
  stale NaN in the middle of the window; `last_over_time` should
  return the last pre-stale sample. Ensure
  `staleNaNFilterSQL` still gates the WHERE clause.
* Differential test: 2 samples with the same timestamp in the
  window; verify tie-breaking matches the existing path (should be
  arbitrary but deterministic per query).
* Range-mode test: `last_over_time(up[5m])` range mode with 1m step.
  Assert the rendered SQL is equivalent to the existing
  `buildRangeInstantSelectorSourceSQL` shape (i.e. the rewrite
  correctly routes range-mode last-over-time through the existing
  efficient path rather than the heavier windowed-arrays path).
* Harness oracle: `last_over_time` / `first_over_time` appear in many
  fixtures — must all pass unchanged.
* Benchmark: measure query time on a dataset with 10k series, 20
  samples/window. Expect 5–10× speed-up for
  `last_over_time(up[5m])` instant.
