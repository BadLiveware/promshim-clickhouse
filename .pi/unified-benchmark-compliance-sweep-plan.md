# Unified benchmark and compliance sweep plan

## Purpose

Promshim already has useful individual entry points for compliance,
long-range seeding, long-range benchmarks, ClickHouse profiling, and matrix
rendering. The user experience is still too manual: agents and humans end up
re-seeding persistent datasets, recreating promshim containers by hand for
transport comparisons, copying overwritten artifacts, and stitching matrices
outside the tooling.

This plan introduces a single user-friendly sweep command that can run any
combination of:

- PromQL compliance modes (`prefer`, native-only / `force_supported`).
- Bench profiles (`10m`, `7d`, `30d`, `1y`).
- Dataset densities (`sparse`, `dense`) and future dataset shapes.
- ClickHouse transports (`http`, `native`).
- Shim execution modes (`prefer`, `force_supported`, `off` where useful).
- Matrix renderings across transport, profile/range, density, mode, category,
  and query.

The default should give a balanced, useful signal without turning every local
run into an exhaustive soak test. Exhaustive sweeps should be one flag away.

## Requirements

1. Provide one primary command for users and agents:

   ```bash
   ./scripts/run-sweep.sh [options]
   ```

   Add `make sweep` as the discoverable Make target.

2. Preserve existing focused commands (`run-compliance.sh`, `run-bench.sh`,
   `seed-long-range.sh`, `bench-matrix.sh`, `ch-profile-capture.sh`) as lower
   level tools, but make the new sweep command the recommended orchestrator.
3. Make all axes explicit and composable:
   - `--suite compliance,bench`
   - `--transport http,native`
   - `--profile 7d,30d,1y`
   - `--density sparse,dense`
   - `--mode prefer,force_supported,off`
   - `--matrix category,query,transport,mode,density,profile`
4. Prefer pre-seeded data. Long-range and dense datasets persist in Docker
   volumes; normal sweeps must check and reuse existing datasets instead of
   spending time re-seeding. Seeding missing data should require an explicit
   setup command or `--seed missing` / `--seed always`.
5. Stop accidental re-seeding. Re-running the same benchmark sweep must be a
   metadata check plus query execution, not another remote-write pass.
6. Keep compliance and benchmark storage isolated. Compliance runs must use the
   existing compliance stack and its frozen fixture volumes; benchmark sweeps
   must use a separate Prometheus + ClickHouse + promshim stack and separate
   Docker volumes so dense/long-range pre-seeding never contaminates the
   compliance fixture or forces another hour-long scrape.
7. When a transport axis is requested, recreate the promshim container with the
   selected `PROM_SHIM_CLICKHOUSE_TRANSPORT`; setting the environment only on
   the bench process is not enough.
8. Keep Prometheus and ClickHouse data comparable. If a benchmark row includes
   Prometheus latency, the dataset must exist in both stores unless the user
   explicitly requests ClickHouse-only measurement.
9. Add dense benchmark variants intended to make Prometheus spend real CPU time
   and target 250-1000 ms p50 for representative processing queries.
10. Keep sparse variants. They remain useful for correctness, routing overhead,
   small-query regressions, and partition-pruning checks.
11. Preserve compliance policy:
   - `prefer` compliance is allowlist-gated.
   - native-only compliance remains a visible gap report and should not expand
     the allowlist.
   - `force_supported` still means native SQL root only.
12. Let users name a run and base all output artifacts on that name. The name
    should become the artifact directory slug and appear in manifests,
    summaries, and matrix titles.
13. Emit stable, non-overwritten artifacts under a per-run directory.
14. Emit both human-readable Markdown summaries and machine-readable JSON.
15. Keep every expensive operation visible in the plan, with clear defaults and
    easy opt-outs.

## Non-goals

- Do not change PromQL semantics or planner behavior as part of this work.
- Do not expand tiers 3/4. Benchmarking `off`/fallback is measurement only.
- Do not hide native coverage gaps behind benchmark baselines or allowlists.
- Do not make dense datasets part of the fast compliance fixture.
- Do not write benchmark seed data into compliance Prometheus or ClickHouse
  volumes during normal operation.
- Do not let benchmark ClickHouse diagnostic logs grow inside the persistent
  benchmark data volume; persistent storage should be dominated by benchmark
  samples, not server/query log files.
- Do not require users to know Docker Compose internals for normal sweeps.

## Current state

Existing useful pieces:

| Capability | Current command | Gap |
|---|---|---|
| Compliance prefer/native | `./scripts/run-compliance.sh` | not matrix-aware; stack transport is implicit |
| Long-range seed | `./scripts/seed-long-range.sh --profile 7d --target both` | points at compliance-stack ports by default; no `all`; no missing-dataset detection; easy to re-seed |
| Long-range bench | `./scripts/run-bench.sh --long-range all` | currently assumes the compliance stack; fixed artifact names; no transport/mode/density matrix orchestration |
| Per-profile matrix | `./scripts/run-bench.sh --matrix` | one profile/report at a time |
| Cross-profile matrix | `./scripts/bench-matrix.sh` | assumes default report names only |
| CH profile counters | `./scripts/ch-profile-capture.sh -- ...` | wraps bench, but artifacts overwrite by default |

Important current benchmark behavior:

- `run-bench.sh --long-range all` already runs 7d, 30d, and 1y sequentially and
  preserves `bench-report-{7d,30d,1y}.json`.
- `promshim-bench` currently hardcodes three timing passes per query:
  Prometheus, shim `force_supported`, and shim `off`.
- The existing long-range profiles are low-cardinality:
  `2 jobs * 5 instances/job * ~13 series/instance = ~130 series`.
- Existing long-range sample totals are good for ClickHouse scan and partition
  signals but often too small/sparse for 250-1000 ms Prometheus p50:
  - `7d` at `15s`: ~5.2M samples.
  - `30d` at `60s`: ~5.6M samples.
  - `1y` at `300s`: ~13.7M samples.

## Proposed user interface

### Primary command

```bash
./scripts/run-sweep.sh [preset] [axes/options]
```

Presets are convenience bundles. Every preset can be overridden by explicit
axes.

```bash
./scripts/run-sweep.sh --preset smoke
./scripts/run-sweep.sh --preset default
./scripts/run-sweep.sh --preset heavy
./scripts/run-sweep.sh --preset full
```

Equivalent shorthand should be accepted:

```bash
./scripts/run-sweep.sh smoke
./scripts/run-sweep.sh default
./scripts/run-sweep.sh heavy
./scripts/run-sweep.sh full
```

### Core options

