# Planner-selected native SQL lowering plan

## Goal

Add a **planner-selected native SQL lowering path** to the Prometheus shim so that:

- if an entire logical plan is safely lowerable, the shim executes it as **one native ClickHouse SQL query**;
- if the full plan is not lowerable, the planner still lowers the **largest supported subtrees** and only falls back to local or delegated evaluation at explicit semantic boundaries;
- generated SQL is optimized enough to be worth using in practice, especially for:
  - **pushing label filters to the deepest possible source relations**,
  - **flattening redundant subqueries / CTE layers**, and
  - **constructing JOINs deliberately** instead of accidentally materializing large intermediate relations.

This plan is for the current repo architecture, not for a greenfield rewrite.

---

## Why this is the right next step

The current shim already has the core ingredients needed for this direction:

- logical planning exists:
  - `internal/promshim/logical_builder.go`
  - `internal/promshim/plan/logicaltypes.go`
- execution strategy selection already exists:
  - `internal/promshim/planner.go`
  - `internal/promshim/aggregation_pushdown.go`
- explainability already exists:
  - `internal/promshim/explain.go`
- there is already a seed of native SQL execution:
  - `maybeBuildNativeAggregationPlan(...)`
  - `buildNativeAggregationSource(...)`
  - `internal/promshim/storage/sql.go`

So the next step is **not** “invent a second independent query engine.”

The next step is to generalize the current narrow pushdown into:

**logical plan -> native lowering analysis -> SQL fragment optimization -> final strategy selection**

That architecture lets the current delegated PromQL path remain:

- the fallback,
- the rollout safety net,
- and the correctness oracle during shadow comparison.

---

## Preserve / must-not-break requirements

### Preserve
- Existing HTTP API surface.
- Existing explain endpoints and response envelopes.
- Existing local evaluator semantics for already-supported features.
- Existing delegated PromQL path via ClickHouse as a fallback and comparison oracle.
- Existing guardrails for oversized range queries and oversized responses.
- Existing error classification boundaries (`bad_data`, `unsupported`, `execution`) unless intentionally changed.

### Add
- Native lowering of maximal supported subtrees.
- SQL-aware optimization passes.
- Better explain visibility for why a subtree did or did not lower.
- Shadow-compare and rollout controls so native lowering can be promoted safely.

### Non-goals for the initial implementation
- Do **not** replace the local evaluator wholesale.
- Do **not** silently approximate unsupported PromQL semantics.
- Do **not** optimize by routing native-lowered fragments back through `prometheusQuery()` / `prometheusQueryRange()` internally; that defeats most of the value of lowering.
- Do **not** chase full PromQL completeness before the common dashboard subset is fast and stable.

---

## External references to use deliberately

These local references should inform the implementation:

### Prometheus
- `~/code/external/prometheus/promql/`
- `~/code/external/prometheus/promql/parser/`

Use for:
- parser/AST semantics,
- vector matching rules,
- range evaluation rules,
- staleness / lookback behavior,
- duplicate-labelset failure behavior.

### ClickHouse
- `~/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/`
- `~/code/external/ClickHouse/src/Storages/TimeSeries/`

Especially useful reference patterns:
- `NodeEvaluationRangeGetter.*`
  - propagates required evaluation windows down to leaves
- `applySimpleBinaryOperator.cpp`
  - careful JOIN-key construction
  - duplicate-series checks on the “one” side
  - metric-name preservation/drop behavior
- `fromSelector.cpp`
  - selector lowering shape
- `finalizeSQL.cpp`
  - separate internal storage form from final result form

These references are for implementation ideas and semantic cross-checking, not for copying blindly.

---

## Current repo starting point

### What exists now
- PromQL parsing and support analysis.
- Logical-plan construction.
- Planner-selected execution strategies.
- Local execution for a growing subset.
- Delegated ClickHouse PromQL execution via:
  - `prometheusQuery(...)`
  - `prometheusQueryRange(...)`
- First native SQL pushdown for simple aggregations over delegatable leaves.

### Current limitation
The current “native” path is still effectively:

- **delegated child expression**
- wrapped by **repo-owned SQL aggregation**

That is useful, but it is not yet a true PromQL-to-SQL lowering track. It cannot deeply optimize:

