# 7d profile-50k benchmark matrix

`profile-50k-baseline` is the first full 7d active-series benchmark sweep against the seeded 50k-series fixture. Use this as a point-in-time comparison of promshim/ClickHouse against Prometheus, not as a general production claim.

## Takeaway

On the successfully served rows, promshim in `prefer` mode is usually faster than Prometheus on the 7d/50k fixture: median shim/Prometheus p50 is `0.59×` across comparable rows. The result is not cleanly green because several range-function rows failed warmup or timed out, and two served rows are materially slower than Prometheus.

The latest PR #14 processing refresh shows the 8-row processing corpus improved on every representative `prefer` row versus this baseline: geometric mean p50 is `0.60×` of baseline (`~40%` lower), and current median shim/Prometheus p50 is `0.31×`.

| Scope | Value |
|---|---:|
| Sweep artifact | `harness/artifacts/bench/sweeps/profile-50k-baseline/` |
| Profile | `7d` |
| Active-series preset | `profile-50k` |
| Actual active series | `50,024` |
| Samples | `2,016,967,680` |
| Transport | `native` |
| promshim mode | `prefer` |
| Routing policy | `strict` |
| Corpora | `bench-native-lowering-7d.json`, `bench-processing-7d.json` |
| Compliance | skipped |
| Memory mode | `summary` |

## Aggregate comparison

Lower S/P is better for promshim; `0.59×` means shim p50 is 59% of Prometheus p50.

| Metric | Result |
|---|---:|
| Total benchmark mode rows | `42` |
| Rows with recorded serving strategy | `33` |
| Native SQL rows | `31` |
| Whole-query delegation rows | `2` |
| Comparable Prometheus/shim p50 rows | `32` |
| Faster than Prometheus (`S/P < 0.9`) | `28` |
| Near parity (`0.9 <= S/P <= 1.1`) | `1` |
| Slower than Prometheus (`S/P > 1.1`) | `3` |
| Median S/P across comparable rows | `0.59×` |
| Geomean S/P across comparable rows | `0.71×` |
| Geomean S/P excluding tiny-query outlier | `0.61×` |

The tiny-query outlier is `absent_or_vector_default_instant`: Prometheus p50 was `0.24 ms`, so promshim's `18.68 ms` absolute latency becomes a `78.8×` ratio.

## PR #14 processing refresh

Focused current-branch processing run on the same `7d` / `profile-50k` fixture. Lower p50 is better; S/P is current shim p50 divided by current Prometheus p50. This is a focused processing-corpus update, not a replacement for the full broad matrix below.

| Scope | Value |
|---|---:|
| Current artifact | `harness/artifacts/bench/standalone/pr14-current-processing-prom-profile/` |
| Baseline artifact | `harness/artifacts/bench/sweeps/profile-50k-baseline/` |
| Command family | `run-bench.sh` focused processing corpus |
| Active series | `50,024` |
| Modes | `prefer` |
| Routing policy | `strict` |
| Repeats / warmup | `3 / 1` |
| Memory / CH / Prom profile | `summary / summary / runtime` |
| Query-log coverage | `8` rows, `0` missing log comments |

| Query | Baseline prefer p50 ms | Current prefer p50 ms | Δ vs baseline | Current Prom p50 ms | Current S/P |
|---|---:|---:|---:|---:|---:|
| `sum rate 1h instant` | `123.95` | `76.95` | `-37.9%` | `248.85` | `0.31×` |
| `sum rate 6h instant` | `487.30` | `286.70` | `-41.2%` | `874.57` | `0.33×` |
| `avg memory 6h instant` | `476.47` | `218.75` | `-54.1%` | `880.17` | `0.25×` |
| `histogram quantile instant` | `217.95` | `217.27` | `-0.3%` | `397.27` | `0.55×` |
| `sum rate 5m / 24h range` | `1,977.16` | `1,757.74` | `-11.1%` | `4,297.77` | `0.41×` |
| `sum rate 1h / 7d range` | `10,337.95` | `5,478.87` | `-47.0%` | `21,956.91` | `0.25×` |
| `avg memory 1h / 24h range` | `13,997.08` | `6,444.00` | `-54.0%` | `13,810.35` | `0.47×` |
| `histogram quantile / 24h range` | `2,151.60` | `1,480.99` | `-31.2%` | `5,339.24` | `0.28×` |