```text
--suite compliance,bench,profile          Which suites to run.
--transport http,native                   ClickHouse transport axis.
--profile 10m,7d,30d,1y,all               Time-range profile axis.
--density sparse,dense,all                Dataset density axis.
--mode prefer,force_supported,off,all     Shim execution mode axis for bench.
--compliance-mode prefer,native,all       Compliance mode axis.
--seed reuse|missing|always|never         Dataset seed behavior. Default: reuse.
--target both|ch|prom                     Seed target when seeding is requested.
--bench-stack isolated|compliance         Benchmark stack selection; default isolated.
--setup                                   Seed missing selected datasets, then exit.
--dry-run                                 Resolve plan and print actions without executing.
--estimate                                Print dataset/work/runtime estimates; implies dry-run unless paired with --execute.
--execute                                 Execute even when --estimate is present.
--matrix category,query,transport,mode,density,profile,all
--repeats N                               Timed bench repeats.
--warmup N                                Warmup repeats.
--timeout DURATION                        Per-request timeout.
--ready-timeout SECONDS                   Stack readiness timeout.
--build|--no-build                        Build promshim image before stack use.
--keep-up|--down                          Stack lifecycle.
--artifact-dir PATH                       Root artifact directory.
--name NAME                               Human-friendly run name used for artifact paths.
--run-id NAME                             Alias for --name; retained for script-style usage.
--overwrite                               Allow replacing an existing named run directory.
--baseline PATH|none                      Regression baseline policy.
--update-baseline                         Explicitly rewrite baseline.
--profile-events auto|on|off              Capture ClickHouse ProfileEvents.
--memory off|summary|detailed             Capture memory trade-off signals by execution mode.
--format markdown,json,both               Summary output format.
```

Use CSV values consistently. Support repeated flags as sugar if convenient.

### Presets

#### `smoke`

Fast sanity check for local edit loops.

```text
suite: compliance,bench
compliance-mode: prefer
transport: current/default transport
profile: 10m
mode: prefer,force_supported
seed: never
repeats: 3
warmup: 1
profile-events: off
matrix: category
```

#### `default`

Balanced default for a useful local or pre-PR run. It should combine correctness
and representative benchmark spread without doing every expensive axis.

```text
suite: compliance,bench
compliance-mode: prefer,native
transport: native
profile: 7d,30d
density: sparse
mode: prefer,force_supported
seed: reuse
target: both
repeats: 5
warmup: 1
profile-events: auto/on for ClickHouse-backed bench
matrix: category,profile,mode
```

Rationale:

- Compliance runs both normal and native-only visibility.
- 7d gives dense time-step scan behavior; 30d crosses monthly partitions.
- Native transport is the expected performance path once available.
- Sparse data keeps default runtime bounded.
- `prefer` vs `force_supported` surfaces whole-query delegation/routing
  differences without requiring fallback as a default axis.

#### `heavy`

Real processing benchmark targeting Prometheus p50 in the 250-1000 ms range.

```text
suite: bench
transport: native
profile: 7d,30d
density: dense
mode: prefer,force_supported
seed: reuse
target: both
repeats: 3
warmup: 1
profile-events: on
matrix: category,query,profile,mode,density
```

#### `full`

Exhaustive sweep for release/performance investigation.

```text
suite: compliance,bench
compliance-mode: prefer,native
transport: http,native
profile: 7d,30d,1y
density: sparse,dense
mode: prefer,force_supported,off
seed: reuse
target: both
repeats: 5
warmup: 1
profile-events: on
matrix: all
```

`full` is allowed to take a long time and use substantial disk. By default it
still reuses already-seeded datasets and fails fast with setup instructions when
required data is absent. If the user adds `--seed missing` or `--setup`, it
should print an explicit warning listing selected axes and expected sample
counts before seeding dense datasets, with `--yes` for non-interactive runs.
Users should be able to preview the same information without executing via
`--dry-run` or `--estimate`.

### Example commands

```bash
# Balanced default. Writes to an auto-named run directory.
./scripts/run-sweep.sh

# Named run. Writes under harness/artifacts/sweeps/pr-42-native-default/.
./scripts/run-sweep.sh --name pr-42-native-default

# Preview the default sweep without running it.
./scripts/run-sweep.sh --dry-run

# Estimate dense setup and benchmark cost before committing to it.
./scripts/run-sweep.sh heavy --estimate

# One-time setup for missing sparse long-range data.
./scripts/run-sweep.sh --setup --profile all --density sparse --target both

# Just long-range bench across existing sparse profiles; do not re-seed.
./scripts/run-sweep.sh --suite bench --profile all --density sparse --seed reuse

# Transport comparison on 7d/30d sparse data.
./scripts/run-sweep.sh --suite bench --transport http,native --profile 7d,30d

# Prefer vs native-only mode matrix on dense 7d.
./scripts/run-sweep.sh --preset heavy --profile 7d --mode prefer,force_supported

# Compliance only, both transports.
./scripts/run-sweep.sh --suite compliance --transport http,native --compliance-mode all

# One-time setup for dense real-processing data.
./scripts/run-sweep.sh --setup --profile 7d,30d --density dense --target both

# Dense real-processing benchmark using pre-seeded data.
./scripts/run-sweep.sh heavy
```

## Dry-run and estimate mode

Users should be able to ask the sweep command what it will do before it touches
Docker, remote-write endpoints, or artifact directories.

### `--dry-run`

`--dry-run` resolves presets, axes, seed policy, artifact paths, and planned
subcommands, then exits without executing stack, seed, compliance, bench, or
matrix work.

It should print:

- selected preset and fully-expanded axes;
- run name, sanitized slug, and artifact root;
- stack actions that would happen, including build/start/recreate/down;
- seed checks that would happen;
- datasets that are required and whether they are expected to be pre-seeded;
- compliance passes that would run;
- benchmark combinations that would run;
- expected report/matrix files;
- warnings for expensive dense/full combinations;
- exact follow-up commands when data is likely missing.

`--dry-run` should not require the stack to be running. If the stack is running
and cheap checks are available, it may report live dataset presence as
`present`; otherwise use `unknown` and show what would be checked during a real
run.

### `--estimate`

`--estimate` includes `--dry-run` output plus quantitative estimates. By
default it should not execute. If users want estimates printed and then the run
executed, they can pass:

```bash
./scripts/run-sweep.sh heavy --estimate --execute
```

Estimates should include:

| Estimate | Method |
|---|---|
| Series count | `jobs * instances_per_job * metric-series-per-instance` |
| Points per series | `duration / step` |
| Total samples per dataset | `series * points_per_series` |
| Missing samples to seed | total samples for missing `(profile,density,target)` only |
| Remote-write POST count | `ceil(samples / batch_samples)` per target |
| Approx ingest bytes | rough bytes/sample range, clearly labeled approximate |
| Bench request count | `queries * (warmup + repeats) * selected shim/prom modes` |
| Compliance pass count | selected compliance modes x transports |
| Matrix count | selected matrix views |
| Runtime estimate | calibrated from previous sweep manifests when available; otherwise a broad rough class |
| Disk footprint estimate | rough bytes/sample range for Prometheus and ClickHouse volumes; excludes stdout logs |
| Diagnostic log overhead | expected to be negligible in persistent volumes; query_log bounded by TTL |

Runtime estimates must be presented as rough and environment-dependent, not as a
promise. Prefer buckets when no local history exists:

```text
runtime: short (<2 min), medium (2-15 min), long (15-60 min), very_long (>60 min)
```

