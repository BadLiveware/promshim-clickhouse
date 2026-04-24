# 01 — Stack isolation and pre-seeded benchmark data

## Goal

Create a safe benchmark data lifecycle before changing benchmark semantics. The
benchmark stack must be isolated from the compliance stack so long-range and
dense pre-seeding never contaminates the frozen compliance fixture.

## Scope

### In scope

- Add an isolated benchmark Compose stack.
- Add benchmark-only Prometheus and ClickHouse volumes.
- Add benchmark-only ports and endpoint configuration.
- Ensure benchmark ClickHouse text logs do not grow inside persistent data
  volumes.
- Add seed markers and dataset presence checks.
- Add sparse/dense seed configuration and `--profile all` support.
- Add setup/status/reset affordances for benchmark data.
- Update lower-level scripts to accept explicit endpoints and default benchmark
  sweep usage to the benchmark stack.

### Out of scope

- Changing PromQL semantics.
- Changing compliance fixture generation.
- Adding the final sweep orchestrator UI beyond what is needed for setup/status
  wrappers.
- Adding final matrix rendering.
- Expanding tier 3/4 coverage.

## Requirements

1. Compliance and benchmark storage are separate.
2. Benchmark setup must never write to compliance Prometheus/ClickHouse by
   default.
3. Benchmark volumes can be reset without deleting compliance volumes.
4. Normal benchmark runs prefer pre-seeded data.
5. Re-running setup with `--seed reuse` must not remote-write again.
6. Missing data must produce an exact setup command.
7. ClickHouse `system.query_log` remains available for ProfileEvents.
8. High-volume ClickHouse diagnostic logs remain bounded.
9. ClickHouse text logs are visible via `docker logs` but not persisted in the
   benchmark data volume.

## Proposed benchmark stack

Add:

```text
harness/bench/docker-compose.yml
harness/bench/prometheus/prometheus.yml
harness/bench/clickhouse/config.d/logging.xml
```

Use separate named volumes, for example:

```text
promshim_bench_clickhouse_data
promshim_bench_prometheus_data
```

Use non-conflicting default ports, for example:

```text
Prometheus: http://localhost:29190
promshim:   http://localhost:29191
ClickHouse: http://localhost:28124
CH native:  localhost:29100
CH write:   http://localhost:29192/write
```

The exact ports may change during implementation, but centralize them and print
them in status/dry-run/estimate output.

## ClickHouse logging policy

Configure benchmark ClickHouse so persistent storage is dominated by samples,
not logs.

Tasks:

- Route text logs to stdout/stderr, preferably via benchmark-only config.
- If file paths are required, redirect to `/proc/1/fd/1` and `/proc/1/fd/2`, or
  mount `/var/log/clickhouse-server` as tmpfs / non-persistent storage.
- Add Docker log rotation to benchmark services (`max-size`, `max-file`) so
  stdout JSON logs do not grow unbounded on the host.
- Keep `system.query_log` enabled with short TTL.
- Disable or aggressively TTL high-volume logs such as `text_log`, `trace_log`,
  `processors_profile_log`, `query_metric_log`, `background_schedule_pool_log`,
  and metric logs unless diagnostics are explicitly enabled. `text_log` is the
  highest-priority table to bound because it can exceed benchmark sample data by
  multiple GiB during noisy runs.

## Dataset marker and detection

Add marker series to generated data:

```text
promshim_seed_info{
  profile="7d",
  density="dense",
  generator="ch-seed-long",
  seed="42",
  jobs="2",
  instances_per_job="100"
} 1
```

Detection must check the selected target endpoints:

- Prometheus marker query at profile+density eval time.
- ClickHouse marker query against the benchmark ClickHouse table.
- A local `seed-registry.json` can be a cache, but not the source of truth.

Seed policies:

```text
--seed reuse    default; require data, fail with setup command if missing
--seed missing  seed only missing targets
--seed always   deliberately write again
--seed never    skip checks/writes; valid for fixture/smoke cases only
--setup         shorthand for --seed missing, then exit after data exists
```

## Sparse/dense isolation decision

Sparse and dense variants must not overlap in a way that creates duplicate or
dedup-dependent samples.

Choose one model in this phase and document it:

1. Preferred: non-overlapping time windows per `(profile, density)`.
2. Alternative: separate benchmark database/table per density.
3. Alternative: add `bench_density` label and require corpora to select it.

Whichever model is chosen, status/estimate output must show the exact eval time
and data window for each profile+density.

## CLI / script changes

Lower-level scripts should accept explicit endpoints so the sweep can direct
work to the benchmark stack:

```text
--prom-url
--shim-url
--ch-url
--ch-native-addr
--ch-write-endpoint
```

Add or prepare user-facing helpers:

```bash
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --bench-reset --yes
./scripts/run-sweep.sh --setup --profile all --density sparse --target both
```

If implemented before the full orchestrator exists, these can be thin wrappers
around lower-level scripts, but they must use benchmark endpoints.

## Validation

```bash
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --setup --profile 7d --density sparse --target both
./scripts/run-sweep.sh --setup --profile 7d --density sparse --target both --seed reuse
./scripts/run-sweep.sh --bench-reset --yes
```

Manual checks:

- Benchmark data appears in benchmark Prometheus and benchmark ClickHouse.
- Compliance Prometheus/ClickHouse volumes are unchanged.
- `docker logs` shows ClickHouse logs.
- Benchmark data volume does not contain growing ClickHouse text log files.
- `system.query_log` is queryable in benchmark ClickHouse.
- `system.text_log`, `system.processors_profile_log`, `system.trace_log`,
  `system.query_metric_log`, and `system.background_schedule_pool_log` have
  short TTLs or are disabled in the benchmark stack.

## Risks

- Accidentally reusing compliance volume names.
- Existing scripts assuming compliance ports.
- Marker detection becoming expensive or flaky.
- Dense seed writes taking long enough that users need clear setup progress.

## Exit criteria

- Benchmark stack exists and is separate from compliance stack.
- Benchmark setup targets benchmark endpoints by default.
- Benchmark volumes can be reset without touching compliance volumes.
- Sparse long-range data can be seeded, detected, and reused.
- ClickHouse logging policy keeps text logs out of persistent benchmark data.
- High-volume ClickHouse system log tables, especially `system.text_log`, are
  TTL-bounded or disabled while `system.query_log` remains available.
- Missing data produces a clear setup command.
