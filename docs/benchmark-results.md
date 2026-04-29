# Benchmark results snapshot

This page preserves the current detailed benchmark snapshot. Use it as routing-calibration and regression-tripwire context, not as a general claim that promshim beats Prometheus.

Benchmark matrices below were refreshed from live sweep artifacts on this branch.
For the newer 7d/50k active-series comparison against Prometheus, see
[`profile-50k-benchmark-matrix.md`](profile-50k-benchmark-matrix.md).

Latest focused PR #14 processing refresh:

- `harness/artifacts/bench/standalone/pr14-current-processing-focused`

Historical matrix artifacts:

- `harness/artifacts/bench/sweeps/readme-refresh-20260426-7d-sparse`
- `harness/artifacts/bench/sweeps/readme-refresh-20260426-long-range-sparse`
- `harness/artifacts/bench/sweeps/readme-refresh-20260426-7d-dense-processing`

These are not a claim that promshim beats Prometheus in this local harness.
They are a routing calibration/tripwire dataset. In addition to p50 latency, the
matrices include CBE decision telemetry (`routingDecision`, `routingReason`,
strict/served candidate IDs) and memory-side signals from
`memory-summary-*.json`.

The matrix artifacts below were captured before native-grid lowering became the
default. They therefore reflect the explicit rollback behavior
`PROM_SHIM_NATIVE_GRID_FUNCTIONS=off`. The native-grid default was measured
separately as a focused before/after check:
`harness/artifacts/bench/sweeps/native-grid-focused-baseline` and
`harness/artifacts/bench/sweeps/native-grid-focused-candidate`.

### PR #14 processing refresh

On the `7d` / `profile-50k` fixture, the current PR branch improved all 8 processing-corpus `prefer` rows versus the original `profile-50k-baseline`: geometric mean p50 is `0.60×` of baseline (`~40%` lower), and current median shim/Prometheus p50 is `0.31×`. Conditions: focused `run-bench.sh` processing run, `prefer,force_supported`, `strict`, 3 repeats, 1 warmup, memory and ClickHouse profile summaries enabled.

| Query | Baseline prefer p50 ms | Current prefer p50 ms | Δ vs baseline | Current Prom p50 ms | Current S/P |
|---|---:|---:|---:|---:|---:|
| `sum rate 1h instant` | `123.95` | `77.47` | `-37.5%` | `241.96` | `0.32×` |
| `sum rate 6h instant` | `487.30` | `294.05` | `-39.7%` | `888.35` | `0.33×` |
| `avg memory 6h instant` | `476.47` | `225.68` | `-52.6%` | `893.96` | `0.25×` |
| `histogram quantile instant` | `217.95` | `217.58` | `-0.2%` | `402.12` | `0.54×` |
| `sum rate 5m / 24h range` | `1,977.16` | `1,263.25` | `-36.1%` | `4,360.70` | `0.29×` |
| `sum rate 1h / 7d range` | `10,337.95` | `4,249.01` | `-58.9%` | `21,952.81` | `0.19×` |
| `avg memory 1h / 24h range` | `13,997.08` | `6,531.18` | `-53.3%` | `14,010.09` | `0.47×` |
| `histogram quantile / 24h range` | `2,151.60` | `1,635.32` | `-24.0%` | `5,402.51` | `0.30×` |

Tradeoff: the heavy range rows are still large ClickHouse jobs, with current memory p95 up to `15.4 GiB` and read volume up to `931.4M` rows; treat these numbers as local harness evidence, not broad production claims.

### Native range auto-chunking check

Default native range auto-chunking (`PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES=289`) trades latency and extra ClickHouse work for lower peak memory on native-grid range aggregation rows. Conditions: focused hit-set run, `prefer`, `strict`, 3 repeats, 1 warmup, memory and ClickHouse profile summaries enabled; Prometheus comparison used the same hit set with Prom timings enabled.

| Query | No-chunk p50 | Auto-chunk p50 | Auto vs Prom | No-chunk mem p95 | Auto mem p95 | Read rows/logical request |
|---|---:|---:|---:|---:|---:|---:|
| `sum rate 5m / 24h @1m` | `1,257 ms` | `1,917 ms` | `0.43×` | `3.59 GiB` | `0.78 GiB` | `0.34B → 0.56B` |
| `sum rate 1h / 7d @15m` | `4,519 ms` | `5,221 ms` | `0.24×` | `18.98 GiB` | `8.26 GiB` | `0.93B → 1.27B` |
| `sum rate 1h / 7d @5m` | `4,494 ms` | `6,370 ms` | `0.24×` | `15.41 GiB` | `2.35 GiB` | `0.93B → 1.87B` |

Across the hit set, auto-chunking was `1.36×` slower by geomean p50, but reduced ClickHouse memory p95 to `0.24×` of the non-chunked path; read rows rose `1.66×` and user CPU rose `1.27×`. The strategy is visible as `chunked_native` in benchmark output, response headers, and explain plans.