When previous sweep manifests exist, compute better estimates from observed
history for matching `(suite, transport, profile, density, mode)` dimensions and
show both:

```text
observed similar runs: median 8m42s, p90 11m10s, n=4
rough fallback: medium
```

### Example estimate output

```text
Sweep estimate: dense-calibration-april
Artifact root: harness/artifacts/sweeps/dense-calibration-april
Preset: heavy
Axes:
  suite: bench
  transport: native
  profile: 7d,30d
  density: dense
  mode: prefer,force_supported
Seed policy: reuse

Datasets:
  7d dense both: ~2,600 series, ~40,320 points/series, ~105M samples/store, status unknown
  30d dense both: ~2,600 series, ~43,200 points/series, ~112M samples/store, status unknown

Planned work:
  stack: build promshim, start isolated benchmark stack, recreate benchmark promshim for native
  logging: benchmark ClickHouse text logs to stdout/stderr; query_log bounded by short TTL
  seed: check markers only; no writes unless --seed missing or --setup
  bench: 2 profiles x 1 transport x 2 modes x 8 queries x (1 warmup + 3 repeats)
  profile events: capture after each bench profile
  matrices: category, query, profile, mode, density

Estimated runtime:
  benchmark requests: 128 shim requests + 64 Prometheus requests
  data writes: 0 with --seed reuse if data exists
  rough class: medium (2-15 min), excluding missing dataset setup

If datasets are missing, run:
  ./scripts/run-sweep.sh --setup --profile 7d,30d --density dense --target both --name dense-calibration-april-setup
```

## Stack isolation

Compliance and benchmarking should use different Docker Compose projects,
volumes, and ports.

### Compliance stack

Purpose:

- correctness oracle and upstream PromQL compliance fixture;
- frozen / pre-scraped compliance dataset;
- allowlist-gated `prefer` compliance and native-only gap reporting.

It should continue to live under:

```text
harness/compliance/
```

and keep its current volumes and ports:

```text
Prometheus: http://localhost:29090
promshim:   http://localhost:29091
ClickHouse: http://localhost:28123, native localhost:29000, write localhost:29092
```

Benchmark seeding must not target these endpoints unless the user explicitly
opts into a debug/legacy mode such as:

```bash
./scripts/run-sweep.sh --suite bench --bench-stack compliance --yes-i-know-this-contaminates-compliance-data
```

That escape hatch is optional; the recommended implementation can simply avoid
supporting benchmark writes to the compliance stack.

### Benchmark stack

Purpose:

- sparse and dense long-range benchmark datasets;
- repeated pre-seeded benchmark runs;
- transport and execution-mode sweeps;
- destructive/resettable benchmark experimentation.

Add a separate stack, preferably:

```text
harness/bench/docker-compose.yml
```

with separate named volumes:

```text
promshim_bench_clickhouse_data
promshim_bench_prometheus_data
```

and non-conflicting default ports, for example:

```text
Prometheus: http://localhost:29190
promshim:   http://localhost:29191
ClickHouse: http://localhost:28124
CH native:  localhost:29100
CH write:   http://localhost:29192/write
```

The exact port numbers can change during implementation, but they must be
centralized in one config file and printed by `--dry-run` / `--estimate`.

### Benchmark ClickHouse logging policy

The benchmark ClickHouse container should not accumulate ordinary server logs in
its persistent data volume. Configure the benchmark stack so:

- ClickHouse text logs go to stdout/stderr instead of persistent files. Prefer a
  benchmark-only config such as `harness/bench/clickhouse/config.d/logging.xml`
  with console logging enabled and file log paths removed or redirected to
  `/proc/1/fd/1` and `/proc/1/fd/2`.
- If the image insists on `/var/log/clickhouse-server`, keep that path outside
  the persistent data volume, or mount it as tmpfs / a non-persistent anonymous
  volume.
- System log tables in ClickHouse remain bounded. Keep `system.query_log`
  because ProfileEvents capture depends on it, but give it a short TTL in the
  benchmark stack and disable or aggressively TTL high-volume logs that are not
  part of normal sweep output (`text_log`, `trace_log`,
  `processors_profile_log`, `query_metric_log`, `background_schedule_pool_log`,
  metric logs) unless an explicit diagnostics flag enables them. Prioritize
  `system.text_log`: noisy runs can grow it by multiple GiB, larger than the
  benchmark sample data itself.
- `--estimate` should include a small, separate "diagnostic log overhead" line
  and note that stdout logs are not counted in benchmark volume size.
- `--bench-reset` should remove benchmark data volumes and any benchmark-only
  transient log volume, but never compliance volumes.

`run-sweep.sh` should use the benchmark stack for all benchmark, setup, seed,
and ProfileEvents work by default. Compliance work should use the compliance
stack. A mixed sweep therefore starts the compliance stack for compliance phases
and the benchmark stack for benchmark phases, never sharing storage volumes.

### Shared implementation options

To avoid duplicating Compose YAML too heavily, either approach is acceptable:

1. Create `harness/bench/docker-compose.yml` by reusing the same images and
   mounted config files with distinct volume names and ports.
2. Parameterize the existing compliance Compose file with project name, port,
   and volume environment variables, then wrap it with bench-specific defaults.

Prefer the first approach if it is easier to reason about and safer for users.
The core requirement is storage isolation, not YAML minimization.

### Reset and inspect commands

The sweep command should expose safe benchmark-stack maintenance commands or
print equivalent helpers:

```bash
./scripts/run-sweep.sh --bench-reset --yes
./scripts/run-sweep.sh --bench-status
```

`--bench-reset` may delete benchmark volumes only. It must never delete
compliance volumes.

## Dataset profiles and densities

### Profile dimensions

A profile defines a pinned non-overlapping time window:

| Profile | End time | Duration | Step | Existing purpose |
|---|---:|---:|---:|---|
| `10m` | compliance fixture end | ~10m/1h fixture | existing fixture | fast tripwire |
| `7d` | `2026-03-22T21:45:42Z` | 7d | 15s | dense time-step scan baseline |
| `30d` | `2026-02-22T21:45:42Z` | 30d | 60s | monthly partition crossing |
| `1y` | `2025-03-22T21:45:42Z` | 365d | 300s | many partitions / long retention |

### Density dimensions

Keep density separate from profile. This lets users run `7d sparse` and
`7d dense` as different benchmark datasets without inventing one-off profile
names in scripts.

| Density | Jobs | Instances/job | Approx series | Intended use |
|---|---:|---:|---:|---|
| `sparse` | 2 | 5 | ~130 | current long-range behavior; fast spread |
| `dense` | 2 | profile-specific | ~1.3k-2.6k | real processing benchmark |

Proposed dense defaults:

| Profile | Dense instances/job | Approx samples | Notes |
|---|---:|---:|---|
| `7d` | 100 | ~105M | best first target for 250-1000 ms Prom p50 |
| `30d` | 100 | ~112M | partition-crossing heavy benchmark |
| `1y` | 50 | ~137M | avoids a very large default 1y dense write |

