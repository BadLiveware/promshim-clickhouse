# Common-subexpression elimination in rangeFunctionValueExpr

## Problem

`rangeFunctionValueExpr` in `internal/promshim/native/renderer/range.go`
constructs each range/instant function value expression by pasting the same
sub-expression trees back together through `renderSQLExprNoParams`, which
flattens them into raw SQL text. The same `values` / `finiteValues` /
`hasNaN` / `seriesLength` / `timestamps` subtrees are rendered multiple
times and emitted verbatim into the final `SELECT` column list, producing
massive repeated sub-expressions in the SQL sent to ClickHouse.

Concrete emitted SQL for `avg_over_time(up[5m])` in instant mode (wrapping
preserved for readability):

```sql
SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS tags,
       tupleElement(arrayElement(time_series, length(time_series)), 1) AS timestamp,
       if(
         (
           arrayExists(v -> isNaN(v),
             arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series))
           OR
           (length(arrayFilter(v -> NOT isNaN(v),
             arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series))) = 0)
         ),
         nan,
         arrayAvg(arrayFilter(v -> NOT isNaN(v),
           arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series)))
       ) AS value
FROM ...
```

The subterm
`arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series)`
(the `valuesExpr`) appears **4 times** in the `value` column alone, and
`arrayFilter(v -> NOT isNaN(v), arrayMap(...))` (the `finiteValues`) appears
**3 times**. A single `avg_over_time` SELECT expands from ~180 semantic bytes
to ~650 emitted bytes.

For `rate(up[5m])` in range mode against the direct-window-join path the
per-bucket `value` expression re-renders `window_values`, `window_series`
and `window_timestamps` sub-expressions five times, and the literal
`arrayElement(window_timestamps, length(window_series)) - arrayElement(window_timestamps, 1)`
denominator is pasted twice (once in the `<= 0` guard, once as the divisor).
For `irate` the last / previous samples and their timestamps are spelled
out four times each. For `deriv` and `predict_linear` the `arrayMap(ts ->
(toFloat64(toUnixTimestamp64Milli(ts)) - (<intercept>)) / 1000.0, <ts_expr>)`
rewrites the whole timestamp array twice.

Costs:

* Parse + analyse time on the ClickHouse side scales linearly with query
  text size. ClickHouse does perform CSE, but it is bounded by
  `max_expanded_ast_elements` and `max_subquery_depth`, so very large
  emitted queries can hit these limits even though the semantic tree is
  small.
* Transit cost: HTTP request body grows with every duplicated subtree.
* The planner has to prove equivalence on each duplicate before it can
  share the computation — this is exactly the work a WITH binding would
  avoid.
* On the driver side the parameter-stripping and normalisation passes
  (e.g. `sqlb.NormalizeSQL`, harness snapshots, fixture diffs) all become
  noisier and harder to compare by a human.

## Current behaviour

* `range.go:256-408` — `rangeFunctionValueExpr` rebuilds its output by
  repeatedly calling `renderSQLExprNoParams` on the same `valuesExpr`,
  `finiteValues`, `hasNaN`, `seriesLength`, `timestampsExpr` subterms.
* `range.go:228-240` — `extrapolationFactorSQL` computes `firstMS`,
  `lastMS`, `sampledMS`, `avgMS`, `threshold`, `gapStart`, `gapEnd` by
  string-concatenating the same `tsSQL` and `lenSQL` inputs; there is no
  binding, so `firstMS` appears 3 times, `lastMS` appears 3 times,
  `sampledMS` appears 4 times.
* `range.go:279-290` — `interpolatedQuantileExpr` closes over the caller’s
  raw `valuesSQL` string and repeats the sorted-array construction
  `arrayConcat(arrayFilter(isNaN, vs), arraySort(arrayFilter(NOT isNaN, vs)))`
  and its `length(...)` four times.

## Proposed technique

Emit local bindings once per row, using either:

1. **`WITH <expr> AS <alias>` at the innermost `SELECT` scope.** The
   `sqlb.Select` type already has a `With []CTE` slot (`sqlb.go:132-140`),
   but it only supports full sub-selects. Add a lighter `WithAlias{Name,
   Expr}` binding that emits an inline `WITH <expr> AS <name>` in the same
   `SELECT` (ClickHouse supports scalar WITH aliases inside a single
   query). For the window case this lives at the `perStep` level; for the
   instant case it lives at the outer projection.

2. **Lambda-bound tuples** where the whole range-fn expression becomes
   `arrayReduce('finalizeAggregation', …)`-style or a single nested
   `(<expr>) AS <alias>` column, then the projection references the alias.
   ClickHouse’s column-alias reuse inside a single SELECT list is the
   lowest-risk mechanism: `SELECT arrayMap(...) AS values_all,
   arrayFilter(v -> NOT isNaN(v), values_all) AS values_finite,
   length(values_finite) AS len_finite, ...` — downstream columns can
   refer to earlier aliases by name.

Recommended target for `rangeFunctionValueExpr`:

* `values_all` = `arrayMap(point -> ifNull(toFloat64(tupleElement(point,
  2)), nan), <series>)`
* `values_finite` = `arrayFilter(v -> NOT isNaN(v), values_all)`
* `has_nan` = `arrayExists(v -> isNaN(v), values_all)`
* `len_all` = `length(<series>)` (or `length(values_all)`)
* `len_finite` = `length(values_finite)`
* `ts_all` = caller-provided timestamps array (already an alias in window
  case; add one in instant case)
