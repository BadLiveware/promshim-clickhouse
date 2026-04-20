# Prometheus Shim Implementation Order Plan

## Purpose

This document captures the recommended implementation order for the Go-based Prometheus-compatible shim.

The goal is to maximize real compatibility while keeping the implementation incremental, testable, and dependency-aware.

---

## Status legend

Use these meanings when reading this file:

- **Status: Done** — the phase goal is complete enough to move on
- **Status: In progress** — partial groundwork exists, but the phase goal is not complete
- **Status: Not started** — no meaningful implementation yet beyond parsing or explicit unsupported errors
- `[x]` = implemented/verified in the current repo state
- `[ ]` = not done yet

---

## Assumptions

- Keep the HTTP API unchanged:
  - `/health`
  - `/-/healthy`
  - `/-/ready`
  - `/api/v1/query`
  - `/api/v1/query_range`
  - `/api/v1/labels`
  - `/api/v1/label/{name}/values`
  - `/api/v1/series`
- Keep ClickHouse as the execution backend.
- Prefer a **hybrid planner/evaluator**:
  - delegate supported leaf expressions to ClickHouse
  - evaluate missing PromQL semantics in the shim
- Optimize for **real dashboard compatibility**, not abstract language completeness.
- Preserve implementation-language-agnostic HTTP integration tests grouped by difficulty.

---

## External reference repositories

These local repositories are part of the implementation context for the shim and should be used during planning and implementation when needed.

### Prometheus
- Local checkout: `~/code/external/prometheus/`
- Use for:
  - checking PromQL parser/AST behavior
  - understanding aggregation, binary-op, vector-matching, time-modifier, and subquery semantics
  - borrowing implementation ideas and test cases
  - comparing expected Prometheus query engine behavior against shim behavior
- Especially relevant areas:
  - `promql/`
  - `promql/parser/`

### ClickHouse
- Local checkout: `~/code/external/ClickHouse/`
- Use for:
  - understanding how ClickHouse implements Prometheus-related query handling internally
  - checking `TimeSeries` and `prometheusQuery()` / `prometheusQueryRange()` internals
  - validating what can realistically be delegated vs what must be implemented locally in the shim
  - borrowing implementation ideas where useful
- Especially relevant areas:
  - `src/Storages/TimeSeries/`
  - `src/Storages/TimeSeries/PrometheusQueryToSQL/`

### How to use these references
- They are reference material for:
  - checking how things work internally
  - borrowing implementation ideas
  - understanding edge cases and semantics
  - validating assumptions before adding new shim behavior
- They are **not** a replacement for the shim’s own HTTP contract tests.
- When behavior differs between the current shim and upstream references, prefer making the difference explicit in tests and plan notes.

---

## Current repo snapshot

### What exists now
- [x] Go shim module exists
- [x] HTTP API contract exists and matches the current shim surface
- [x] PromQL parsing uses the Prometheus parser/AST
- [x] Supported-vs-unsupported classification exists before execution
- [x] A first logical-plan layer now exists separately from execution-plan lowering
- [x] `buildPlan(...)` now composes logical planning and execution-plan lowering
- [x] A first request-aware execution-strategy context now exists for planning
- [x] Execution plans are now explainable through an internal explain tree
- [x] A first range guardrail and chunked local range-execution path now exists
- [x] Easy subset works through ClickHouse delegation:
  - [x] selectors
  - [x] equality matchers
  - [x] regex matchers
  - [x] some delegated range functions such as `rate(...)`
  - [x] labels / label values / series metadata endpoints
- [x] First broader local aggregation subset exists locally in Go:
  - [x] `sum(...)`
  - [x] `count(...)`
  - [x] `min(...)`
  - [x] `max(...)`
  - [x] `avg(...)`
  - [x] `by (...)` / `without (...)` forms for the implemented aggregators
  - [x] range-query equivalents for the implemented aggregators
- [x] Unit tests exist for parser support classification and some aggregation semantics
- [x] HTTP integration tests exist for `easy`, `medium`, and `hard`