Add an expert override:

```text
--instances-per-job N
--jobs demo-api,demo-worker,...
```

When the override is used, artifact names should include a density descriptor
such as `custom-i200-j2`.

### Pre-seeded data policy

The sweep command should optimize for repeated use of already-seeded Docker
volumes. The normal path is:

1. Check whether each selected `(profile, density, target)` exists.
2. Reuse it when present.
3. If missing and `--seed reuse` is active, fail fast with an exact setup
   command, for example:

   ```bash
   ./scripts/run-sweep.sh --setup --profile 7d,30d --density dense --target both
   ```

4. Only write data when the user explicitly asks via `--setup`,
   `--seed missing`, or `--seed always`.

This avoids spending remote-write time on every benchmark run while preserving a
one-command setup path for fresh machines or reset Docker volumes.

### Seed idempotency

Add a marker series to generated data:

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

Write the marker to both Prometheus and ClickHouse when `target=both`. Use it to
implement:

```text
--seed reuse: default; require pre-seeded data, fail with setup instructions if missing.
--seed missing: seed only missing targets.
--seed always: write again deliberately.
--seed never: skip seed checks and seed writes; valid only for profiles that do not need generated data, such as 10m smoke.
--setup: shorthand for --seed missing plus exit after data is present.
```

Detection should be cheap:

- Prometheus: query the marker at the profile end time.
- ClickHouse: query the marker through `timeSeriesData(...)` or an equivalent
  metadata query at the profile end time.
- Also record a local `seed-registry.json` as a cache, but do not trust it as
  the source of truth because Docker volumes can outlive or be reset separately
  from artifacts.

### Dense calibration and target-band reporting

Dense datasets are intended to make Prometheus do real work, but exact p50
latency depends on host resources and Docker settings. Do not hard-fail solely
because a machine is faster or slower. Instead:

1. Add optional target metadata to heavy corpus rows:

   ```json
   "targetPromP50Ms": { "min": 250, "max": 1000 }
   ```

2. Add sweep summary columns:

   ```text
   Prom band: too_fast | in_band | too_slow | n/a
   ```

3. Add a final calibration note:

   - If most dense rows are `too_fast`, suggest increasing
     `--instances-per-job` or selecting `30d dense`.
   - If most dense rows are `too_slow`, suggest lowering
     `--instances-per-job`, using fewer profiles, or increasing range steps.

## Benchmark corpora

### Keep existing corpora

Retain:

- `harness/corpus/bench-native-lowering.json`
- `harness/corpus/bench-native-lowering-7d.json`
- `harness/corpus/bench-native-lowering-30d.json`
- `harness/corpus/bench-native-lowering-1y.json`

They remain the sparse/default corpora and should stay stable for trend
comparison.

### Add real-processing corpora

Add dense-oriented corpora that scan a lot and return bounded output:

```text
harness/corpus/bench-processing-7d.json
harness/corpus/bench-processing-30d.json
harness/corpus/bench-processing-1y.json
```

Representative query families:

- Aggregated long-window counters:

  ```promql
  sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))
  sum by (job, mode) (rate(demo_cpu_usage_seconds_total[6h]))
  ```

- Long-window gauges with collapsed labels:

  ```promql
  sum by (job, type) (avg_over_time(demo_memory_usage_bytes[6h]))
  sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1d]))
  ```

- Histogram bucket processing with bounded output:

  ```promql
  histogram_quantile(
    0.95,
    sum by (le) (rate(demo_api_request_duration_seconds_bucket[1h]))
  )
  ```

- Range queries with scan-heavy but response-bounded shape:

  ```promql
  sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))
  sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1h]))
  ```

  Use steps that avoid response explosion:

  | Profile | Range | Step |
  |---|---:|---:|
  | `7d` | 24h and 7d rows | 1m / 5m |
  | `30d` | 7d and 30d rows | 15m / 1h |
  | `1y` | 30d and 1y rows | 6h / 1d |

- Add a small number of subquery rows only after simple shapes are calibrated.
  Subqueries are useful, but they can become pathological and obscure the
  storage/processing signal.

## Bench runner changes

### `promshim-bench` should support configurable modes

Current behavior is fixed: Prometheus, shim `force_supported`, shim `off`.
Refactor to support arbitrary shim mode lists while keeping backward-compatible
output for existing scripts.

Proposed flags:

```text
--shim-modes prefer,force_supported,off
--include-prom=true|false
--compare-mode structural|exact defaults from corpus rows
--artifact-name bench-report.json
--artifact-dir PATH
--run-label KEY=VALUE repeated
```

Report schema v2 should model dimensions explicitly:

```json
{
  "schemaVersion": 2,
  "runLabels": {
    "transport": "native",
    "profile": "7d",
    "density": "dense",
    "suite": "bench"
  },
  "rows": [
    {
      "name": "heavy_sum_rate_1h_by_job_range_7d",
      "category": "heavy_range_rate",
      "endpoint": "query_range",
      "query": "...",
      "prom": { "p50Ms": 312.4, "p95Ms": 410.8 },
      "shim": {
        "prefer": { "p50Ms": 180.2, "p95Ms": 220.1, "strategy": "delegated_promql" },
        "force_supported": { "p50Ms": 205.0, "p95Ms": 260.0, "strategy": "native_sql" },
        "off": { "p50Ms": 900.0, "p95Ms": 1200.0, "strategy": "local" }
      },
      "ratios": {
        "preferProm": 0.58,
        "forceProm": 0.66,
        "preferForce": 0.88
      },
      "targetPromBand": "in_band"
    }
  ]
}
```

Compatibility options:

- Continue writing v1-shaped `bench-report.json` when invoked by old scripts
  without `--shim-modes` or with a `--legacy-report` flag.
- Or update `run-bench.sh` and `bench-matrix.sh` together and keep a small
  `jq` adapter for existing baselines.

### Run naming and artifact paths

Users should be able to name every sweep:

```bash
./scripts/run-sweep.sh --name pr-42-native-default
./scripts/run-sweep.sh heavy --name dense-calibration-april
```

The orchestrator should sanitize the name into a filesystem-safe slug and use it
as the run directory:

```text
harness/artifacts/sweeps/pr-42-native-default/
harness/artifacts/sweeps/dense-calibration-april/
```

Sanitization rules:

- lowercase by default;
- replace whitespace and path separators with `-`;
- allow only `[a-z0-9._-]` after sanitization;
- trim repeated separators;
- reject empty names after sanitization.

If no name is provided, generate one from timestamp + preset, for example:

```text
20260424T213000Z-default
```

Collision behavior:

- By default, fail if the named run directory already exists and contains a
  manifest. Print the existing path and suggest a new `--name` or `--overwrite`.
- With `--overwrite`, remove or archive only the selected run directory, never
  the root artifact directory.
- With `--resume` in a future extension, continue a partial named run. Do not
  implement resume in the first pass unless it falls out naturally.

### Avoid artifact overwrites

