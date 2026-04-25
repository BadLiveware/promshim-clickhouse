# Cost routing calibration

Generated: `2026-04-25T16:36:55Z`

## Sources

- sweep `promshim-optimization-foundation-7d-sparse` manifest `harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/bench-report-7d-sparse-bench-optimization-tuning-7d.json`
  - memory `harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/memory-summary-bench-report-7d-sparse-bench-optimization-tuning-7d.json`
- sweep `settings-profile-default-safe-smoke` manifest `harness/artifacts/sweeps/settings-profile-default-safe-smoke/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/settings-profile-default-safe-smoke/bench-report-7d-sparse-bench-optimization-tuning-7d.json`
  - ClickHouse reference profile `promshim-ch-timeseries-reference-v1`
  - promshim settings profile `default_safe`
  - memory `harness/artifacts/sweeps/settings-profile-default-safe-smoke/memory-summary-bench-report-7d-sparse-bench-optimization-tuning-7d.json`
- sweep `settings-profile-benchmark-control-smoke` manifest `harness/artifacts/sweeps/settings-profile-benchmark-control-smoke/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/settings-profile-benchmark-control-smoke/bench-report-7d-sparse-bench-optimization-tuning-7d.json`
  - ClickHouse reference profile `promshim-ch-timeseries-reference-v1`
  - promshim settings profile `benchmark_control`
  - memory `harness/artifacts/sweeps/settings-profile-benchmark-control-smoke/memory-summary-bench-report-7d-sparse-bench-optimization-tuning-7d.json`

## Class recommendations

| Family | Profile | Density | Settings profile | CH ref profile | Rows | Native p50 ms | Local p50 ms | CostPrefer p50 ms | L/N | Strict cand. | Selected cand. | Served cand. | Cand. flips | Confidence | Recommendation | Reasons |
|---|---|---|---|---|---:|---:|---:|---:|---:|---|---|---|---:|---|---|---|
| aggregation | 7d | sparse | default_safe | — | 4 | 106.17 | 23.59 | — | 0.52 | — | — | — | 0 | medium | native_required | native remains preferred for family; local/native median 0.52 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| aggregation | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 4 | 412.43 | 828.33 | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| aggregation | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 4 | 421.96 | 840.88 | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| binary | 7d | sparse | default_safe | — | 1 | 91.58 | 29.02 | — | 0.32 | — | — | — | 0 | low | insufficient_data | no initial rule for local/native median 0.32 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| binary | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 89.77 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| binary | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 90.60 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| histogram_quantile | 7d | sparse | default_safe | — | 1 | 161.52 | 19.30 | — | 0.12 | — | — | — | 0 | low | local_candidate | local/native median 0.12 <= 0.70 for bounded candidate family |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| histogram_quantile | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 158.27 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| histogram_quantile | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 159.69 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| increase | 7d | sparse | default_safe | — | 1 | 48.61 | 9.85 | — | 0.20 | — | — | — | 0 | low | insufficient_data | no initial rule for local/native median 0.20 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| increase | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 47.98 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| increase | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 47.64 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_function | 7d | sparse | default_safe | — | 1 | 36.86 | 73.47 | — | 1.99 | — | — | — | 0 | low | native_required | native remains preferred for family; local/native median 1.99 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_function | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 36.13 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_function | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 37.87 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_rate | 7d | sparse | default_safe | — | 1 | 538.23 | 3574.78 | — | 6.64 | — | — | — | 0 | low | native_required | native remains preferred for family; local/native median 6.64 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_rate | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 535.13 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_rate | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 526.60 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_selector | 7d | sparse | default_safe | — | 1 | 305.19 | 14.26 | — | 0.05 | — | — | — | 0 | low | native_required | native remains preferred for family; local/native median 0.05 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_selector | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 13.01 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_selector | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 13.31 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| rate | 7d | sparse | default_safe | — | 2 | 38.51 | 21.76 | — | 0.56 | — | — | — | 0 | medium | local_candidate | local/native median 0.56 <= 0.70 for bounded candidate family |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| rate | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 2 | 38.27 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| rate | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 2 | 37.97 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| selector | 7d | sparse | default_safe | — | 2 | 31.15 | 13.17 | — | 0.42 | — | — | — | 0 | medium | local_candidate | local/native median 0.42 <= 0.70 for bounded candidate family |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| selector | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 2 | 12.75 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| selector | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 2 | 12.65 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| vector_match | 7d | sparse | default_safe | — | 1 | 19.89 | 16.75 | — | 0.84 | — | — | — | 0 | low | insufficient_data | no initial rule for local/native median 0.84 |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| vector_match | 7d | sparse | benchmark_control | promshim-ch-timeseries-reference-v1 | 1 | 18.92 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| vector_match | 7d | sparse | default_safe | promshim-ch-timeseries-reference-v1 | 1 | 18.77 | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