### 7d sparse CBE category matrix (strict vs cost_prefer)

10 timed repeats, 2 warmups, mode `prefer`, routing policies
`strict,cost_shadow,cost_prefer`, with `cost_shadow` warmup and
`PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=rate_instant`.

| Category | Count | Strict strategy/p50 | Cost-prefer strategy/p50 | CBE decision | Δ vs strict |
|---|---:|---:|---:|---|---:|
| `aggregation_by_projection` | 1 | native_sql:1/31.95 | native_sql:1/30.69 | `strict_low_confidence:1` | -3.9% |
| `instant_avg_over_time` | 1 | native_sql:1/36.50 | native_sql:1/36.20 | `strict_over_cap:1` | -0.8% |
| `instant_histogram_quantile` | 2 | native_sql:2/133.50 | native_sql:2/133.03 | `strict_low_confidence:2` | -0.4% |
| `instant_rate_long` | 1 | native_sql:1/37.59 | native_sql:1/38.11 | `strict_over_cap:1` | +1.4% |
| `instant_rate_short` | 2 | native_sql:2/33.65 | local:2/21.24 | `local_override:2` | -36.9% |
| `instant_repeated_aggregation_subexpr` | 2 | native_sql:2/36.92 | native_sql:2/37.16 | `strict_over_cap:2` | +0.7% |
| `instant_repeated_subexpr` | 1 | native_sql:1/32.91 | native_sql:1/33.16 | `strict_low_confidence:1` | +0.8% |
| `instant_sum_rate` | 1 | native_sql:1/36.47 | native_sql:1/36.96 | `strict_low_confidence:1` | +1.3% |
| `range_aggregation_by_projection` | 1 | native_sql:1/48.83 | native_sql:1/45.28 | `strict_low_confidence:1` | -7.3% |
| `range_rate` | 1 | native_sql:1/99.54 | native_sql:1/100.20 | `strict_over_cap:1` | +0.7% |
| `range_repeated_aggregation_subexpr` | 1 | native_sql:1/168.08 | native_sql:1/168.71 | `strict_over_cap:1` | +0.4% |
| `range_repeated_subexpr` | 5 | native_sql:5/98.56 | native_sql:5/99.03 | `strict_over_cap:5` | +0.5% |
| `range_sum_rate` | 1 | native_sql:1/164.33 | native_sql:1/163.09 | `strict_over_cap:1` | -0.8% |
| `selector_plain` | 1 | delegated_promql:1/12.69 | delegated_promql:1/11.61 | `strict_low_confidence:1` | -8.6% |
| `selector_regex` | 1 | delegated_promql:1/12.51 | delegated_promql:1/12.55 | `strict_low_confidence:1` | +0.4% |

### Long-range sparse matrix (category medians)

Category medians across `7d`, `30d`, and `1y` corpora, comparing strict
(`prefer` + `strict`) to `cost_prefer`.

| Category | 7d strict | 7d cost_prefer | 7d decision | 30d strict | 30d cost_prefer | 30d decision | 1y strict | 1y cost_prefer | 1y decision |
|---|---:|---:|---|---:|---:|---|---:|---:|---|
| `aggregation_by_projection` | 31.41 | 30.78 | `strict_low_confidence:1` | — | — | — | — | — | — |
| `instant_avg_over_time` | 35.76 | 35.98 | `strict_over_cap:1` | 40.68 | 40.19 | `strict_over_cap:1` | 37.06 | 38.32 | `strict_over_cap:1` |
| `instant_histogram_quantile` | 129.54 | 130.34 | `strict_low_confidence:2` | 119.88 | 119.55 | `strict_over_cap:1` | 122.54 | 120.77 | `strict_over_cap:1` |
| `instant_rate_long` | 38.21 | 36.67 | `strict_over_cap:1` | 38.66 | 38.51 | `strict_over_cap:2` | 34.52 | 34.04 | `strict_over_cap:3` |
| `instant_rate_short` | 33.56 | 20.78 | `local_override:2` | 33.93 | 15.96 | `local_override:1` | — | — | — |
| `instant_repeated_aggregation_subexpr` | 37.00 | 36.96 | `strict_over_cap:2` | — | — | — | — | — | — |
| `instant_repeated_subexpr` | 32.59 | 32.16 | `strict_low_confidence:1` | — | — | — | — | — | — |
| `instant_sum_rate` | 36.59 | 37.20 | `strict_low_confidence:1` | 37.54 | 37.36 | `strict_over_cap:1` | 37.62 | 36.70 | `strict_over_cap:1` |
| `range_aggregation_by_projection` | 46.54 | 45.78 | `strict_low_confidence:1` | — | — | — | — | — | — |
| `range_avg_over_time` | — | — | — | 396.12 | 407.89 | `strict_over_cap:1` | 543.21 | 542.96 | `strict_over_cap:1` |
| `range_rate` | 97.80 | 98.32 | `strict_over_cap:1` | 497.04 | 501.34 | `strict_over_cap:1` | 564.27 | 571.16 | `strict_over_cap:1` |
| `range_repeated_aggregation_subexpr` | 163.14 | 164.90 | `strict_over_cap:1` | — | — | — | — | — | — |
| `range_repeated_subexpr` | 100.21 | 99.39 | `strict_over_cap:5` | — | — | — | — | — | — |
| `range_sum_rate` | 157.37 | 162.78 | `strict_over_cap:1` | 184.54 | 188.81 | `strict_over_cap:1` | 344.72 | 344.60 | `strict_over_cap:1` |
| `selector_plain` | 12.81 | 12.01 | `strict_low_confidence:1` | 12.42 | 11.93 | `strict_low_confidence:1` | 12.27 | 11.91 | `strict_low_confidence:1` |
| `selector_regex` | 12.30 | 13.33 | `strict_low_confidence:1` | — | — | — | — | — | — |