The remaining heavy rows are resource-heavy despite the p50 gains. Duration-capped native chunking moved `sum rate 1h / 7d range` to `2.36 GiB` memory p95 and `307.9M` read rows; current ClickHouse profile highlights now show `avg memory 1h / 24h range` at `8.2 GiB` memory p95 and `710.1M` read rows, and `histogram quantile / 24h range` at `3.9 GiB` memory p95 and `437.4M` read rows.

## Resource vs Prometheus runtime profile

The current artifact was run with `--prometheus-profile runtime`, which samples Prometheus `/metrics` around one measured Prometheus query per row. The Prometheus figures are process/Go-runtime deltas, not exact per-query accounting; ClickHouse figures are from query-log/profile summaries. Lower S/P is better. Weighted total uses latency `50%`, memory `30%`, and CPU `20%`.

| Query | Strategy | Lat S/P | Mem S/P | CPU S/P | Weighted total | CH mem p95 | Prom heap Δ | CH CPU | Prom CPU |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `sum rate 1h instant` | `native_sql` | `0.31×` | `0.5×` | `3.5×` | `0.6×` | `0.02 GiB` | `35 MiB` | `1.0s` | `0.3s` |
| `sum rate 6h instant` | `native_sql` | `0.33×` | `2.1×` | `5.9×` | `1.0×` | `0.14 GiB` | `70 MiB` | `6.8s` | `1.2s` |
| `avg memory 6h instant` | `native_sql` | `0.25×` | `0.3×` | `4.3×` | `0.4×` | `0.02 GiB` | `77 MiB` | `5.1s` | `1.2s` |
| `histogram quantile instant` | `native_sql` | `0.55×` | `0.7×` | `2.6×` | `0.8×` | `0.03 GiB` | `42 MiB` | `1.5s` | `0.6s` |
| `sum rate 5m / 24h range` | `chunked_native` | `0.41×` | `4.2×` | `1.3×` | `1.0×` | `0.78 GiB` | `190 MiB` | `6.6s` | `5.1s` |
| `sum rate 1h / 7d range` | `chunked_native` | `0.25×` | `6.3×` | `0.7×` | `0.8×` | `2.36 GiB` | `386 MiB` | `19.0s` | `25.9s` |
| `avg memory 1h / 24h range` | `native_sql` | `0.47×` | `26.7×` | `2.3×` | `2.2×` | `8.23 GiB` | `315 MiB` | `37.6s` | `16.5s` |
| `histogram quantile / 24h range` | `native_sql` | `0.28×` | `31.4×` | `5.2×` | `2.1×` | `3.89 GiB` | `127 MiB` | `33.8s` | `6.5s` |

Aggregate geomeans: all 8 rows latency `0.34×`, memory `2.80×`, CPU `2.67×`, weighted total `0.97×`; range rows latency `0.34×`, memory `12.20×`, CPU `1.83×`, weighted total `1.39×`.

## Native range auto-chunking tradeoff

Native range auto-chunking is a resource-safety path for native-grid range aggregation rows. It is not a latency optimization. The current default combines a point cap with a safe output-duration cap (`PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES=289`, `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS=86400`, `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS=12`) so coarse-step long-range queries do not keep multi-day chunks only because they have fewer output points.

The current duration-cap hit-set benchmark (`harness/artifacts/bench/standalone/pr14-native-chunking-duration-cap/`) keeps the high-risk rows faster than Prometheus while lowering ClickHouse peak memory. It also fixes the coarse-step `sum rate 1h / 7d @15m` case that was still `8.26 GiB` with the earlier point-only cap.