### What does not exist yet
- [x] A first recursive planner/evaluator path now exists for the current supported subset
- [x] Basic AST-to-plan lowering now exists for delegated leaves and local `sum(...)`
- [x] A basic typed runtime value model now exists for scalar/vector/matrix evaluation
- [x] Planner/evaluator errors now carry stage/expression context without depending on HTTP response types
- [x] A basic local aggregation framework now exists behind the current `sum` path
- [x] A first scalar execution path now exists
- [x] A first local binary-operator execution path now exists for scalar/scalar and vector/scalar expressions
- [x] Local label mutation helpers now exist (`label_replace`, `label_join`)
- [x] A first execution-strategy planner now exists for aggregation pushdown decisions
- [x] A first native ClickHouse SQL pushdown path now exists for aggregation over delegatable leaves
- [x] A first range guardrail now rejects oversized range queries before execution
- [x] A first chunked local range-execution path now exists for large local range plans
- [x] A first output-aware response limit now exists for oversized final query results
- [x] Query/query_range success responses now stream directly instead of building one more full top-level result object graph first
- [ ] No histogram support
- [ ] No vector matching
- [ ] No time modifiers
- [ ] No subquery execution

---

## Guiding Principles

- Build the smallest reusable execution model before adding many more one-off operators.
- Prefer recursive planning over handler special-casing.
- Keep PromQL semantics in Go-owned planning/execution structures; ClickHouse delegation and SQL are execution targets, not the semantic source of truth.
- Land features in an order that unlocks more downstream features.
- Keep compatibility behavior explicit:
  - supported queries should succeed
  - unsupported queries should fail clearly and consistently
- Keep validation close to each phase.

---

## Recommended Milestones

### Milestone A: Composable Medium Subset
**Status: In progress**

Focus on turning the current baseline into a composable evaluator.

- planner/evaluator
- broader aggregations
- scalar + vector-scalar ops
- label mutation helpers

### Milestone B: Dashboard-Heavy Compatibility
**Status: Not started**

Focus on real dashboard patterns.

- histogram support
- vector matching
- set operators

### Milestone C: Time Semantics and Long Tail
**Status: Not started**

Focus on deeper PromQL execution semantics.

- `offset`
- `@`
- subqueries
- compatibility polish

---

## Ordered Checklist

### Phase 1 — Build a real recursive planner/evaluator
**Status: Done**

#### Current repo state
- [x] PromQL AST parsing and support analysis exist
- [x] Some local execution exists for top-level `sum(...)`
- [x] Shared execution helpers now exist for:
  - [x] label-set normalization
  - [x] grouping key generation
  - [x] current `__name__` handling for `sum` aggregation
  - [x] timestamp alignment for range construction / merge validation
  - [x] current NaN/null conversion helpers
- [x] Query handlers now route through the planner/evaluator path instead of top-level `sum(...)` special-cases
- [x] A recursive planner/evaluator path now exists for delegated leaves and local `sum(...)`
- [x] Logical PromQL planning is now separated from execution-plan lowering
- [x] A first execution-strategy context now flows into execution-plan lowering
- [x] Execution plans now carry an internal explain tree
- [x] Range planning now includes a first guardrail and chunking pass
- [x] `buildPlan(...)` now acts as a compatibility wrapper over logical planning + execution lowering
- [x] Current `sum(...)` support is now expressed as reusable plan nodes
- [x] The planner/evaluator path is now transport-agnostic; HTTP status mapping lives in the handler layer

#### Why now
This is the main prerequisite for nearly everything else. Without it, the code will keep accreting special-cases for each new feature.

#### Depends on
- Nothing

#### Scope
- [x] Replace top-level handler special-cases with a recursive AST planner/evaluator for the current supported subset
- [x] Separate logical PromQL planning from execution-plan lowering for the current supported subset
- [x] Add a first execution-strategy context and explainable execution-plan layer for the current supported subset
- [x] Introduce plan nodes for the current supported subset:
  - [x] delegated leaf expression
  - [x] local aggregate
  - [x] local binary op
  - [x] local label transform
- [ ] Feature-specific plan nodes still belong to later phases:
  - [ ] local histogram op (Phase 5)
  - [ ] time modifier (Phase 8)
