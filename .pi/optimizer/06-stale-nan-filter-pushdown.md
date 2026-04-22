# Stale-NaN filtering: push down, drop per-row `NOT isNaN(v)` lambdas

## Problem

Prometheus writes a distinct NaN bit pattern (`0x7FF0000000000002`) to
mark a series as "stale" — the target stopped exposing it. Storage
already knows how to filter these out:

```go
// internal/promshim/storage/staleness.go
func staleNaNFilterSQL(column string) string {
    return "reinterpretAsUInt64(" + column + ") != 9218868437227405314"
}
```

This filter is applied **only** for range-vector selectors used by the
direct-window-join and the basic range-matrix selector shapes
(`storage/selector_sql.go:107` and `:313`). Other paths do not apply it:

* `buildRangeInstantSelectorSourceSQL` uses `argMax(d.value,
  d.timestamp)` plus `HAVING NOT isNaN(value)` (line 272). This rejects
  **all** NaN including stale markers — correct for instant selectors,
  so stale-NaN filtering is effectively in place there.
* `buildInstantSelectorSourceSQL` — same pattern, `HAVING NOT
  isNaN(value)` (line 202). Same observation.
* The materialize-then-window path
  (`renderer/range.go::buildWindowedArraysSourceSQL`) builds
  `window_series` from a `source.time_series` column that was
  materialised earlier. It does not apply any stale-NaN filter; the
  upstream selector for the materialize path uses
  `buildRangeMatrixSelectorSourceSQL` which **does** apply the filter
  at `staleNaNFilterSQL("d.value")`. So the stale markers are already
  gone by the time the renderer sees the array.

Meanwhile, `rangeFunctionValueExpr` applies a per-row `arrayFilter(v ->
NOT isNaN(v), values)` on top of the already-stale-filtered array, for
every aggregation that needs a finite-values subset (sum, avg, min,
max, stddev, stdvar, mad, quantile, resets).

This per-row filter is redundant for **stale** NaN because storage
already removed them. It is **not** redundant for computed NaN (e.g. a
`sqrt(-1)` upstream or an `/0` division in a transform), because those
can appear as legitimate NaN values in the series after the selector.

The problem is the code conflates both:

```go
finiteValues := arrayFilter(v -> NOT isNaN(v), values)
```

This is pessimistic: it assumes any path could have produced a computed
NaN. In practice, for the range-vector selector paths (the only place
range functions consume), the input goes through `staleNaNFilterSQL`,
and computed-NaN values are only possible if a subquery / range-vector
expression produces them — which is rare and identifiable at the plan
level.

Concretely for `avg_over_time(up[5m])` range mode direct-window, the
emitted SQL today:

```sql
if(
  arrayExists(v -> isNaN(v), window_values)
  OR length(arrayFilter(v -> NOT isNaN(v), window_values)) = 0,
  nan,
  arrayAvg(arrayFilter(v -> NOT isNaN(v), window_values))
)
```

The two `arrayFilter(v -> NOT isNaN(v), window_values)` passes are
unnecessary if we can prove `window_values` contains no NaN. Since
`staleNaNFilterSQL` runs in the storage-layer WHERE clause and the
selector is a raw `timeSeriesData` read, we know the values are
finite. We can simplify to:

```sql
if(length(window_values) = 0, nan, arrayAvg(window_values))
```

And for `sum_over_time`:

```sql
-- today
if(arrayExists(v -> isNaN(v), window_values), nan,
   arraySum(arrayFilter(v -> NOT isNaN(v), window_values)))
-- proposed (when NaN-free can be proved)
arraySum(window_values)
```

The `hasNaN` guard is also redundant under the same proof.

## Current behaviour

* `storage/staleness.go` — defines the staleness-marker bit filter.
* `storage/selector_sql.go:107, 313` — applies `staleNaNFilterSQL` in
  the WHERE clause of `BuildRangeWindowSelectorQuerySQLWithFinalTags`
  and `buildRangeMatrixSelectorSourceSQL`.
* `storage/selector_sql.go:202, 272` — `HAVING NOT isNaN(value)` on
  instant and range-instant selectors (rejects all NaN).
* `renderer/range.go:263-264` — `hasNaN` and `finiteValues` built
  unconditionally for each range function that uses them.
* `renderer/range.go:297-344` — `sum_over_time`, `avg_over_time`,
  `min_over_time`, `max_over_time`, `stddev_over_time`,
  `stdvar_over_time`, `mad_over_time`, `quantile_over_time`,
  `increase`, `changes`, `resets` all use `finiteValues` and/or
  `hasNaN`.

