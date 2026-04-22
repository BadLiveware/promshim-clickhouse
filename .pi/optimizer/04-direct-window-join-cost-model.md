# Cost-based direct-window-join threshold

## Problem

`preferDirectSelectorWindowJoin` in `internal/promshim/native/renderer/range.go`
(lines 150-161) picks between two very different SQL shapes for range
functions over a direct range-vector selector:

* **Direct-window join** (`BuildRangeWindowSelectorQuerySQLWithFinalTags`):
  cross-join the step-grid against the selector, then join the raw
  `time_series_data` table once and let ClickHouse group `(d.timestamp,
  d.value)` per `(id, eval_ts)` bucket. Duplicates each raw point into
  every overlapping step bucket (~`lookback / step` times).
* **Materialize-then-window** (`buildRangeFunctionOverWindowedArraysSQL` →
  `buildWindowedArraysSourceSQL`): first materialise each series as a
  single `time_series` array, then `arrayFilter` windows out of it per
  `eval_ts`. Single pass over the selector plus array-subsetting per
  step.

The current decision is a **hardcoded rule**:

```go
overlapSlots := ((lookbackMS + stepMS - 1) / stepMS) + 1
return overlapSlots <= 4
```

For `5m / 30s` = 11 slots → materialise path. For `1m / 30s` = 3 slots →
direct path. The threshold is fixed independent of:

* Selector cardinality (how many series the matchers resolve to).
* Samples per series (which depends on scrape interval, not just
  lookback).
* Step count (how wide the eval grid is).
* Memory pressure of array materialisation (`groupArray`, `arraySort`)
  on wide series.
* Sort cost of `arraySort(item -> item.1, groupArray(...))` inside the
  `windowed` SELECT.

Concrete cases where the hardcoded rule is wrong:

1. **High-cardinality, low-sample selector with shallow overlap**:
   `rate(http_requests_total{job="api"}[1m])` against 500k series at a
   1s scrape interval. Overlap = 3 → direct path. Cross-join fan-out:
   `500k × 20_steps = 10M` grid rows; each raw point replicated ~3×.
   Direct path is fine here — it wins. Rule works.
2. **Low-cardinality, high-sample selector with shallow overlap**:
   `rate(disk_io_bytes[1m])` against 10 series but sampled at 100Hz for
   a range query covering 1h. Overlap still = 3 → direct path picks.
   But each series has 360k samples — materialising once and
   `arrayFilter`ing 120 windows is dramatically cheaper than replicating
   samples into each grid bucket.
3. **High-cardinality, high-sample, deep overlap**:
   `sum_over_time(cpu_usage[1h])` against 50k series at 5s scrape rate
   with a 10-minute step. Overlap = 7 → materialise path. But the
   materialised array is `3600/5 = 720` samples/series, 50k series
   means a 36M-element intermediate — dominating memory. Direct path
   would stream it.
4. **Instant evaluation against long lookback**: `rate(up[5m])` instant
   mode with offset aligned to scrape grid, one series per target.
   Overlap irrelevant (only one eval_ts). Direct path replicates
   minimally; materialise path still sorts the full array. Direct path
   is cheaper but there’s no step-overlap signal for the rule.

The rule’s authors note in the comment that it is a rough proxy for
cross-join fan-out, not a cost model. It is a reasonable default but it
hides decisions the optimiser *can* make if it has the data.

## Current behaviour

* `range.go:92` — in range mode, if `preferDirectSelectorWindowJoin`
  returns true the renderer picks the direct window path. Otherwise
  falls through to `buildRangeFunctionOverWindowedArraysSQL`.
* `range.go:150-161` — the rule itself. Fixed threshold of 4 overlap
  slots, no other inputs.
* `storage/selector_sql.go:62` — `BuildRangeWindowSelectorQuerySQLWithFinalTags`
  builds the direct-window shape. It joins `time_series_data` once per
  matched series, groups by `(grid.id, grid.tags, grid.eval_ts)` and
  emits `(timestamp, value)` arrays per bucket.

## Proposed technique

Replace the fixed threshold with a cost function evaluated in the
optimiser (`native/optimizer.go::applyFunctionPatternRewrites`, the
placeholder pass that currently does nothing, see lines 211-218).

Cost model inputs:

1. **Selector cardinality estimate** — number of series matching the
   matchers. Pulled from `time_series_tags` (already the left side of
   `buildMatchedSeriesSQL`). Two ways to obtain it:
   * *Cached statistics*: maintain a per-(metric_name, matcher_subset)
     cardinality cache keyed by matcher signature, with TTL. Populate
     lazily at render time with a cheap `SELECT count() FROM
     timeSeriesTags(...) WHERE <matcher clauses>` issued once per
     analysis.
   * *Sampling probe*: issue
     `SELECT count() FROM timeSeriesTags(...) WHERE ... SETTINGS max_rows_to_read=<N>, read_overflow_mode='break'`
     before the main query, fall back to heuristics if the probe
     exceeds the budget.
2. **Expected samples per series over the lookback** — heuristic based
   on lookback and default scrape interval. Default assumption: 15s
   scrape → `samples_per_series = ceil(lookback_ms / 15000)`. Allow
   per-matcher override via a configuration map keyed by metric name
   prefix (e.g. `node_*` → 60s, `histogram_quantile` buckets → shared).
   For counter-family metrics we can infer from `min_time`/`max_time`
   delta in `time_series_tags` if we already query it: `(max_time -
   min_time) / count` for the series is approximate samples_per_second.
3. **Step count** — `(endMS - startMS) / stepMS + 1`. Known at render
   time.
4. **Overlap factor** — `lookback_ms / step_ms` (continuous, not the
   bucketed value currently used).

Cost functions:

* **Direct-window-join cost**:
  `C_direct = cardinality × samples_per_series × overlap_factor`
  (each sample is pushed into `overlap_factor` buckets during the
  INNER JOIN fan-out, then `groupArray` collapses per-bucket tuples).
* **Materialize-then-window cost**:
  `C_mat = cardinality × samples_per_series × log(samples_per_series)`
  (sort cost of `arraySort(item -> item.1, groupArray(...))` over the
  whole lookback per series) `+ cardinality × step_count × samples_per_window`
  (the per-step `arrayFilter` pass).

Pick direct if `C_direct < C_mat × α` where `α` is a safety margin
(initial value 0.7 — prefer materialise unless direct is clearly
better).

Signal path:

* Add an `OptimizationContext` field: `SelectorStatistics map[string]SelectorStat`
  (keyed by the `SelectorSignature()` of the fragment, populated by a
  new analysis pre-pass).
* `preferDirectSelectorWindowJoin(selector, lookbackMS, stepMS, stepCount,
  stats)` returns the cost-based decision; if stats are missing it falls
  back to the current heuristic.
* In `applyFunctionPatternRewrites`, annotate the fragment with
  `PreferredRangeStrategy = "direct" | "materialize"` so the renderer
  does not recompute the cost.
* Expose `EXPLAIN ESTIMATE` as an opt-in diagnostic: add a debug flag on
  `RenderParams` that, when set, emits both candidate SQL bodies plus
  the cost numbers to the report (mirrors `OptimizationReport.AppliedRewrites`
  — already a list we can stuff hints into).

## Expected gain

* For the `rate(disk_io_bytes[1m])` + 100Hz example above the cost model
  picks materialise: estimated 3–5× reduction in fan-out rows. On
  synthetic benchmarks where we force 100Hz samples, the current shape
  reads ~3× the working set because each sample lands in 3 buckets.
* For the `sum_over_time(cpu_usage[1h])` + 10-minute-step case the cost
  model picks direct: avoids building a 36M-element intermediate. Peak
  memory on the ClickHouse node drops proportionally to the avoided
  `groupArray` size (many GB of transient state for wide fan-outs).
* For the common case (`rate(foo[5m])` at 30s step, default 15s scrape)
  the model picks the same shape as the current rule, so no regression.

Order-of-magnitude: on the "bad" edge cases (cases 2 and 3 above) the
wrong choice can be 3–10× slower; the cost model should close that gap.
On the common case, no change.

## Risk / semantics caveats

* A sampling probe adds one round-trip before the main query. Needs to
  be cached or fast-pathed when the planner already has statistics from
  a previous request on the same matchers.
* Cardinality estimates from `time_series_tags` include series that are
  out-of-time-window; the time-overlap WHERE clauses already bound this
  but the estimate is still an upper bound. That is fine for the cost
  model (both strategies scale with matched cardinality).
* Samples-per-series heuristic is only correct if scrape intervals are
  homogeneous. For mixed scrape intervals (e.g. federation) the
  estimate can be off by 10× — use `max_time - min_time` delta as a
  fallback when it disagrees with the default by more than 2×.
* The cost model must not destabilise deterministic test fixtures.
  Implementation option: in tests set `SelectorStatistics = nil`, which
  falls back to the old hardcoded rule. Production sets actual stats.
* Cost-based choice can flip per-query depending on current statistics;
  fixtures that assert on rendered SQL shape should use the fallback
  path or pin the strategy via a test hook (`RenderParams.ForceRangeStrategy`).
* Memory pressure modelling is the hardest; `C_mat` undercounts sort
  cost on pathological series (e.g. 1M samples in a single long-window
  query). Add a hard cap: `samples_per_series > 100k` → force direct.

## Implementation sketch

1. New file `native/optimizer_rangefn_strategy.go`:
   * `type SelectorStat struct { Cardinality int64; AvgSamplesPerSeries
     float64 }`
   * `func estimateRangeStrategy(fragment *NativeFragment, ctx
     OptimizationContext) RangeStrategy`
   * `RangeStrategy` is an enum {Auto, Direct, Materialize}. Auto
     invokes the cost function.
2. Extend `OptimizationContext` with `SelectorStatistics map[string]SelectorStat`
   and `DefaultScrapeMS int64` (default 15000).
3. `applyFunctionPatternRewrites` populates
   `fragment.RangeFunction.PreferredStrategy` (new field on
   `RangeFunctionFragment`).
4. `range.go::renderRangeFunctionFragment` consults
   `fragment.RangeFunction.PreferredStrategy` before falling back to
   `preferDirectSelectorWindowJoin`.
5. Add a `storage` helper to issue the cardinality probe:
   `EstimateSelectorCardinality(ctx, cfg, selector, requiredStartMS,
   requiredEndMS) (SelectorStat, error)`. Share the same matcher SQL
   as `buildMatchedSeriesSQL`.
6. Wire probes in the service layer only when the render context
   requests strategy estimation (`EnableCostBasedPlanning` on query
   config).

## Test coverage idea

* Unit tests for `estimateRangeStrategy` with synthetic
  `SelectorStat` values covering the four scenarios from the Problem
  section. Each asserts the chosen strategy.
* Golden-SQL tests with `RenderParams.ForceRangeStrategy` set to Direct
  and Materialize to verify both code paths still render correctly.
* A regression test that replays the existing
  `preferDirectSelectorWindowJoin` threshold cases with
  `SelectorStatistics = nil` and asserts the output is unchanged.
* An integration test (behind a build tag) that issues the probe,
  renders both candidates, times them via ClickHouse `query_log`, and
  reports which strategy is faster. Used as a calibration tool, not a
  CI gate.
* Harness run (`go test ./...` against the full PromQL oracle corpus)
  must remain green: correctness should not depend on strategy choice.