Add `--artifact-name` or `--artifact-prefix` to lower-level runners. The sweep
orchestrator should write paths based on the sanitized run name:

```text
harness/artifacts/sweeps/<run-name>/bench/native/sparse/7d/bench-report.json
harness/artifacts/sweeps/<run-name>/bench/native/dense/7d/ch-profile.json
harness/artifacts/sweeps/<run-name>/bench/http/sparse/30d/bench-report.json
```

A top-level manifest should link every generated artifact.

### ProfileEvents capture

Integrate `ch-profile-capture.sh` behavior into the sweep path without requiring
users to remember it.

Default policy:

```text
profile-events=auto
```

`auto` means:

- enabled for benchmark suites that hit ClickHouse;
- disabled for pure compliance-only smoke runs;
- written under the same axis-specific artifact directory as the bench report.

ProfileEvents are especially important for ClickHouse-native optimization
claims, so full/heavy sweeps should always include them.

## Compliance sweep changes

### Make compliance transport-aware

When `--transport http,native` is selected, the sweep must recreate promshim per
transport before running compliance. The lower-level `run-compliance.sh` can gain
explicit support:

```text
./scripts/run-compliance.sh --transport native --mode prefer,native
```

or the sweep can own container lifecycle and call the existing mode-specific
script. Prefer moving transport/mode awareness into `run-compliance.sh` so the
lower-level command is also user-friendly.

### Artifact paths

Compliance reports should be copied or emitted directly to:

```text
harness/artifacts/sweeps/<run-name>/compliance/native/prefer/compliance-report.json
harness/artifacts/sweeps/<run-name>/compliance/native/native/compliance-report.json
harness/artifacts/sweeps/<run-name>/compliance/http/prefer/compliance-report.json
```

Keep the existing timestamped artifacts in `harness/compliance/artifacts/` for
compatibility if needed, but the sweep manifest should point to stable per-run
copies.

### Summary semantics

The sweep summary should distinguish:

- compliance failures in prefer mode: gating failure;
- native-only unsupported/gap rows: visible gaps, not allowlist entries;
- infrastructure/tooling failures: sweep failure;
- skipped axes: explicit in summary, never silent.

## Matrix output

### Matrix types

Generate both JSON and Markdown matrices from normalized sweep artifacts.

Required matrix views:

1. Category matrix: category rows, selected axes as columns.
2. Query matrix: query rows, selected axes as columns.
3. Transport matrix: compare `http` vs `native` for same profile/density/mode.
4. Mode matrix: compare `prefer` vs `force_supported` vs `off`.
5. Profile matrix: compare `7d`, `30d`, `1y` for same density/mode/transport.
6. Density matrix: compare `sparse` vs `dense` for same profile/mode/transport.
7. Compliance matrix: transport x compliance mode with pass/fail/gap counts.

Columns should include at least:

```text
Prom p50
Shim p50
Shim/P ratio
CH millis
CH round trips
Strategy
Prom target band
SelectedRows / ReadBytes / FunctionExecute where profile events are available
```

For multi-row category cells, use median by default and record count. Add
`--matrix per-query` or `--matrix query` for exact query-level rows.

### Matrix command compatibility

Update `scripts/bench-matrix.sh` so it can read either:

- old explicit `profile:path` inputs; or
- a sweep manifest:

  ```bash
  ./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json
  ```

The sweep command should call matrix rendering automatically based on
`--matrix`.

## Orchestration architecture

### Recommended implementation shape

Use a Bash wrapper for discoverability and a Go orchestrator for robust flag
parsing, manifests, matrix joins, and subprocess lifecycle:

```text
scripts/run-sweep.sh          thin wrapper: go run ./cmd/promshim-sweep -- "$@"
cmd/promshim-sweep/main.go    orchestration, flags, manifest, summaries
internal/promharness/sweep/   reusable sweep types and artifact/matrix helpers
```

Why Go for the orchestrator:

- Better CSV/repeated flag parsing and validation.
- Easier JSON manifest/matrix generation.
- Easier subprocess status aggregation without brittle shell arrays.
- Existing benchmark code is already Go under `internal/promharness`.

Shell remains appropriate for lower-level Docker and ClickHouse helper scripts.
The Go orchestrator can call those scripts with explicit env and output paths.

### Run manifest

Every sweep writes under the sanitized run name:

```text
harness/artifacts/sweeps/<run-name>/manifest.json
```

Suggested schema:

```json
{
  "schemaVersion": 1,
  "runName": "pr-42-native-default",
  "runSlug": "pr-42-native-default",
  "runId": "20260424T213000Z-pr-42-native-default",
  "artifactRoot": "harness/artifacts/sweeps/pr-42-native-default",
  "startedAt": "...",
  "finishedAt": "...",
  "git": { "sha": "...", "dirty": false },
  "preset": "default",
  "axes": {
    "suites": ["compliance", "bench"],
    "transports": ["native"],
    "profiles": ["7d", "30d"],
    "densities": ["sparse"],
    "benchModes": ["prefer", "force_supported"],
    "complianceModes": ["prefer", "native"],
    "benchStack": "isolated"
  },
  "stacks": {
    "compliance": {
      "composeDir": "harness/compliance",
      "prometheusURL": "http://localhost:29090",
      "shimURL": "http://localhost:29091",
      "clickhouseURL": "http://localhost:28123"
    },
    "bench": {
      "composeDir": "harness/bench",
      "prometheusURL": "http://localhost:29190",
      "shimURL": "http://localhost:29191",
      "clickhouseURL": "http://localhost:28124",
      "clickhouseWriteURL": "http://localhost:29192/write"
    }
  },
  "seed": {
    "policy": "reuse",
    "targets": ["prom", "ch"],
    "datasets": [
      {
        "profile": "7d",
        "density": "sparse",
        "prom": "present",
        "ch": "present",
        "seeded": false,
        "estimatedSeries": 130,
        "estimatedSamplesPerStore": 5241600
      }
    ]
  },
  "estimate": {
    "dryRun": false,
    "seriesTotal": 130,
    "samplesPerStoreTotal": 5241600,
    "missingSamplesToSeed": 0,
    "benchRequests": 240,
    "memoryMode": "summary",
    "runtimeClass": "medium",
    "history": { "matchedRuns": 0 }
  },
  "artifacts": [
    {
      "kind": "bench-report",
      "transport": "native",
      "profile": "7d",
      "density": "sparse",
      "path": "bench/native/sparse/7d/bench-report.json"
    },
    {
      "kind": "memory-summary",
      "transport": "native",
      "profile": "7d",
      "density": "sparse",
      "path": "memory/native/sparse/7d/memory-summary.json"
    }
  ],
  "status": {
    "overall": "pass|fail|partial",
    "complianceFailures": 0,
    "nativeGaps": 0,
    "benchRegressions": 0,
    "toolFailures": 0
  }
}
```

### Stack lifecycle

The sweep should own lifecycle by default:

1. Acquire named locks for the stacks it will touch:
   - `compliance-stack` for compliance phases;
   - `bench-stack` for benchmark/setup/profile phases.
