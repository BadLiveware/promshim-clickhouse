# promshim-clickhouse

## Purpose

`promshim` is a Prometheus HTTP API compatibility layer for metrics stored
in ClickHouse's experimental `TimeSeries` table engine. It parses PromQL
with the upstream Prometheus parser and executes it against ClickHouse via
a hierarchical execution strategy.

## Playbooks — consult the matching skill before acting

Three project playbooks live at `.pi/skills/` (also `.claude/skills/` via
symlink). When a trigger below fires you **must read the matching `SKILL.md`
before running commands or making a call** — each one encodes discipline
(signal-vs-noise rules, allowlist policy, stack-isolation gotchas) you
cannot derive from this file or the diff alone.

| Trigger | Playbook |
|---|---|
| Evaluating any commit/PR with a perf, CSE, alias, pushdown, or scan-reduction claim — *especially* small (<5%) wall-clock deltas, matrix bench green, or `strategy_used` changed | [`measuring-ch-optimizations`](.pi/skills/measuring-ch-optimizations/SKILL.md) |
| Running the compliance suite, triaging a failure, the native-mode gap report, or considering **any** edit to `harness/compliance/expected-failures.json` | [`running-compliance`](.pi/skills/running-compliance/SKILL.md) |
| Setting up or running benchmarks, seeding long-range/dense data, before/after measurement, or comparing profiles/densities/transports/modes | [`running-sweep`](.pi/skills/running-sweep/SKILL.md) |

A 3% wall-clock delta is not evidence on its own; an `expected-failures.json`
entry for a shim bug is forbidden; long-range benchmarks must not run
against the compliance ports. The playbooks tell you why and what to do
instead. **Read them when the trigger fires, not after a reviewer asks.**

## Execution priority

Promshim routes every query through the first strategy that can answer it:

> **1.** Whole-query delegation to ClickHouse PromQL
> **2.** Native SQL lowering
> **3.** Local Go executor with subtree pushdown (native-SQL or Prometheus-query subtree)
> **4.** Full local execution

Higher tiers define capability priority, but cost-based execution (CBE) may
route among tiers 2, 3, and 4 as real execution candidates when more than one
candidate is known-correct for the query. Native SQL is not automatically
preferred for small data sets when measured costs show local execution or
subtree-pushdown is faster. Tier 1 whole-query delegation remains the preferred
ClickHouse-native endpoint when it can serve the query correctly, but below that
CBE may choose the cheapest safe candidate across native SQL lowering, local Go
execution with subtree pushdown, and full local execution. Everything targets
the `TimeSeries` schema directly so retiring tiers is a routing change, not a
data migration.

### CBE rule: tiers 3 and 4 are routing candidates again

> CBE work may improve, instrument, and iterate on tiers 2, 3, and 4 as routing
> candidates. The goal is to choose the cheapest safe strategy for the current
> query shape and data size, not to prefer native SQL unconditionally.

Correctness remains mandatory: any tier considered by CBE must be known-correct
for the query, must preserve Prometheus semantics, and must stay visible in
compliance/differential validation. Tier 3/4 work is allowed when it supports CBE
routing quality, safety caps, observability, or performance for already-supported
semantics. Do not add unrelated lower-tier feature coverage opportunistically;
new semantic coverage should still be justified by the CBE plan, a correctness
bug, or an explicit user request.

CBE decisions must be explainable and reversible. Missing estimates, uncertain
costs, over-cap inputs, known divergences, or absent validation must choose the
safe/reference route. Use named `run-sweep` artifacts and ProfileEvents evidence
when claiming that a lower tier is faster, especially for small-data cases where
local execution often beats native SQL.

**Where new work is welcome:** tier 1 in `internal/promshim/native/`
delegation classifier; tier 2 in `internal/promshim/native/renderer/` and
`plan*`; tier 3/4 candidate planning, local execution, subtree pushdown,
cost-model plumbing, hard caps, and observability when tied to CBE;
harness/validation in `harness/`, `scripts/`, `cmd/promshim-*`,
`cmd/promharness-*`.

### Native SQL builder evolution

Grow `internal/promshim/native/sqlb/` as a typed ClickHouse SQL subset, not as a
full SQL parser or a big-bang renderer rewrite. New native SQL physical shapes
should add typed `sqlb` expressions, predicates, sources, or ClickHouse helper
functions exactly where needed, while retaining raw escape hatches for legacy or
unsupported syntax.

When changing native SQL lowering:

- Represent semantic and physical choices in plan structs first (strategy,
  predicate placement, stale-marker placement, matched-series distinctness,
  join/aggregation shape, execution settings), then render through `sqlb`.
- Prefer typed `sqlb` nodes/helpers for new optimization logic; keep raw SQL at
  compatibility edges and make it visible in review.
- Measure optimizer claims with named benchmark/profile artifacts and
  ClickHouse `ProfileEvents`, not p50 alone. If a query shape has measured
  execution-setting behavior, encode it through typed physical preferences
  (`RenderParams.Physical`, including set/no-cap execution hints) at the point
  in planning where the whole-query shape is known.
