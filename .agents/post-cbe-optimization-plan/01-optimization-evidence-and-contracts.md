# 01. Optimization evidence and contracts

## Purpose and scope

Create the evidence loop and semantic contracts needed before restarting
optimization work. This file is intentionally front-loaded with measurement,
explainability, and invariants so later IR rewrites, ClickHouse settings, and
SQL-shape changes can be reviewed as real optimizations rather than cosmetic
changes.

This slice should not change served behavior. It prepares the optimizer work by
making current behavior inspectable and by defining what future stages must
preserve.

## Prerequisites

- CBE candidate routing plan exists under
  [`../cbe-candidate-routing-plan/`](../cbe-candidate-routing-plan/).
- Sweep and ProfileEvents workflows are owned by
  `.pi/skills/running-sweep/SKILL.md` and
  `.pi/skills/measuring-ch-optimizations/SKILL.md`.
- Compliance expected-failure policy remains unchanged.

## Affected areas

- IR model and explain/debug output.
- CBE candidate metadata and rejection reasons.
- Benchmark corpus definitions and sweep artifacts.
- ClickHouse query logging / ProfileEvents correlation.
- Documentation for optimizer evidence standards.

## Requirements

- Every optimization claim must identify the expected signal before measurement:
  storage pruning, fewer function executions, lower memory, fewer round trips,
  smaller transfer, better route choice, or reduced local CPU/memory.
- Explain output must make it possible to connect a PromQL request to:
  - original query family;
  - original IR shape;
  - optimized IR shape, once stage 02 exists;
  - candidate set;
  - selected/reference candidate;
  - ClickHouse SQL and settings profile, when applicable;
  - rejection reasons; and
  - query/log IDs used to find ClickHouse ProfileEvents.
- The IR contract must distinguish semantic facts from physical hints.
- Missing, unavailable, or untrusted evidence must keep optimized serving off.

## Implementation tasks

### 1. Survey external examples as non-authoritative inputs

Use `~/code/external/` as a pattern library before locking down contracts. The
survey should identify useful ideas, not copy architecture wholesale.

- [ ] Review `datafusion` and `calcite` for optimizer pass structure,
  logical/physical plan boundaries, rule preconditions, statistics/cost use, and
  explain output patterns.
- [ ] Review `ClickHouse` for analyzer/planner behavior, query settings,
  ProfileEvents, `TimeSeries` storage, and PromQL endpoint implementation
  details relevant to generated SQL and per-query tuning.
- [ ] Review `prometheus` for canonical PromQL semantics that the IR must model
  and that rewrites must preserve.
- [ ] Review `VictoriaMetrics` for alternate PromQL/MetricsQL execution ideas,
  treating differences from Prometheus as compatibility risks rather than
  defaults.
- [ ] Review `hyperdx` or other ClickHouse-backed observability tools for
  operational/query-shape tuning ideas where relevant.
- [ ] For focused tasks, consider fetching or consulting additional examples
  listed in `README.md`: Promscale, Mimir/Cortex/Thanos/M3, DuckDB, Trino,
  PostgreSQL, CockroachDB, TiDB, TimescaleDB, InfluxDB IOx, Druid, Pinot,
  GreptimeDB, SigNoz, PostHog, Velox, Arrow Acero, Polars, Substrait, and
  ClickBench. These can be inspected locally, via shallow/sparse checkout, or
  remotely through GitHub/MCP/web search.
- [ ] Prefer bounded Go fuzzing, property tests, and curated adversarial corpora
  for robustness. Do not make AFL++ part of routine local validation.
- [ ] Record each adopted pattern with its source, why it fits promshim, what was
  deliberately not adopted, and what validation is required.

### 2. Define the query-family taxonomy

- [ ] Enumerate the initial families to optimize and benchmark:
  - instant vector selectors;
  - simple range selectors;
  - rollups such as `rate`, `increase`, and `*_over_time`;
  - aggregations with `by` and `without`;
  - repeated subexpressions;
  - scalar/vector binary operations;
  - vector/vector binary operations with explicit matching;
  - queries requiring full local/reference behavior.
