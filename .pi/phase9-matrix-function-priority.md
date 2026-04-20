# Phase 9 — Prioritized next matrix-consuming functions/operators

## Selection criteria

1. High dashboard/query frequency in typical Prometheus usage.
2. Good fit for the new generic local matrix-range abstraction.
3. Low semantic risk for first incremental additions.
4. Enables useful nested subquery compositions in both instant and range contexts.

## Priority order

### #1 `sum_over_time(range-vector)`
- **Why first:** very common in SLI/SLO and utilization-style queries; simple accumulator semantics.
- **Output type:** vector (instant), matrix (query_range via existing range-function plan step-loop).
- **Key semantics to validate:**
  - empty range returns no sample for that series
  - deterministic timestamp selection (last sample timestamp in window)
  - NaN propagation behavior consistent with current local conventions

### #2 `avg_over_time(range-vector)`
- **Why second:** common companion to `sum_over_time`; high practical value for smoothing metrics.
- **Output type:** vector/matrix as above.
- **Key semantics to validate:**
  - average over points in window
  - empty range behavior
  - NaN handling parity with existing local conventions

### #3 `max_over_time(range-vector)`
- **Why first:** done.
- **Output type:** vector/matrix as above.
- **Status:** implemented.

### #4 `min_over_time(range-vector)`
- **Why fourth:** useful for gauges and threshold checks; completed in prior sub-slice.
- **Output type:** vector/matrix as above.
- **Status:** implemented.

### #5 `count_over_time(range-vector)`
- **Why now:** most common next extension for practical SLO/alert math, and now aligns cleanly with the existing range-function abstraction.
- **Output type:** vector (instant), matrix (query_range via existing range-function step loop).
- **Key semantics to validate:**
  - count equals sample cardinality in each range-vector series
  - stable timestamp selection (last sample timestamp in window)
  - empty series are dropped

## Deferred for later sub-slices

- `quantile_over_time`, `stddev_over_time`, `stdvar_over_time`, etc.
- These are valuable but can follow once count/min/max variants are stable and parity-validated.

## Execution mapping to current task tree

- Task #3: implement `sum_over_time`
- Task #4: implement `avg_over_time`
- Task #5: implement `max_over_time`
- Task #6: implement `min_over_time`
- Task #7: implement `count_over_time`