* `ts_first_ms`, `ts_last_ms`, `sampled_ms` for the extrapolation factor

Expose a new renderer helper
`buildRangeFunctionBindings(seriesExpr, valuesSourceExpr,
timestampsSourceExpr) []sqlb.ColExpr` that produces these aliases, then
rewrite `rangeFunctionValueExpr` to reference them by `Ident` rather than
re-rendering subtrees.

For `extrapolationFactorSQL` the same approach yields bindings for
`first_ms`, `last_ms`, `sampled_ms`, `avg_ms`, `threshold`, `gap_start`,
`gap_end`, `add_start`, `add_end`, `extrapolate_to`. Each binding is
written once and referenced by name.

## Expected gain

For the `avg_over_time(up[5m])` instant query shown above the emitted
`value` column shrinks from ~650 bytes to ~150 bytes (roughly 4×). For
`rate(http_requests_total[5m])` range mode with direct window join the
`value` column shrinks from ~780 bytes to ~180 bytes. Whole-query text
size drops by 20–35% for a typical range-function query and by 40–55%
for compound queries like
`sum by (job) (rate(http_requests_total[5m])) / sum by (job)
(rate(http_requests_all[5m]))` where the same rate shape appears twice.

Server-side: ClickHouse re-evaluates `arrayMap` calls if CSE fails to
prove equivalence (it usually does for simple lambdas, but the cost of
the CSE pass itself is measurable on very wide projection lists). With
explicit column aliases the planner skips CSE entirely for those subterms.
On a 10k-series fan-out this saves one pass over `values_all` per row,
which is cheap in absolute terms but meaningful for per-series arrays of
1k–10k samples.

## Risk / semantics caveats

* ClickHouse column aliases reference other columns from the **same
  SELECT list by name**. Some versions disallow forward references within
  the same list; order must be: `values_all` → `values_finite` →
  `has_nan`/`len_finite` → `value`. Verify against the minimum supported
  ClickHouse version with a fixture test.
* Aliased columns are logically projected; if the optimiser does not
  eliminate the unused `values_all` alias from the output stream, we
  might end up materialising them in the rowset. Remedy: wrap in an
  outer `SELECT value AS value FROM (… with aliases …)` so only the
  final `value` bubbles up. This is the same shape `perStep → outer`
  already uses.
* Stale-NaN semantics (Prometheus writes `0x7FF0000000000002` to mark a
  target as gone) must still be handled before `values_all` is computed,
  otherwise `has_nan` flips a real evaluation to NaN unexpectedly. Today
  the direct-window-join already filters stale NaN at the storage layer
  (`staleNaNFilterSQL` in `storage/selector_sql.go:107`) but the
  instant/materialize paths do not. This interacts with technique #4.
* `stddev_over_time` / `stdvar_over_time` currently pass `valuesExpr`
  (unfiltered) into `arrayReduce('stddevPop', …)`. If we alias
  `values_finite` we must *not* silently swap the input — Prometheus
  behaviour returns `nan` when any sample is NaN, and the current code
  encodes that via the `hasNaN` guard. Keep that guard.

## Implementation sketch

1. Extend `sqlb.Select` with `Aliases []ColExpr` (or reuse `Columns` as
   an ordered list of `Expr AS Name` bindings). Alternatively a helper
   `buildBindingSelect(bindings, finalColumns)` that emits
   `SELECT bindings…, finalColumns… FROM (sub)`.
2. Add `renderer/rangefn_bindings.go`:
   * `rangeFnCommonBindings(seriesIdent, valuesSrc, timestampsSrc sqlb.Expr)
     []sqlb.ColExpr` returning named aliases.
3. Rewrite `rangeFunctionValueExpr` cases to emit aliases plus a small
   `value` expression in terms of those aliases. For cases that need
   extra sub-aliases (`extrapolation_factor`, `sorted_values`,
   `counter_delta`) append them to the binding list.
4. Update `buildInstantRangeFunctionSQL` and
   `buildRangeFunctionOverWindowedArraysSQL` to invoke the binding
   builder and emit a single `value` column referring to the aliases.
5. Update `BuildRangeWindowSelectorQuerySQLWithFinalTags` similarly —
   the `perStep` SELECT is the natural home for the bindings because
   `window_values`, `window_series`, `window_timestamps` are already
   aliases in the outer `windowed` SELECT.

## Test coverage idea

* Add a renderer test that renders `avg_over_time(up[5m])` and counts
  occurrences of the substring `arrayMap(point -> ifNull(toFloat64(`.
  Pre-change: 4 occurrences in the `value` column. Post-change: exactly
  1. Same for `rate` / `irate` / `deriv`.
* Add a size-budget test that asserts the rendered SQL length for a
  fixed set of benchmark queries (sum, avg, rate, irate, increase,
  deriv, quantile_over_time) stays below a regression threshold.
* Run the full PromQL harness (`harness/...`) — the CSE refactor must
  be value-identical, so no result diffs are allowed. A renderer-only
  refactor should not change any oracle comparisons.
* Add an integration test against ClickHouse verifying that the aliased
  query parses and executes and returns the same values as the current
  shape for a known fixture (2 series × 20 samples).
