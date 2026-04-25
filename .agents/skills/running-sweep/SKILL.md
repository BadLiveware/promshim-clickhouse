---
name: running-sweep
description: Use when running promshim's one-command benchmark/compliance sweep, setting up or resetting isolated benchmark data, comparing profiles/densities/transports/modes, or reviewing sweep artifacts from scripts/run-sweep.sh.
---

# Running Benchmark/Compliance Sweeps

## Overview

`./scripts/run-sweep.sh` is the primary user-facing workflow for combined
compliance and benchmark evaluation. It keeps benchmark data in an isolated
benchmark stack under `harness/bench/` so long-range or dense datasets never
contaminate the frozen compliance fixture.

Use this instead of hand-written seed/bench loops unless you are debugging a
low-level script directly.

## Stack isolation

The sweep uses separate benchmark endpoints and volumes:

| Service | Benchmark endpoint | Compliance endpoint |
|---|---|---|
| Prometheus | `http://localhost:29190` | `http://localhost:29090` |
| promshim | `http://localhost:29191` | `http://localhost:29091` |
| ClickHouse HTTP | `http://localhost:28124` | `http://localhost:28123` |
| ClickHouse remote write | `http://localhost:29192/write` | `http://localhost:29092/write` |

Benchmark volumes:

- `promshim_bench_clickhouse_data`
- `promshim_bench_prometheus_data`

`--bench-reset --yes` deletes only these benchmark volumes. It must not be used
as a compliance cleanup command.

## Common workflows

Preview the default run without side effects:

```bash
./scripts/run-sweep.sh --dry-run --estimate
```

Show benchmark stack and seed-marker state:

```bash
./scripts/run-sweep.sh --bench-status
```

One-time sparse setup for all long-range profiles:

```bash
./scripts/run-sweep.sh --setup --profile all --density sparse --target both
```

One-time dense setup for processing benchmarks:

```bash
./scripts/run-sweep.sh --setup --profile 7d --density dense --target both
```

Run a named default sweep:

```bash
./scripts/run-sweep.sh --name pr-42-default
```

Run a focused benchmark-only smoke using existing data:

```bash
./scripts/run-sweep.sh \
  --name sweep-smoke \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

Compare multiple execution modes:

```bash
./scripts/run-sweep.sh \
  --name mode-compare \
  --profile 7d \
  --density sparse \
  --skip-compliance \
  --shim-modes prefer,force_supported,off \
  --memory summary
```

Reset benchmark data only:

```bash
./scripts/run-sweep.sh --bench-reset --yes
```

Stop benchmark containers while keeping data for reuse:

```bash
(cd harness/bench && docker compose down)
```

## Seed policies

| Policy | Meaning |
|---|---|
| `reuse` | Normal default; require selected data to already exist. |
| `missing` | Setup default; seed only missing selected profile/density/target data. |
| `always` | Deliberately write selected data again. Use sparingly. |
| `never` | Skip seed checks/writes. Useful for unusual manual setups. |

If a normal run reports missing data, run the printed `--setup` command rather
than switching to compliance-stack seeding.

## Profiles, densities, and corpora

Profiles:

- `7d`
- `30d`
- `1y`
- `all`

Densities:

- `sparse` — faster, broad benchmark signal.
- `dense` — higher cardinality; use for real processing-workload latency.
- `all`

Corpus sets:

- `native` — native-lowering benchmark corpora.
- `processing` — heavy/bounded-output processing corpora with advisory Prom p50 target bands.
- `both`

Use `--estimate` before dense/all runs. Estimates are rough and host-dependent.
`--estimate` implies dry-run unless `--execute` is passed.

## Artifacts

Named sweep artifacts live under:

```text
harness/artifacts/sweeps/<run-name>/
```

Important files:

| Artifact | Meaning |
|---|---|
| `manifest.json` | Machine-readable sweep manifest with axes, endpoints, reports, memory artifacts. |
| `summary.md` | Human-readable summary with strategy histogram, target bands, top slow rows. |
| `summary.json` | Machine-readable summary. |
| `bench-report-*.json` | v2 benchmark report, one per profile/density/corpus. |
| `memory-summary-*.json` | ClickHouse query_log/ProfileEvents + promshim metrics snapshot. |
| `memory-detail-*/manifest.json` | Whole-run pprof snapshot manifest for `--memory detailed`. |

Render matrices from a completed sweep:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json --per-query
```

## Memory modes

| Mode | Behavior |
|---|---|
| `off` | No memory artifacts. |
| `summary` | Default; writes ClickHouse query memory/ProfileEvents and promshim process/Go metrics. |
| `detailed` | Also writes whole-run pprof heap/allocs/goroutine snapshots when pprof is enabled. |

Local benchmark/compliance compose stacks enable `PROM_SHIM_ENABLE_PPROF=1` so
`--memory detailed` can collect snapshots. Do not enable pprof in production
unless access is protected.

Current detailed-mode limitation: artifacts are whole-run snapshots, not
per-query/per-mode heap captures. Cgroup current/peak is not captured yet.

## Known gotchas

- Do not benchmark long-range/dense data against the compliance ports.
- Do not run ad-hoc `curl`, `docker exec`, or ClickHouse queries during a sweep
  if you plan to interpret `system.query_log` memory/ProfileEvents artifacts.
- `run-sweep.sh` serializes scripted runs with project locks, but cannot guard
  manual interactive access.
- If `manifest.json` lacks run labels/profile/density, suspect a CLI parsing
  regression around Go boolean flags; `run-bench.sh` must pass boolean flags as
  `--include-prom=true`, not `--include-prom true`.
- `run-sweep.sh` rebuilds buildable benchmark services on stack start. Docker
  cache keeps no-op rebuilds cheap, and this prevents post-change sweeps from
  accidentally measuring an already-running old promshim image.
- If memory summaries have missing log comments, rerun from a quiet stack and
  verify `X-Promshim-Log-Comment` propagation.

## Validation checklist

After changing sweep-related code, run at least:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/seed-long-range.sh scripts/bench-matrix.sh
go test ./cmd/ch-seed-long ./cmd/promshim ./internal/promharness ./cmd/promshim-bench
./scripts/run-sweep.sh --dry-run --estimate --name smoke
```

For a live smoke that exercises isolated benchmark data and memory summaries:

```bash
./scripts/run-sweep.sh \
  --name sweep-smoke-live \
  --profile 7d \
  --density sparse \
  --seed missing \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

Then verify:

```bash
jq '.bench' harness/artifacts/sweeps/sweep-smoke-live/manifest.json
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/sweep-smoke-live/memory-summary-*.json
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/sweep-smoke-live/manifest.json
```