- [x] Introduce typed runtime values:
  - [x] scalar
  - [x] vector
  - [x] matrix
- [x] Introduce shared execution helpers:
  - [x] label-set normalization
  - [x] grouping key generation
  - [x] basic current `__name__` handling
  - [x] timestamp alignment rules as a shared evaluator concern
  - [x] basic current NaN/null handling
- [x] Move current `sum(...)` support onto the new planner path

#### Test gates
- [x] Unit tests for AST support classification exist
- [x] Unit tests for AST-to-plan lowering exist for the current subset
- [x] Basic unit tests for runtime value rendering/conversion exist for the current subset
- [x] Unit tests now cover logical -> execution lowering and first native SQL pushdown selection/explain behavior
- [x] Unit tests now cover range guardrail rejection and chunked local range execution
- [x] Unit tests now cover shared label-normalization and timestamp-alignment helpers
- [x] Existing easy integration tests pass unchanged
- [x] Existing medium `sum(...)` integration tests pass on the planner/evaluator path

#### Exit criteria
- [x] New features can now be added as plan/eval nodes instead of handler branches
- [x] The code now has an explicit place to insert future execution-strategy planning between logical planning and execution lowering
- [x] The reusable planner/runtime foundation is complete enough that migration and optimization work can proceed as Phase 1.5 instead of extending Phase 1 indefinitely

---

### Phase 1.5 — Migrate onto the new execution approach
**Status: In progress**

#### Current repo state
- [x] Logical planning, execution lowering, execution-strategy context, explain trees, first pushdown, and first range guardrail/chunking already exist
- [x] Request handlers already plan through the new contextual planning path
- [ ] The repo still mixes “feature implementation” progress with “migration to the new strategy-based execution model” progress
- [x] Output-aware safety for large final query responses now exists via configurable response series/point limits
- [x] Query/query_range success rendering now streams directly from runtime values instead of building a second full top-level response graph
- [x] A deterministic differential validation harness now exists for comparing Prometheus vs promshim over the same seeded samples
- [ ] Native SQL pushdown is still intentionally narrow, but now covers aggregation over delegatable leaves and pushdown-safe unary/vector-scalar arithmetic transforms over those leaves

#### Why now
This phase makes the architectural pivot explicit. The goal is to finish migrating the shim onto the new strategy-driven execution model before more feature work expands the surface area and makes migration messier.

#### Depends on
- [x] Phase 1

#### Scope
- [x] Make logical planning + execution lowering + strategy selection the default execution story for supported queries
- [x] Add request-aware planning context so handlers choose execution strategies intentionally
- [x] Add first explainable execution strategies:
  - [x] delegated PromQL leaf execution
  - [x] native SQL aggregation pushdown
  - [x] local execution
  - [x] chunked local range execution
- [x] Add first range safety mechanisms:
  - [x] max points-per-series guardrail
  - [x] chunking threshold for large local range plans
- [x] Add first output-aware response safety for very large final query results:
  - [x] configurable response series/point limits
  - [x] streamed query/query_range success rendering
- [x] Add a deterministic differential validation harness for the new execution model:
  - [x] docker-compose services for Prometheus, ClickHouse, and promshim
  - [x] a seeded one-shot metric generator job that emits deterministic sample streams with a per-run manifest-recorded base timestamp
  - [x] dual-write the same generated remote-write samples to Prometheus and ClickHouse
  - [x] a one-shot comparator job that runs a query corpus against Prometheus and promshim and diffs normalized responses
  - [x] scenario-based datasets for selectors, aggregations, binary/vector-scalar behavior, label helpers, and later histogram patterns
- [ ] Widen native SQL pushdown further where it is semantically safe and materially reduces local result size
- [x] Make execution-strategy selection rules more explicit/documented as the source of truth for future work

#### Test gates
- [x] Unit tests cover logical -> execution lowering
- [x] Unit tests cover first strategy selection/explain behavior
- [x] Unit tests cover range guardrail rejection and chunked local range execution
- [x] Unit tests cover output-aware response limits and streamed success rendering
- [x] Existing integration suites remain green on the migrated path
- [x] Live HTTP spot checks validate:
  - [x] native aggregation pushdown
  - [x] chunked local range execution
  - [x] guardrail rejection
  - [x] response-limit rejection
