# Counter-reset delta formula reuse

## Problem

`rangeFunctionValueExpr` rebuilds the same pairwise-neighbour iteration
for `rate` / `irate` / `increase` / `resets` / `changes` every time it
produces a value expression:

```go
prevValues := arraySlice(valuesExpr, 1, len-1)
curValues  := arraySlice(valuesExpr, 2, len-1)
counterDeltaExpr := arraySum(arrayMap((prev, cur) ->
    if(cur < prev, cur, cur - prev), prevValues, curValues))
changesExpr     := toFloat64(arraySum(arrayMap((prev, cur) ->
    if(cur != prev, 1, 0), prevValues, curValues)))
```

The emitted SQL for `rate(up[5m])` in range mode (direct window path)
contains the whole delta machinery inline:

```sql
arraySum(arrayMap(
  (prev, cur) -> if(cur < prev, cur, cur - prev),
  arraySlice(window_values, 1, length(window_series) - 1),
  arraySlice(window_values, 2, length(window_series) - 1)))
/ (arrayElement(window_timestamps, length(window_series))
   - arrayElement(window_timestamps, 1))
```

Two things are wrong:

1. **Reused formula rebuilt per range function**: `rate`, `irate`,
   `increase`, `resets`, and `changes` all iterate pairwise neighbours
   of `window_values`. Today each case in the big `switch` (range.go
   lines 336–372) re-constructs `counterDeltaExpr` or `changesExpr`
   from scratch, pasting `arraySlice(values, 1, len-1)` and
   `arraySlice(values, 2, len-1)` into the emitted SQL verbatim. A
   query like `rate(foo[5m]) - increase(foo[5m])` emits the same
   pairwise iteration twice against the same windowed values.

2. **Sub-optimal slice primitives**: `arraySlice(v, 1, len-1)` and
   `arraySlice(v, 2, len-1)` allocate two new arrays each at ~O(n)
   cost. ClickHouse provides `arrayPopBack(v)` (equivalent to
   `arraySlice(v, 1, length(v) - 1)`) and `arrayPopFront(v)`
   (equivalent to `arraySlice(v, 2, length(v) - 1)`), which are
   cheaper because they avoid the `length()` call and the generic
   slice machinery. Notably, the `resets` case already uses
   `arrayPopBack` / `arrayPopFront` (range.go:343), proving the
   primitives are available — but `rate`, `irate`, `increase`, and
   `changes` still use the slower `arraySlice` pattern.

Additional observation: the *pair of neighbours* is needed at a point
when we have the materialised `time_series` array already in the
selector-row output. For range-vector selectors used by range
functions, the selector row always carries a sorted `time_series`
array; pairwise neighbours can be computed once during that
materialisation and exposed as `delta_pairs` / `changes_pairs`
(or even `counter_delta_sum` / `changes_count`) columns.

Pushing this computation into the selector row avoids rebuilding
`arrayMap((prev, cur) -> ..., popBack(vals), popFront(vals))` inside
every range function wrapper that happens to look at the same window.
The `rate(foo[5m]) - rate(foo[5m] offset 5m)` shape is the canonical
example — same window, same delta iteration, emitted twice today.

## Current behaviour

* `range.go:274-277` — `prevValues` and `curValues` built using
  `arraySlice(v, 1, len-1)` / `arraySlice(v, 2, len-1)`.
* `range.go:276` — `counterDeltaExpr` = `arraySum(arrayMap((prev, cur)
  -> if(cur < prev, cur, cur - prev), prevValues, curValues))`.
* `range.go:277` — `changesExpr` uses the same prev/cur pair with a
  different lambda.
* `range.go:336-344` — `increase`, `changes`, `resets` emit different
  reducers over the same pairwise iteration.
* `range.go:345-349` — `rate` re-emits `counterDeltaExpr` inline.
* `range.go:343` — `resets` already uses `arrayPopBack` / `arrayPopFront`
  on `finiteValues`, demonstrating the primitive exists in ClickHouse.
* `storage/selector_sql.go:98-108` — `windowed` SELECT produces
  `window_series`, `window_timestamps`, `window_values` columns. This
  is the natural place to add counter-delta / change-count columns.

## Proposed technique

Two linked changes:

### (A) Emit pairwise-neighbour bindings once per row

