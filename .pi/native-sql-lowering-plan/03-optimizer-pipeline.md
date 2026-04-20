# 03 — Optimizer pipeline

## Optimizer structure

This should be a **small staged rule-based optimizer**, not a general-purpose cost-based or Volcano-style planner.

Reference patterns:
- [query-optimizer.md](file:///home/fl/code/external/datafusion/docs/source/library-user-guide/query-optimizer.md)
- [push_down_filter.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs)
- [optimize_projections/mod.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/optimize_projections/mod.rs)
- [analyzer/function_rewrite.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/analyzer/function_rewrite.rs)
- [CoreRules.java](file:///home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/CoreRules.java)
- [SQLQueryPiece.h](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/SQLQueryPiece.h)
- [finalizeSQL.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/finalizeSQL.cpp)

The optimizer should run in three layers:

1. **Pre-lowering logical rewrites**
   - operate on PromQL/logical-plan nodes
   - improve lowerability and selector selectivity before SQL exists
2. **Fragment / relational rewrites**
   - operate on `NativeFragment` IR
   - push predicates/projections, normalize joins, flatten wrappers
3. **Final SQL shaping**
   - render minimal SQL / AST
   - late-materialize tags and matrix output

## Fixed pass order for v1

1. trivial expression normalization
2. evaluation-range propagation
3. common matcher inference
4. label predicate pushdown
5. required-column / projection pushdown
6. function / pattern rewrites
7. join normalization and duplicate detection
8. flatten redundant wrappers
9. final SQL shaping and late materialization

Keep this as **RBO only**, with at most one or two fixed-point iterations for the small subset of interacting passes.

## Passes

### Pass 0 — Evaluation-range propagation
Primary references:
- [NodeEvaluationRangeGetter.h](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/NodeEvaluationRangeGetter.h)
- [NodeEvaluationRangeGetter.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/NodeEvaluationRangeGetter.cpp)
- [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp)
- [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)
- [promql/functions.go](file:///home/fl/code/external/prometheus/promql/functions.go)

Objective:
- calculate the earliest raw sample time each subtree needs before selector lowering happens

### Pass 1 — Common matcher inference + label pushdown
Primary references:
- [metricsql/optimizer.go](file:///home/fl/code/external/VictoriaMetrics/vendor/github.com/VictoriaMetrics/metricsql/optimizer.go)
- [push_down_filter.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs)
- [CoreRules.java](file:///home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/CoreRules.java)
- [promql/parser/parse.go](file:///home/fl/code/external/prometheus/promql/parser/parse.go)
- [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)

Objective:
- infer safe common label predicates across binary/vector boundaries
- push explicit and inferred predicates to the deepest safe source relation

Key safety machinery:
- label lineage
- semantic barriers
- `on(...)` / `ignoring(...)` / `group_left` / `group_right` trimming

### Pass 2 — Projection pushdown and late tag materialization
Primary references:
- [optimize_projections/mod.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/optimize_projections/mod.rs)
- [projection_pushdown.rs](file:///home/fl/code/external/datafusion/datafusion/physical-optimizer/src/projection_pushdown.rs)
- [finalizeSQL.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/finalizeSQL.cpp)

Objective:
- derive required columns top-down
- prune helper columns aggressively
- keep `tags` optional and late-materialized
- make `SELECT *` forbidden in native fragments and final SQL

### Pass 3 — Function / pattern rewrites
Primary references:
- [promql/functions.go](file:///home/fl/code/external/prometheus/promql/functions.go)
- [applyFunctionOverRange.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/applyFunctionOverRange.cpp)
- [analyzer/function_rewrite.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/analyzer/function_rewrite.rs)

Initial catalog:
- `last_over_time`
- `sum_over_time`
- `avg_over_time`
- `min_over_time`
- `max_over_time`
- `count_over_time`
- `rate`
- `irate`
- `increase`

Then add composite patterns:
- `sum(rate(...))`
- `sum by(...) (rate(...))`
- later, selected histogram patterns

Safety rule:
- do rewrites on **typed IR**, not raw SQL strings
- keep them constrained by Prometheus semantics

### Pass 4 — Flatten redundant wrappers
Primary references:
- [CoreRules.java](file:///home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/CoreRules.java)
- [simplify_exprs.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/simplify_expressions/simplify_exprs.rs)

Objective:
- remove pure projection / alias-only / trivial nested wrapper layers
- preserve true semantic barriers:
  - aggregation boundaries
  - duplicate-detection boundaries
  - join-key normalization boundaries
  - range-window materialization boundaries
  - post-join label-copy boundaries
  - lineage-changing transforms

### Pass 5 — Careful JOIN construction
Primary references:
- [applySimpleBinaryOperator.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/applySimpleBinaryOperator.cpp)
- [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)
- [promql/parser/parse.go](file:///home/fl/code/external/prometheus/promql/parser/parse.go)

Design rules:
1. compute a dedicated `join_group`
2. keep `original_group` long enough to reconstruct result labels
3. drop `__name__` from join key by default unless explicitly matched
4. validate uniqueness on the “one” side before the final join
5. carry only columns needed downstream
6. align on `eval_ts` + `join_group` when step-based relations are involved
7. copy extra labels only after a successful match
8. re-check duplicate result groups if label-copying can collapse rows

### Additional early passes
- constant folding / trivial simplification via [simplify_exprs.rs](file:///home/fl/code/external/datafusion/datafusion/optimizer/src/simplify_expressions/simplify_exprs.rs)
- join-side transitive predicate ideas via [CoreRules.java](file:///home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/CoreRules.java)
- late matrix/array materialization following [finalizeSQL.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/finalizeSQL.cpp)

## Explain visibility requirements

The explain API should expose:
- `lowerable`
- `selectedStrategy`
- `nativeScope`
- `fallbackReason`
- `rulesApplied`
- `pushedPredicates`
- `inferredPredicates`
- `requiredColumns`
- `materializedColumns`
- `semanticBarriers`
- optional `renderedSQL`
- optional `estimatedSeries`, `estimatedPoints`, `estimatedJoinCardinality`

Rendered SQL is especially valuable in explain/shadow modes after filter/projection/finalization passes.

## Distinct tasks in this chunk

1. **Lock pass ordering**
   - make the optimizer explicitly staged instead of a loose set of future ideas

2. **Define the first mandatory passes**
   - evaluation range
   - matcher inference + predicate pushdown
   - projection pushdown
   - function rewrite scaffolding
   - flattening
   - join normalization

3. **Define explain observability**
   - make explain output a first-class rollout/debugging tool for native lowering