| Query | No-chunk p50 | Duration-cap p50 | Prom p50 | Duration-cap S/P | No-chunk mem p95 | Duration-cap mem p95 |
|---|---:|---:|---:|---:|---:|---:|
| `sum rate 5m / 24h @1m` | `1,257 ms` | `1,725 ms` | `4,226 ms` | `0.41×` | `3.59 GiB` | `0.77 GiB` |
| `sum rate 1h / 7d @15m` | `4,519 ms` | `5,954 ms` | `19,074 ms` | `0.31×` | `18.98 GiB` | `2.86 GiB` |
| `sum rate 1h / 7d @5m` | `4,494 ms` | `5,464 ms` | `21,956 ms` | `0.25×` | `15.41 GiB` | `2.36 GiB` |

The selected path is visible as `chunked_native` in `X-Promshim-Strategy`, benchmark strategy columns, and explain plans so regressions can be correlated with the resource-safety route.

## Category matrix

| Category | Count | Strategy mix | Prom p50 median | Shim p50 median | S/P median | Target bands |
|---|---:|---|---:|---:|---:|---|
| `aggregation_by_projection` | 1 | `native_sql:1` | `66.21` | `20.89` | `0.3×` | `n/a:1` |
| `instant_absent_default` | 1 | `native_sql:1` | `0.24` | `18.68` | `78.8×` | `n/a:1` |
| `instant_avg_over_time` | 1 | `native_sql:1` | `2753.11` | `2095.08` | `0.8×` | `n/a:1` |
| `instant_clamp_max` | 1 | `native_sql:1` | `77.29` | `41.07` | `0.5×` | `n/a:1` |
| `instant_histogram_quantile` | 2 | `native_sql:2` | `382.66` | `233.36` | `0.6×` | `n/a:2` |
| `instant_offset_pct_change` | 1 | `native_sql:1` | `219.18` | `177.81` | `0.8×` | `n/a:1` |
| `instant_predict_linear` | 1 | error | `235.12` | `0.00` | n/a | `n/a:1` |
| `instant_rate_long` | 1 | `native_sql:1` | `2724.81` | `2147.86` | `0.8×` | `n/a:1` |
| `instant_rate_short` | 2 | `native_sql:2` | `545.25` | `329.12` | `0.6×` | `n/a:2` |
| `instant_repeated_aggregation_subexpr` | 2 | `native_sql:2` | `1719.63` | `484.19` | `0.3×` | `n/a:2` |
| `instant_repeated_subexpr` | 1 | `native_sql:1` | `496.12` | `150.54` | `0.3×` | `n/a:1` |
| `instant_subquery_aggregation` | 1 | `native_sql:1` | `225.06` | `132.00` | `0.6×` | `n/a:1` |
| `instant_subquery_smoothed_rate` | 1 | `native_sql:1` | `96.34` | `41.84` | `0.4×` | `n/a:1` |
| `instant_sum_rate` | 1 | `native_sql:1` | `859.79` | `484.69` | `0.6×` | `n/a:1` |
| `processing_instant_avg_over_time` | 1 | `native_sql:1` | `855.48` | `476.47` | `0.6×` | `in_band:1` |
| `processing_instant_histogram_quantile` | 1 | `native_sql:1` | `389.72` | `217.95` | `0.6×` | `in_band:1` |
| `processing_instant_sum_rate` | 2 | `native_sql:2` | `551.59` | `305.63` | `0.5×` | `in_band:1, too_fast:1` |
| `processing_range_avg_over_time` | 1 | `native_sql:1` | `13422.25` | `13997.08` | `1.0×` | `too_slow:1` |
| `processing_range_histogram_quantile` | 1 | `native_sql:1` | `5150.11` | `2151.60` | `0.4×` | `too_slow:1` |
| `processing_range_sum_rate` | 2 | `native_sql:2` | `12713.58` | `6157.55` | `0.5×` | `too_slow:2` |
| `range_aggregation_by_projection` | 1 | `native_sql:1` | `7229.56` | `11297.30` | `1.6×` | `n/a:1` |
| `range_avg_over_time_gauge` | 1 | timeout | `17540.90` | `0.00` | n/a | `n/a:1` |
| `range_max_over_time_gauge` | 1 | timeout | `17418.60` | `0.00` | n/a | `n/a:1` |
| `range_rate` | 1 | error | `3132.00` | `0.00` | n/a | `n/a:1` |
| `range_ratio_and_on_guard` | 1 | `native_sql:1` | `6834.54` | `4285.27` | `0.6×` | `n/a:1` |
| `range_repeated_aggregation_subexpr` | 1 | `native_sql:1` | — | `11074.20` | n/a | `n/a:1` |
| `range_repeated_subexpr` | 5 | error | `11182.73` | `0.00` | n/a | `n/a:5` |
| `range_sibling_rate_dedup` | 1 | `native_sql:1` | `2961.94` | `2360.25` | `0.8×` | `n/a:1` |
| `range_subquery_rate_over_aggregate` | 1 | `native_sql:1` | `3010.61` | `6444.35` | `2.1×` | `n/a:1` |
| `range_sum_rate` | 1 | `native_sql:1` | `17313.23` | `10363.39` | `0.6×` | `n/a:1` |
| `range_topk_histogram_quantile` | 1 | `native_sql:1` | `5511.68` | `3814.90` | `0.7×` | `n/a:1` |
| `selector_plain` | 1 | `delegated_promql:1` | `77.58` | `65.58` | `0.8×` | `n/a:1` |
| `selector_regex` | 1 | `delegated_promql:1` | `72.73` | `63.04` | `0.9×` | `n/a:1` |

