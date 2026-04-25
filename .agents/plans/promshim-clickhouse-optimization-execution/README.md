# Promshim ClickHouse optimization execution plan

## Purpose

This plan supersedes the earlier post-CBE optimization plan for the next round of
work. The earlier branch established contracts, metadata, rollback gates,
settings-profile plumbing, log correlation, and one conservative SQL-shape
optimization. This plan is for actually running optimization experiments and
landing measured changes.

The goal is to produce evidence-backed improvements for promshim over ClickHouse
`TimeSeries` data across five work areas:

1. ClickHouse deployment tuning for this workload, applied to this repository's
   benchmark setup and documented for users as operator guidance.
2. Promshim-owned ClickHouse query/session tuning that promshim can safely apply
   per request or per connection profile.
3. Native SQL and IR optimizations that reduce ClickHouse work or transfer.
4. CBE tuning that chooses tier 2 versus tier 3/4 based on measured cost.
5. Tier 3/4 local and subtree-pushdown optimizations for families where they are
   cheaper than native SQL.

## Current foundation already in the branch

The branch already provides the scaffolding this plan should use instead of
rebuilding:

- `docs/optimizer-contracts.md`: family taxonomy, invariants, evidence standards,
  explain/artifact contracts, rejection reasons.
- `docs/clickhouse-tuning-inventory.md`: ClickHouse setting categories and
  shim-owned allowlist boundaries.
- `docs/clickhouse-reference-profile.md`: operator-facing ClickHouse reference
  profile guidance.
- `docs/optimization-rollout.md`: rollout, calibration, regression, rollback, and
  review guidance.
- Logical optimizer tracing and rollback via `PROM_SHIM_DISABLE_OPTIMIZED_IR`.
- ClickHouse settings profile plumbing and provenance via
  `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE` and `X-Promshim-Settings-Profile`.
- Bounded ClickHouse log comments for `system.query_log` correlation.
- Aggregation label projection rollback via
  `PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION`.
- Benchmark artifact capture of settings profile and routing/candidate metadata.

## Execution order

1. [`01-benchmark-foundation.md`](01-benchmark-foundation.md)
2. [`02-clickhouse-deployment-tuning.md`](02-clickhouse-deployment-tuning.md)
3. [`03-promshim-session-tuning.md`](03-promshim-session-tuning.md)
4. [`04-native-sql-ir-optimizations.md`](04-native-sql-ir-optimizations.md)
5. [`05-cbe-tier-selection.md`](05-cbe-tier-selection.md)
6. [`06-tier-3-4-optimizations.md`](06-tier-3-4-optimizations.md)

## Dependency graph

```text
01 benchmark foundation
  -> 02 ClickHouse deployment tuning
  -> 03 promshim session/query tuning
  -> 04 native SQL / IR optimization
  -> 05 CBE tier selection
  -> 06 tier 3/4 optimization
```

The order is intentional. Deployment and settings experiments must be measured
before CBE calibration, and tier 3/4 work must know where tier 2 is already
cheap or still expensive.

## Hard constraints

- Do not expand `harness/compliance/expected-failures.json` for shim bugs.
- Do not run long-range or dense benchmarks against compliance ports.
- Do not present ClickHouse server/operator tuning as a promshim correctness
  dependency.
- Do not enable result-query cache by default for PromQL paths.
- Do not claim wins from wall-clock deltas alone. Every claim must include a
  named expected signal and matching evidence from EXPLAIN, ProfileEvents,
  query-log counters, transfer/round-trip metrics, or Go profiles.
- Do not make native SQL unconditionally preferred below whole-query delegation;
  CBE may choose tier 2, 3, or 4 when a candidate is known-correct and cheaper.
- Keep rollback configuration-first for every served behavior change.
- Keep produced product docs domain-facing. Do not mention internal execution
  files, iteration names, or this plan in README/docs content.

## Cross-cutting artifact standards

Every experiment must write artifacts under a named directory and record:

- git revision and dirty diff summary;
- ClickHouse version and transport;
- benchmark profile, density, corpus, and seeded data status;
- ClickHouse reference profile name or explicit deviation;
- promshim settings profile and concrete applied/skipped settings;
- routing policy, strict/selected/served candidate, and family label;
- request log comment or query ID used for ClickHouse correlation;
- EXPLAIN SYNTAX, PLAN, and PIPELINE for SQL-shape claims;
- `system.query_log` rows with duration, read rows/bytes, memory, Settings, and
  ProfileEvents;
- memory summary with zero missing log comments; and
- negative-control results where route cliffs, dense data, long ranges, or
  freshness could change the conclusion.

## Cross-cutting validation commands

Routine checks after code changes:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
go test ./cmd/promshim-routing-calibrate/...
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
git diff --check
```

Sweep helpers:

```bash
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --dry-run --estimate --name promshim-optimization-dry-run
./scripts/bench-matrix.sh --sweep <manifest.json> --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' <memory-summary.json>
```

## Final acceptance criteria

The work is complete when:

- The repository benchmark setup includes a measured ClickHouse reference profile
  for promshim workloads, and docs explain how users can apply it themselves.
- At least one promshim-owned query/session settings profile is promoted from
  provenance-only to applied performance tuning with version checks, rollback,
  explain output, and measured evidence, or each tested profile is explicitly
  rejected with evidence.
- At least one native SQL/IR optimization produces executor-visible improvement
  beyond cosmetic SQL changes, or the highest-priority candidates are rejected
  with EXPLAIN/ProfileEvents proof that ClickHouse already normalizes them.
- CBE calibration is regenerated from named artifacts and encodes when tier 2,
  tier 3, or tier 4 should serve for measured families.
- At least one tier 3/4 optimization is implemented or rejected with enough
  profiling evidence to explain why tier 2 remains preferable.
- Compliance remains clean in prefer mode except existing allowed deviances, and
  native-only gaps remain visible.
- README/docs describe the actual tuning and rollout guidance without implying
  hidden ClickHouse server requirements.

## Recommended execution mode

Use `execute-long-plan` for this directory. Each numbered file is a coherent
execution slice and should be completed before moving to the next unless a
measured blocker forces reprioritization.