- [x] Differential harness validation exists for seeded dual-write scenarios and compares Prometheus vs promshim over the same samples

#### Exit criteria
- [ ] Supported queries run through the strategy-based execution path as the clear default architecture, not as a transitional compatibility wrapper in practice
- [x] The remaining major memory-risk path is now reduced for oversized final responses via response limits and streamed success rendering
- [ ] Future feature phases can assume the new execution model instead of continuing the migration ad hoc
- [x] Query-engine changes can be regression-checked against a deterministic Prometheus oracle over the same seeded samples

---

### Phase 2 — Generalize aggregation framework
**Status: In progress**

#### Current repo state
- [x] A local aggregation path exists for the first implemented aggregation subset
- [x] `sum by (...)` works
- [x] `sum without (...)` works
- [x] `count`, `min`, `max`, and `avg` now work on the local aggregation path
- [x] Instant aggregation works
- [x] Range/matrix aggregation works by timestamp for the implemented aggregators
- [x] A first native SQL aggregation pushdown path now exists for aggregations over delegatable leaves
- [x] Native aggregation pushdown now also covers pushdown-safe unary and scalar-arithmetic transforms over delegatable leaves (for example `sum by (...) (metric * 100)` and `avg by (...) (-metric)`)
- [x] Large local range aggregation plans can now be chunked instead of materializing one full child range at once
- [x] A reducer/factory boundary now exists for adding more aggregators cleanly
- [ ] Higher-order aggregators such as `topk`, `bottomk`, and `count_values` are not implemented yet

#### Why now
Aggregation unlocks a large amount of dashboard compatibility and is a prerequisite for histogram-heavy query patterns.

#### Depends on
- [ ] Phase 1

#### Recommended internal order
- [x] `sum`
- [x] `count`
- [x] `min`
- [x] `max`
- [x] `avg`
- [ ] `topk`
- [ ] `bottomk`
- [ ] `count_values`

#### Scope
- [x] A first reusable local aggregation framework now exists
- [x] Reuse current grouping/label semantics for:
  - [x] `by (...)`
  - [x] `without (...)`
- [x] Current Prometheus-like label behavior for `sum`:
  - [x] drop `__name__` where appropriate
  - [x] preserve only requested grouping labels for `by (...)`
  - [x] remove listed labels and `__name__` for `without (...)`
- [x] Support both for the implemented aggregators:
  - [x] instant vector aggregation
  - [x] matrix/range aggregation by timestamp
- [x] First execution-strategy pushdown now exists for the implemented aggregators when their child is a delegatable leaf
- [ ] Extend the framework to higher-order aggregators

#### Test gates
- [x] Unit tests for some grouping behavior exist
- [x] Basic unit tests now exist for the reducer/runtime aggregation boundary
- [x] Medium integration tests now cover:
  - [x] `count(...)`
  - [x] `avg(...)`
  - [x] `min(...)`
  - [x] `max(...)`
- [ ] Medium integration tests for:
  - [ ] `topk(...)`
  - [ ] `bottomk(...)`
- [x] Existing `sum(...)` tests remain green

#### Exit criteria
- [x] New implemented aggregators are added by plugging into the reducer framework, not custom code paths

---

### Phase 3 — Add scalar support and pointwise binary ops
**Status: In progress**

#### Current repo state
- [x] Scalar instant execution now exists
- [x] Unary `+` and `-` now exist for the current supported scalar/instant-vector subset
- [x] Vector-scalar arithmetic now exists for the first implemented operator subset
- [x] Vector-scalar comparison now exists for the first implemented comparison subset
- [x] `bool` comparison mode now exists for vector-scalar comparisons
- [x] Vector-vector and vector-matching binary expressions still fail explicitly as unsupported today

#### Why now
This is simpler than vector-vector matching, but it unlocks many common dashboard expressions.

#### Depends on
- [ ] Phase 1

