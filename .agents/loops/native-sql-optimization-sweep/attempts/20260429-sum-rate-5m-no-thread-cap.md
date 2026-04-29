# Sum-rate 5m fused-rate no-thread-cap

## Scope
Tier-2/native-SQL only.

Target row:
- `processing_sum_rate_5m_by_job_range_24h_7d`

Baseline telemetry reference:
- `physicalDecisions`: `fused_range_aggregation=native_grid_sum_aggregation,query_settings=set_max_threads`

## Hypothesis
For fused `sum(rate(...))` range aggregation, removing thread cap (`no_thread_cap`) may improve p50 by allowing wider parallelism on high-series 5m range workload.

## Change
- `internal/promshim/native/renderer/thread_cap_policy.go`
- `fusedRateAggregationThreadSettings` now prefers no thread cap for `rate` path.

## Validation
- `go test ./internal/promshim/native/renderer ./internal/promshim/native/physical -run 'TestLowerAggregationGolden|TestLowerRangeFunctionGolden|TestLowerHistogramFunctionGolden|TestChooseFusedRangeAggregation|TestPreferNoThreadCap'`

## Status
Benchmark running:
- process: `proc_3`
- artifact: `harness/artifacts/bench/standalone/20260429-iter40-sum-rate-5m-no-thread-cap/`

## Post-change measurement

Artifact:
- `harness/artifacts/bench/standalone/20260429-iter40-sum-rate-5m-no-thread-cap/`

Compared with accepted reference (`iter20`):
- shim p50: `3462.0ms -> 1198.9ms` (**-65.4%**)
- CH millis header: `75 -> 78` (flat/noise)
- telemetry decision pattern shifted as intended:
  - from `query_settings=set_max_threads`
  - to `query_settings=no_thread_cap`

## Correctness / compliance

Compliance run:

```bash
./scripts/run-compliance.sh --skip-native
```

Artifact root:
- `harness/artifacts/compliance/20260429T091037Z/`

Result:
- `537 passed`, `1 accepted_tolerance`, `0 unexpected_failure`
- `RECONCILE: CLEAN`

## Decision

Accepted.

This is a strategy-level runtime win on a key heavy row with clean compliance and explicit telemetry confirmation of the intended decision change.