2. Build promshim unless `--no-build`.
3. If compliance is selected, start the compliance stack with volumes preserved.
4. If benchmark/setup/profile work is selected, start the isolated benchmark
   stack with benchmark volumes preserved.
5. For each selected transport, recreate only the promshim container in the
   relevant stack with `PROM_SHIM_CLICKHOUSE_TRANSPORT=<transport>`.
6. Wait for stack-specific readiness:
   - compliance Prometheus/promshim/ClickHouse on compliance ports;
   - bench Prometheus/promshim/ClickHouse on benchmark ports.
7. Run compliance axes only against the compliance stack.
8. Run seed, benchmark, matrix, and ProfileEvents axes only against the
   benchmark stack.
9. Leave selected stacks up only with `--keep-up`; otherwise stop containers
   while preserving volumes.

Do not run ad-hoc `curl`/`docker exec` against either stack during a sweep except
through the orchestrator; query-log/profile tooling assumes a quiet benchmark
window. Benchmark query-log/profile reads must use the benchmark ClickHouse, not
the compliance ClickHouse.

## Additional safeguards and design points

### Keep sparse and dense datasets from overlapping

Sparse and dense variants must not accidentally share the same metric label set
at the same timestamps. If `7d sparse` and `7d dense` both use the same end time
and labels, the first sparse instances overlap with dense instances and repeated
seeding can create duplicate or dedup-dependent samples.

Pick one isolation model and make it explicit:

1. Prefer separate non-overlapping time windows for every `(profile, density)`
   pair, with distinct eval times recorded in the manifest and corpus mapping.
2. Or use separate benchmark databases/tables per density.
3. Or add a dataset label such as `bench_density="dense"` to every generated
   series and update corpora to select it.

The first option keeps PromQL closest to user-facing examples and fits the
existing long-range profile model. Whichever option is chosen, `--estimate` and
`--dry-run` must show the exact eval time/window for each profile+density.

### Stabilize pre-seeded Prometheus data before timing

Prometheus query cost can differ depending on whether samples are still in head /
WAL / out-of-order buffers versus compacted blocks. Dense benchmark setup should
therefore include a stabilization barrier before declaring data ready:

- wait for remote-write ingestion to finish;
- query marker series from Prometheus and ClickHouse;
- record Prometheus TSDB/head/block stats when available;
- optionally restart the benchmark Prometheus after setup if that gives more
  repeatable post-WAL-replay behavior;
- record whether the run is `fresh_seed`, `post_restart`, or `compacted` in the
  manifest.

Do not silently mix these states in baselines. If exact compaction control is not
available, report the state and rely on warmups plus repeated runs.

### Avoid host-level benchmark noise

Even with isolated volumes, compliance and benchmark containers share host CPU,
memory, disk, and Docker logging. The default mixed sweep should not run
compliance and benchmark timing concurrently. Prefer:

1. run compliance phases;
2. stop or idle compliance containers if heavy benchmarking is selected;
3. run benchmark phases from a quiet benchmark stack.

Record host/container context in the manifest where cheap:

- ClickHouse and Prometheus image tags/digests;
- promshim git SHA and dirty state;
- Docker CPU/memory limits if configured;
- selected transport and ClickHouse settings;
- benchmark stack port/config hashes.

### Bound Docker stdout log growth too

Routing ClickHouse text logs to stdout keeps them out of ClickHouse data
volumes, but Docker may still write JSON logs on the host. The benchmark Compose
file should set conservative log rotation, for example `max-size` and
`max-file`, for ClickHouse, Prometheus, and promshim services. This keeps long
seed/setup runs from consuming host disk outside the benchmark volume estimate.

### Make benchmark limits explicit

Dense processing profiles can hit Prometheus or promshim limits before they hit
the intended 250-1000 ms band. The benchmark stack should set and report:

- Prometheus query timeout;
- Prometheus max samples, if configured;
- promshim response series/point limits;
- promshim range chunk settings;
- per-request benchmark timeout.

The sweep summary should classify limit failures separately from correctness,
transport, and performance failures.

### Cache policy

Warm-cache measurements are usually the most useful default for repeatable local
comparison. The sweep should make that explicit:

- default: warm-cache, using configured warmup repeats;
- optional future flag: `--cache cold|warm` if a reliable cold-cache procedure
  is added;
- disable ClickHouse query cache for benchmark SQL unless explicitly testing it;
- record cache-relevant settings in the manifest.

### Memory trade-off measurement

One of the key questions for future CBE is whether a query should stay in native
SQL or run locally for small datasets. Latency alone is not enough: native SQL
may shift memory into ClickHouse, while tier 3/4 local execution may shift memory
into promshim. The sweep should measure this relative trade-off explicitly.

Add:

```text
--memory off|summary|detailed
```

Default:

```text
--memory summary
```

Report memory by domain, not as a single fake-precise number:

| Domain | Captures | Signal |
|---|---|---|
| ClickHouse query memory | native SQL, delegated PromQL, subtree pushdowns | `system.query_log.memory_usage`, `ProfileEvents.MemoryTrackerUsage` |
| promshim process memory | local executor, planning, decode, response construction | Go runtime metrics and process RSS snapshots |
| container memory | coarse end-to-end promshim footprint | cgroup current/peak when available |

For `summary` mode:

- keep benchmark requests serialized for memory-sensitive comparisons;
- capture ClickHouse memory from benchmark `system.query_log`:
  - p50/p95/max `memory_usage`;
  - `ProfileEvents.MemoryTrackerUsage` where available;
- capture promshim process memory around each query/mode group:
  - heap alloc / heap in-use before and after;
  - RSS before and after;
  - deltas and high-water observations when available;
- capture cgroup current/peak for the promshim container when the host exposes
  it;
- report memory columns in mode matrices:
  - CH memory p50/max;
  - shim RSS delta;
  - shim heap delta;
  - container peak/current delta;
- keep CH and promshim values separate so reviewers can see whether native SQL
  shifted memory from shim to ClickHouse.

For `detailed` mode:

- run a smaller selected query set unless the user explicitly requests all;
- optionally force a GC before a measured query group to reduce Go heap noise,
  and record that this was done;
- capture pprof heap/allocs snapshots before and after selected query/mode
  groups;
- write artifacts under the named run directory, for example:

  ```text
  harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/heap-before.pb.gz
  harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/heap-after.pb.gz
  harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/allocs.pb.gz
  ```

Interpretation rules:

- Native mode memory should be read as `ClickHouse memory + promshim overhead`.
- Tier 3 memory should be read as `subtree ClickHouse memory + promshim local
  executor memory`.
- Tier 4/off memory should be read mostly as promshim memory plus any storage
  fetch overhead.
- Per-request Go heap deltas are noisy because of GC and heap reuse; use them
  for relative trade-offs across execution modes in the same sweep, not as
  absolute capacity guarantees.
- RSS does not necessarily shrink after a query; report peak/current and deltas,
  but do not treat negative or flat deltas as proof that a query used no memory.