#### Scope
- [x] Add scalar execution and vector-scalar binary semantics for the current local subset
- [x] Scalar literals
- [x] Unary `+` and `-`
- [x] Vector-scalar arithmetic:
  - [x] `+`
  - [x] `-`
  - [x] `*`
  - [x] `/`
  - [x] `%`
  - [x] `^`
- [x] Vector-scalar comparisons:
  - [x] `==`
  - [x] `!=`
  - [x] `>`
  - [x] `<`
  - [x] `>=`
  - [x] `<=`
- [x] `bool` comparison mode
- [ ] Vector-vector binary semantics remain out of scope for this phase slice

#### Example queries unlocked
- [x] `up == 0`
- [x] `rate(...[5m]) * 100`
- [x] `sum(...) / 60`
- [x] `1 + 2`

#### Test gates
- [x] Unit tests now cover scalar result evaluation and arithmetic/comparison semantics for the implemented subset
- [x] Medium integration tests now cover representative vector-scalar queries
- [x] Explicit tests now exist for `bool` output behavior
- [ ] Additional tests for scalar-only range-query behavior if/when that is implemented

#### Exit criteria
- [x] Binary operator semantics now exist for scalar-involved queries and can be reused later for vector-vector ops
- [ ] Vector-vector matching and set semantics remain future work

---

### Phase 4 — Implement label mutation helpers
**Status: Done**

#### Current repo state
- [x] `label_replace` is implemented on the local planner/evaluator path
- [x] `label_join` is implemented on the local planner/evaluator path
- [x] Label mutation helpers now work for both instant and range evaluation over the supported subset

#### Why now
These are medium-complexity, high-value compatibility features that do not require vector matching.

#### Depends on
- [ ] Phase 1

#### Scope
- [x] Implement `label_replace`
- [x] Implement `label_join`
- [x] Regex handling and replacement behavior
- [x] Label overwrite/creation semantics
- [x] Preserve current-label behavior unless the destination label is explicitly overwritten

#### Test gates
- [x] Unit tests now cover regex capture/replacement semantics and plan-time validation
- [x] Unit tests now cover key label mutation edge cases, including duplicate labelset handling
- [x] Medium integration tests now cover common label transform queries

#### Exit criteria
- [x] Common label-rewrite dashboard queries no longer fail as unsupported

---

### Phase 5 — Implement histogram support
**Status: Not started**

#### Current repo state
- [x] Delegated range functions such as `rate(...)` already work in the current easy subset
- [ ] `histogram_quantile` is not implemented
- [ ] `histogram_count` is not implemented
- [ ] `histogram_sum` is not implemented
- [ ] `histogram_avg` is not implemented

#### Why now
This is one of the highest-value features for real service latency dashboards.

#### Depends on
- [ ] Phase 1
- [ ] Phase 2
- [x] Existing delegated range-function support such as `rate(...)`

#### Recommended internal order
- [ ] `histogram_quantile`
- [ ] `histogram_count`
- [ ] `histogram_sum`
- [ ] `histogram_avg`

#### Scope
- [ ] Implement histogram-oriented compatibility starting with `histogram_quantile`
- [ ] Support classic bucket-label patterns such as `le`
- [ ] Support expressions like:
  - [ ] `histogram_quantile(0.9, sum by (le, job) (rate(..._bucket[5m])))`
- [ ] Define handling for missing buckets / incomplete series

#### Test gates
- [ ] Unit tests for quantile interpolation logic
- [ ] Unit tests for bucket grouping behavior
- [ ] Hard integration tests for representative histogram dashboard queries

#### Exit criteria
- [ ] Service latency dashboards using classic Prometheus histogram patterns are materially more compatible

---

### Phase 6 — Implement vector matching core
**Status: Not started**

#### Current repo state
- [x] Vector matching queries fail explicitly as unsupported today
- [ ] No matching-key infrastructure exists yet
- [ ] No vector-vector execution exists yet

#### Why now
This is a major semantic subsystem and is best implemented after scalar binary semantics and label/grouping utilities are stable.

#### Depends on
- [ ] Phase 1
- [ ] Phase 3