## Proposed technique

Introduce a **value-NaN-provenance** annotation on the selector-source
metadata and a parallel annotation on fragments. Default is "may
contain NaN" (current behaviour). Storage paths that apply
`staleNaNFilterSQL` plus `HAVING NOT isNaN(value)` set
`ValueNaNFree = true` on the produced selector source.

Then `rangeFunctionValueExpr` receives an extra boolean `valuesNaNFree`
from the renderer caller (derived from the fragment’s selector chain)
and takes one of two code paths:

* **NaN-free**: drop `hasNaN` guard, use `values` directly in
  `arraySum` / `arrayAvg` / `arrayMin` / `arrayMax`, drop the
  `finiteValues` filter entirely. `mad`, `quantile`,
  `stddev_over_time`, `stdvar_over_time` similarly skip the
  filter-then-reduce dance.
* **May-have-NaN**: keep the existing shape (the paranoid version).

For `rate` / `irate` / `increase`: today the `hasNaN` guard returns
NaN if any sample is NaN. If the input is proven NaN-free the guard
is a constant-false, and the whole `if` collapses to just the result
expression. This is a significant simplification of emitted SQL.

Tracking NaN-freeness through fragments:

* `LeafSource + SelectorKindRangeVector` with `staleNaNFilterSQL`
  applied in storage → `ValueNaNFree = true`.
* `Subquery` child: NaN-freeness depends on the inner expression. A
  subquery that computes `rate(foo[5m])` *can* produce NaN (the rate
  formula emits NaN when duration ≤ 0). So subqueries default to
  `false`.
* `Aggregation` over NaN-free source: most aggregations propagate
  NaN-freeness (sum, avg, min, max all return non-NaN given non-NaN
  input). `quantile` returns non-NaN. `count` returns non-NaN.
  Exceptions: `stddev`/`stdvar` of a single sample return 0 (finite);
  of zero samples return NaN. For safety, set `ValueNaNFree = true`
  only when the aggregation guarantees a finite result on finite
  input.
* `BinaryOp`: division can produce NaN (0/0) and Inf (x/0). Conservative
  default: `false`.
* `LabelTransform`, `ValueTransform`, `ClampTransform`: propagate.

For now the highest-value case is just the direct range-vector selector
path, which is the dominant input to `*_over_time` and `rate`/`irate`.

## Expected gain

SQL text size reductions (instant mode, `*_over_time(up[5m])`):

* `sum_over_time`: `if(arrayExists(v -> isNaN(v), values), nan,
  arraySum(arrayFilter(v -> NOT isNaN(v), values)))` (~110 bytes in
  the value position) → `arraySum(values)` (~20 bytes). 5× smaller.
* `avg_over_time`: ~170 bytes → ~35 bytes (need `length=0 → nan`
  guard: `if(length(values)=0, nan, arrayAvg(values))`). ~5×.
* `min_over_time`, `max_over_time`: similar 4–5× reduction.
* `stddev_over_time`, `stdvar_over_time`: the redundant `hasNaN` guard
  disappears, and `arrayReduce('stddevPop', values)` replaces the
  `if(hasNaN OR len=0, nan, arrayReduce(...))` shape.
* `rate`, `irate`, `increase`: the `hasNaN` guard drops out of the
  condition; `if(duration<=0, nan, delta/duration)` remains.

Server-side per-row work:

* Today: one `arrayExists` pass + one `arrayFilter` pass + one reducer
  pass = 3 passes over each window. Proposed: one reducer pass. 3×
  reduction in per-row array operations on simple aggregators.
* For wide series (e.g. 1000-sample windows on a 100Hz scrape) this is
  a measurable reduction; for typical 10–30 sample windows the cost is
  dominated by join fan-out and the gain is mostly in SQL text size.

Memory:

* `arrayFilter(v -> NOT isNaN(v), values)` materialises a new array of
  size `len(values)`. Skipping it saves `len(values) × 8 bytes` of
  transient allocation per bucket. For 1000-sample windows × 10k series
  × 100 buckets, that is ~8 GB of avoided allocation churn.

## Risk / semantics caveats

* **False-negative NaN-freeness is a correctness bug**: if we claim a
  source is NaN-free but it actually contains NaN samples, sum /
  avg / min / max produce NaN in Prometheus semantics but may produce
  unexpected values or propagate NaN without the expected guard
  (which some downstream code may rely on for `if(hasNaN, nan, ...)`
  to short-circuit). Mitigation: default to `false`, only flip to
  `true` when storage filter is proven applied.
