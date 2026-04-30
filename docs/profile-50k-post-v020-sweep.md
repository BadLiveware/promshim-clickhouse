# Profile-50k post-v0.2.0 sweep notes

This note summarizes the profile-50k benchmark runs captured after `v0.2.0`.
Use it as local routing/resource evidence, not as a portable production claim.
The 1y/profile-50k setup did not complete and is intentionally excluded from
summary ratios.

## Artifacts

Raw sweep artifacts were written locally and are not checked into the repository:

- `harness/artifacts/bench/sweeps/post-v020-7d-50k-prom-profile/`
- `harness/artifacts/bench/sweeps/post-v020-30d-50k-prom-profile/`

Curated rendered matrices are checked in under `docs/assets/benchmarks/post-v020-profile-50k/`:

- [`7d-matrix.md`](assets/benchmarks/post-v020-profile-50k/7d-matrix.md)
- [`7d-per-query.md`](assets/benchmarks/post-v020-profile-50k/7d-per-query.md)
- [`30d-matrix.md`](assets/benchmarks/post-v020-profile-50k/30d-matrix.md)
- [`30d-per-query.md`](assets/benchmarks/post-v020-profile-50k/30d-per-query.md)

Both sweeps used:

- `profile-50k` active-series preset
- native + processing corpora
- `prefer,force_supported,off` modes
- strict routing
- memory summaries
- ClickHouse profile summaries
- Prometheus runtime profiling

The 7d sweep also ran the compliance suite before benchmarking.

## 7d profile-50k results

Compliance passed:

| Total | Passed | Accepted tolerance | Diff failures | Unexpected failures |
|---:|---:|---:|---:|---:|
| 538 | 537 | 1 | 0 | 0 |

Strategy histogram:

| Strategy bucket | Count |
|---|---:|
| `force_supported:native_sql` | 43 |
| `prefer:native_sql` | 41 |
| `prefer:chunked_native` | 2 |
| `prefer:delegated_promql` | 2 |
| `off:local` | 21 |
| `off:delegated_promql` | 2 |

Prefer-mode latency geomeans where Prometheus completed:

| Corpus | Rows with Prometheus ratio | Prefer shim/Prometheus p50 |
|---|---:|---:|
| native-lowering 7d | 36 | `0.36×` |
| processing 7d | 8 | `0.26×` |

Processing resource-vs-Prometheus rough geomeans:

| Latency | ClickHouse memory / Prometheus heap Δ | ClickHouse CPU / Prometheus CPU |
|---:|---:|---:|
| `0.26×` | `0.22×` | `2.04×` |

Interpretation: the 7d/profile-50k run is broadly favorable. `prefer` is much
faster than Prometheus on successful comparisons, and `chunked_native` is active
for two processing rows. CPU remains the main tradeoff; Prometheus runtime
memory comparisons are directional process metrics, but this run did not show
the same memory gap as the earlier focused PR matrix.

## 30d profile-50k results

Strategy histogram:

| Strategy bucket | Count |
|---|---:|
| `force_supported:native_sql` | 19 |
| `prefer:native_sql` | 18 |
| `prefer:chunked_native` | 1 |
| `prefer:delegated_promql` | 1 |
| `off:local` | 8 |
| `off:delegated_promql` | 1 |

Prefer-mode latency geomeans where Prometheus completed:

| Corpus | Rows with Prometheus ratio | Prefer shim/Prometheus p50 |
|---|---:|---:|
| native-lowering 30d | 7 | `0.33×` |
| processing 30d | 6 | `0.29×` |

Processing resource-vs-Prometheus rough geomeans:

| Latency | ClickHouse memory / Prometheus heap Δ | ClickHouse CPU / Prometheus CPU |
|---:|---:|---:|
| `0.29×` | `0.23×` | `2.51×` |

Caveat: several 30d Prometheus range queries timed out under the benchmark
timeout, so some S/P cells are missing. That is useful signal: ClickHouse often
completed work where Prometheus did not return in time, but the missing
Prometheus rows make aggregate comparisons partial.

## Largest ClickHouse resource hotspots

Top p95-memory rows across 7d/30d `prefer` and `force_supported` runs:

| Query | Mode | CH memory p95 | Read rows | CH CPU | Duration |
|---|---|---:|---:|---:|---:|
| `subquery_rate_over_aggregate_1h_range_30d` | `prefer` | `35.5 GiB` | `1.1B` | `40.9s` | `23.1s` |
| `subquery_rate_over_aggregate_1h_range_30d` | `force_supported` | `35.5 GiB` | `1.1B` | `40.9s` | `22.7s` |
| `rate_1h_range_30d` | `prefer` | `20.7 GiB` | `2.2B` | `205.9s` | `9.4s` |
| `rate_1h_range_30d` | `force_supported` | `20.5 GiB` | `2.2B` | `203.5s` | `7.5s` |
| `processing_avg_memory_1h_by_job_type_range_24h_7d` | `prefer` | `8.2 GiB` | `0.7B` | `35.4s` | `5.7s` |
| `processing_histogram_quantile_6h_range_7d_30d` | `prefer` | `8.1 GiB` | `0.5B` | `47.0s` | `1.9s` |
| `topk_histogram_quantile_by_instance_job_1h_range_30d` | `prefer` | `6.9 GiB` | `2.2B` | `275.2s` | `10.9s` |

The largest remaining resource problems are not the short processing rows; they
are long-range native range/subquery/histogram shapes that keep many rows alive
inside ClickHouse execution.

## 1y/profile-50k setup result

The 1y/profile-50k setup did not complete. The estimated data size was about
`5.26B` samples per target. During setup, 30d Prometheus ingestion already
needed a longer local seeder timeout than the default, and 1y was terminated
while seeding was in progress.

Conclusion: 1y/profile-50k is not a practical default sweep target on this
machine. If we need 1y evidence, run it as a separate long job with explicit
seeder timeout controls and preferably a smaller or more realistic density
profile.

## Density and measurement caveats

The current `profile-50k` label is useful as a repeatable stress target, but the
density measurement probably overstates what we should treat as a normal
end-to-end benchmark profile:

- The active-series label hides very different sample counts across profiles.
  7d and 30d are already multi-billion-sample datasets; 1y jumps to more than
  five billion samples per target.
- Setup cost and benchmark feasibility are driven by samples, time span, output
  points, and query shape, not just active-series count.
- Prometheus ingestion and query timeout behavior becomes part of the result for
  30d+ profiles. That is useful operational signal, but it makes Prometheus
  ratios partial.
- ClickHouse query-log/ProfileEvents are per-query execution metrics, while
  Prometheus runtime profiling is process-level directional telemetry. Resource
  ratios should be read as trend indicators, not exact accounting.

For future sweeps, prefer naming profiles by both active series and approximate
sample volume, or record separate axes for active series, points per series,
output points, and total stored samples.

## Conclusions

- `v0.2.0` materially improved the default `prefer` path for the completed
  7d/30d profile-50k comparisons: geomean shim/Prometheus p50 is about
  `0.26–0.36×` for 7d and `0.29–0.33×` for 30d where Prometheus completed.
- `chunked_native` is active and visible, but it only applies to a small subset
  of long-range native-grid aggregation rows. It helps memory safety for those
  rows rather than solving all long-range resource debt.
- CPU is still the main cross-engine tradeoff. Processing rows are much faster
  than Prometheus, but ClickHouse often spends more CPU.
- The worst ClickHouse memory rows are now 30d subquery/range shapes, especially
  `subquery_rate_over_aggregate_1h_range_30d` and `rate_1h_range_30d`.
- Histogram/topk range paths remain expensive and should be treated as future
  targeted work, not incidental cleanup.
- 1y/profile-50k should not be part of the routine full sweep until the seeder
  and density model are adjusted.

## Follow-up work

1. Add explicit seeder timeout controls instead of the current hardcoded stream
   timeout.
2. Rework density naming/reporting so sweep labels include total samples and
   points per series, not just active-series presets.
3. Investigate 30d range/subquery memory paths before expanding 1y benchmarking.
4. Add a smaller long-range profile for routine 1y signal, or run 1y as a
   deliberate overnight stress job.
5. Keep Prometheus runtime profiling enabled for resource comparison sweeps, but
   report timeout counts next to any aggregate S/P numbers.