- selector predicates,
- range windows,
- subquery shapes,
- vector-vector joins,
- or subquery/CTE inflation.

---

## High-level target architecture

The target execution stack should look like this:

1. **Parse PromQL**
2. **Build logical plan**
3. **Analyze native lowerability bottom-up**
4. **Select maximal native subtrees top-down**
5. **Lower selected subtrees to native SQL fragments**
6. **Optimize SQL fragments**
7. **Render final SQL or mixed execution plan**
8. **Execute**
9. **Compare / explain / enforce guardrails**

The critical design rule is:

> The planner should prefer lowering the largest semantically-safe subtree, not just hard-coded node types.

That means:

- if the root lowers, emit one native SQL subtree for the whole query;
- if the root does not lower, recurse into children and lower the largest child subtrees that do;
- keep local or delegated execution only where native lowering is unsafe or not yet implemented.

---

## Core planning model

### 1. Bottom-up capability analysis

For every logical node, compute a `NativeLoweringInfo`-style result.

Example shape:

```go
// sketch only
 type NativeLoweringInfo struct {
     Lowerable bool
     OutputKind NativeOutputKind
     Reason string

     // Metadata needed for optimization and composition.
     PreservesMetricName bool
     MayDuplicateSeries bool
     ProducesStepGrid bool
     RequiresLookback bool

     // Label provenance / predicate safety.
     LabelLineage map[string]LabelLineage

     // Time requirements for leaf pushdown.
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

This phase is **capability analysis**, not execution strategy selection.

### 2. Top-down strategy selection

Once every node has a capability result, do a top-down pass:

- if the current node is lowerable and policy prefers native here, select it as a native subtree root and stop descending;
- otherwise recurse into children;
- wrap child native fragments in local or delegated parents as needed.

This gives the desired behavior:

- full-query lowering when possible,
- otherwise maximal-island lowering.

### 3. Native subtree execution node

Introduce a general execution-plan node for full native subtrees.

Example:

```go
// sketch only
 type nativeSubtreePlan struct {
     Expr string
     Fragment NativeFragment
     Estimate *planEstimate
     Reason string
     Children []ExplainNode
 }