### 7d dense processing matrix

Dense-profile processing corpus (`bench-processing-7d.json`) remains a hard-cap
control where `cost_prefer` should not flip serving.

| Query | Prom band | Strict strategy/p50 | Cost-prefer strategy/p50 | Decision | Reason |
|---|---|---:|---:|---|---|
| `processing_sum_rate_1h_by_job_instant_7d` | too_fast | native_sql/53.52 | native_sql/50.15 | `strict_over_cap` | `hard_cap` |
| `processing_sum_rate_6h_by_job_mode_instant_7d` | too_fast | native_sql/60.51 | native_sql/60.07 | `strict_over_cap` | `hard_cap` |
| `processing_avg_memory_6h_by_job_type_instant_7d` | too_fast | native_sql/57.77 | native_sql/59.79 | `strict_over_cap` | `hard_cap` |
| `processing_histogram_quantile_1h_instant_7d` | too_fast | native_sql/131.61 | native_sql/130.71 | `strict_over_cap` | `hard_cap` |
| `processing_sum_rate_5m_by_job_range_24h_7d` | too_fast | native_sql/11514.28 | native_sql/11625.58 | `strict_over_cap` | `hard_cap` |
| `processing_sum_rate_1h_by_job_range_7d` | too_slow | error/timeout | error/timeout | `n/a` | `n/a` |
| `processing_avg_memory_1h_by_job_type_range_24h_7d` | in_band | native_sql/12122.48 | native_sql/11788.71 | `strict_over_cap` | `hard_cap` |
| `processing_histogram_quantile_1h_range_24h_7d` | in_band | native_sql/5325.95 | native_sql/5290.24 | `strict_over_cap` | `hard_cap` |

### Native-grid default check

With the default `PROM_SHIM_NATIVE_GRID_FUNCTIONS=prefer`, focused 7d sparse
range-rate rows showed large wins while staying on `native_sql` with one
ClickHouse roundtrip:

| Query | Rollback/off p50 | Native-grid p50 | Δ |
|---|---:|---:|---:|
| `sum_rate_by_job_range_7d` prefer | 161.12 | 70.03 | -56.5% |
| `sum_rate_by_job_range_7d` force_supported | 165.28 | 69.09 | -58.2% |
| `rate_5m_range_1d` prefer | 92.75 | 48.31 | -47.9% |
| `rate_5m_range_1d` force_supported | 97.79 | 48.71 | -50.2% |

### What this implies for CBE today

- **Strict remains the reference default and rollback path.** `cost_prefer` is
  still gated by explicit confidence/cap checks and family allowlists.
- **One served flip remains consistently validated:** short-window instant
  `rate` can serve `full_local` (`local_override`) with a large p50 win versus
  strict native SQL on sparse `7d`/`30d` profiles.
- **Most other families stay strict by design:** `strict_over_cap`,
  `strict_low_confidence`, and disabled family gates dominate outside that
  narrow allowlist.
- **Native SQL range-rate work has materially improved strict behavior:** the
  rollback SQL kernel has sparse 7d range-rate category medians around
  `98–100 ms`, and range-sum-rate medians around `157–164 ms`; the native-grid
  default is substantially faster for the focused range-rate rows above.
- **Native-grid is the default range-function kernel where validated, with a
  simple rollback:** set `PROM_SHIM_NATIVE_GRID_FUNCTIONS=off` to return
  supported `rate`, `irate`, `delta`, `idelta`, and `last_over_time` range
  selectors to promshim's SQL-level implementations.
- **Dense range processing is still the main gap:** heavy 24h range processing
  rows remain multi-second and `processing_sum_rate_1h_by_job_range_7d` still
  times out in this harness.
- **Memory telemetry was mostly clean:** the sparse runs had no missing log
  comments; the dense processing run had missing query-log comments only for the
  timed-out `processing_sum_rate_1h_by_job_range_7d` row.