- Memory-sensitive comparisons should not run concurrently with compliance or
  other benchmark requests.

The default summary should answer:

> For this query/profile/density, did native SQL reduce promshim memory enough to
> justify any ClickHouse memory increase, or would a CBE policy reasonably choose
> local execution for small-data cases?

### Baseline policy

Heavy and dense benchmark baselines are machine-sensitive. Do not gate dense
benchmarks against committed wall-clock baselines by default. Prefer:

- strategy/roundtrip/coverage regressions as hard failures;
- latency reports and target-band classification as advisory unless the user
  supplies an explicit local baseline;
- ProfileEvents deltas for ClickHouse optimization claims;
- memory trade-off reports as advisory unless the user supplies an explicit
  local memory baseline.

## Implementation phases

### Phase 1. Normalize lower-level script options

Goal: make existing scripts composable without manual loops or copied artifacts.

Code areas:

- `harness/bench/docker-compose.yml`
- `harness/bench/prometheus/prometheus.yml`
- `harness/bench/clickhouse/config.d/logging.xml`
- `scripts/seed-long-range.sh`
- `scripts/run-bench.sh`
- `scripts/run-compliance.sh`
- `scripts/ch-profile-capture.sh`
- `scripts/bench-matrix.sh`

Tasks:

1. Add an isolated benchmark Compose stack under `harness/bench/` with separate
   volume names and non-conflicting ports.
2. Add benchmark-only ClickHouse logging config that routes text logs to
   stdout/stderr or non-persistent storage, keeps `system.query_log` available
   for ProfileEvents, and bounds system log table retention with short TTLs,
   especially `system.text_log`, `processors_profile_log`, `trace_log`,
   `query_metric_log`, and `background_schedule_pool_log`.
3. Update lower-level benchmark/seed/profile scripts to accept explicit
   Prometheus, promshim, ClickHouse HTTP, ClickHouse native, and ClickHouse
   write endpoints. Their sweep defaults should point at the benchmark stack,
   not the compliance stack.
4. Add `seed-long-range.sh --profile all` and `--density sparse|dense`.
5. Add `--seed-marker` support in `cmd/ch-seed-long` and emit
   `promshim_seed_info` marker series.
6. Add seed detection helpers for Prometheus and ClickHouse using the selected
   stack endpoints.
7. Add `--artifact-dir` / `--artifact-name` / `--artifact-prefix` options to
   bench and profile scripts so artifacts do not need manual copying.
8. Add explicit `--transport` to `run-compliance.sh` and ensure it recreates
   promshim with the chosen transport in the compliance stack.
9. Add explicit benchmark-stack transport recreation for the sweep path.
10. Add `--no-baseline` to `run-bench.sh` to replace the `/tmp/no-baseline` hack.

Validation:

```bash
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --setup --profile all --density sparse --target both
./scripts/run-sweep.sh --suite bench --profile all --density sparse --seed reuse --no-baseline --artifact-dir /tmp/promshim-bench-test
./scripts/run-compliance.sh --transport native --skip-native --keep-up
```

Risks / notes:

- Marker queries must be cheap and reliable across both stores.
- The benchmark stack must never reuse compliance volume names.
- Verify ClickHouse text logs are visible in `docker logs` and are not written
  into benchmark persistent volumes.
- Keep old flags working for current automation, but document any compatibility
  mode that still targets the compliance stack as debug-only.

### Phase 2. Refactor benchmark reports around explicit dimensions

Goal: support `prefer` vs `force_supported` vs `off` and transport/profile/
density labels without baking dimensions into filenames.

Code areas:

- `cmd/promshim-bench/main.go`
- `internal/promharness/bench.go`
- `internal/promharness/bench_test.go`
- `internal/promharness/memory*`
- `scripts/run-bench.sh`
- `scripts/bench-matrix.sh`

Tasks:

1. Add configurable `--shim-modes` to `promshim-bench`.
2. Add `prefer` bench mode support alongside current `force_supported` and
   `off` timings.
3. Add report schema v2 with `runLabels` and per-mode shim result objects.
4. Add `--memory off|summary|detailed` support to the bench/sweep path.
5. Capture promshim process RSS/Go heap snapshots around query/mode groups and
   cgroup current/peak when available.
6. Join promshim memory summaries with ClickHouse query-log memory by query name
   and execution mode where possible.
7. Keep legacy report compatibility or add a documented adapter.
8. Update baseline comparison to understand either v1 or v2 reports.
9. Update matrix rendering to consume v2 dimensions and include memory columns.

Validation:

```bash
go test ./internal/promharness ./cmd/promshim-bench
./scripts/run-bench.sh --corpus harness/corpus/bench-native-lowering.json --shim-modes prefer,force_supported --memory summary --no-baseline --matrix
```

Risks / notes:

- Baseline compatibility is the main review risk. Avoid breaking existing CI or
  Make targets silently.

### Phase 3. Add dense datasets and processing corpora

Goal: provide benchmark variants that produce real Prometheus processing times
without pathological response sizes.

Code areas:

- `cmd/ch-seed-long/main.go`
- `scripts/seed-long-range.sh`
- `harness/corpus/bench-processing-7d.json`
- `harness/corpus/bench-processing-30d.json`
- `harness/corpus/bench-processing-1y.json`
- `harness/README.md`

Tasks:

1. Add density presets to the long-range generator.
2. Keep sparse identical to current generator defaults.
3. Add dense profile defaults:
   - `7d`: 100 instances/job.
   - `30d`: 100 instances/job.
   - `1y`: 50 instances/job.
4. Add processing corpora with bounded-output heavy query shapes.
5. Add target-band metadata for rows intended to land in 250-1000 ms Prom p50.
6. Add summary reporting for target-band classification.

Validation:

```bash
./scripts/run-sweep.sh --setup --profile 7d --density dense --target both
./scripts/run-sweep.sh --suite bench --profile 7d --density dense --seed reuse --corpus harness/corpus/bench-processing-7d.json --mode prefer,force_supported --repeats 3 --warmup 1 --no-baseline
```

Risks / notes:

- Dense seeding can consume significant disk. Print selected sample counts and
  require `--yes` for `full` or dense multi-profile non-interactive runs.
- Dense target-band success is host-dependent. Report, do not hard fail, unless
  an explicit `--require-prom-band` flag is set.

### Phase 4. Implement the sweep orchestrator and estimate planner

Goal: one command coordinates stack lifecycle, seeding, compliance, benches,
profiles, transports, matrix rendering, and dry-run/estimate output.

Code areas:

- `scripts/run-sweep.sh`
- `cmd/promshim-sweep/main.go`
- `internal/promharness/sweep/`
- `Makefile`
- `README.md` / `harness/README.md`

Tasks:

1. Implement preset expansion and axis parsing.
2. Implement `--name` / `--run-id` parsing, slug sanitization, collision checks,
   and `--overwrite` behavior.
3. Implement a plan/estimate builder that resolves selected axes into concrete
   stack actions, seed checks, bench jobs, compliance jobs, matrices, and
   artifact paths without executing them.
