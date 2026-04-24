# 03 — Dense datasets and processing corpora

## Goal

Add realistic sparse/dense benchmark variants and processing-heavy corpora that
make Prometheus spend real CPU time while returning bounded results. Dense rows
should target roughly 250-1000 ms Prometheus p50 on typical local benchmark
hardware, but the target band is advisory and host-dependent.

## Dependencies

- [`01-stack-isolation-and-seeding.md`](01-stack-isolation-and-seeding.md) for
  isolated benchmark storage and seed lifecycle.
- [`02-bench-runner-schema-and-modes.md`](02-bench-runner-schema-and-modes.md)
  for dimension-aware report labels.

## Scope

### In scope

- Define sparse and dense dataset densities.
- Choose sparse/dense isolation model.
- Add dense generator presets.
- Add processing-heavy corpora for 7d, 30d, and 1y.
- Add target Prometheus p50 band metadata.
- Add seed stabilization and TSDB state recording.
- Add sample/disk estimate constants for `--estimate`.

### Out of scope

- Final sweep orchestration.
- Final matrices.
- Memory reporting implementation.

## Dataset density targets

Sparse should remain equivalent to current long-range generator defaults:

```text
jobs: 2
instances/job: 5
metric series/instance: ~13
series: ~130
```

Dense defaults:

| Profile | Step | Dense instances/job | Approx series | Approx samples/store |
|---|---:|---:|---:|---:|
| `7d` | 15s | 100 | ~2,600 | ~105M |
| `30d` | 60s | 100 | ~2,600 | ~112M |
| `1y` | 300s | 50 | ~1,300 | ~137M |

Expert overrides:

```text
--instances-per-job N
--jobs demo-api,demo-worker,...
```

Custom density names in artifacts should include the effective shape, e.g.
`custom-i200-j2`.

## Sparse/dense isolation

Do not let sparse and dense data overlap with identical labels and timestamps.
Pick and implement one model:

1. Preferred: non-overlapping windows per `(profile, density)`.
2. Alternative: separate benchmark database/table per density.
3. Alternative: add dataset labels and require corpora to select them.

Record exact eval time/window in corpus metadata and sweep manifest.

## Processing corpora

Add:

```text
harness/corpus/bench-processing-7d.json
harness/corpus/bench-processing-30d.json
harness/corpus/bench-processing-1y.json
```

Use query shapes that scan substantial data but return bounded output.

Representative rows:

```promql
sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))
sum by (job, mode) (rate(demo_cpu_usage_seconds_total[6h]))
sum by (job, type) (avg_over_time(demo_memory_usage_bytes[6h]))
sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1d]))
histogram_quantile(0.95, sum by (le) (rate(demo_api_request_duration_seconds_bucket[1h])))
```

Range-query guidance:

| Profile | Range rows | Step guidance |
|---|---:|---:|
| `7d` | 24h and 7d | 1m / 5m |
| `30d` | 7d and 30d | 15m / 1h |
| `1y` | 30d and 1y | 6h / 1d |

Add subquery rows only after simpler rows are calibrated. Subqueries can become
pathological and obscure storage/processing signal.

## Target band metadata

Add optional metadata to processing corpus rows:

```json
"targetPromP50Ms": { "min": 250, "max": 1000 }
```

Report target-band classification:

```text
too_fast | in_band | too_slow | n/a
```

Do not hard-fail by default if a host is faster/slower than expected. Add a
future/optional `--require-prom-band` if strict local calibration is needed.

## Prometheus seed stabilization

Dense setup should not declare data ready until it has passed a stabilization
barrier:

- wait for remote-write ingestion to finish;
- marker query succeeds in Prometheus and ClickHouse;
- record Prometheus TSDB/head/block stats when cheap;
- optionally restart benchmark Prometheus after setup if that improves repeated
  post-WAL-replay behavior;
- record setup state in manifest, e.g. `fresh_seed`, `post_restart`,
  `compacted_unknown`.

Do not silently mix seed states in baselines.

## Disk estimate guidance

For the planned dense profiles, budget roughly:

```text
single 1y dense profile: ~5 GB likely, ~10 GB conservative headroom
all dense profiles:      ~10 GB likely, ~20 GB conservative headroom
```

Estimates should scale linearly with `instances-per-job` and be presented as
rough/environment-dependent.

## Implementation tasks

1. Add density presets to `cmd/ch-seed-long` / seed script.
2. Implement sparse/dense isolation model.
3. Update seed marker labels with density, shape, and seed.
4. Add processing corpora and metadata.
5. Add target-band metadata parsing to bench reports.
6. Add estimate constants for series, samples, disk, and setup warnings.
7. Add seed stabilization checks and manifest fields.
8. Add tests for density/profile config calculations.

## Validation

```bash
./scripts/run-sweep.sh --setup --profile 7d --density dense --target both
./scripts/run-sweep.sh \
  --suite bench \
  --profile 7d \
  --density dense \
  --seed reuse \
  --corpus harness/corpus/bench-processing-7d.json \
  --mode prefer,force_supported \
  --repeats 3 \
  --warmup 1 \
  --no-baseline
```

Check:

- dense data does not overlap sparse data unexpectedly;
- queries return bounded result sizes;
- Prometheus p50 target-band is reported;
- setup status records seed/TSDB state;
- compliance stack remains untouched.

## Risks

- Dense setup can take long and consume disk.
- Prometheus performance varies by host and compaction state.
- Query rows can accidentally return too much data.
- Non-overlapping windows require careful eval-time/corpus mapping.

## Exit criteria

- Sparse and dense profiles are explicitly defined and isolated.
- Dense setup can be run once and reused.
- Processing corpora exist for 7d, 30d, and 1y.
- Target-band classification appears in reports.
- Estimate output can compute sample/disk sizes for dense profiles.
