# 05. Reference ClickHouse deployment profile

## Purpose and scope

Document the ClickHouse server/operator profile recommended for promshim-style
PromQL workloads. This is separate from shim-owned per-query settings: these are
operator-facing recommendations and benchmark assumptions, not hidden
correctness requirements.

The goal is to make performance claims reproducible and to give users a clear
starting point when running ClickHouse primarily for promshim over `TimeSeries`
data.

## Prerequisites

- Stage 01 tuning inventory distinguishes operator/server settings from
  shim-owned statement/session settings.
- Stage 03 defines what promshim may set for its own queries.
- Stage 04 identifies query-family workloads that stress different ClickHouse
  resources.

## Affected areas

- Operator documentation.
- Benchmark/reference environment documentation.
- Local harness/compose defaults if they are explicitly intended to model the
  reference profile.
- Troubleshooting and observability docs.

## Requirements

- Recommendations must be labeled as advisory unless promshim truly requires
  them for correctness.
- Benchmark reports should name the reference profile when results depend on it.
- The docs must distinguish:
  - server-level tuning;
  - user/profile defaults;
  - promshim-owned statement/session settings;
  - generated SQL shape;
  - distributed ClickHouse concerns.
- Do not change production-facing defaults without explicit validation and
  rollback guidance.
- Use `~/code/external/ClickHouse`, `~/code/external/hyperdx`, and other local
  ClickHouse-backed examples as inputs for deployment guidance, but label every
  recommendation by its evidence level and fit for promshim.

## Implementation tasks

### 1. Define the reference workload

- [ ] Describe the intended workload:
  - read-heavy PromQL queries;
  - latency-sensitive dashboards and alerts;
  - high variance in cardinality and range width;
  - repeated dashboard queries;
  - background ingestion and merges sharing resources with reads.
- [ ] Define what the local benchmark stack represents and what it does not
  represent.
- [ ] Record assumptions about `TimeSeries` engine usage, retention, and query
  freshness.

### 2. Resource and concurrency guidance

- [ ] Survey ClickHouse and ClickHouse-backed observability examples under
  `~/code/external/` for resource/concurrency patterns that may apply to
  promshim's read-heavy PromQL workload.
- [ ] Document CPU and memory considerations for PromQL-style query latency.
- [ ] Recommend bounded query concurrency and per-query resource limits.
- [ ] Explain tradeoffs between high parallelism for wide scans and contention
  for dashboard workloads.
- [ ] Describe expected behavior under concurrent ingestion/merge pressure.
- [ ] Provide symptoms and evidence to inspect when ClickHouse is saturated.

### 3. Storage and cache guidance

- [ ] Document storage expectations such as local SSD preference and retention
  tradeoffs.
- [ ] Describe relevant cache surfaces at a high level:
  - filesystem cache;
  - mark/index caches;
  - uncompressed cache, if appropriate;
  - query cache caveats.
- [ ] Call out freshness/semantic caution before recommending query cache for
  PromQL paths.
- [ ] Explain how storage/caching affects interpretation of wall-clock versus
  ProfileEvents.

### 4. Observability requirements for tuning

- [ ] Recommend enabling enough query logging to capture:
  - query IDs/log comments;
  - duration;
  - read rows/bytes;
  - memory usage;
  - ProfileEvents.
- [ ] Document how promshim request IDs and CBE candidate IDs correlate with
  `system.query_log`.
- [ ] Include warnings about noisy query-log windows and manual probes during
  sweeps.
- [ ] Point users to named sweep artifacts as the reproducible measurement path.

### 5. Distributed ClickHouse guidance

If distributed ClickHouse is in scope for the docs:

- [ ] Explain when promshim should hit a coordinator versus local replicas.
- [ ] Document cross-shard aggregation and fanout risks.
- [ ] Identify settings that are distributed-only or require separate validation.
- [ ] Make clear which benchmark results are single-node only.

### 6. Local harness/reference profile alignment

- [ ] Decide whether harness/bench compose defaults should intentionally model
  the reference profile.
- [ ] If yes, document which defaults are part of the reference profile.
- [ ] If no, document the gap between local benchmark stack and production
  recommendations.
- [ ] Keep compliance stack isolated from benchmark/long-range profile tuning.

## Validation tasks

Documentation-only validation:

- [ ] Check that every recommendation is labeled as advisory, required, or
  benchmark-reference-only.
- [ ] Check that no operator/server setting is presented as a promshim-owned
  query setting.
- [ ] Check that distributed guidance is either validated or explicitly scoped as
  future work.

If harness defaults are changed as part of this stage, use the running-sweep
playbook and validate with:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/seed-long-range.sh scripts/bench-matrix.sh
./scripts/run-sweep.sh --dry-run --estimate --name post-cbe-reference-profile-dry-run
```

For a live smoke after harness changes:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-reference-profile-smoke \
  --profile 7d \
  --density sparse \
  --seed missing \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

## Exit criteria

- [ ] Reference ClickHouse deployment profile is documented.
- [ ] Operator/server recommendations are clearly separated from shim-owned
  execution settings.
- [ ] Benchmark assumptions are explicit.
- [ ] Observability requirements for ProfileEvents-based tuning are documented.
- [ ] Distributed ClickHouse guidance is either included with caveats or
  explicitly deferred.
- [ ] No hidden dependency on global server tuning is introduced.

## Handoff to next file

Stage 06 uses the reference profile as context for calibration and rollout.
Optimization claims should state whether they were measured under this profile
or under a different environment.
