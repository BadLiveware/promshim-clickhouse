# Post-CBE optimization plan

## Purpose

Restart optimization work now that promshim has two important foundations:

1. CBE can treat tiers 2, 3, and 4 as runtime candidates when they are
   known-correct for the query.
2. Tier 2 PromQL lowering now flows through a generalized IR that can support
   semantic normalization, logical rewrites, and plan-shape optimization before
   SQL rendering.

The desired end state is not "make native SQL win everything." It is a
measured optimizer loop where promshim:

- represents PromQL semantics explicitly in IR;
- applies conservative, testable IR rewrites;
- generates one or more safe physical candidates;
- selects the cheapest safe route with CBE;
- applies shim-owned ClickHouse query/session settings when appropriate;
- documents the ClickHouse server profile we recommend for this workload; and
- validates optimization claims with sweep artifacts, ClickHouse ProfileEvents,
  and compliance/differential evidence.

## Optimization tracks

This plan separates three related but distinct tracks:

1. **IR and plan-shape optimization**
   - PromQL AST to semantic IR.
   - IR normalization and logical rewrites.
   - Candidate generation and SQL/local/hybrid plan shapes.
2. **Shim-owned ClickHouse execution tuning**
   - Per-query/session settings applied only to promshim's ClickHouse requests.
   - Safety caps, query IDs, traceability, and later measured performance
     profiles.
3. **Reference ClickHouse deployment profile**
   - Operator-facing recommendations for running ClickHouse primarily for
     promshim/PromQL workloads.
   - Advisory defaults and benchmark assumptions, not hidden promshim
     correctness dependencies.

## Research-backed priorities

The initial research pass suggests a practical order of attack for this plan:

1. **Read less data first**
   - exact selector time-bound derivation;
   - matcher normalization;
   - projection/label pruning;
   - safe aggregation pushdown for simple families.
2. **Treat reuse as real only if ClickHouse does less work**
   - repeated-selector and subtree reuse should be judged by `EXPLAIN` and
     ProfileEvents evidence, not shorter SQL text.
3. **Keep storage/layout optimizations explicit**
   - ordering keys, projections, materialized views, and cache choices are
     powerful, but should usually live in the reference deployment profile
     unless promshim explicitly chooses to depend on them.
4. **Keep vector matching conservative**
   - high-risk binary/vector matching work should stay shadow-first until IR
     semantics, caps, and differential evidence are strong.

These priorities come from the local evidence discipline plus external examples
from official ClickHouse docs/engineering material, Prometheus operator docs,
and optimizer patterns from DataFusion and Calcite. They refine the execution
order below; they do not replace the hard constraints in this plan.

## Execution order

1. [`01-optimization-evidence-and-contracts.md`](01-optimization-evidence-and-contracts.md)
   - Establish the measurement loop, explain contracts, query-family corpus,
     IR invariants, and ClickHouse tuning inventory before adding rewrites.
2. [`02-ir-logical-optimizer.md`](02-ir-logical-optimizer.md)
   - Add conservative semantic IR normalization and low-risk logical rewrite
     passes that feed candidate generation and CBE.
3. [`03-shim-clickhouse-execution-profiles.md`](03-shim-clickhouse-execution-profiles.md)
   - Introduce version-aware, explainable, shim-owned ClickHouse settings:
     safety first, then measured query-shape profiles.
4. [`04-query-family-optimization.md`](04-query-family-optimization.md)
   - Optimize specific query families using IR rewrites, SQL shape changes,
     pushdown/local candidates, and CBE evidence.
   - Includes a ranked implementation queue: build next / build later / avoid
     for now.
5. [`05-reference-clickhouse-deployment-profile.md`](05-reference-clickhouse-deployment-profile.md)
   - Document the recommended ClickHouse server/operator profile and the
     assumptions behind promshim benchmark results.
6. [`06-rollout-calibration-and-maintenance.md`](06-rollout-calibration-and-maintenance.md)
   - Roll optimizations out behind gates, calibrate CBE from named sweep
     artifacts, and keep evidence, docs, and rollback paths current.

## Dependency graph

```text
01 evidence + contracts
  -> 02 IR logical optimizer
      -> 04 query-family optimization

01 evidence + contracts
  -> 03 shim ClickHouse execution profiles
      -> 04 query-family optimization

01 evidence + contracts
  -> 05 reference ClickHouse deployment profile

02/03/04/05
  -> 06 rollout, calibration, and maintenance
```

The repository should remain useful after each numbered file. Do not skip from
IR rewrites or ClickHouse setting experiments directly to default serving
behavior without shadow/cost evidence and rollback controls.

## Hard constraints

- Whole-query delegation to ClickHouse PromQL remains the preferred tier-1 path
  when it can serve the query correctly.
- Below tier 1, CBE may choose among native SQL, subtree pushdown, and local
  execution only when candidates are known-correct for the query.