```

This should supersede the current aggregation-specific native node as the general mechanism.

The current `nativeAggregationPlan` can become the first special case re-expressed through this general path.

---

## SQL fragment IR: do not lower directly to final response shape

A major implementation choice:

> Lower into a canonical internal SQL fragment shape first, then finalize to the HTTP/API result shape late.

This is the same general pattern ClickHouse upstream uses internally with result type + store method separation.

### Why this matters
If every node lowers directly to the final HTTP shape, composition becomes messy fast.

Examples of pain if we do not use an internal fragment contract:
- range functions need a different shape than aggregations,
- binary joins need different shapes than selectors,
- subqueries need extra time axes,
- redundant materialize/dematerialize steps become hard to detect.

### Recommended native fragment kinds

#### Scalar fragment
Single-row value relation.

Suggested contract:
- columns:
  - `value`
  - optional `eval_ts`

#### Instant-vector fragment
One row per output series at one evaluation timestamp.

Suggested contract:
- columns:
  - `series_key`
  - `tags`
  - `eval_ts`
  - `value`
- metadata:
  - whether `__name__` is still present / meaningful
  - uniqueness guarantees for `series_key`

#### Range-vector fragment
A normalized relation, not final arrays.

Suggested contract:
- columns:
  - `series_key`
  - `tags`
  - `eval_ts`
  - `sample_ts`
  - `sample_value`

This “tall” form is usually a better composition target than an array-of-points form because:
- filters push more naturally,
- joins and grouping are easier,
- flattening redundant subqueries is easier,
- range/window operators can aggregate over rows directly.

Late in the pipeline, the renderer may convert this to final matrix rows / grouped arrays when needed.

### Recommended fragment metadata

Each fragment should also carry:

- `Kind` / output kind
- `SQL` or SQL builder tree
- `Columns`
- `LabelLineage`
- `MetricNameState`
- `RequiredInputTimeRange`
- `EvaluationGrid`
- `UniqueBy` guarantees
- `Predicates`
- `SemanticBarriers`
- `EstimatedCost`

---

## Source access strategy for native lowering

For the pure native track, the repo should prefer lowering selectors against the **TimeSeries backing shape**, not against delegated PromQL functions.

### Preferred source pattern
Use one of:
- `timeSeriesData(db.table)`
- `timeSeriesTags(db.table)`
- `timeSeriesMetrics(db.table)`

or an equivalent repo-owned selector source over the target tables.

### Why not use `prometheusQuery()` as the fragment source?
Because if a “native” fragment is built on top of delegated PromQL again:
- label-filter pushdown stops there,
- range-window control stops there,
- join planning becomes opaque,
- subquery flattening becomes much less effective.

### Selector leaf contract
A lowered selector leaf should already know:
- metric name matcher,
- label matchers,
- required time interval,
- step / lookback needs,
- whether it is serving instant or range semantics.

---

## Optimization passes

The native lowerer needs explicit optimizer passes. Three are mandatory from the start.

### Pass 1 — Push label filters to the deepest safe source relations

#### Objective
Move selector and inherited label predicates as close as possible to the source scan so ClickHouse reads fewer series and fewer rows.

#### Why this matters
A large part of query cost in observability workloads is:
- how many series IDs survive the label filter,
- how many timestamps are scanned for those series,
- how early aggregation or joins can operate on reduced inputs.

#### Required machinery: label provenance / lineage
To push predicates correctly, the planner must know whether a label at a higher layer still corresponds to an original source label.

Example lineage states:
- `Original("job")`
- `CopiedFrom("job")`
- `Mutated`
- `Dropped`
- `Synthetic`
- `Unknown`

This is especially important for:
- `label_replace`
- `label_join`
- aggregations with `without(...)`
- binary joins that copy extra labels from one side
- metric-name dropping / preservation

#### Safe pushdown rules

##### Safe to push through
- pure selectors
- transparent parentheses / wrappers
- unary arithmetic that does not touch labels
- scalar/vector arithmetic that does not change labels
- aggregations when the predicate only references labels guaranteed to exist in the child and does not depend on post-aggregation labels

##### Not safe to push through without proof
- `label_replace`
- `label_join`
- binary joins that create / copy labels
- operators that drop `__name__`
- any node with ambiguous duplicate-labelset behavior

#### Implementation note
Pushdown should operate on a normalized predicate representation, not on SQL strings.

For example:

```go
 type LabelPredicate struct {
     Label string
     Op PredicateOp // eq, neq, regex, nregex, exists, etc.
     Value string
 }
```

Attach predicates to fragments, then push them down during optimization based on lineage and semantic barriers.

#### Example: full pushdown

PromQL:

```promql
sum by(job) (up{namespace="prod", job=~"api|worker"})
```

Bad lowered shape:

```sql
SELECT group_tags, sum(value)
FROM (
  SELECT tags, value
  FROM selector_source
)
WHERE tags['namespace'] = 'prod'
  AND match(tags['job'], 'api|worker')
GROUP BY group_tags
```

Better lowered shape:

```sql
WITH leaf AS (
  SELECT
    t.id,
    t.tags,
    d.timestamp,
    d.value
  FROM timeSeriesData(observability.prometheus) AS d
  INNER JOIN timeSeriesTags(observability.prometheus) AS t USING (id)
  WHERE t.metric_name = 'up'
    AND t.tags['namespace'] = 'prod'
    AND match(t.tags['job'], 'api|worker')
    AND d.timestamp <= fromUnixTimestamp64Milli({eval_ms:Int64})
)
SELECT
  arraySort(tag -> tag.1, arrayFilter(tag -> tag.1 IN ['job'], tags)) AS tags,
  max(timestamp) AS timestamp,
  sum(value) AS value
