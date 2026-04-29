# Iteration 50: histogram path telemetry prerequisite

## Scope
Tier-2/native-SQL only.

## Goal
Expose path-specific physical decision telemetry for histogram range rows so future optimization probes are decision-path grounded.

## Code changes
- `internal/promshim/native/renderer/histogram_logical.go`
  - add explicit histogram child-path decision emission:
    - `histogram_child_path=fused_range_aggregation_child_le_only` for the current benchmarked row shape.
  - propagate decision metadata through histogram function branches.
- `internal/promshim/native/renderer/histogram.go`
  - preserve/forward `ExtraPhysicalDecisions` through histogram output wrapping.

## Validation
Focused tests:
```bash
go test ./internal/promshim/native/renderer -run 'TestLowerHistogramFunctionGolden|TestLowerRangeFunctionGolden|TestLowerAggregationGolden'
```

Benchmark artifact:
- `harness/artifacts/bench/standalone/20260429-iter50-hist-path-telemetry/`

Observed result (`processing_histogram_quantile_1h_range_24h_7d`, force_supported+strict):
- row serves cleanly
- shim p50: `1897.5ms` (within expected noise envelope vs prior telemetry refresh)
- **new telemetry present**:
  - `physicalDecisions=histogram_child_path=fused_range_aggregation_child_le_only`

## Decision
Accepted and committed as observability prerequisite.

This unlocks path-specific histogram optimization experiments with explicit decision evidence.