#### Scope
- [ ] Add vector matching infrastructure for vector-vector operations
- [ ] Matching-key generation
- [ ] `on(...)`
- [ ] `ignoring(...)`
- [ ] Cardinality checks
- [ ] `group_left`
- [ ] `group_right`
- [ ] Use this infrastructure for vector-vector arithmetic and comparisons

#### Test gates
- [ ] Unit tests for matching key behavior
- [ ] Unit tests for cardinality validation
- [ ] Hard integration tests for representative kube-style join queries

#### Exit criteria
- [ ] Vector-vector arithmetic/comparison can be implemented on top of shared matching infrastructure

---

### Phase 7 — Implement set operators
**Status: Not started**

#### Current repo state
- [x] Set operators fail explicitly as unsupported today
- [ ] No set-operator evaluation exists yet

#### Why now
These should reuse the vector matching infrastructure from Phase 6.

#### Depends on
- [ ] Phase 6

#### Scope
- [ ] Implement vector set operators
- [ ] `and`
- [ ] `or`
- [ ] `unless`

#### Test gates
- [ ] Unit tests for set membership semantics
- [ ] Hard integration tests for representative set-operator queries

#### Exit criteria
- [ ] Set operators behave consistently with the vector matching model

---

### Phase 8 — Implement time modifiers
**Status: Not started**

#### Current repo state
- [ ] `offset` is not implemented
- [ ] `@` is not implemented

#### Why now
These are mostly orthogonal, but are best added after the recursive planner exists and before subqueries.

#### Depends on
- [ ] Phase 1

#### Recommended internal order
- [ ] `offset`
- [ ] `@`

#### Scope
- [ ] Adjust delegated query evaluation time/range
- [ ] Adjust local evaluation context consistently
- [ ] Define supported combinations and validation behavior

#### Test gates
- [ ] Unit tests for evaluation-time shifting
- [ ] Integration tests for simple offset queries
- [ ] Integration tests for `@` instant evaluation behavior

#### Exit criteria
- [ ] Time-shifted and fixed-time queries work without handler special-casing

---

### Phase 9 — Implement subqueries
**Status: Not started**

#### Current repo state
- [x] Subqueries fail explicitly as unsupported today
- [ ] No subquery evaluation exists yet

#### Why now
Subqueries depend on a working planner, matrix model, and stable time semantics.

#### Depends on
- [ ] Phase 1
- [ ] Preferably Phase 8

#### Scope
- [ ] Implement `[range:step]`
- [ ] Implement nested matrix evaluation
- [ ] Define local/delegated execution rules for subquery children

#### Test gates
- [ ] Unit tests for subquery planning
- [ ] Hard integration tests for representative subquery expressions
- [ ] Tests for nested range evaluation shapes

#### Exit criteria
- [ ] Subqueries no longer fail as unsupported for the implemented subset

---

### Phase 10 — Long-tail compatibility and polish
**Status: Not started**

#### Current repo state
- [x] Unsupported features already fail explicitly in many current cases
- [x] The current HTTP contract is stable enough to preserve while internals evolve
- [ ] The long-tail semantic gaps are not being worked through systematically yet

#### Why last
These features are important, but they benefit from the core execution model being stable first.

#### Depends on
- [ ] Phases 1–9 as relevant

#### Candidate scope
- [ ] `absent`
- [ ] `absent_over_time`
- [ ] staleness edge cases
- [ ] stricter Prometheus-style error shaping
- [ ] duplicate labelset edge behavior
- [ ] native histogram nuances
- [ ] execution/perf cleanup if needed

#### Test gates
- [ ] Add targeted regression tests for each bug/semantic gap fixed
- [ ] Expand hard-suite coverage for edge-case behavior
- [ ] Re-run representative dashboard query corpus if available

#### Exit criteria
- [ ] The shim handles the most important remaining compatibility edge cases with focused regression coverage

---

## Dependency Summary

### Hard prerequisites
- Phase 1 is the prerequisite for almost everything else.
- Phase 1.5 is the preferred prerequisite for broader pushdown, output-safety work, and future high-cost query features.
- Phase 6 is the prerequisite for:
  - vector-vector binary ops
  - set operators