- Migrate path-by-path as optimization work touches code. Do not pause feature
  or performance work for a repository-wide SQL-builder migration.
- Preserve rendered SQL/goldens unless the commit intentionally changes the
  physical shape; when it does, validate correctness and the claimed runtime
  signal with the project benchmark/compliance playbooks.
- Let this incremental subset evolve toward a fuller typed renderer only if the
  codebase keeps needing those constructs.

## Promshim service

Drops in where Prometheus used to live. Serves `/api/v1/query`,
`/api/v1/query_range`, `/metrics`, plus explain endpoints
(`/api/v1/query_explain`, `/api/v1/query_range_explain`, `explain=1` on
the normal endpoints). Every request goes through the upstream Prometheus
parser, then the priority tiers above.

### Rollout modes

`PROM_SHIM_NATIVE_LOWERING_MODE` (or per-request `native_lowering_mode=`):

- `off` — tier 4 only.
- `prefer` — adaptive: walk tiers 1→4, pick the first that fits.
- `explain` — `prefer` planning + always emit explain plan.
- `shadow` — serves tier 4, runs tiers 1/2 in background, records divergences.
- `force_supported` — hard fails unless the root plan is `native_sql`; the
  native-only compliance pass uses this to keep gaps visible.

### Validation harness

> The `running-sweep` and `running-compliance` playbooks own the workflows
> here. `scripts/run-sweep.sh` is the primary entry point and uses an
> isolated benchmark stack; running `run-bench.sh --long-range` against
> compliance ports is a known gotcha. The harness runs fast — do not wrap
> these scripts in minute-long timeouts.

Top-level scripts (full flags via `--help`):

- `run-sweep.sh` — combined compliance + benchmark on isolated stack.
- `run-compliance.sh` — two-pass compliance (`prefer` gated against
  `expected-failures.json`, `force_supported` informational).
- `run-bench.sh` — native-SQL tripwire bench against frozen fixture
  (Prom :29090, promshim :29091).
- `seed-long-range.sh`, `bench-matrix.sh`, `run-harness.sh` — supporting tools.
- Go drivers: `cmd/promshim-bench`, `cmd/promshim-matrix`,
  `cmd/promshim-promql-compliance`, `cmd/promharness-compare`,
  `cmd/promharness-seed`.

Cross-check upstream `prometheus/compliance` and the Prom parser when
changing planner/delegation semantics; reproduce divergences in the
harness first, fix second.

### `expected-failures.json` policy

Full rules in the `running-compliance` playbook. Reserved for **allowed
deviances only** — three valid categories:

1. Impossible-to-replicate reference-side behavior (e.g.
   `topk-tie-break-ordering`: Prom's tie-break depends on TSDB postings
   order).
2. Fundamental CH-vs-Prom primitive differences with bounded numeric
   impact (`tolerances[]`, e.g. `native-modulo-small-float-drift`).
3. Small deviances that significantly simplify or speed up the native
   SQL path — **explicit user approval required in the current
   conversation; never preemptively**.

Anything else — a shim bug, a missing feature, a planner error — stays a
visible failure. Do not expand the allowlist to make compliance green.

## External references

Read first, reinvent second. Browse upstream or clone locally as you prefer.

- **Prometheus** (`github.com/prometheus/prometheus`) — canonical
  parser/engine/TSDB (`promql/parser`, `promql/engine.go`,
  `promql/functions.go`). Tier 4 semantics must match this; the compliance
  suite derives from it.
- **ClickHouse** (`github.com/ClickHouse/ClickHouse`) — storage + the
  tier-1 PromQL endpoint (`src/Functions/PromQL*`, `src/Storages/TimeSeries*`).
- **VictoriaMetrics** (`github.com/VictoriaMetrics/VictoriaMetrics`) —
  second-source PromQL (MetricsQL) in `app/vmselect/promql/`; useful for
  tie-breaking edges.
- **DataFusion** (`github.com/apache/datafusion`) — logical/physical plan
  split + rule-based optimization patterns; reference for tier-2 plan
  rewrites.
- **Calcite** (`github.com/apache/calcite`) — canonical SQL relational
  algebra / optimizer rules.

## Must-nots / constraints

- SSO is out of scope.
- Read-only agent querying uses the ClickHouse MCP server.
- First-party ClickHouse operator
  (`github.com/ClickHouse/clickhouse-operator`), **not** Altinity. Web
  search for "clickhouse operator" returns mostly Altinity — filter to
  the first-party repo or docs.

## Where to look first

- `.pi/skills/` — the three required playbooks (see Playbooks table).
- `README.md` — local usage, shim endpoints, PromQL support matrix.
- `internal/promshim/` — the service.
- `harness/README.md`, `harness/compliance/README.md` — validation flow
  and the "gaps stay visible" policy.