- [ ] Assign stable bounded family labels suitable for reports and metrics.
- [ ] Identify which families are eligible for early CBE serving and which are
  shadow-only until stronger correctness evidence exists.
- [ ] Add representative corpus entries for small, long-range, dense, and
  high-cardinality cases where coverage is missing.

### 3. Define IR semantic invariants

- [ ] Document what the IR represents independent of execution strategy:
  - value kind: scalar, instant vector, range vector, histogram if applicable;
  - time requirements;
  - lookback/range requirements;
  - label-set production and required labels;
  - grouping keys;
  - vector matching rules;
  - staleness and NaN sensitivity;
  - output cardinality expectations;
  - support/correctness status.
- [ ] Document which annotations are physical hints rather than semantic facts:
  - estimated rows/samples/bytes;
  - candidate route eligibility;
  - preferred execution location;
  - ClickHouse settings profile.
- [ ] Require rewrite passes to declare preconditions, preserved invariants, and
  known non-goals.

### 4. Define explain and artifact contracts

- [ ] Extend or document explain output shape for:
  - original IR;
  - optimized IR;
  - rewrite list and skipped rewrites;
  - candidate list;
  - strict/reference candidate;
  - selected CBE candidate;
  - settings profile and statement settings;
  - ClickHouse query ID/log comment.
- [ ] Ensure sweep reports can preserve strategy/candidate/settings fields for
  before/after comparisons.
- [ ] Define stable rejection-reason enums for support, correctness, estimate,
  cap, confidence, setting availability, and policy gates.

### 5. Inventory ClickHouse tuning surfaces

- [ ] Split settings into categories:
  - server/operator recommendation;
  - user/profile setting;
  - session/query setting promshim may own;
  - SQL-shape alternative rather than setting;
  - unsafe or out-of-scope setting;
  - version-dependent/experimental setting;
  - distributed-only setting.
- [ ] For each candidate setting, record:
  - purpose;
  - scope;
  - default behavior;
  - expected query families;
  - risk;
  - validation signal;
  - whether promshim may set it.
- [ ] Do not enable performance settings in this stage; only create the inventory
  and evidence requirements.

### 6. Establish baseline artifacts

- [ ] Capture a named strict/prefer baseline on sparse 7d data.
- [ ] Capture long-range and dense negative-control baselines where data exists.
- [ ] Preserve memory summaries and ProfileEvents artifacts under named sweep
  directories.
- [ ] Identify current noisy or missing signals before optimizing.

## Validation tasks

Fast checks for any implementation work in this slice:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

Sweep setup and baseline commands:

```bash
./scripts/run-sweep.sh --dry-run --estimate --name post-cbe-opt-contracts-dry-run
./scripts/run-sweep.sh --bench-status

./scripts/run-sweep.sh \
  --name post-cbe-opt-contracts-baseline \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary
```

Artifact checks:

```bash
./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/post-cbe-opt-contracts-baseline/manifest.json \
  --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/post-cbe-opt-contracts-baseline/memory-summary-*.json
```

## Exit criteria

- [ ] Query-family labels are stable and bounded.
- [ ] IR invariants and rewrite responsibilities are documented.
- [ ] Explain/artifact contracts can carry optimized IR, candidate, settings,
  and ClickHouse log correlation data.
- [ ] ClickHouse tuning inventory exists and distinguishes operator guidance
  from shim-owned statement/session settings.
- [ ] At least one named baseline sweep exists or a missing-environment reason
  is documented.
- [ ] No served behavior changed.

## Handoff to next file

After this slice, stage 02 can begin adding conservative IR rewrite passes with
clear preconditions and measurable expected signals. Stage 03 can begin turning
the ClickHouse settings inventory into safe, scoped execution profiles.