FROM leaf
GROUP BY tags
```

The key property is that the label filters are attached to the leaf scan, not to the outer aggregation.

---

### Pass 2 — Flatten redundant subqueries and CTE layers

#### Objective
Avoid generating SQL like:

```sql
SELECT ...
FROM (
  SELECT ...
  FROM (
    SELECT ...
    FROM (...)
  )
)
```

when intermediate layers only:
- rename columns,
- reorder columns,
- wrap a trivial alias,
- or materialize/unmaterialize a shape without a semantic reason.

#### Why this matters
Without flattening, a maximal-subtree lowerer often degenerates into a “lower-every-node-to-its-own-subquery” generator. That becomes:
- hard to inspect,
- hard to optimize,
- and often slower.

#### Subqueries that are usually safe to flatten
- pure projection layers
- alias-only wrappers
- single-consumer CTEs that only rename columns
- nested `SELECT ... FROM (SELECT ...)` where the inner query has no grouping, no HAVING, no LIMIT, no DISTINCT, no windowing, and no join boundary worth preserving

#### Semantic barriers: do not flatten across these blindly
- aggregation boundaries
- duplicate-detection boundaries
- join-key normalization boundaries
- range window materialization boundaries
- deliberate CTE reuse by multiple consumers
- post-join label-copy boundaries
- transformations where label lineage changes

#### Example: flattening projection-only wrappers

Bad:

```sql
WITH leaf AS (
  SELECT tags, eval_ts, value
  FROM base_selector
),
renamed AS (
  SELECT tags AS tags, eval_ts AS eval_ts, value AS value
  FROM leaf
)
SELECT tags, eval_ts, value
FROM renamed
```

Optimized:

```sql
SELECT tags, eval_ts, value
FROM base_selector
```

#### Example: flattening a lowerable subquery boundary when safe

PromQL:

```promql
avg_over_time((sum by(job) (up{namespace="prod"}))[30m:1m])
```

If the full root is lowerable, avoid generating:

```sql
WITH step_1 AS (... selector ...),
step_2 AS (... sum by(job) over step_1 ...),
step_3 AS (... subquery expansion over step_2 ...),
step_4 AS (... avg_over_time over step_3 ...)
SELECT ... FROM step_4
```

when the same semantics can be rendered more compactly as a smaller number of composed CTEs or nested grouped selects.

The optimizer should preserve only the boundaries that matter for:
- evaluation grid expansion,
- grouping,
- and final range folding.

---

### Pass 3 — Carefully construct JOINs

#### Objective
Build vector-vector joins in a way that is explicit about:
- join key,
- cardinality expectation,
- duplicate detection,
- metric-name semantics,
- extra-label propagation,
- timestamp alignment.

#### Why this matters
Joins are one of the highest-risk areas for both correctness and performance.

Naive join generation tends to:
- carry too many columns,
- join on the wrong tag set,
- accidentally retain `__name__` when it should be ignored,
- or fail to detect duplicate-series conditions that Prometheus would reject.

#### Design rules

##### Rule 1: compute a dedicated join key
Do not join directly on the raw full tag set unless that is explicitly what the PromQL semantics require.

Instead derive:
- `join_group`
- `result_group`

This is the same broad pattern used in upstream ClickHouse.

##### Rule 2: drop `__name__` from the join key by default
Unless `on(__name__, ...)` or equivalent semantics explicitly keep it in the match key.

This avoids obvious non-matches between semantically joinable series with different metric names.

##### Rule 3: pre-normalize the “one” side
For one-to-one / many-to-one / one-to-many rules:
- identify the “one” side,
- group or validate uniqueness on its join key,
- throw duplicate-series errors before the final join if Prometheus would.

##### Rule 4: only carry columns needed downstream
Before the join, prune each side to:
- join key,
- result-group source fields,
- value(s),
- any explicitly copied labels.

##### Rule 5: align timestamps before or during the join
For instant vectors, both sides must be on the same evaluation timestamp.
For range-root step relations, joins should happen on:
- `eval_ts`, and
- `join_group`

not just on labels.

#### Join-shape example

PromQL:

```promql
up{job="api"} * on(instance) group_left(version) target_info{job="api"}
```

Planned shape:

1. Lower left selector with its own filters.
2. Lower right selector with its own filters.
3. Build `join_group = tags restricted to ['instance']` on both sides.
4. Validate right side uniqueness on `join_group` because it is the “one” side.
5. Join left to right.
6. Build result tags from left tags plus copied `version` from right.

Pseudo-SQL sketch:

```sql
WITH left_side AS (
  SELECT
    normalize_join_group(tags, ['instance']) AS join_group,
    tags AS original_tags,
    eval_ts,
    value
  FROM lowered_selector_up
),
right_side AS (
  SELECT
    normalize_join_group(tags, ['instance']) AS join_group,
    tags AS original_tags,
    eval_ts,
    value
  FROM lowered_selector_target_info
),
right_dedup AS (
  SELECT
    join_group,
    any(original_tags) AS original_tags,
    any(value) AS value,
    eval_ts
  FROM right_side
  GROUP BY join_group, eval_ts
  HAVING throw_if_duplicate(count() > 1, join_group) = 0
)
SELECT
  copy_extra_labels(left_side.original_tags, right_dedup.original_tags, ['version']) AS tags,
  left_side.eval_ts AS timestamp,
  left_side.value * right_dedup.value AS value
