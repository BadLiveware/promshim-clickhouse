# 02 — Planner and fragment model

## Target execution architecture

The target execution stack should look like this:

1. parse PromQL
2. build logical plan
3. analyze native lowerability bottom-up
4. select maximal native subtrees top-down
5. lower selected subtrees to native SQL fragments
6. optimize SQL fragments
7. render final SQL or mixed execution plan
8. execute
9. compare / explain / enforce guardrails

Critical design rule:

> The planner should prefer lowering the largest semantically-safe subtree, not just hard-coded node types.

That means:
- if the root lowers, emit one native SQL subtree for the whole query
- if the root does not lower, recurse into children and lower the largest lowerable child subtrees
- keep local or delegated execution only where native lowering is unsafe or not yet implemented

## Core planning model

### Bottom-up capability analysis
For every logical node, compute a `NativeLoweringInfo`-style result.

Example shape:

```go
// sketch only
type NativeLoweringInfo struct {
    Lowerable        bool
    OutputKind       NativeOutputKind
    Reason           string

    PreservesMetricName bool
    MayDuplicateSeries  bool
    ProducesStepGrid    bool
    RequiresLookback    bool

    LabelLineage map[string]LabelLineage
    RequiredRange NativeTimeRequirement
}
```

This phase must answer:
- can this subtree be expressed in repo-owned SQL?
- what relation shape does it produce?
- which labels are still original / mutated / dropped?
- what earliest input timestamp must be fetched for correctness?
- can outer filters be pushed through it?
- would lowering this subtree require unsupported semantics?

### Top-down strategy selection
Once every node has a capability result, do a top-down pass:
- if the current node is lowerable and policy prefers native here, select it as a native subtree root and stop descending
- otherwise recurse into children
- wrap child native fragments in local or delegated parents as needed

This gives:
- full-query lowering when possible
- otherwise maximal-island lowering

### Native subtree execution node
Introduce a general execution-plan node for full native subtrees.

Example:

```go
// sketch only
type nativeSubtreePlan struct {
    Expr     string
    Fragment NativeFragment
    Estimate *planEstimate
    Reason   string
    Children []ExplainNode
}
```

This should supersede the current aggregation-specific native node as the general mechanism.

## SQL fragment IR

Major implementation choice:

> Lower into a canonical internal SQL fragment shape first, then finalize to the HTTP/API result shape late.

This follows the same broad separation ClickHouse uses in:
- [SQLQueryPiece.h](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/SQLQueryPiece.h)
- [finalizeSQL.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/finalizeSQL.cpp)

### Scalar fragment
Suggested contract:
- columns:
  - `value`
  - optional `eval_ts`

### Instant-vector fragment
Suggested contract:
- mandatory columns:
  - `series_key`
  - `eval_ts`
  - `value`
- optional columns materialized only when required:
  - `tags`
  - `metric_name`
  - `join_group`
  - `original_group`
  - copied label columns
- metadata:
  - whether `__name__` is still present / meaningful
  - uniqueness guarantees for `series_key`
  - which optional columns are currently materialized

### Range-vector fragment
Suggested contract:
- mandatory columns:
  - `series_key`
  - `eval_ts`
  - `sample_ts`
  - `sample_value`
- optional columns materialized only when required:
  - `tags`
  - `metric_name`
  - `join_group`
  - `original_group`
  - copied label columns

This “tall” form is a better composition target because:
- filters push naturally
- joins and grouping are easier
- flattening redundant subqueries is easier
- range/window operators can aggregate over rows directly

### Fragment metadata
Each fragment should also carry:
- `Kind`
- `SQL` or SQL builder tree
- `Columns`
- `LabelLineage`
- `MetricNameState`
- `RequiredInputTimeRange`
- `EvaluationGrid`
- `UniqueBy`
- `Predicates`
- `SemanticBarriers`
- `EstimatedCost`

## Source access strategy for native lowering

For the pure native track, prefer lowering selectors against the **TimeSeries backing shape**, not delegated PromQL functions.

Primary reference:
- [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp)

That lowering relies on:
- [NodeEvaluationRangeGetter.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/NodeEvaluationRangeGetter.cpp)

### Preferred source pattern
Use one of:
- `timeSeriesData(db.table)`
- `timeSeriesTags(db.table)`
- `timeSeriesMetrics(db.table)`
- or another repo-owned selector relation over the target tables

### Why not use `prometheusQuery()` as the fragment source?
If a “native” fragment is built on top of delegated PromQL again:
- label-filter pushdown stops there
- range-window control stops there
- join planning becomes opaque
- projection pushdown becomes much weaker
- subquery flattening becomes much less effective

### Selector leaf contract
A lowered selector leaf should already know:
- metric name matcher
- label matchers
- required time interval
- step / lookback needs
- whether it is serving instant or range semantics
- which columns are actually required downstream

## Desired planner behavior examples

### Example 1 — Entire query lowers
```promql
sum by(job) (up{namespace="prod", job=~"api|worker"})
```
Desired strategy:
- whole query -> `native_sql`

### Example 2 — Unsupported root, lowerable child subtree
```promql
histogram_quantile(0.99, sum by(le, job) (rate(http_request_duration_seconds_bucket{job="api"}[5m])))
```
Desired strategy:
- child subtree `sum by(le, job) (rate(...))` -> `native_sql`
- root `histogram_quantile(...)` -> `local`

### Example 3 — Entire subquery lowers if root can lower
```promql
avg_over_time((sum by(job) (up{namespace="prod"}))[30m:1m])
```
Desired behavior:
- if subquery + outer range function are supported, lower the **entire root**
- do **not** treat the subquery boundary as mandatory execution barrier

### Example 4 — Root does not lower, but subquery does
```promql
label_join((sum by(job, namespace) (up{namespace="prod"}))[30m:1m], "job_ns", "/", "job", "namespace")
```
Desired behavior:
- lower the subquery subtree to native SQL
- apply `label_join` locally at the boundary

## Distinct tasks in this chunk

1. **Define the planner contracts**
   - add `NativeLoweringInfo`
   - add `NativeFragment`
   - define fragment metadata needed by later passes

2. **Define maximal-island strategy selection**
   - make full-root lowering and child-island lowering explicit in planner terms

3. **Define fragment shape contracts**
   - lock mandatory vs optional columns
   - make late tag materialization part of the design, not an optimization afterthought

4. **Define selector leaf contract**
   - make leaf lowering repo-owned and explicitly time-bounded