## Best wins

| Query | Category | Prom p50 ms | Shim p50 ms | S/P |
|---|---|---:|---:|---:|
| `repeated_sum_rate_average_by_job_mul_6h_instant` | `instant_repeated_aggregation_subexpr` | `1717.71` | `482.87` | `0.28×` |
| `repeated_sum_rate_average_by_job_6h_instant` | `instant_repeated_aggregation_subexpr` | `1721.55` | `485.50` | `0.28×` |
| `repeated_rate_average_1h_instant_long` | `instant_repeated_subexpr` | `496.12` | `150.54` | `0.30×` |
| `sum_by_job_instant_long` | `aggregation_by_projection` | `66.21` | `20.89` | `0.32×` |
| `processing_histogram_quantile_1h_range_24h_7d` | `processing_range_histogram_quantile` | `5150.11` | `2151.60` | `0.42×` |
| `processing_sum_rate_1h_by_job_range_7d` | `processing_range_sum_rate` | `21263.21` | `10337.95` | `0.49×` |

## Slower served rows

| Query | Category | Prom p50 ms | Shim p50 ms | S/P | Interpretation |
|---|---|---:|---:|---:|---|
| `absent_or_vector_default_instant` | `instant_absent_default` | `0.24` | `18.68` | `78.8×` | Ratio is dominated by near-zero Prometheus latency; shim absolute latency is still small. |
| `subquery_rate_over_aggregate_5m_range_1d` | `range_subquery_rate_over_aggregate` | `3010.61` | `6444.35` | `2.1×` | Real slower row; good optimization target. |
| `sum_by_job_range_7d` | `range_aggregation_by_projection` | `7229.56` | `11297.30` | `1.6×` | Real slower row; good optimization target. |
| `processing_avg_memory_1h_by_job_type_range_24h_7d` | `processing_range_avg_over_time` | `13422.25` | `13997.08` | `1.0×` | Near parity, slightly slower. |

`subquery_rate_over_aggregate_5m_range_1d` and `sum_by_job_range_7d` are the main served regressions to profile before claiming a broad win.

## Failed or incomplete rows

Rows below did not produce comparable shim p50s. The harness matrix renders failed shim timings as `0.00`; treat these as failures, not wins.