FROM left_side
LEFT JOIN right_dedup
  ON left_side.join_group = right_dedup.join_group
 AND left_side.eval_ts = right_dedup.eval_ts
```

The exact SQL helper names can differ, but the plan must preserve this deliberate structure.

---

## Additional optimizer passes worth adding early

### Evaluation-range propagation

A native lowerer must know the earliest raw sample time required by a subtree.

This should be an explicit planner pass inspired by ClickHouse’s `NodeEvaluationRangeGetter`.

#### Why this matters
Example:

```promql
avg_over_time(rate(http_requests_total{job="api"}[5m])[30m:1m])
```

To evaluate the outer subquery/range correctly, the leaf selector needs more than just `[start, end]`.
It needs a window that accounts for:
- outer query range,
- subquery range,
- inner range selector window,
- step alignment,
- lookback / offset semantics.

If this pass is missing, the lowerer will either:
- under-fetch and be wrong,
- or over-fetch badly and be slow.

### Projection pruning

After each pass, prune unused columns aggressively.

Example:
- after an aggregation, raw `id` may no longer be needed;
- after final join-key construction, original raw tag maps may be replaceable by result tags;
- after finalization, internal helper columns like `series_key` or `join_group` should be gone.

### Constant folding / trivial expression folding

Examples:
- unary `+` -> no-op
- arithmetic with constant scalar where safe to inline
- remove identity projections and dead aliases

### Late materialization of array / matrix output

Do not build final `time_series` arrays earlier than necessary.

Keep internal fragments relational as long as possible, then finalize late.

---

## Recommended explain output changes

The explain API is already a strength of this repo and should become the main visibility tool for native lowering rollout.

Add fields like:

- `lowerable: true|false`
- `selectedStrategy: native_sql | local | delegated_promql | mixed`
- `nativeScope: full | fragment | none`
- `fallbackReason`
- `optimizerPassesApplied`
- optional `renderedSQL` in explain-only mode
- optional `estimatedSeries`, `estimatedPoints`, `estimatedJoinCardinality`

Example explain intent:

```json
{
  "kind": "aggregation",
  "strategy": "native_sql",
  "expr": "sum by(job) (up{namespace=\"prod\"})",
  "reason": "entire subtree lowered to native SQL",
  "children": [
    {
      "kind": "leaf",
      "strategy": "native_sql_expression",
      "reason": "selector lowered directly against TimeSeries backing tables with label-filter pushdown"
    }
  ]
}
```

---

## Examples of desired planner behavior

### Example 1 — Entire query lowers

PromQL:

```promql
sum by(job) (up{namespace="prod", job=~"api|worker"})
```

Desired strategy:
- whole query -> `native_sql`

Why:
- selector lowerable
- aggregation lowerable
- grouping lowerable
- all label filters pushable

Expected behavior:
- filters pushed into leaf
- no delegated PromQL call
- no local evaluator involvement

---

### Example 2 — Unsupported root, lowerable child subtree

PromQL:

```promql
histogram_quantile(0.99, sum by(le, job) (rate(http_request_duration_seconds_bucket{job="api"}[5m])))
```

If `histogram_quantile` root is still local-only but `rate(...)` and `sum by(...)` are lowerable, desired strategy is:

- child subtree `sum by(le, job) (rate(...))` -> `native_sql`
- root `histogram_quantile(...)` -> `local`

That avoids materializing the raw leaf data or delegated child shape in Go unnecessarily.

---

### Example 3 — Entire subquery lowers if root can lower

PromQL:

```promql
avg_over_time((sum by(job) (up{namespace="prod"}))[30m:1m])
```

Desired behavior:
- if subquery + outer range function are supported, lower the **entire root**;
- do **not** force the planner to stop at the subquery boundary just because it is a subquery.

This is important. Subqueries are just one node type in the tree, not a mandatory execution boundary.

---

### Example 4 — Root does not lower, but subquery does

PromQL:

```promql
label_join((sum by(job, namespace) (up{namespace="prod"}))[30m:1m], "job_ns", "/", "job", "namespace")
```

If `label_join` remains local but the subquery subtree is lowerable, desired strategy:

- lower the subquery subtree to native SQL
- apply `label_join` locally at the boundary

This is exactly the “maximal lowerable island” behavior the planner should support.

---

## Implementation plan

### Phase 1 — Native lowering analysis scaffolding

#### Goal
Introduce a generic native-lowering analysis pass without changing most runtime behavior yet.

#### Scope
- Add native output kinds and fragment metadata types.
- Add bottom-up lowerability analysis over existing `logicalPlan` nodes.
- Add label-lineage tracking.
- Add evaluation-range requirement propagation.
- Extend explain to expose lowerability and reasons.

#### Likely files
- new:
  - `internal/promshim/native/analysis.go`
  - `internal/promshim/native/types.go`
  - `internal/promshim/native/lineage.go`
  - `internal/promshim/native/time_requirements.go`
- touched:
  - `internal/promshim/planner.go`
  - `internal/promshim/explain.go`

#### Validation
- unit tests for lowerability classification
- unit tests for label-lineage propagation
- unit tests for required-time-range propagation

---

### Phase 2 — General native subtree plan and renderer skeleton

#### Goal
Replace the aggregation-specific native node with a general native subtree execution node.

#### Scope
- Introduce `nativeSubtreePlan`.
- Introduce `NativeFragment` builder + renderer.
- Keep the initial supported subset small:
  - selectors
  - scalar literals
  - unary arithmetic
  - scalar/vector arithmetic
  - simple aggregations (`sum`, `count`, `min`, `max`, `avg`)
- Re-express current native aggregation pushdown through the general native subtree mechanism.

#### Validation
- existing native aggregation tests adapted to new architecture
- SQL rendering snapshot tests
- explain tests showing `native_sql` subtree selection

---

### Phase 3 — Selector lowering against TimeSeries backing tables

#### Goal
Make the native path truly repo-owned at the leaf level.

#### Scope
- Add native selector source generation using:
  - `timeSeriesData(...)`
  - `timeSeriesTags(...)`
  - or an equivalent repo-owned selector relation
- implement matcher lowering:
  - metric name
  - equality / inequality
  - regex / negative regex
- implement time-bound pushdown based on evaluation-range analysis
- define selector result contracts for instant and range modes

#### Validation
- unit tests for matcher-to-SQL lowering
- integration tests against seeded ClickHouse fixture data
- delegated-vs-native differential comparison for supported selectors

---

### Phase 4 — SQL optimization passes

#### Goal
Add the optimizer passes that make native lowering useful rather than merely functional.

#### Required first passes
1. label-filter pushdown
2. redundant subquery flattening
3. projection pruning
4. evaluation-range tightening

#### Optional in same slice if simple
- constant folding
- alias normalization

#### Validation
- snapshot tests on rendered SQL before/after optimization
- optimizer-specific unit tests proving:
  - a predicate moved deeper,
  - a redundant layer disappeared,
  - a semantic barrier was preserved

---

### Phase 5 — JOIN and vector matching lowering

#### Goal
Add careful lowering for vector-vector binary operators.

#### Scope
- one-to-one joins
- `on(...)` / `ignoring(...)`
- `group_left` / `group_right`
- duplicate-series error detection on the “one” side
- explicit metric-name preservation/drop rules
- timestamp-aware join conditions

#### Validation
- focused unit tests mirroring upstream Prometheus/ClickHouse join cases
- integration tests for duplicate-series failures
- explain output showing join-key derivation and join-kind choice

---

### Phase 6 — Range functions and subquery lowering

#### Goal
Make subqueries and range functions participate fully in maximal subtree lowering.

#### Scope
- normalized range-vector fragment lowering
- subquery step-grid expansion
- lowering for first range-heavy subset, likely:
  - `last_over_time`
  - `sum_over_time`
  - `avg_over_time`
  - `min_over_time`
  - `max_over_time`
  - `count_over_time`
- partial lowering around unsupported roots
- whole-root lowering when range subtree is fully supported

#### Validation
- unit tests for time-window propagation
- integration tests for subquery examples
- differential tests against delegated path / Prometheus where practical

---

### Phase 7 — Rollout controls and shadow comparison

#### Goal
Promote the feature safely.

#### Scope
Add request / config controls such as:
- `nativeLoweringMode = off`
- `nativeLoweringMode = explain`
- `nativeLoweringMode = shadow`
- `nativeLoweringMode = prefer`
- `nativeLoweringMode = force_supported`

Shadow mode should:
- execute the selected native subtree,
- compare against delegated/local result for supported comparison cases,
- log divergence details,
- avoid serving native results until confidence is high.

#### Validation
- service-level tests for explain and mode behavior
- controlled corpus promotion based on comparison results

---

## Validation strategy

### Unit tests
Add small, deterministic tests for:
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
For supported native subset:
- compare native result vs delegated PromQL result
- compare native result vs Prometheus where practical for edge semantics

### Explain tests
Ensure explain surfaces:
- why a subtree lowered,
- why it did not,
- what optimizer passes were applied,
- and what fallback happened.

---

## Risks / hard parts

### 1. Semantic drift
The biggest risk is not SQL generation complexity. It is semantic mismatch:
- metric-name rules
- duplicate-labelset rules
- lookback / staleness
- range boundary handling
- NaN / null / absent behavior

### 2. SQL blow-up
Without explicit flattening and projection pruning, generated SQL may become deeply nested and unreadable.

### 3. Join cardinality surprises
Vector matching is correctness-sensitive and can also explode runtime costs.

### 4. Predicate pushdown mistakes
Pushing a label predicate through a label mutation boundary can silently return wrong results.

### 5. Time-window underfetch
If evaluation-range propagation is incomplete, native lowering can be subtly wrong for subqueries and range functions.

---

## Design decisions to make explicit early

### Decision A — Internal fragment shape
Use normalized relational fragments internally, especially for range vectors.

### Decision B — Leaf source
Prefer TimeSeries backing tables / table functions for native lowering, not `prometheusQuery*` as an inner source.

### Decision C — Lineage-aware optimizer
Do not push SQL strings around blindly. Use typed predicates + label lineage metadata.

### Decision D — Subqueries are not mandatory execution barriers
Let them lower as part of a bigger native subtree when supported.

### Decision E — Keep delegated PromQL path as oracle
This is essential for safe rollout.

---

## Recommended first concrete slice

The smallest high-value slice after planning scaffolding is:

1. Introduce native lowering analysis + fragment IR.
2. Add selector lowering against TimeSeries backing tables.
3. Re-express existing simple aggregation pushdown using the new fragment path.
4. Add label-filter pushdown and redundant-subquery flattening.
5. Expose native lowering in explain output.
6. Add shadow-compare mode.

That slice would already prove the architecture on highly common dashboard shapes like:

```promql
sum by(job) (up{namespace="prod"})
avg by(instance) (node_load1{job="node-exporter"})
max without(cpu) (node_cpu_seconds_total{mode="idle"})
```

before joins and deeper range-heavy semantics are added.

---

## Definition of done for this track

Do not consider the native lowering track “real” until all of the following are true:

- The planner can select **maximal lowerable subtrees**, not only special-cased top-level aggregations.
- The native lowerer has explicit optimizer passes for:
  - label-filter pushdown,
  - redundant-subquery flattening,
  - careful JOIN construction.
- Explain output shows strategy selection and fallback reasons clearly.
- A shadow-compare mode exists and is used for promotion.
- The common dashboard subset is covered by differential tests.
- The delegated path remains available as a fallback and correctness oracle.

---

## Short summary

The right shape for this repo is:

**logical plan -> lowerability analysis -> maximal native subtree selection -> SQL fragment optimization -> execution**

The success criterion is not just “we can emit SQL.”

The success criterion is:
- the planner lowers the biggest safe subtree,
- the generated SQL is structurally good,
- filters get pushed deep,
- redundant layers get flattened,
- joins are deliberate,
- and rollout is guarded by explain + shadow comparison.
