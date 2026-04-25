# Cost routing calibration

Generated: `2026-04-25T10:28:50Z`

## Sources

- sweep `cbe-shadow-rate-7d-sparse` manifest `harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/bench-report-7d-sparse-bench-native-lowering-7d.json`
  - memory `harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/memory-summary-bench-report-7d-sparse-bench-native-lowering-7d.json`
- sweep `cbe-prefer-rate-7d-sparse-warm` manifest `harness/artifacts/sweeps/cbe-prefer-rate-7d-sparse-warm/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/cbe-prefer-rate-7d-sparse-warm/bench-report-7d-sparse-bench-native-lowering-7d.json`
  - memory `harness/artifacts/sweeps/cbe-prefer-rate-7d-sparse-warm/memory-summary-bench-report-7d-sparse-bench-native-lowering-7d.json`
- sweep `cbe-prefer-rate-long-range-sparse` manifest `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/bench-report-1y-sparse-bench-native-lowering-1y.json`
  - bench `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/bench-report-30d-sparse-bench-native-lowering-30d.json`
  - bench `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/bench-report-7d-sparse-bench-native-lowering-7d.json`
  - memory `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/memory-summary-bench-report-1y-sparse-bench-native-lowering-1y.json`
  - memory `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/memory-summary-bench-report-30d-sparse-bench-native-lowering-30d.json`
  - memory `harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/memory-summary-bench-report-7d-sparse-bench-native-lowering-7d.json`
- sweep `cbe-prefer-rate-7d-dense-processing` manifest `harness/artifacts/sweeps/cbe-prefer-rate-7d-dense-processing/manifest.json` compliance `skipped`
  - bench `harness/artifacts/sweeps/cbe-prefer-rate-7d-dense-processing/bench-report-7d-dense-bench-processing-7d.json`
  - memory `harness/artifacts/sweeps/cbe-prefer-rate-7d-dense-processing/memory-summary-bench-report-7d-dense-bench-processing-7d.json`

## Class recommendations

| Family | Profile | Density | Rows | Native p50 ms | Local p50 ms | CostPrefer p50 ms | L/N | Strict cand. | Selected cand. | Served cand. | Cand. flips | Confidence | Recommendation | Reasons |
|---|---|---|---:|---:|---:|---:|---:|---|---|---|---:|---|---|---|
| aggregation | 1y | sparse | 2 | — | 2401.51 | 2369.05 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| aggregation | 30d | sparse | 2 | — | 929.58 | 929.79 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| aggregation | 7d | dense | 5 | — | 79.63 | 79.63 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| aggregation | 7d | sparse | 6 | — | 783.49 | 786.59 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| histogram_quantile | 1y | sparse | 1 | 159.19 | — | 160.05 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| histogram_quantile | 30d | sparse | 1 | 160.93 | — | 160.59 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| histogram_quantile | 7d | dense | 2 | 2729.03 | — | 2712.60 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| histogram_quantile | 7d | sparse | 3 | 158.50 | — | 158.93 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| processing_range_sum_rate | 7d | dense | 1 | — | — | — | — | — | — | — | 0 | low | insufficient_data | insufficient candidate data: native/local pair missing and no cost_prefer rows |
  - coverage: no cost_prefer rows in class; candidate headers missing in class rows
| range_function | 1y | sparse | 1 | 38.21 | — | 40.89 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_function | 30d | sparse | 1 | 42.27 | — | 42.08 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_function | 7d | sparse | 3 | 35.71 | — | 36.03 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_range_function | 1y | sparse | 1 | 556.25 | — | 556.25 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_range_function | 30d | sparse | 1 | 435.98 | — | 434.46 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_rate | 1y | sparse | 1 | 9674.61 | — | 9639.24 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_rate | 30d | sparse | 1 | 7127.83 | — | 7023.85 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| range_rate | 7d | sparse | 3 | 524.27 | — | 529.10 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| rate | 1y | sparse | 3 | 41.20 | — | 41.48 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| rate | 30d | sparse | 3 | 40.35 | — | 40.36 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| rate | 7d | sparse | 9 | 38.82 | 14.80 | 39.68 | 0.40 | — | — | — | 0 | high | local_candidate | local/native median 0.40 <= 0.70 for bounded candidate family |
  - coverage: candidate headers missing in class rows
| selector | 1y | sparse | 1 | 11.81 | — | 12.10 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| selector | 30d | sparse | 1 | 12.48 | — | 12.38 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
| selector | 7d | sparse | 6 | 12.59 | — | 12.55 | — | — | — | — | 0 | low | insufficient_data | native/local pair missing from sweep |
  - coverage: candidate headers missing in class rows