| Query | Category | Prom p50 ms | Failure |
|---|---|---:|---|
| `repeated_rate_average_5m_range_1d` | `range_repeated_subexpr` | `7271.25` | HTTP `400` during warmup |
| `repeated_rate_average_3x_5m_range_1d` | `range_repeated_subexpr` | `11139.19` | HTTP `400` during warmup |
| `repeated_rate_average_4x_mul_5m_range_1d` | `range_repeated_subexpr` | `15064.15` | HTTP `400` during warmup |
| `repeated_rate_average_4x_unit_fraction_5m_range_1d` | `range_repeated_subexpr` | `15070.68` | HTTP `400` during warmup |
| `repeated_rate_average_3x_unit_fraction_5m_range_1d` | `range_repeated_subexpr` | `11182.73` | HTTP `400` during warmup |
| `rate_5m_range_1d` | `range_rate` | `3132.00` | HTTP `400` during warmup |
| `max_over_time_gauge_by_3labels_1h_range_7d` | `range_max_over_time_gauge` | `17418.60` | client timeout awaiting headers |
| `avg_over_time_gauge_by_3labels_1h_range_7d` | `range_avg_over_time_gauge` | `17540.90` | client timeout awaiting headers |
| `predict_linear_lt_zero_instant` | `instant_predict_linear` | `235.12` | HTTP `422` during warmup |
| `repeated_sum_rate_average_by_job_range_7d` | `range_repeated_aggregation_subexpr` | — | Prometheus p50 missing in report; shim p50 was `11074.20 ms`. |

## Disk context for the seeded fixture

The same seeded fixture used for this sweep measured approximately half the Prometheus disk footprint in ClickHouse.

| Store | Measurement |
|---|---:|
| Prometheus `/prometheus` | `11.6G` (`12,416,275,221` bytes, ~`11.56 GiB`) |
| ClickHouse `/var/lib/clickhouse` | `5.7G` (`6,019,446,122` bytes, ~`5.61 GiB`) |
| ClickHouse TimeSeries data table | `5.39 GiB` compressed / `60.11 GiB` uncompressed |

Per-sample storage on this fixture: Prometheus data dir `~6.16 B/sample`, ClickHouse full data dir `~2.98 B/sample`, and ClickHouse compressed TimeSeries data table `~2.87 B/sample`.

## Series coverage

The benchmark queries did not each hit every active series. They mostly scan one metric family at a time, so they exercise large per-metric subsets rather than full-database scans across all metric names.

Distinct series in ClickHouse after the seed:

| Metric family | Distinct series | Share of real seeded series |
|---|---:|---:|
| `demo_api_request_duration_seconds_bucket` | `19,240` | `38.5%` |
| `demo_cpu_usage_seconds_total` | `11,544` | `23.1%` |
| `demo_memory_usage_bytes` | `11,544` | `23.1%` |
| `demo_api_request_duration_seconds_count` | `3,848` | `7.7%` |
| `demo_api_request_duration_seconds_sum` | `3,848` | `7.7%` |

Implications:

- CPU queries without `mode` filters scan about `11.5k` series (`23%` of active series); with `mode="user"|"system"|"idle"`, they scan about `3.8k` series (`7.7%`).
- Memory queries scan about `11.5k` series (`23%`) unless filtered by `type`.
- Histogram bucket queries scan about `19.2k` series (`38.5%`).
- Ratio/join queries that combine metric families can touch multiple subsets, but most rows are not all-series scans.
- `absent(demo_cpu_usage_seconds_total{job="nonexistent"})` intentionally matches no real data.

## Follow-up targets

1. Triage the HTTP `400` and `422` warmup failures so the matrix does not contain false `0.00 ms` rows.
2. Profile the two timeout rows to distinguish missing caps from genuinely slow plans.
3. Optimize or route around the slower served rows: `subquery_rate_over_aggregate_5m_range_1d` and `sum_by_job_range_7d`.
4. Repeat the sweep after those fixes before using the result in external performance claims.