4. Implement `--dry-run` to print the resolved plan and exit without side
   effects.
5. Implement `--estimate` to add dataset size, request count, rough runtime,
   disk, ingest, and memory-measurement overhead estimates; use historical
   manifests when available.
6. Validate incompatible combinations early, with actionable messages.
7. Acquire stack-specific locks once for the full sweep, except pure
   dry-run/estimate paths that do not need live dataset checks.
8. Build/start/recreate the compliance and benchmark stacks independently based
   on selected suites.
9. Resolve required benchmark-stack datasets and apply seed policy without
   touching compliance volumes.
10. Invoke compliance phases against the compliance stack and benchmark phases
    against the benchmark stack, with axis-specific artifact paths under the
    named run directory.
11. Capture ProfileEvents from benchmark ClickHouse according to policy.
12. Capture memory summaries according to `--memory` and write them beside
    benchmark artifacts.
13. Write `manifest.json` incrementally so partial runs are inspectable.
14. Render Markdown and JSON summaries.
15. Add `make sweep` and document examples.

Validation:

```bash
./scripts/run-sweep.sh smoke --name smoke-local --dry-run
./scripts/run-sweep.sh heavy --name heavy-preview --estimate
./scripts/run-sweep.sh --name native-7d-sparse --suite bench --transport native --profile 7d --density sparse --mode prefer,force_supported --seed reuse
./scripts/run-sweep.sh --name compliance-transport-check --suite compliance --transport http,native --compliance-mode prefer --keep-up
```

Risks / notes:

- The orchestrator must not hide lower-level failures. Preserve subprocess exit
  codes and emit a final status classification.
- Be strict about not mixing query-log windows between parallel runs; use
  stack-specific locks and query benchmark ProfileEvents only from the benchmark
  ClickHouse.
- Treat any attempt to seed benchmark data into compliance endpoints as a
  blocker unless the user explicitly selected a documented debug escape hatch.

### Phase 5. Matrix and reporting polish

Goal: make sweep output useful for users without opening raw JSON.

Code areas:

- `internal/promharness/sweep/matrix*`
- `scripts/bench-matrix.sh`
- generated Markdown summaries

Tasks:

1. Generate a top-level `summary.md` with selected axes, status, and links to
   artifacts.
2. Generate category and query matrices for benchmark results.
3. Generate transport/mode/profile/density comparison matrices when those axes
   have more than one value.
4. Include target-band summaries for dense processing benchmarks.
5. Include memory trade-off columns and summaries by execution mode.
6. Include compliance pass/fail/gap matrix.
7. Include ClickHouse ProfileEvents counters when available.

Validation:

```bash
./scripts/run-sweep.sh --preset default --format both
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json --per-query
```

Risks / notes:

- Keep Markdown readable. Large full sweeps should link to detailed matrices
  instead of dumping everything to stdout.

### Phase 6. Documentation and migration cleanup

Goal: make the new command the obvious path and reduce agent/manual misuse.

Code areas:

- `README.md`
- `harness/README.md`
- `Makefile`
- script `--help` output

Tasks:

1. Document `run-sweep.sh` presets and common examples.
2. Document `--dry-run`, `--estimate`, and how to interpret rough runtime /
   dataset estimates.
3. Mark old manual loops as lower-level/debug workflows.
4. Explain seeding persistence and seed policies.
5. Explain sparse vs dense and the 250-1000 ms target-band expectation.
6. Explain memory trade-off reporting and how to read CH-vs-promshim memory for
   native SQL vs tier 3/4 / future CBE decisions.
7. Document run naming, artifact directory layout, collision behavior, and how
   to attach named results to reviews.
8. Update `make bench` if appropriate, or add `make sweep` without changing
   existing behavior.

Validation:

```bash
./scripts/run-sweep.sh --help
make sweep
```

## Exit criteria

The plan is complete when:

1. A user can run `./scripts/run-sweep.sh` with no arguments and get a balanced
   correctness + benchmark sweep from pre-seeded data with a concise summary.
2. If required data is absent, the default command fails fast with an exact
   `./scripts/run-sweep.sh --setup ...` command instead of silently seeding.
3. A user can run `./scripts/run-sweep.sh --name my-run` and all artifacts land
   under `harness/artifacts/sweeps/my-run/`.
4. A user can run `./scripts/run-sweep.sh full` and get transport x profile x
   density x mode matrices without hand-written loops or manual artifact copies.
5. A user can run `./scripts/run-sweep.sh heavy --estimate` and see selected
   axes, artifact paths, dataset/sample estimates, request counts, rough runtime
   class, memory measurement mode/overhead, stack endpoints, and setup commands
   without side effects.
6. Benchmark setup and benchmark runs use isolated benchmark Prometheus /
   ClickHouse volumes by default; compliance volumes remain untouched.
7. Re-running a sweep does not re-seed existing datasets unless requested.
8. Dense processing benchmark rows report whether Prometheus p50 landed in the
   250-1000 ms target band.
9. Mode matrices report memory trade-offs by domain so native SQL, tier 3, and
   local/off execution can be compared for CBE decisions.
10. Transport comparisons recreate promshim correctly instead of relying on a
   bench-process environment variable.
11. Compliance failures, native gaps, benchmark regressions, and tooling failures
   are classified separately.
12. Existing focused scripts and Make targets continue to work or have documented
   replacements.

## Review checklist

- [ ] Does `--help` explain presets before advanced axes?
- [ ] Are defaults safe for laptops and local Docker volumes?
- [ ] Does `--seed reuse` fail clearly with setup instructions when data is missing?
- [ ] Does `--seed missing` seed only absent datasets?
- [ ] Does the default path avoid remote-write work when data is already present?
- [ ] Do benchmark seed/setup commands target benchmark ports and benchmark volumes only?
- [ ] Can benchmark volumes be reset without deleting compliance volumes?
- [ ] Are benchmark ClickHouse text logs routed to stdout/stderr or otherwise kept out of persistent data volumes?
- [ ] Is `system.query_log` still available for ProfileEvents while high-volume diagnostic logs stay bounded, especially `system.text_log`?
- [ ] Does `--dry-run` avoid Docker, remote-write, bench, and artifact side effects?
- [ ] Does `--estimate` show dataset size, request count, rough runtime, memory overhead, and missing setup commands?
- [ ] Are runtime estimates clearly marked as rough/environment-dependent?
- [ ] Does `--name` produce a sanitized, predictable artifact directory?
- [ ] Does an existing named run fail safely unless `--overwrite` is set?
- [ ] Does every artifact include transport/profile/density/mode labels?
- [ ] Does the matrix show `prefer` and `force_supported` separately?
- [ ] Does the matrix include memory trade-off columns for execution modes when `--memory` is enabled?
- [ ] Does the matrix reveal strategy changes/flaps?
- [ ] Does dense benchmarking avoid returning huge payloads?
- [ ] Are ProfileEvents captured for ClickHouse performance claims?
- [ ] Are compliance allowlist rules unchanged?