Add at the `perStep` SELECT (or the `windowed` SELECT it reads from)
the following columns, computed once per `(series, eval_ts)` bucket:

* `window_values_prev` = `arrayPopBack(window_values)`
* `window_values_cur`  = `arrayPopFront(window_values)`
* `counter_delta_sum`  = `arraySum(arrayMap((p, c) -> if(c < p, c, c -
  p), window_values_prev, window_values_cur))`
* `changes_count`      = `arraySum(arrayMap((p, c) -> if(c != p, 1,
  0), window_values_prev, window_values_cur))`
* `resets_count`       = `arraySum(arrayMap((p, c) -> if(c < p, 1, 0),
  arrayPopBack(window_values_finite), arrayPopFront(window_values_finite)))`
  (note: `resets` works on the NaN-filtered array; keep a separate
  `window_values_finite` binding as well — see doc #3 on CSE).

Then the `rangeFunctionValueExpr` switch rewrites to simple column
references:

```go
case "rate":
    return "if(has_nan OR window_duration_ms <= 0, nan,
             counter_delta_sum / window_duration_ms)"
case "increase":
    return "if(has_nan, nan, counter_delta_sum * <extrapolation>)"
case "changes":
    return "if(has_nan, nan, changes_count)"
case "resets":
    return "toFloat64(resets_count)"
case "irate":
    // still needs last two points individually, but shares
    // window_values_prev / window_values_cur: take last element of each
    return "if(has_nan OR irate_duration <= 0, nan,
             arrayElement(window_values_cur, length(window_values_cur))
             - arrayElement(window_values_prev, length(window_values_prev)))"
```

### (B) Replace `arraySlice(v, 1, len-1)` / `arraySlice(v, 2, len-1)`
     with `arrayPopBack(v)` / `arrayPopFront(v)`

Direct one-liner swap in range.go:274-275. `resets` already does this.

Note: because `arrayPopBack([])` returns `[]` (safe), and `len-1`
computation becomes implicit, we also drop one `renderSQLExprNoParams(
seriesLength) + " - 1"` string-interpolation per emission.

## Expected gain

SQL text size:

* `rate` value expression today: ~290 bytes. With (A) + (B): ~80
  bytes. Saves ~210 bytes per emission.
* `rate(foo[5m]) - increase(foo[5m])` today: two full delta formulas
  (~580 bytes). With (A): a single binding in the selector row plus
  two tiny references (~120 bytes). Saves ~460 bytes.

Server-side per-row work:

* `arraySlice` is O(n) with a copy; `arrayPopBack` is O(n) but avoids
  the `length()` evaluation and the generic-slice dispatch (measured
  ~10–20% cheaper on microbenchmarks for small arrays; negligible for
  arrays >1000). Not the main win.
* The big win is computing `counter_delta_sum` once per
  `(series, eval_ts)` bucket at the `windowed` SELECT, then reading it
  as a scalar column from the outer aggregation. `rate - irate` no
  longer evaluates the delta twice; `rate / <scalar>` no longer
  inlines the delta into the ratio expression.
* For `increase` the gain is smaller because `extrapolation_factor`
  dominates, but the formula still deduplicates.

Memory:

* `window_values_prev` and `window_values_cur` add two arrays per
  bucket (`~samples_per_window × 8 bytes` each). This is already the
  cost of `arraySlice` today; the difference is a single materialisation
  versus two.

## Risk / semantics caveats

* **NaN handling differs between functions**:
  * `rate` / `increase`: return `nan` if any sample in the window is
    NaN. Computed via `hasNaN` guard.
  * `irate`: returns NaN only if the last two samples are NaN (more
    permissive than `rate`). Today the code checks `hasNaN` which is
    stricter than Prometheus. Be careful not to regress this if the
    shared `counter_delta_sum` pushdown alters the gating.
  * `resets`: Prometheus only counts resets between finite samples,
    i.e. operates on `finiteValues` not `values`. Today the
    implementation already does this by slicing `finiteValues`.
  * `changes`: Prometheus counts NaN → finite and finite → NaN as
    changes, so it operates on *all* samples including NaN-transitions.
    Today the code returns `nan` if any sample is NaN (via the
    `hasNaN` guard). That matches older Prometheus behaviour; newer
    Prometheus has more nuanced rules here. Keep the current
    semantics as a baseline and document the caveat.
* **Counter-reset detection scope**: the `if(cur < prev, cur, cur -
  prev)` formula is Prometheus’s counter-reset-safe delta. It treats
  any decrease as a full reset (assumes counter went to 0 and came
  back up to `cur`). This is correct only for monotonic counters; for
  native histograms the rule is different. The optimisation does not
  change the formula, only where it is computed — but if we push the
  computation into the selector row we must be sure the selector is
  actually a counter-family selector. Two options:
  1. Compute `counter_delta_sum` unconditionally and only the
     consuming range function decides whether to use it.
  2. Gate the materialisation on `fragment.RangeFunction.Func ∈ {rate,
     irate, increase, resets}` so non-counter selectors skip it.
  Option 1 is simpler; option 2 saves per-row work when the selector
  is used only for `sum_over_time` / `avg_over_time`.
* **Boundary case: `length(values) < 2`**: `arrayPopBack([x])` →
  `[]`, `arrayPopFront([x])` → `[]`, `arraySum([])` → 0. So
  `counter_delta_sum` defaults to 0, which is wrong if consumed
  without guard (would produce a rate of 0 where Prometheus would
  return NaN for insufficient samples). The
  `minimumSeriesLengthForRangeFunction` WHERE filter (range.go:411)
  already rejects rows with too few samples; ensure the binding
  column is computed *after* that filter or is wrapped in an `if`.
* **stale-NaN interaction**: see doc #6; if we switch to
  `window_values_finite` we must ensure the extrapolation factor
  denominator uses the correct timestamp pair (first finite / last
  finite, not raw array first/last).

## Implementation sketch

1. In `storage/selector_sql.go::BuildRangeWindowSelectorQuerySQLWithFinalTags`,
   extend the `windowed` SELECT (lines 98-109) to add:
   ```go
   {Expr: sqlb.RawLit{V: "arrayPopBack(window_values)"}, Alias: "window_values_prev"},
   {Expr: sqlb.RawLit{V: "arrayPopFront(window_values)"}, Alias: "window_values_cur"},
   {Expr: sqlb.RawLit{V: "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), window_values_prev, window_values_cur))"}, Alias: "counter_delta_sum"},
   {Expr: sqlb.RawLit{V: "arraySum(arrayMap((p, c) -> if(c != p, 1, 0), window_values_prev, window_values_cur))"}, Alias: "changes_count"},
   ```
   Then in the `perStep` SELECT (lines 110-114), reference the new
   aliases rather than recomputing.
2. Do the same in `renderer/range.go::buildWindowedArraysSourceSQL`
   (lines 163-176) so the materialise path carries the same columns.
3. Refactor `rangeFunctionValueExpr` to emit short expressions that
   reference the new column aliases when the caller is the range
   renderer (i.e. the `seriesExpr == "window_series"` path). For the
   instant path (`seriesExpr == "time_series"`) either emit the
   bindings at the outer SELECT (see doc #3) or keep the current inline
   shape.
4. Swap `arraySlice(v, 1, len-1)` → `arrayPopBack(v)` and
   `arraySlice(v, 2, len-1)` → `arrayPopFront(v)` globally in
   `rangeFunctionValueExpr`.
5. Plumb a new signal on `RangeFunctionFragment`: `UsesPairwiseDelta
   bool` so the renderer can skip emitting the pairwise columns when
   no consumer needs them (small optimisation; the columns aren’t free
   even as aliases).

## Test coverage idea

* Renderer test: render `rate(up[5m])` range mode, assert that the
  emitted SQL contains `counter_delta_sum` as a column alias and does
  *not* contain `arraySlice(window_values, 1,` or `arraySlice(
  window_values, 2,`. Before: matches `arraySlice` 2 times; after: 0.
* Renderer test: render `rate(up[5m]) - irate(up[5m])`, assert the
  `arrayMap((prev, cur) -> if(cur < prev,` substring appears exactly
  once (shared via the alias). Before: 2 times.
* Differential test against Prometheus: `rate` / `irate` / `increase`
  / `resets` / `changes` on a synthetic counter that resets at known
  points. Must match within oracle tolerance.
* Boundary test: single-sample series. Today the `length > 1` filter
  drops it; after the refactor the alias still evaluates safely and
  the filter still drops it.
* Harness oracle: no behaviour change expected. All existing PromQL
  compliance tests must stay green.
