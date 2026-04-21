# 01 — Context and guardrails

## Goal

Add a **planner-selected native SQL lowering path** to the Prometheus shim so that:

- if an entire logical plan is safely lowerable, the shim executes it as **one native ClickHouse SQL query**;
- if the full plan is not lowerable, the planner still lowers the **largest supported subtrees** and falls back only at explicit semantic boundaries;
- generated SQL is optimized enough to be worth using in practice, especially for:
  - pushing label filters to the deepest possible source relations,
  - flattening redundant subqueries / CTE layers,
  - constructing JOINs deliberately.

This is for the current repo architecture, not a greenfield rewrite.

## Why this is the right next step

The repo already has the ingredients needed for this direction:

- logical planning:
  - `internal/promshim/logical_builder.go`
  - `internal/promshim/plan/logicaltypes.go`
- execution strategy selection:
  - `internal/promshim/planner.go`
  - `internal/promshim/aggregation_pushdown.go`
- explainability:
  - `internal/promshim/explain.go`
- seed native SQL execution:
  - `maybeBuildNativeAggregationPlan(...)`
  - `buildNativeAggregationSource(...)`
  - `internal/promshim/storage/sql.go`

The next step is to generalize the current narrow pushdown into:

**logical plan -> native lowering analysis -> SQL fragment optimization -> final strategy selection**

## Preserve / add / non-goals

### Preserve
- existing HTTP API surface
- existing explain endpoints and response envelopes
- existing local evaluator semantics for already-supported features
- existing delegated PromQL path via ClickHouse as fallback and oracle
- existing oversized-query / oversized-response guardrails
- existing error classification boundaries unless intentionally changed

### Add
- native lowering of maximal supported subtrees
- SQL-aware optimization passes
- better explain visibility for why a subtree did or did not lower
- shadow-compare and rollout controls

### Non-goals for the initial implementation
- do **not** replace the local evaluator wholesale
- do **not** silently approximate unsupported PromQL semantics
- do **not** route native-lowered fragments back through `prometheusQuery()` / `prometheusQueryRange()` internally
- do **not** chase full PromQL completeness before the common dashboard subset is fast and stable

## Reference precedence

Use references in this order when there is tension between them:

1. **Prometheus semantics first**
2. **ClickHouse lowering shape second**
3. **VictoriaMetrics / DataFusion / Calcite optimizer structure third**

### Prometheus — semantic oracle
Treat these as correctness authority:

- [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)
- [promql/functions.go](file:///home/fl/code/external/prometheus/promql/functions.go)
- [promql/parser/parse.go](file:///home/fl/code/external/prometheus/promql/parser/parse.go)
- [promql/parser/](file:///home/fl/code/external/prometheus/promql/parser/)

Use for:
- vector matching legality
- duplicate-series failure behavior
- range / subquery timing behavior
- metric-name preservation/drop behavior
- `rate` / `irate` / `increase` / reset semantics
- offset / lookback / staleness behavior

### ClickHouse — lowering-shape oracle
Treat these as primary references for native lowering structure:

- [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp)
- [NodeEvaluationRangeGetter.h](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/NodeEvaluationRangeGetter.h)
- [NodeEvaluationRangeGetter.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/NodeEvaluationRangeGetter.cpp)
- [applyFunctionOverRange.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/applyFunctionOverRange.cpp)
- [applySimpleBinaryOperator.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/applySimpleBinaryOperator.cpp)
- [SQLQueryPiece.h](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/SQLQueryPiece.h)
- [finalizeSQL.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/finalizeSQL.cpp)

Use for:
- selector lowering shape
- evaluation-range propagation
- range-function lowering patterns
- join-group construction and duplicate checks
- internal fragment/result-shape contracts
- late finalization patterns

### VictoriaMetrics / DataFusion / Calcite — optimizer references
Use these for optimizer structure, not for semantic authority:

- [metricsql/optimizer.go](file:///home/fl/code/external/VictoriaMetrics/vendor/github.com/VictoriaMetrics/metricsql/optimizer.go)
- [query-optimizer.md](file:///home/fl/code/external/datafusion/docs/source/library-user-guide/query-optimizer.md)
- [push_down_filter.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs)
- [optimize_projections/mod.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/optimize_projections/mod.rs)
- [projection_pushdown.rs](file:///home/fl/code/external/datafusion/datafusion/physical-optimizer/src/projection_pushdown.rs)
- [analyzer/function_rewrite.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/analyzer/function_rewrite.rs)
- [simplify_exprs.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/simplify_expressions/simplify_exprs.rs)
- [CoreRules.java](file:///home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/CoreRules.java)

## Semantic conformance and rewrite safety

Prometheus remains the correctness oracle. Native lowering is only useful if it preserves PromQL behavior for the supported subset.

### Must cross-check against Prometheus
- vector matching legality and duplicate-series failures
- `rate`, `irate`, `increase`, `delta`, and counter-reset behavior
- lookback / staleness / offset / subquery timing behavior
- metric-name preservation/drop behavior for range functions, binary operators, and aggregations

### Rewrite safety policy
- no native rewrite ships without explicit reasoning against the Prometheus references above
- no native rewrite ships without differential tests against Prometheus itself or the delegated path
- do **not** add blanket string substitutions such as `sum(rate(...)) -> sum(runningDifference(...))`
- instead, rewrite supported patterns into **typed native counter/window primitives** only when reset handling, lookback, range-boundary, and step semantics are modeled

## Current repo starting point

See [00-status-and-drift.md](./00-status-and-drift.md) for the authoritative
current-state snapshot. The summary below is kept in sync with it.

### What exists now
- PromQL parsing and support analysis
- logical-plan construction
- planner-selected execution strategies
- local execution for a growing subset, including range functions
  (`rate`, `irate`, `increase`, `delta`, `*_over_time`, histogram helpers,
  etc.) and subquery step-grid evaluation implemented in
  `internal/promshim/exec/`
- delegated ClickHouse PromQL execution via:
  - `prometheusQuery(...)`
  - `prometheusQueryRange(...)`
- first native SQL pushdown for simple aggregations over delegatable leaves

### Three execution paths exist today
1. **Delegated** — `delegatedExprPlan` in `internal/promshim/planner.go`.
2. **Native SQL** — `nativeAggregationPlan` in
   `internal/promshim/planner.go`, still special-cased for aggregation over
   a delegatable leaf.
3. **Local execution** — `internal/promshim/exec/*.go`, including range
   functions and subqueries that were originally planned for path 2.

The policy going forward (see [00-status-and-drift.md](./00-status-and-drift.md)):

- keep the local implementations as the correctness oracle
- do not add net-new range / counter functions on path 3 unless the
  conformance harness strictly requires it
- native replacements must ship with a differential test against the local
  implementation before the local implementation can be retired

### Current limitation
The current “native” path is still effectively:

- **delegated child expression**
- wrapped by **repo-owned SQL aggregation**

That is useful, but it is not yet a true PromQL-to-SQL lowering track. It cannot deeply optimize:
- selector predicates
- range windows
- subquery shapes
- vector-vector joins
- subquery / CTE inflation

## Distinct tasks in this chunk

1. **Freeze semantic authority and rewrite safety rules**
   - document Prometheus-first semantics and ClickHouse-second lowering shape
   - make this the review policy for future native-lowering PRs

2. **Inventory current repo touchpoints**
   - map the existing planner / explain / SQL entry points that the split plan will evolve

3. **Define the starter differential corpus**
   - pick the first Prometheus-backed comparison queries for selectors, aggregations, joins, counters, and subqueries
