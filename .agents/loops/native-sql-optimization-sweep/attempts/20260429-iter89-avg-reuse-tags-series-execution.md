# Iteration 89: avg-memory matched-series reuse execution

## Scope
Tier-2/native-SQL only.

## Candidate
Reduce duplicate matched-series work in cumulative avg path by reusing the tags-bearing matched-series query and deriving id-only series from it.

## Code change
- `internal/promshim/storage/selector_sql.go`
  - in `buildRangeWindowSelectorCumulativeAvgPerStepSQL`:
    - when `selector.NeedTags=true`, build `finalMatchedSeriesSQL` first,
    - derive `matchedSeriesSQL` as `SELECT DISTINCT id FROM (finalMatchedSeriesSQL)` instead of issuing an independent id-only matched-series query.

## Focused validation
```bash
go test ./internal/promshim/storage ./internal/promshim/native/renderer -run 'TestBuildRangeWindowSelectorCumulativeAvg|TestLowerAggregationAvgOverTimeUsesCumulativeRowsFastPath|TestLowerRangeFunctionAvgOverTimeUsesCumulativeRowsFastPath|TestLowerAggregationGolden|TestLowerRangeFunctionGolden'
```

## Served-safety pre-gate benchmark
Artifact:
- `harness/artifacts/bench/standalone/20260429-iter89-avg-reuse-tags-series-pregate/`

Outcome:
- served successfully (`force_supported/strict`, `native_sql`)
- no `HTTP 502`

## Measurement signal
Compared to accepted iter88 run:
- iter88 shim p50: `6305.19ms`
- iter89 shim p50: `6035.01ms`
- delta: `-270.18ms` (`-4.3%`)

- iter88 CH p50: `6230.5ms`
- iter89 CH p50: `6194.5ms`
- delta: `-36.0ms` (`-0.6%`)

Resource class:
- memory p95 improved slightly (`8.3GiB` -> `8.2GiB`)
- read/join/filter row classes unchanged (~710M / ~119M / ~16.6M)

## Interpretation
- Safety passed.
- Shim latency improved further on the bottleneck row while resource profile remained stable.
- This is a bounded simplification with positive signal and no observed downside.

## Decision
Accepted.
