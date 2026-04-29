# Native optimization investigation notes

This note preserves the durable findings from the native-SQL optimization work without keeping the full local execution trace in the repository. Treat it as context for future tuning, not as a benchmark contract.

## Accepted changes

| Area | Outcome | Evidence |
|---|---|---|
| Physical decision telemetry | Native planning now emits explain/bench-visible physical decisions, including fused range aggregation, row-source reuse, thread-cap choices, histogram child-path decisions, and `chunked_native` strategy visibility. | PR #14 docs and `X-Promshim-Physical-Decisions` / explain output. |
| Range-source reuse | Identical range-function vector operands can reuse native SQL row sources across supported arithmetic/comparison shapes; rejected reuse paths now report why. | Renderer tests and physical decision metadata. |
| Cumulative `avg_over_time` | Boundary probing was clamped and matched-series work is reused to reduce redundant scans for cumulative average windows. | Focused processing refresh and cumulative avg tests. |
| Native-grid sum-rate range path | Native-grid fused `sum(rate(...))` remains faster than Prometheus and is now guarded by duration-capped native chunking for memory safety. | `harness/artifacts/bench/standalone/pr14-native-chunking-duration-cap/` and `pr14-current-processing-prom-profile/`. |
| Local pushdown mode | `local_pushdown` is exposed as an analysis/routing mode for local roots with native subtree pushdown without relaxing normal serving caps. | Local pushdown tests and docs. |
| Prometheus runtime profiling | `run-bench.sh --prometheus-profile runtime` records Prometheus process/runtime resource deltas in v2 benchmark reports. | `harness/artifacts/bench/standalone/pr14-current-processing-prom-profile/`. |

## Rejected or deferred approaches

### Extreme `sum(rate(...[1h]))` 7d range query

The high-resource row was `processing_sum_rate_1h_by_job_range_7d`:

```promql
sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))
```

Rejected SQL reshapes:

- Direct `sumForEach(...)` instead of `arrayReduce('sumForEach', groupArray(...))` did not materially reduce scan or peak memory.
- Grouping-label-only tag projection did not solve the memory profile for the native-grid shape.
- Row-oriented final aggregation did not provide enough improvement to justify a more complex physical shape.
- Manual thread caps changed the tradeoff but were not a predictable memory-safety strategy.

Accepted mitigation:

- Use native range auto-chunking for `native_grid_sum_aggregation` roots.
- Prefer first-request safety based on query shape, output duration, and point count rather than learning from a prior unsafe execution.
- Default knobs: `PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES=289`, `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS=86400`, `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS=12`.

Result: the 7d sum-rate row moved from roughly `15.4 GiB` ClickHouse memory p95 before chunking to about `2.36 GiB` with duration-capped `chunked_native`, while staying around `0.25×` Prometheus p50 on the focused profile-50k processing run.

### Point-only chunking was insufficient

A point cap alone handled `7d @5m` but missed coarse-step requests. For `7d @15m`, `289` points still spans about three days per chunk, so memory stayed high (`8.26 GiB` in the fixed point-cap run). The duration cap lowered that row to about `2.86 GiB` memory p95.

### Query-log adaptation is not first-request safety

ClickHouse query-log feedback is useful for future advisory tuning, but it cannot be the primary safety mechanism. If the first under-chunked request can exhaust ClickHouse, there may be no safe follow-up request to adapt from. Static shape/time-span caps must be the first-request guardrail; feedback may shrink chunks further, but should not expand beyond safe defaults without an explicit latency-biased mode.

### Histogram range resource debt

Histogram range queries remain faster than Prometheus but resource-heavy. The current profile shows `histogram quantile / 24h range` around `0.28×` Prometheus latency, but with much higher ClickHouse memory/CPU than Prometheus runtime deltas. This is accepted debt for now because:

- Prometheus histogram semantics are awkward to reconstruct over bucket series in generic SQL.
- ClickHouse may add native Prometheus histogram support.
- A future project may special-case a limited known histogram metric set.

### Remaining gauge range debt

`avg memory 1h / 24h range` is now the clearest non-histogram resource target:

- latency remains favorable at about `0.47×` Prometheus p50,
- ClickHouse memory p95 is about `8.23 GiB`,
- read rows are about `710.1M`,
- weighted latency/resource score remains worse than Prometheus.

This is a better follow-up than generic histogram work because it is a plain gauge range shape and may benefit broader `avg_over_time`/gauge-family queries.

## Current evidence summary

Primary artifacts:

- `harness/artifacts/bench/standalone/pr14-current-processing-prom-profile/` — current processing matrix with ClickHouse profile summaries and Prometheus runtime profiling.
- `harness/artifacts/bench/standalone/pr14-native-chunking-duration-cap/` — focused native chunking hit-set with current duration-cap defaults.
- `harness/artifacts/bench/standalone/pr14-native-chunking-hitset-off/` — no native chunking comparison.
- `harness/artifacts/bench/standalone/pr14-native-chunking-hitset-auto/` — earlier point-only chunking comparison.
- `harness/artifacts/bench/standalone/pr14-native-chunking-hitset-prom-compare/` — Prometheus comparison for the hit set.

Current focused processing interpretation:

- Overall latency remains materially faster than Prometheus: geomean latency S/P about `0.34×`.
- Overall rough latency/resource weighted score is about parity: `0.97×` across all 8 processing rows.
- Range rows still carry resource debt: weighted score about `1.39×` and memory S/P about `12.20×`.
- Resource debt is concentrated in `avg memory 1h / 24h range` and histogram range rows.

## Follow-up candidates

1. Reduce `avg_over_time` / gauge range resource usage.
2. Add stricter SRE-safe native chunking mode if operators need hard rejection instead of max-chunk enlargement.
3. Investigate ClickHouse projections, rollups, or materialized views for scan reduction; query rewrites alone did not reduce the fundamental raw scan enough.
4. Revisit histogram range paths only when ClickHouse native support or a bounded metric-specific implementation is available.
5. Use `--prometheus-profile runtime` on future processing runs when claiming resource-vs-Prometheus tradeoffs.
