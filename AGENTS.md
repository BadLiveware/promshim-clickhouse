# Project AGENTS (ch-observability)

## Purpose

The repo's original goal was a migration path from Tradera's `example-namespace`
Prometheus+Thanos stack to an OpenTelemetry + ClickHouse stack. That
groundwork landed (see `chart/` and `ROADMAP.md`); the active work has
shifted into `promshim` — a Go service that speaks the Prometheus HTTP API
and executes PromQL against ClickHouse's `TimeSeries` table.

## Execution priority — READ THIS FIRST

Promshim routes every query through the first strategy that can answer
it. **The priority is strict and hierarchical:**

> **1. Whole-query delegation to ClickHouse PromQL**
> **2. Native SQL lowering**
> **3. Local Go executor with subtree pushdown**
>    **a. subtree native-SQL delegation**
>    **b. subtree Prometheus-query delegation**
> **4. Full local execution**

Higher tiers always beat lower tiers. Within tier 3, subtree pushdown
(either variant) always beats leaving a subtree in Go. Full local
(tier 4) is the unconditional fallback — it must stay correct, but
nothing should *prefer* to land there.

### Why this order

Promshim is a bridge, not a destination. As ClickHouse's native PromQL
catches up, tier 1 grows and the tiers below it shrink — ideally until
tier 1 serves everything and the shim is a thin router. Everything
targets the `TimeSeries` schema directly so retiring tiers becomes a
routing change, not a data migration.

### Hard rule: new work lives in tiers 1 and 2 only

> **New work — features, coverage, refactors — is allowed only in**
> **tier 1 (whole-query delegation) and tier 2 (native SQL lowering).**
> **Everything below tier 2 is frozen unless the user explicitly asks.**

Tiers 3 and 4 exist as fallbacks for what tiers 1 and 2 can't yet
serve. They must stay correct, but they should not grow — the goal is
for tiers 1+2 to cover more over time, shrinking what lands in tiers
3+4. Expanding subtree pushdown or the full local executor is the
opposite of that goal.

"Explicitly asks" means a direct instruction in the current conversation
naming the tier, file, or feature. It does **not** include:

- a failing compliance/harness test that happens to live under tier 3/4,
- a user asking "why is X slow/wrong" without naming where to fix it,
- a tempting optimization noticed while reading the code,
- "while I'm here" cleanups.

Correctness bug fixes to tier 3/4 regressions are allowed (they keep
the fallback honest). New coverage, new pushdown shapes, perf work,
and refactors in tiers 3/4 are not. If a fix can live in tier 1 or
tier 2, put it there. If a tier 3/4 change seems unavoidable, stop
and ask.

### Where new work is welcome

- **Tier 1** — `internal/promshim/native/` delegation classifier and
  capability map. Grow safe whole-query delegation as upstream lands
  more PromQL.
- **Tier 2** — `internal/promshim/native/renderer/`,
  `internal/promshim/native/plan*`. Expand native SQL coverage, close
  compliance gaps, improve rendered-SQL performance.
- **Harness & validation** — `harness/`, `scripts/`, `cmd/promshim-*`,
  `cmd/promharness-*`. Keep gaps visible, reproducible, and fast.
- **Chart** — operational/scaling fixes only.

## Chart (`chart/`)

`chart/ch-observability-poc` (plus `chart/ch-observability-cnpg` for the
CNPG-backed variant) renders the HA stack: ClickHouse, OTel operator +
scrape collectors, Grafana, CloudBeaver. Deployment is
`helm template | kubectl apply --server-side` via
`scripts/bootstrap-kind.sh` — not `helm install`. Environment differences
are scaling knobs only (replica counts, resource sizing). SSO, Thanos
backfill, and legacy compatibility are out of scope. OpenTelemetry is
operator-only in this chart; no legacy chart-managed Deployment/Service
fallbacks. The chart is stable; most active edits today live in
`internal/promshim/**` and `harness/**`, not here.

## Promshim (`internal/promshim/`, `cmd/promshim/`)

Promshim drops in where Prometheus used to live. It serves the standard
Prometheus HTTP surface (`/api/v1/query`, `/api/v1/query_range`,
`/metrics`) plus explain-only debug endpoints (`/api/v1/query_explain`,
`/api/v1/query_range_explain`, and `explain=1` on the normal endpoints).
Every request is parsed with the upstream Prometheus parser, planned
into a logical tree, and routed through the execution-priority tiers
above.

### Rollout modes

Controlled globally via `PROM_SHIM_NATIVE_LOWERING_MODE` and per-request
via `native_lowering_mode=...`:

- `off` — tier 4 only.
- `prefer` — normal adaptive: walk tiers 1→4, pick the first that fits.
- `explain` — same planning as `prefer`, but always emits the explain plan.
- `shadow` — serves tier 4, runs tiers 1/2 in the background, records
  divergences in process-local metrics.
