
## Sweep benchmark matrix: post-v020-30d-50k-prom-profile

Manifest: `harness/artifacts/bench/sweeps/post-v020-30d-50k-prom-profile/manifest.json`

| Category | Profile | Active series | Transport | Mode | Routing policy | Count | Strategies | Candidate flips | Prom p50 med | Shim p50 med | S/P med | Target bands |
|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| instant_avg_over_time | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 5212.45 | 1168.41 | 0.2× | n/a:1 |
| instant_avg_over_time | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | 5212.45 | 0.00 | 0.0× | n/a:1 |
| instant_avg_over_time | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 5212.45 | 1157.76 | 0.2× | n/a:1 |
| instant_histogram_quantile | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 648.28 | 245.18 | 0.4× | n/a:1 |
| instant_histogram_quantile | 30d | profile-50k | native | off | strict | 1 | local:1 | 0 | 648.28 | 2353.17 | 3.6× | n/a:1 |
| instant_histogram_quantile | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 648.28 | 243.96 | 0.4× | n/a:1 |
| instant_rate_long | 30d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 2970.98 | 1285.57 | 0.4× | n/a:2 |
| instant_rate_long | 30d | profile-50k | native | off | strict | 2 | :1, local:1 | 0 | 2970.98 | 2719.33 | 3.0× | n/a:2 |
| instant_rate_long | 30d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 2970.98 | 1276.60 | 0.4× | n/a:2 |
| instant_rate_short | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 411.51 | 167.76 | 0.4× | n/a:1 |
| instant_rate_short | 30d | profile-50k | native | off | strict | 1 | local:1 | 0 | 411.51 | 1439.82 | 3.5× | n/a:1 |
| instant_rate_short | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 411.51 | 165.49 | 0.4× | n/a:1 |
| instant_sum_rate | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 915.13 | 274.48 | 0.3× | n/a:1 |
| instant_sum_rate | 30d | profile-50k | native | off | strict | 1 | local:1 | 0 | 915.13 | 5480.12 | 6.0× | n/a:1 |
| instant_sum_rate | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 915.13 | 274.10 | 0.3× | n/a:1 |
| processing_instant_avg_over_time | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 943.29 | 214.83 | 0.2× | in_band:1 |
| processing_instant_avg_over_time | 30d | profile-50k | native | off | strict | 1 | local:1 | 0 | 943.29 | 5497.59 | 5.8× | in_band:1 |
| processing_instant_avg_over_time | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 943.29 | 211.80 | 0.2× | in_band:1 |
| processing_instant_histogram_quantile | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 654.04 | 244.14 | 0.4× | in_band:1 |
| processing_instant_histogram_quantile | 30d | profile-50k | native | off | strict | 1 | local:1 | 0 | 654.04 | 2360.32 | 3.6× | in_band:1 |
| processing_instant_histogram_quantile | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 654.04 | 243.24 | 0.4× | in_band:1 |
| processing_instant_sum_rate | 30d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 699.34 | 219.03 | 0.3× | in_band:2 |
| processing_instant_sum_rate | 30d | profile-50k | native | off | strict | 2 | local:2 | 0 | 699.34 | 3501.20 | 4.5× | in_band:2 |
| processing_instant_sum_rate | 30d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 699.34 | 217.15 | 0.3× | in_band:2 |
| processing_range_avg_over_time | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 5468.79 | — | n/a:1 |
| processing_range_avg_over_time | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| processing_range_avg_over_time | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 5359.40 | — | n/a:1 |
| processing_range_histogram_quantile | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 8315.48 | 1934.10 | 0.2× | too_slow:1 |
| processing_range_histogram_quantile | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | 8315.48 | 0.00 | 0.0× | too_slow:1 |
| processing_range_histogram_quantile | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 8315.48 | 1927.13 | 0.2× | too_slow:1 |
| processing_range_sum_rate | 30d | profile-50k | native | force_supported | strict | 2 | :1, native_sql:1 | 0 | 5747.10 | 2836.98 | 0.0× | n/a:1, too_slow:1 |
| processing_range_sum_rate | 30d | profile-50k | native | off | strict | 2 | :2 | 0 | 5747.10 | 0.00 | 0.0× | n/a:1, too_slow:1 |
| processing_range_sum_rate | 30d | profile-50k | native | prefer | strict | 2 | chunked_native:1, native_sql:1 | 0 | 5747.10 | 3968.71 | 0.4× | n/a:1, too_slow:1 |
| range_avg_over_time | 30d | profile-50k | native | force_supported | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_avg_over_time | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_avg_over_time | 30d | profile-50k | native | prefer | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_avg_over_time_gauge | 30d | profile-50k | native | force_supported | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_avg_over_time_gauge | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_avg_over_time_gauge | 30d | profile-50k | native | prefer | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_max_over_time_gauge | 30d | profile-50k | native | force_supported | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_max_over_time_gauge | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_max_over_time_gauge | 30d | profile-50k | native | prefer | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_rate | 30d | profile-50k | native | force_supported | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_rate | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_rate | 30d | profile-50k | native | prefer | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_ratio_and_on_guard | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 14850.26 | — | n/a:1 |
| range_ratio_and_on_guard | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_ratio_and_on_guard | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 14096.25 | — | n/a:1 |
| range_sibling_rate_dedup | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 8140.48 | — | n/a:1 |
| range_sibling_rate_dedup | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_sibling_rate_dedup | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 7739.35 | — | n/a:1 |
| range_subquery_rate_over_aggregate | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 22739.92 | — | n/a:1 |
| range_subquery_rate_over_aggregate | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_subquery_rate_over_aggregate | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 23008.75 | — | n/a:1 |
| range_sum_rate | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 4832.39 | — | n/a:1 |
| range_sum_rate | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_sum_rate | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 4805.38 | — | n/a:1 |
| range_topk_histogram_quantile | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 11316.44 | — | n/a:1 |
| range_topk_histogram_quantile | 30d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_topk_histogram_quantile | 30d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 10927.77 | — | n/a:1 |
| selector_plain | 30d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 270.18 | 70.00 | 0.3× | n/a:1 |
| selector_plain | 30d | profile-50k | native | off | strict | 1 | delegated_promql:1 | 0 | 270.18 | 59.20 | 0.2× | n/a:1 |
| selector_plain | 30d | profile-50k | native | prefer | strict | 1 | delegated_promql:1 | 0 | 270.18 | 60.41 | 0.2× | n/a:1 |

