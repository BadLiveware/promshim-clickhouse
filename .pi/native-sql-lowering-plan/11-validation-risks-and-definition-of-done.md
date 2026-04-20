# 11 — Validation, risks, and definition of done

## Validation strategy

### Unit tests
Add deterministic tests for:
- lowerability classification
- label lineage
- predicate pushdown safety
- subquery flattening safety
- join planning
- rendered SQL fragments / snapshots

### Integration tests
Against the local ClickHouse fixture:
- native selector correctness
- native aggregation correctness
- vector join correctness
- range/subquery correctness

### Differential tests
For the supported native subset:
- compare native result vs delegated PromQL result
- compare native result vs Prometheus where practical for edge semantics

### Explain tests
Ensure explain surfaces:
- why a subtree lowered
- why it did not
- what optimizer passes were applied
- what fallback happened

## Risks / hard parts

### 1. Semantic drift
Biggest risk is semantic mismatch:
- metric-name rules
- duplicate-labelset rules
- lookback / staleness
- range boundary handling
- NaN / null / absent behavior

### 2. SQL blow-up
Without flattening and projection pruning, generated SQL can become deeply nested and unreadable.

### 3. Join cardinality surprises
Vector matching is correctness-sensitive and can explode runtime costs.

### 4. Predicate pushdown mistakes
Pushing a label predicate through a label mutation boundary can silently return wrong results.

### 5. Time-window underfetch
If evaluation-range propagation is incomplete, native lowering can be subtly wrong for subqueries and range functions.

## Design decisions to keep explicit

### Decision A — Internal fragment shape
Use normalized relational fragments internally, especially for range vectors, with optional late-materialized columns for `tags`, `metric_name`, and join helper columns.

### Decision B — Leaf source
Prefer TimeSeries backing tables / `timeSeriesSelector`-like repo-owned sources for native lowering, not `prometheusQuery*` as an inner source. See [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp).

### Decision C — Semantic authority
Prometheus is the semantic oracle; ClickHouse is the lowering-shape oracle; VictoriaMetrics / DataFusion / Calcite inform optimizer mechanics.

### Decision D — Lineage-aware optimizer
Do not push SQL strings around blindly. Use typed predicates, label lineage metadata, and required-column tracking.

### Decision E — Small staged RBO, not a general CBO
Keep the optimizer as a fixed-pass, reviewable rule pipeline rather than a general planner framework.

### Decision F — Subqueries are not mandatory execution barriers
Let them lower as part of a bigger native subtree when supported.

### Decision G — Keep delegated PromQL path as oracle
This is essential for safe rollout.

## Definition of done
Do not consider the native lowering track “real” until all of the following are true:

- the planner can select **maximal lowerable subtrees**, not only special-cased top-level aggregations
- the native lowerer has explicit optimizer passes for:
  - evaluation-range propagation
  - label-filter pushdown and common matcher inference
  - projection pushdown with no `SELECT *`
  - function / pattern rewrites for the supported subset
  - redundant-subquery flattening
  - careful JOIN construction
- explain output shows strategy selection and fallback reasons clearly
- a shadow-compare mode exists and is used for promotion
- the common dashboard subset is covered by differential tests
- the delegated path remains available as a fallback and correctness oracle

## Short summary
The right shape for this repo is:

**logical plan -> lowerability analysis -> staged RBO passes -> maximal native subtree selection -> SQL fragment optimization -> final SQL shaping -> execution**

Success is not just “we can emit SQL”. Success is:
- the planner lowers the biggest safe subtree
- the generated SQL is structurally good
- filters get pushed deep
- redundant layers get flattened
- joins are deliberate
- rollout is guarded by explain + shadow comparison
