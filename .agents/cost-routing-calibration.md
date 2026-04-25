# Cost routing calibration

Generated: `2026-04-24T23:51:03Z`

## Sources

- sweep `sweep-smoke-live-2` manifest `harness/artifacts/sweeps/sweep-smoke-live-2/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/sweep-smoke-live-2/bench-report-7d-sparse-bench-native-lowering-7d.json`
  - memory `harness/artifacts/sweeps/sweep-smoke-live-2/memory-summary-bench-report-7d-sparse-bench-native-lowering-7d.json`

## Class recommendations

| Family | Profile | Density | Rows | Native p50 ms | Local p50 ms | L/N | Recommendation | Reasons |
|---|---|---|---:|---:|---:|---:|---|---|
| instant_avg_over_time | 7d | sparse | 1 | 17.08 | — | — | insufficient_data | native/local pair missing from sweep |
| instant_histogram_quantile | 7d | sparse | 1 | 132.81 | — | — | insufficient_data | native/local pair missing from sweep |
| instant_rate_long | 7d | sparse | 1 | 25.35 | — | — | insufficient_data | native/local pair missing from sweep |
| instant_rate_short | 7d | sparse | 2 | 17.22 | — | — | insufficient_data | native/local pair missing from sweep |
| instant_sum_rate | 7d | sparse | 1 | — | — | — | insufficient_data | native/local pair missing from sweep |
| range_rate | 7d | sparse | 1 | 616.75 | — | — | insufficient_data | native/local pair missing from sweep |
| range_sum_rate | 7d | sparse | 1 | — | — | — | insufficient_data | native/local pair missing from sweep |
| selector_plain | 7d | sparse | 1 | 9.40 | — | — | insufficient_data | native/local pair missing from sweep |
| selector_regex | 7d | sparse | 1 | 10.21 | — | — | insufficient_data | native/local pair missing from sweep |
