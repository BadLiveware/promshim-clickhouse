# 02. ClickHouse deployment tuning for promshim workloads

## Purpose and scope

Find ClickHouse server/operator tuning that improves promshim-style PromQL
workloads. Apply the measured reference profile to this repository's benchmark
setup so future measurements are reproducible. Document the same guidance for
users as optional operator configuration; promshim must not automatically mutate
external ClickHouse deployments.

## Prerequisites

- Benchmark foundation corpus and baseline artifacts exist.
- `docs/clickhouse-reference-profile.md` exists and distinguishes operator
  guidance from promshim-owned query settings.
- Benchmark stack data is available for at least 7d sparse; dense/long-range
  availability is known.

## Affected areas

- `harness/bench/docker-compose.yml`
- `harness/bench/clickhouse/config.d/` and related benchmark-only config files
- `harness/clickhouse/config.d/` if shared config must be split carefully
- `docs/clickhouse-reference-profile.md`
- `README.md` benchmark/reference-profile section
- `scripts/run-sweep.sh` only if manifests need to record reference profile name.

Do not tune the compliance stack for long-range/dense benchmark behavior.
Compliance may keep only correctness-required and logging settings.

## Tuning candidates

Evaluate these as operator/server-profile candidates. Each candidate needs a
before/after artifact and a decision: adopt in benchmark reference profile,
document as optional, or reject.

1. Query logging/profile evidence defaults.
   - Ensure benchmark ClickHouse records enough query-log/ProfileEvents detail.
   - Expected signal: memory summaries complete; query-log rows include Settings
     and ProfileEvents.

2. Read concurrency and thread pressure.
   - Evaluate user/profile defaults such as bounded `max_threads` and query
     concurrency limits for dashboard-style mixed workloads.
   - Expected signal: lower p95/p99 under concurrent mixed corpus without making
     single-query p50 the only criterion.

3. Memory guardrails for heavy aggregation/range families.
   - Evaluate user/profile memory caps and spill-related settings only if dense
     controls show memory pressure.
   - Expected signal: fewer OOM/timeouts or bounded memory with acceptable p95;
     no silent partial results.

4. Cache surfaces.
   - Filesystem/mark/uncompressed cache: document warm/cold context and memory
     tradeoffs; change benchmark defaults only when evidence is stable.
   - Query condition cache: evaluate only as a repeated selective workload aid.
   - Query cache: keep disabled by default for PromQL freshness unless a clearly
     separated experiment proves an acceptable freshness contract.

5. Storage/layout guidance.
   - For the current `TimeSeries` table engine setup, measure what can actually
     be controlled by repository config versus ClickHouse internals.
   - Evaluate projections/materialized views only for a named hot family and
     document freshness/storage costs; do not make them hidden requirements.

6. Operator manifest guidance.
   - Map the benchmark reference profile to first-party ClickHouse Operator
     concepts: resources, storage class, replicas, logging, and monitoring.
   - Expected output is documentation; do not add Kubernetes manifests unless the
     repository already has a place for example manifests.

## Implementation tasks

1. Add benchmark reference profile metadata.
   - Ensure sweep manifests or summary notes record the ClickHouse reference
     profile name, starting with `promshim-ch-timeseries-reference-v1`.
   - Acceptance: a sweep summary names the reference profile or explicitly says
     `default-benchmark-compose` when not using it.

2. Create isolated benchmark config variants.
   - Add benchmark-only config files under `harness/bench/clickhouse/config.d/`
     or `users.d/` for candidate operator settings.
   - Keep variants selectable by env var or separate compose override so the
     baseline profile remains reproducible.
   - Acceptance: `docker compose config` for the benchmark stack shows the
     selected config and compliance compose is unchanged except correctness/logging.

3. Run candidate experiments.
   - For each candidate, run baseline and candidate sweeps over the tuning corpus
     on 7d sparse.
   - Run dense or long-range controls when the expected effect could reverse
     under high cardinality or wide ranges.
   - Capture EXPLAIN/ProfileEvents only when the setting is expected to affect
     plan shape or read work; otherwise query-log/memory/process evidence is
     sufficient.
   - Acceptance: each candidate has an artifact note with adopt/reject decision.

4. Adopt benchmark reference profile changes.
   - Only apply settings to the repository benchmark setup when the evidence
     shows reproducible value or improved measurement reliability.
   - Keep user-visible docs clear that this is operator guidance, not promshim
     auto-configuration.
   - Acceptance: `docs/clickhouse-reference-profile.md` names adopted benchmark
     defaults and optional production guidance separately.

5. Document rejected settings.
   - Add a table of tested-but-rejected settings with reason and artifact path.
   - Acceptance: readers can see why result query cache, overly high parallelism,
     or unproven projections were not adopted if experiments reject them.

## Validation tasks

Before changing benchmark config:

```bash
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --dry-run --estimate --name ch-reference-profile-dry-run
```

After config changes:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/seed-long-range.sh scripts/bench-matrix.sh
./scripts/run-sweep.sh --name ch-reference-profile-smoke \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --corpus-set native --memory summary
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/ch-reference-profile-smoke/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/ch-reference-profile-smoke/memory-summary-*.json
```

If compliance-relevant config is touched, also run:

```bash
./scripts/run-sweep.sh --name ch-reference-profile-compliance --skip-bench
```

## Exit criteria

- Benchmark ClickHouse reference profile is either updated with measured settings
  or explicitly kept unchanged with evidence.
- User docs distinguish adopted benchmark defaults from optional production
  guidance and rejected experiments.
- Compliance stack remains isolated from benchmark-only tuning.
- Named artifacts support every adopted or rejected setting.

## Handoff to next file

Use the adopted benchmark profile as the baseline environment for promshim-owned
session/query tuning. Do not mix server/operator tuning changes with per-query
settings experiments in the same artifact comparison.