- Optimization work must not add unrelated lower-tier semantic coverage
  opportunistically. New semantic coverage needs a correctness bug, CBE-driven
  justification, or explicit user request.
- Missing estimates, uncertain costs, hard-cap failures, unsupported settings,
  known divergences, stale calibration, and absent validation choose the safe or
  reference route.
- Do not add compliance allowlist entries for optimization work.
- Do not rely on global ClickHouse server tuning as a hidden correctness or
  performance dependency.
- Promshim-owned ClickHouse settings must be allowlisted, version-aware,
  explainable, and scoped to promshim sessions/statements.
- Raw PromQL, matchers, labels, tenant data, and high-cardinality values must not
  become metric labels.
- Long-range/dense benchmark data must use the isolated benchmark stack via
  `./scripts/run-sweep.sh`, never the compliance ports.
- A small wall-clock p50 delta is not enough to accept an optimization claim.
  Use ProfileEvents, explain output, strategy/candidate fields, and sweep
  artifacts.

## Non-goals

- Do not replace CBE with a native-SQL-first policy.
- Do not build a black-box or ML cost model before transparent rules and
  measured thresholds are exhausted.
- Do not tune ClickHouse globally from promshim.
- Do not require operators to exactly match the reference deployment profile for
  correctness.
- Do not merge high-risk PromQL rewrites, especially around vector matching,
  staleness, histograms, NaN behavior, or extrapolation, without focused
  semantic tests and differential evidence.

## Source evidence, examples, and workflows to reuse

Start with official, directly relevant sources before secondary examples:

- ClickHouse docs: PREWHERE, EXPLAIN, projections, query condition cache,
  time-series query performance, and `prometheusQuery`.
- ClickHouse engineering material on query optimization and storage/layout.
- Prometheus operator and vector-matching docs for semantic guardrails.
- Local playbooks in `.pi/skills/` for measurement/compliance discipline.

Use external repos under `~/code/external/` as pattern libraries after the
official sources clarify what is actually available in ClickHouse and what
PromQL semantics must preserve.

- [`../cbe-candidate-routing-plan/`](../cbe-candidate-routing-plan/) defines the
  current CBE candidate model and rollout discipline.
- [`../cost-based-routing-plan/`](../cost-based-routing-plan/) captures the
  earlier strict/shadow/prefer routing groundwork.
- `.pi/skills/running-sweep/SKILL.md` owns benchmark/compliance sweep workflow,
  isolated stack usage, artifact interpretation, and memory/ProfileEvents
  collection.
- `.pi/skills/measuring-ch-optimizations/SKILL.md` defines how optimization
  claims must be verified: strategy flips, `SelectedRows`, `SelectedBytes`,
  `ReadCompressedBytes`, `FunctionExecute`, `MemoryTrackerUsage`, EXPLAIN
  output, and ProfileEvents matter more than noisy p50 alone.
- `.pi/skills/running-compliance/SKILL.md` owns compliance triage and the
  expected-failures policy.

External repos under `~/code/external/` are useful examples to learn from, but
must not be treated as exhaustive or authoritative for promshim. Use them as
pattern libraries and second opinions, then adapt only the parts that fit
PromQL semantics, ClickHouse `TimeSeries`, and promshim's CBE contract.

Initial local example map:

- `~/code/external/datafusion` — logical/physical plan split, optimizer rules,
  projection/filter pushdown, statistics-driven planning, explainability.
- `~/code/external/calcite` — rule-based relational optimization, trait/cost
  modeling, planner rule organization, adapter-specific pushdown boundaries.
- `~/code/external/ClickHouse` — native analyzer/planner behavior, settings,
  ProfileEvents, `TimeSeries` storage, PromQL endpoint implementation, EXPLAIN
  behavior.
- `~/code/external/prometheus` — canonical PromQL parser/engine semantics,
  staleness, extrapolation, vector matching, histogram behavior, compliance
  expectations.
- `~/code/external/VictoriaMetrics` — alternate PromQL/MetricsQL execution and
  optimization ideas, useful for comparison but not canonical for compatibility.
- `~/code/external/hyperdx` — ClickHouse-backed observability product patterns,
  operational/query-shape examples, and deployment tuning ideas.
- Other repos under `~/code/external/` may provide useful supporting patterns
  for benchmarking, CLI workflows, or operational docs. Add them to the plan
  only when a concrete optimization task benefits from them.

Additional open-source examples worth consulting for focused tasks. They may be
cloned locally, fetched with a shallow/sparse checkout, or inspected remotely via
GitHub/MCP/web search; they do not need to already exist under
`~/code/external/`.

