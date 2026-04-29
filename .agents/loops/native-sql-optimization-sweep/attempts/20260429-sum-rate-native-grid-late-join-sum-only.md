# Sum-rate native-grid late join (sum-aggregation path only)

## Scope
Tier-2/native-SQL only.

Target shape: fused/native-grid sum aggregation over range function rows (e.g. `sum by (...) (rate(...[window]))`).

Representative row:
- `processing_sum_rate_5m_by_job_range_24h_7d`

## Baseline
Reusing baseline artifact:
- `harness/artifacts/bench/sweeps/20260429-iter5-tier2-baseline-processing-15m/`

Baseline for representative row (`force_supported` + `strict`):
- shim p50 ~4994.7 ms
- query-log row count/read bytes from profile artifact in baseline sweep

## Hypothesis
Apply late-series join only in **sum aggregation SQL builder path**:
- compute per-id native-grid values from data with `id IN matchedSeries` filter,
- join tags after per-id aggregation,
- keep generic native-grid rows path unchanged.

Expected: reduce raw-sample join pressure for sum-rate range rows while avoiding broad rows-path regressions from iteration 19.

## Layer
Physical SQL shape change in `internal/promshim/storage/selector_sql.go` (sum aggregation builder only).

## Post-change measurement

Artifact:
`harness/artifacts/bench/standalone/20260429-iter20-sum-rate-5m-late-join-sum-only/`

Representative row (`processing_sum_rate_5m_by_job_range_24h_7d`, force_supported+strict):

- `queryDurationP50Ms`: 4978 -> 3442 (**-30.9%**)
- `memoryP50Bytes`: 3,876,970,977.5 -> 3,827,968,978.5 (**-1.3%**)
- `readRowsP50`: 698,968,337 -> 335,526,720 (**-52.0%**)
- `readBytesP50`: 17,109,574,189 -> 10,342,708,938 (**-39.5%**)
- `joinProbeTableRowCountP50`: 66,783,714.5 -> 11,544 (**~ -99.98%**)
- `joinResultRowCountP50`: 66,724,320 -> 11,544 (**~ -99.98%**)

## Correctness / compliance

Compliance run:

```bash
./scripts/run-compliance.sh --skip-native
```

Artifact root:
`harness/artifacts/compliance/20260429T081103Z/`

Result:
- `537 passed`, `1 accepted_tolerance`, `0 unexpected_failure`
- `RECONCILE: CLEAN`

## Decision

Accepted.

This keeps the late-join optimization scoped to the sum-aggregation native-grid path and delivers material latency and scan/join reductions on the representative heavy row without compliance regressions.
