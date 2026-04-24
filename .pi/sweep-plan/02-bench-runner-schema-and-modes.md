# 02 — Benchmark runner schema and execution modes

## Goal

Make the benchmark runner dimension-aware so one benchmark report can compare
Prometheus and multiple promshim execution modes across transport/profile/density
labels. This prepares the data model for the sweep orchestrator and matrices.

## Dependencies

Depends on [`01-stack-isolation-and-seeding.md`](01-stack-isolation-and-seeding.md)
for safe benchmark endpoints and data lifecycle.

## Scope

### In scope

- Add configurable promshim modes to `promshim-bench`.
- Add `prefer` mode to benchmark timing, alongside current `force_supported`
  and `off`.
- Add report schema v2 with explicit dimensions and per-mode results.
- Preserve compatibility with existing scripts/baselines or provide an adapter.
- Add artifact naming/prefix options to avoid overwritten reports.
- Add memory-summary plumbing hooks, but detailed memory collection is completed
  in plan 05.

### Out of scope

- Dense corpus design.
- Final sweep UI.
- Matrix rendering beyond basic report readability.
- CBE routing implementation.

## Requirements

1. Existing `run-bench.sh` behavior keeps working for current users.
2. New reports can represent multiple shim modes in one row.
3. Reports carry run labels: transport, profile, density, stack, corpus, and
   benchmark mode list.
4. Strategy, fallback reason, ClickHouse round trips, and ClickHouse millis stay
   visible per shim mode.
5. Prometheus timing can be included or skipped explicitly.
6. Baseline comparison handles legacy v1 and new v2 reports, or a documented
   adapter exists.

## CLI changes

Add to `cmd/promshim-bench` / `scripts/run-bench.sh`:

```text
--shim-modes prefer,force_supported,off
--include-prom true|false
--artifact-name bench-report.json
--artifact-prefix PREFIX
--run-label KEY=VALUE repeated
--memory off|summary|detailed
--legacy-report true|false
```

`--memory` may initially record only mode selection and placeholder fields. Plan
05 completes collection and reporting.

## Report schema v2

Suggested shape:

```json
{
  "schemaVersion": 2,
  "runLabels": {
    "transport": "native",
    "profile": "7d",
    "density": "sparse",
    "stack": "bench",
    "corpus": "bench-native-lowering-7d.json"
  },
  "manifest": { "baseUnixSeconds": 1774215942 },
  "rows": [
    {
      "name": "sum_rate_by_job_range_7d",
      "category": "range_sum_rate",
      "endpoint": "query_range",
      "query": "sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))",
      "prom": { "p50Ms": 312.4, "p95Ms": 410.8 },
      "shim": {
        "prefer": {
          "p50Ms": 180.2,
          "p95Ms": 220.1,
          "strategy": "delegated_promql",
          "chRoundtrips": 1,
          "chMillis": 170
        },
        "force_supported": {
          "p50Ms": 205.0,
          "p95Ms": 260.0,
          "strategy": "native_sql",
          "chRoundtrips": 1,
          "chMillis": 198
        },
        "off": {
          "p50Ms": 900.0,
          "p95Ms": 1200.0,
          "strategy": "local"
        }
      },
      "ratios": {
        "preferProm": 0.58,
        "forceProm": 0.66,
        "preferForce": 0.88
      }
    }
  ]
}
```

## Legacy compatibility

Acceptable approaches:

1. Keep writing v1-shaped `bench-report.json` for old invocations and write v2
   only when new mode/dimension flags are used.
2. Always write v2, and provide a stable v1 adapter for `harness/bench/baseline.json`.

Whichever path is chosen, document it and update tests. Do not silently break
`make bench`.

## Implementation tasks

1. Refactor `internal/promharness.RunBench` so Prometheus and shim-mode timings
   are independent measurements.
2. Add `prefer` mode support.
3. Add configurable mode list parsing and validation.
4. Add per-mode strategy/headers/result fields.
5. Add run labels to reports.
6. Add artifact name/prefix options.
7. Add v2 report tests.
8. Add baseline compatibility/adaptation tests.
9. Update `scripts/run-bench.sh` to pass endpoint and label metadata from plan
   01.
10. Keep `X-Promshim-Log-Comment` stable enough to join with ClickHouse
    `system.query_log` in later profile/memory reports.

## Validation

```bash
go test ./internal/promharness ./cmd/promshim-bench
./scripts/run-bench.sh \
  --corpus harness/corpus/bench-native-lowering.json \
  --shim-modes prefer,force_supported \
  --include-prom true \
  --no-baseline \
  --matrix
make bench
```

## Risks

- Report schema change can break existing matrix/baseline tooling.
- Running too many modes can increase bench time unexpectedly.
- `prefer` can choose delegated PromQL while `force_supported` requires native
  SQL; report names must make that distinction obvious.

## Exit criteria

- Current bench command still works.
- New bench command can time `prefer`, `force_supported`, and `off` in one run.
- Report schema contains explicit labels for stack/profile/density/transport.
- Baseline comparison is not silently broken.
- Strategy and roundtrip changes are visible per mode.