- Phase 8 is the preferred prerequisite for:
  - subqueries

### Soft prerequisites
- Phase 3 helps with:
  - parameterized aggregators
  - comparison semantics
  - later vector-vector operator reuse
- Phase 2 helps with:
  - histogram patterns
  - many real dashboard queries

---

## Default Recommended Order

Use this order unless target-dashboard evidence suggests otherwise:

- [x] Phase 1 — recursive planner/evaluator — **Done (logical/execution split + shared helpers + first strategy/explain/guardrail/chunking layer landed)**
- [ ] Phase 1.5 — migrate onto the new execution approach — **In progress (strategy-based execution is real, but migration/output-safety/pushdown widening remain)**
- [ ] Phase 2 — broader aggregations — **In progress (`sum`, `count`, `min`, `max`, `avg` now have local execution, first native pushdown, and chunked local range execution support)**
- [ ] Phase 3 — scalar + vector-scalar ops — **In progress**
- [ ] Phase 4 — label mutation helpers — **Done**
- [ ] Phase 5 — histogram support — **Not started**
- [ ] Phase 6 — vector matching + vector-vector binary ops — **Not started**
- [ ] Phase 7 — set operators — **Not started**
- [ ] Phase 8 — `offset` and `@` — **Not started**
- [ ] Phase 9 — subqueries — **Not started**
- [ ] Phase 10 — long-tail compatibility and polish — **Not started**

---

## Decision Point: Histogram Support vs Vector Matching First

There is one intentional fork in priority depending on target dashboards.

### Default path
- [x] Do histogram support before vector matching

#### Best when targeting
- generic service dashboards
- latency dashboards
- RED/USE-style dashboards

#### Because these often depend on patterns like
```promql
histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))
```

### Alternate path
- [ ] Do vector matching before histogram support

#### Best when targeting
- kube-prometheus-stack-heavy dashboards
- kube-state-metrics-heavy dashboards
- dashboards with many metadata joins

#### Because these often depend on patterns like
```promql
metric_a * on(namespace, pod) group_left(label_x, label_y) metric_b
```

### Current recommendation
- [x] Stay on the default path unless a dashboard inventory shows vector matching is the bigger blocker

---

## Validation Strategy by Phase

### Inner loop
- [x] Unit tests exist for current planner-support classification and current `sum` semantics
- [x] Focused integration tests exist for current `easy`, `medium`, and `hard` coverage slices
- [x] Difficulty grouping is preserved:
  - [x] easy
  - [x] medium
  - [x] hard

### Checkpoints
- [x] `gofmt -w ...`
- [x] `go test ./...`
- [x] Live HTTP integration tests against ClickHouse on `127.0.0.1:8123`
- [x] Local shim verification on `127.0.0.1:9090`
- [x] Differential docker-compose harness with seeded dual-write into Prometheus and ClickHouse
- [x] Query corpus diff against Prometheus direct results vs promshim -> ClickHouse results

### Contract invariants
- [x] HTTP API remains unchanged
- [x] Successful responses remain Prometheus-style JSON
- [x] Unsupported features fail explicitly and consistently for the current implemented subset
- [x] Implementation language remains hidden behind the HTTP contract

---

## Immediate Next Steps

### Recommended next implementation step
- [ ] Continue Phase 1.5 by adding the deterministic docker-compose differential harness with seeded dual-write into Prometheus and ClickHouse

### Recommended first acceptance target after that
- [ ] Continue Phase 1.5 by expanding native SQL pushdown beyond the current leaf + unary/scalar-transform aggregation subset where it is semantically safe and measurably reduces local result size

### Recommended first high-value dashboard target after medium subset
- [ ] Phase 5: `histogram_quantile`

---

## Definition of Done for Each Phase

A phase is not done until:

- [ ] the feature is implemented in the planner/evaluator shape intended for future reuse
- [ ] unit tests cover the new semantics
- [ ] integration tests prove the HTTP contract behavior
- [ ] existing lower-difficulty suites continue to pass
- [ ] unsupported adjacent features still fail explicitly rather than silently misbehaving