* **Computed-NaN from value transforms**: if a `ValueTransform`
  applies e.g. `log(x)` and any input is zero, the output has `-inf`
  but not NaN — safe. If it applies `sqrt(x - 1)` on values < 1, that
  *does* produce NaN. For value transforms we must inspect the
  transform expression to decide. Simplest conservative rule: any
  `ValueTransform` drops NaN-freeness to `false`.
* **`stddev_over_time` / `stdvar_over_time` single-sample** returns
  `arrayReduce('stddevPop', [x]) = 0` in ClickHouse, not NaN. Prometheus
  returns 0 for `stddev_over_time` with one sample, so this matches.
  The existing code returns `nan` when `length == 0` via the `hasNaN OR
  length == 0` guard; the rewrite must still handle zero-length
  (via a separate `if(length = 0, nan, ...)` guard).
* **Quantile semantics with NaN**: Prometheus returns NaN if any input
  is NaN. `interpolatedQuantileExpr` uses `arrayConcat(arrayFilter(v ->
  isNaN(v), vals), arraySort(arrayFilter(v -> NOT isNaN(v), vals)))` so
  the NaN values bubble to the front. This relies on NaN values being
  present in the input; if NaN-free the whole first `arrayFilter`
  returns `[]` and the concat collapses to `arraySort(values)`. Safe
  simplification.
* **Stale NaN can leak through subqueries** if the inner expression
  did not apply the filter. Do not propagate `ValueNaNFree = true`
  through a subquery boundary unless the inner renderer explicitly
  stamped it on its output.
* **Instant-mode range-selectors** (`buildInstantSelectorSourceSQL`,
  `buildRangeInstantSelectorSourceSQL`) use `HAVING NOT isNaN(value)`
  which rejects all NaN including computed ones — this is actually
  *stricter* than needed but it does mean the output is provably
  NaN-free. Mark those paths as `ValueNaNFree = true` for free.

## Implementation sketch

1. Add `ValueNaNFree bool` to `storage.SelectorSource` and
   `native.SelectorSource`. Populate it in `storage` where the filter
   applies.
2. Add `ValueNaNFree bool` to `native.NativeFragment`; propagate
   through the fragment tree in a new analysis pass
   `applyValueNaNProvenance` inserted before `applyFunctionPatternRewrites`.
3. Pass a `valuesNaNFree bool` argument (or context struct) into
   `rangeFunctionValueExpr`. Plumb through
   `buildInstantRangeFunctionSQL`,
   `buildRangeFunctionOverWindowedArraysSQL`, and the direct-window
   call site in `renderer/range.go:98`.
4. In `rangeFunctionValueExpr`, branch on `valuesNaNFree`:
   * If true: emit the simplified form.
   * If false: current shape.
5. Update `BuildRangeWindowSelectorQuerySQLWithFinalTags` and
   `buildWindowedArraysSourceSQL` to also drop the per-row `arrayMap(
   point -> ifNull(toFloat64(tupleElement(point, 2)), nan),
   window_series)` — if we know values never contain NaN *and* the
   stored values are `Float64 NULL`-safe, `ifNull(toFloat64(...), nan)`
   can become `toFloat64(tupleElement(point, 2))`. This is a follow-on
   simplification.

## Test coverage idea

* Unit test: render `sum_over_time(up[5m])` with selector metadata
  marked NaN-free. Assert the emitted SQL does **not** contain
  `arrayFilter(v -> NOT isNaN(v)` and **does** contain `arraySum(
  window_values)`.
* Unit test: render the same with NaN-free = false. Assert the current
  (defensive) shape is emitted.
* Differential test: synthetic dataset with explicit
  `0x7FF0000000000002` stale-NaN markers plus computed-NaN
  (inject via upstream transform). Assert the renderer:
  * Filters staleness at storage.
  * Leaves computed NaNs in place when the transform is involved.
  * Still returns Prometheus-compatible results via the `hasNaN`
    guard for transforms that break NaN-freeness.
* Integration test: confirm that `avg_over_time(rate(foo[5m])[10m:1m])`
  (subquery input) does not incorrectly assume NaN-freeness through
  the subquery boundary.
* Harness oracle: full PromQL compliance suite must remain green.
* Add a regression fixture for stale-NaN markers so a future change
  that accidentally drops `staleNaNFilterSQL` is caught immediately.