- `force_supported` — hard fails unless the final root plan is
  `native_sql`; used in the native-only compliance pass to keep
  coverage gaps visible.

### Validation harness (`harness/`, `scripts/`)

- `scripts/run-compliance.sh` — two-pass upstream `prometheus/compliance`
  run: pass #1 in `prefer` mode (allowlist-gated against
  `harness/compliance/expected-failures.json`, which holds only
  reference-side quirks like the `topk` tie-break); pass #2 in
  `force_supported` (informational gap report, never gated). Full run
  ~15s warm, ~60s cold.
- `scripts/run-bench.sh` — native-SQL tripwire benchmark against the
  frozen fixture (Prometheus :29090, promshim :29091). `--matrix` prints
  a Markdown Native-vs-Prom matrix sorted by N/P ratio. `--baseline` +
  `--update-baseline` maintain `harness/bench/baseline.json`.
- `scripts/run-harness.sh` — differential harness against custom query
  corpora in `harness/corpus/`.
- `cmd/promshim-bench`, `cmd/promshim-matrix`,
  `cmd/promshim-promql-compliance`, `cmd/promharness-compare`,
  `cmd/promharness-seed` — Go drivers behind the above scripts.

### Working in this area

- Cross-check upstream `prometheus/compliance` and the Prom parser when
  changing planner/delegation semantics; reproduce divergences in the
  harness first, fix second.
- Any shim coverage gap is a visible failure, not an allowlist entry.
  `harness/compliance/expected-failures.json` is reserved for **allowed
  deviances only** — cases where matching Prometheus exactly is either
  impossible or not worth it:
  - **Impossible-to-replicate reference-side behavior.** The current
    entry is `topk-tie-break-ordering`: Prom's tie-break is decided by
    TSDB postings/scrape-discovery order, not by labels, so reproducing
    it would mean mirroring Prom's storage layer. Each entry must match
    a specific query and diff shape so unrelated drift surfaces as a
    regression.
  - **Fundamental ClickHouse vs Prometheus primitive differences with
    negligible numeric impact.** Listed under `tolerances` with a bounded
    float margin. The current entry is `native-modulo-small-float-drift`:
    ClickHouse's native modulo uses `x - trunc(x/y)*y` instead of
    Go/Prom's `math.Mod`, producing sub-1e-6 drift on large operands;
    labels and timestamps must still match exactly.
  - **Small deviances that significantly simplify or speed up the
    native SQL path.** Allowed only by explicit user approval. The
    agent must stop, describe the deviance (exact shape, expected
    magnitude, which queries it affects), quantify the simplification
    or speedup it unlocks, and explain why a compliant alternative is
    infeasible. Do not preemptively add this kind of entry; always ask.

  Anything that isn't one of these three — a shim bug, a missing
  feature, a planner error — stays a visible failure. Don't expand the
  allowlist to make the compliance run green.
- The harness runs fast — do not wrap these scripts in minutes-long
  timeouts.

## External references (`~/code/external/`)

Useful upstream sources when evolving the shim. Read first, reinvent second.

- **`prometheus`** — canonical PromQL parser/engine/TSDB
  (`promql/parser`, `promql/engine.go`, `promql/functions.go`). Tier 4
  semantics must match this; the compliance suite is derived from it.
- **`ClickHouse`** — storage engine + the PromQL endpoint we delegate
  to at tier 1 (`src/Functions/PromQL*`, `src/Storages/TimeSeries*`).
- **`VictoriaMetrics`** — second-source PromQL (MetricsQL) in
  `app/vmselect/promql/`; useful for tie-breaking edge cases.
- **`datafusion`** — Rust logical/physical plan split and rule-based
  optimization patterns; reference for tier-2 plan rewrites.
- **`calcite`** — canonical SQL relational algebra / optimizer rules;
  deepest reference for non-trivial planner transforms.
- **`hyperdx`** — ClickHouse-backed observability product; product-side
  comparator for dashboard expectations.

## Must-nots / constraints

- SSO is out of scope.
- Read-only agent querying uses the ClickHouse MCP server.
- Metric backfill from Thanos is desirable but not required for V1.
- Do not maintain compatibility with older versions of this PoC itself —
  recreate instances from scratch if the stack shape changes.
- We use the first-party ClickHouse operator
  (`https://github.com/ClickHouse/clickhouse-operator`), **not** the
  Altinity operator. Web search results for "clickhouse operator" mostly
  return Altinity docs; filter to the first-party repo or docs.

## Where to look first

- `README.md` — local usage, shim endpoints, PromQL support matrix.
- `internal/promshim/` — the service itself.
- `harness/README.md`, `harness/compliance/README.md` — validation flow
  and the "gaps stay visible" policy.
- `ROADMAP.md` — the original migration phase plan.
- `chart/ch-observability-poc/README.md` — Helm template/apply flow.
