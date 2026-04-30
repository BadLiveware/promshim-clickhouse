
## Sweep benchmark matrix: post-v020-7d-50k-prom-profile

Manifest: `harness/artifacts/bench/sweeps/post-v020-7d-50k-prom-profile/manifest.json`

| Category | Profile | Active series | Transport | Mode | Routing policy | Count | Strategies | Candidate flips | Prom p50 med | Shim p50 med | S/P med | Target bands |
|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|
| aggregation_by_projection | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 261.54 | 21.28 | 0.1× | n/a:1 |
| aggregation_by_projection | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 261.54 | 47.97 | 0.2× | n/a:1 |
| aggregation_by_projection | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 261.54 | 22.38 | 0.1× | n/a:1 |
| instant_absent_default | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 0.30 | 24.16 | 79.5× | n/a:1 |
| instant_absent_default | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 0.30 | 4.59 | 15.1× | n/a:1 |
| instant_absent_default | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 0.30 | 24.05 | 79.1× | n/a:1 |
| instant_avg_over_time | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 2980.74 | 937.65 | 0.3× | n/a:1 |
| instant_avg_over_time | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 2980.74 | 21558.72 | 7.2× | n/a:1 |
| instant_avg_over_time | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 2980.74 | 910.33 | 0.3× | n/a:1 |
| instant_clamp_max | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 280.74 | 43.43 | 0.2× | n/a:1 |
| instant_clamp_max | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 280.74 | 138.39 | 0.5× | n/a:1 |
| instant_clamp_max | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 280.74 | 43.52 | 0.2× | n/a:1 |
| instant_histogram_quantile | 7d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 723.48 | 229.49 | 0.3× | n/a:2 |
| instant_histogram_quantile | 7d | profile-50k | native | off | strict | 2 | local:2 | 0 | 723.48 | 1661.78 | 2.3× | n/a:2 |
| instant_histogram_quantile | 7d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 723.48 | 226.72 | 0.3× | n/a:2 |
| instant_offset_pct_change | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 766.61 | 187.43 | 0.2× | n/a:1 |
| instant_offset_pct_change | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 766.61 | 0.00 | 0.0× | n/a:1 |
| instant_offset_pct_change | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 766.61 | 187.92 | 0.2× | n/a:1 |
| instant_predict_linear | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 450.95 | 94.93 | 0.2× | n/a:1 |
| instant_predict_linear | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 450.95 | 1031.04 | 2.3× | n/a:1 |
| instant_predict_linear | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 450.95 | 94.04 | 0.2× | n/a:1 |
| instant_rate_long | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 2933.75 | 1429.93 | 0.5× | n/a:1 |
| instant_rate_long | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 2933.75 | 21870.08 | 7.5× | n/a:1 |
| instant_rate_long | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 2933.75 | 1421.75 | 0.5× | n/a:1 |
| instant_rate_short | 7d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 752.81 | 256.39 | 0.3× | n/a:2 |
| instant_rate_short | 7d | profile-50k | native | off | strict | 2 | local:2 | 0 | 752.81 | 3340.68 | 3.8× | n/a:2 |
| instant_rate_short | 7d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 752.81 | 249.14 | 0.3× | n/a:2 |
| instant_repeated_aggregation_subexpr | 7d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 2437.32 | 228.94 | 0.1× | n/a:2 |
| instant_repeated_aggregation_subexpr | 7d | profile-50k | native | off | strict | 2 | local:2 | 0 | 2437.32 | 5471.41 | 2.2× | n/a:2 |
| instant_repeated_aggregation_subexpr | 7d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 2437.32 | 229.10 | 0.1× | n/a:2 |
| instant_repeated_subexpr | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 1165.64 | 126.23 | 0.1× | n/a:1 |
| instant_repeated_subexpr | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 1165.64 | 1005.95 | 0.9× | n/a:1 |
| instant_repeated_subexpr | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 1165.64 | 128.61 | 0.1× | n/a:1 |
| instant_subquery_aggregation | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 417.18 | 132.90 | 0.3× | n/a:1 |
| instant_subquery_aggregation | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 417.18 | 4885.49 | 11.7× | n/a:1 |
| instant_subquery_aggregation | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 417.18 | 130.00 | 0.3× | n/a:1 |
| instant_subquery_smoothed_rate | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 308.81 | 45.49 | 0.1× | n/a:1 |
| instant_subquery_smoothed_rate | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 308.81 | 749.35 | 2.4× | n/a:1 |
| instant_subquery_smoothed_rate | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 308.81 | 47.30 | 0.2× | n/a:1 |
| instant_sum_rate | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 1042.98 | 226.50 | 0.2× | n/a:1 |
| instant_sum_rate | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 1042.98 | 5479.31 | 5.3× | n/a:1 |
| instant_sum_rate | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 1042.98 | 232.73 | 0.2× | n/a:1 |
| processing_instant_avg_over_time | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 1055.07 | 199.91 | 0.2× | too_slow:1 |
| processing_instant_avg_over_time | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 1055.07 | 5388.22 | 5.1× | too_slow:1 |
| processing_instant_avg_over_time | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 1055.07 | 198.69 | 0.2× | too_slow:1 |
| processing_instant_histogram_quantile | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 701.08 | 206.29 | 0.3× | in_band:1 |
| processing_instant_histogram_quantile | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 701.08 | 1609.10 | 2.3× | in_band:1 |
| processing_instant_histogram_quantile | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 701.08 | 205.89 | 0.3× | in_band:1 |
| processing_instant_sum_rate | 7d | profile-50k | native | force_supported | strict | 2 | native_sql:2 | 0 | 733.72 | 159.80 | 0.2× | in_band:1, too_slow:1 |
| processing_instant_sum_rate | 7d | profile-50k | native | off | strict | 2 | local:2 | 0 | 733.72 | 3223.06 | 3.8× | in_band:1, too_slow:1 |
| processing_instant_sum_rate | 7d | profile-50k | native | prefer | strict | 2 | native_sql:2 | 0 | 733.72 | 161.67 | 0.2× | in_band:1, too_slow:1 |
| processing_range_avg_over_time | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 13646.80 | 5554.41 | 0.4× | too_slow:1 |
| processing_range_avg_over_time | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 13646.80 | 0.00 | 0.0× | too_slow:1 |
| processing_range_avg_over_time | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 13646.80 | 5698.93 | 0.4× | too_slow:1 |
| processing_range_histogram_quantile | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 5520.82 | 1407.01 | 0.3× | too_slow:1 |
| processing_range_histogram_quantile | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 5520.82 | 0.00 | 0.0× | too_slow:1 |
| processing_range_histogram_quantile | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 5520.82 | 1393.81 | 0.3× | too_slow:1 |
| processing_range_sum_rate | 7d | profile-50k | native | force_supported | strict | 2 | :2 | 0 | 13022.78 | 0.00 | 0.0× | too_slow:2 |
| processing_range_sum_rate | 7d | profile-50k | native | off | strict | 2 | :2 | 0 | 13022.78 | 0.00 | 0.0× | too_slow:2 |
| processing_range_sum_rate | 7d | profile-50k | native | prefer | strict | 2 | chunked_native:2 | 0 | 13022.78 | 3398.20 | 0.3× | too_slow:2 |
| range_aggregation_by_projection | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 7585.49 | 5040.81 | 0.7× | n/a:1 |
| range_aggregation_by_projection | 7d | profile-50k | native | off | strict | 1 | local:1 | 0 | 7585.49 | 2469.23 | 0.3× | n/a:1 |
| range_aggregation_by_projection | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 7585.49 | 4997.08 | 0.7× | n/a:1 |
| range_avg_over_time_gauge | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 18342.28 | 6173.19 | 0.3× | n/a:1 |
| range_avg_over_time_gauge | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 18342.28 | 0.00 | 0.0× | n/a:1 |
| range_avg_over_time_gauge | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 18342.28 | 6008.47 | 0.3× | n/a:1 |
| range_max_over_time_gauge | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 17920.47 | 6167.83 | 0.3× | n/a:1 |
| range_max_over_time_gauge | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 17920.47 | 0.00 | 0.0× | n/a:1 |
| range_max_over_time_gauge | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 17920.47 | 6026.10 | 0.3× | n/a:1 |
| range_rate | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 3590.09 | 3146.12 | 0.9× | n/a:1 |
| range_rate | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 3590.09 | 0.00 | 0.0× | n/a:1 |
| range_rate | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 3590.09 | 3144.44 | 0.9× | n/a:1 |
| range_ratio_and_on_guard | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 7645.20 | 3425.50 | 0.4× | n/a:1 |
| range_ratio_and_on_guard | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 7645.20 | 0.00 | 0.0× | n/a:1 |
| range_ratio_and_on_guard | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 7645.20 | 3387.45 | 0.4× | n/a:1 |
| range_repeated_aggregation_subexpr | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | — | 5305.88 | — | n/a:1 |
| range_repeated_aggregation_subexpr | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | — | 0.00 | — | n/a:1 |
| range_repeated_aggregation_subexpr | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | — | 5086.95 | — | n/a:1 |
| range_repeated_subexpr | 7d | profile-50k | native | force_supported | strict | 8 | native_sql:8 | 0 | 10331.02 | 3173.19 | 0.3× | n/a:8 |
| range_repeated_subexpr | 7d | profile-50k | native | off | strict | 8 | :8 | 0 | 10331.02 | 0.00 | 0.0× | n/a:8 |
| range_repeated_subexpr | 7d | profile-50k | native | prefer | strict | 8 | native_sql:8 | 0 | 10331.02 | 3122.41 | 0.3× | n/a:8 |
| range_sibling_rate_dedup | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 3265.76 | 2186.31 | 0.7× | n/a:1 |
| range_sibling_rate_dedup | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 3265.76 | 0.00 | 0.0× | n/a:1 |
| range_sibling_rate_dedup | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 3265.76 | 2199.46 | 0.7× | n/a:1 |
| range_subquery_rate_over_aggregate | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 3290.40 | 2786.37 | 0.8× | n/a:1 |
| range_subquery_rate_over_aggregate | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 3290.40 | 0.00 | 0.0× | n/a:1 |
| range_subquery_rate_over_aggregate | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 3290.40 | 2838.29 | 0.9× | n/a:1 |
| range_sum_rate | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 17904.19 | 5125.77 | 0.3× | n/a:1 |
| range_sum_rate | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 17904.19 | 0.00 | 0.0× | n/a:1 |
| range_sum_rate | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 17904.19 | 5096.13 | 0.3× | n/a:1 |
| range_topk_histogram_quantile | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 6437.03 | 3850.36 | 0.6× | n/a:1 |
| range_topk_histogram_quantile | 7d | profile-50k | native | off | strict | 1 | :1 | 0 | 6437.03 | 0.00 | 0.0× | n/a:1 |
| range_topk_histogram_quantile | 7d | profile-50k | native | prefer | strict | 1 | native_sql:1 | 0 | 6437.03 | 3773.45 | 0.6× | n/a:1 |
| selector_plain | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 339.78 | 69.30 | 0.2× | n/a:1 |
| selector_plain | 7d | profile-50k | native | off | strict | 1 | delegated_promql:1 | 0 | 339.78 | 60.72 | 0.2× | n/a:1 |
| selector_plain | 7d | profile-50k | native | prefer | strict | 1 | delegated_promql:1 | 0 | 339.78 | 61.73 | 0.2× | n/a:1 |
| selector_regex | 7d | profile-50k | native | force_supported | strict | 1 | native_sql:1 | 0 | 307.15 | 71.73 | 0.2× | n/a:1 |
| selector_regex | 7d | profile-50k | native | off | strict | 1 | delegated_promql:1 | 0 | 307.15 | 65.48 | 0.2× | n/a:1 |
| selector_regex | 7d | profile-50k | native | prefer | strict | 1 | delegated_promql:1 | 0 | 307.15 | 62.75 | 0.2× | n/a:1 |