| promshim work area | Examples to learn from | What to look for |
|---|---|---|
| PromQL-to-SQL lowering | Promscale, TimescaleDB, ClickHouse PromQL endpoint | matcher lowering, schema assumptions, SQL pushdown boundaries, semantic fallbacks |
| PromQL correctness and scale | Mimir, Cortex, Thanos, M3 | query limits, query splitting, cardinality protection, frontend routing, compatibility tests |
| IR optimizer mechanics | DuckDB, Spark Catalyst, Polars, Substrait | pass organization, expression simplification, projection/filter pushdown, explain output |
| Cost-based planning | Trino, PostgreSQL, CockroachDB, TiDB | transparent costing, missing-statistics fallback, session/query settings, plan traces |
| Time-series pruning | InfluxDB IOx/InfluxDB 3, TimescaleDB, Druid, Pinot, GreptimeDB | time predicate pushdown, segment/chunk pruning, retention/partition assumptions |
| ClickHouse operations | SigNoz, PostHog, HyperDX, ClickHouse Grafana plugin | deployment defaults, query-shape patterns, dashboard workload tuning, operational limits |
| Local/vectorized execution | DuckDB, Velox, Arrow Acero, Polars | batch execution, memory accounting, expression reuse, operator stats |
| Benchmark methodology | ClickBench, DuckDB/Trino/DataFusion benchmark harnesses | reproducible artifacts, environment disclosure, explain/profile comparisons |
| Bounded robustness testing | Go native fuzzing, property tests, curated adversarial corpora | parser/lowering panics, rewrite equivalence, deterministic edge-case coverage |

Do not include AFL++ in routine validation for this repo. It can monopolize local
resources and lock up developer machines. If fuzzing is useful, prefer bounded Go
native fuzzing, property tests, and curated adversarial corpora with strict time
and parallelism limits.

Keep external-example research bounded: consult the 1-3 most relevant projects
for the current optimization area unless a specific blocker requires deeper
research. Record the source repo/path or URL, borrowed idea, rejected parts,
PromQL/ClickHouse risks, and validation evidence.

## Cross-cutting validation strategy

Fast inner-loop validation for implementation slices:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

Sweep preview and stack-state checks:

```bash
./scripts/run-sweep.sh --dry-run --estimate --name post-cbe-opt-dry-run
./scripts/run-sweep.sh --bench-status
```

Named sparse baseline before performance claims:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-opt-baseline-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary
```

Negative controls for query families that may cliff on larger or denser data:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-opt-negative-long-range \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-opt-negative-dense-processing \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set processing \
  --memory summary
```

Artifact inspection:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run>/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/<run>/memory-summary-*.json
```

Use focused ClickHouse explain/profile tools for specific optimization claims
when the sweep points to a query-family candidate.

## Cross-cutting risks and mitigations

| Risk | Mitigation |
|---|---|
| IR rewrites accidentally change PromQL semantics | Encode IR invariants, rewrite preconditions, differential tests, and explain rewrite traces before serving |
| Local or hybrid wins on sparse fixtures but cliffs on dense/long-range data | Hard caps, negative-control sweeps, missing/stale estimate fallback, family gates |
| ClickHouse settings improve one shape and harm another | Named settings profiles, per-family validation, version gates, explain output, default-off rollout |
| Server tuning becomes an implicit requirement | Separate advisory deployment docs from shim-owned settings; benchmark reports name the reference profile |
| SQL text changes are cosmetic after ClickHouse rewrites | Require EXPLAIN SYNTAX/PLAN/ProfileEvents evidence for SQL-shape claims |
| CBE calibration goes stale after IR or SQL changes | Regenerate from named sweep manifests and reject cost-prefer on stale calibration |
| Optimization masks correctness regressions | Preserve `force_supported`, compliance sweeps, differential CBE/shadow evidence, and no new allowlist entries |

## Rollback principles

Rollback must be configuration-first wherever possible:

- disable CBE serving and return to strict/reference routing;
- disable a query family gate;
- disable a named ClickHouse settings profile;
- fall back from optimized IR to unoptimized IR for affected families;
- remove per-request overrides from dashboards/tests; and
- rerun named sweeps to confirm restored behavior.

## Final acceptance criteria

This plan is complete when:

- promshim has a documented IR optimization contract and explainable rewrite
  trace;
- low-risk IR rewrites improve or preserve measured work for targeted families;
- CBE sees candidate costs, safety gates, and ClickHouse settings profile
  choices in explain output;
- shim-owned ClickHouse settings start with safety/traceability and only add
  performance knobs after measured evidence;
- operator-facing ClickHouse deployment recommendations are documented as
  advisory reference-profile guidance;
- each accepted optimization claim is backed by named sweep artifacts and
  ProfileEvents/EXPLAIN signals appropriate to the claim;
- compliance remains clean without adding allowlist entries; and
- rollback is possible through config or family/profile gates without code
  removal.

## Handoff

This is a planning-only artifact. When implementation starts, use
`execute-long-plan` and work through the numbered files in order unless the user
explicitly reprioritizes a specific file.
