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

## Current repo snapshot

### What exists now
- [x] Go shim module exists
- [x] HTTP API contract exists and matches the current shim surface
- [x] PromQL parsing uses the Prometheus parser/AST
- [x] Supported-vs-unsupported classification exists before execution
- [x] Easy subset works through ClickHouse delegation:
  - [x] selectors
  - [x] equality matchers
  - [x] regex matchers
  - [x] some delegated range functions such as `rate(...)`
  - [x] labels / label values / series metadata endpoints
- [x] First medium feature exists locally in Go:
  - [x] `sum(...)`
  - [x] `sum by (...) (...)`
  - [x] `sum without (...) (...)`
  - [x] range-query equivalents of top-level `sum`
- [x] Unit tests exist for parser support classification and some aggregation semantics
- [x] HTTP integration tests exist for `easy`, `medium`, and `hard`

### What does not exist yet
- [ ] No real recursive planner/evaluator yet
- [ ] No generic AST-to-plan lowering yet
- [ ] No generic typed runtime value model for scalar/vector/matrix evaluation
- [ ] No general aggregation framework beyond the current `sum` path
- [ ] No scalar execution
- [ ] No binary operator execution
- [ ] No label mutation helpers
- [ ] No histogram support
- [ ] No vector matching
- [ ] No time modifiers
- [ ] No subquery execution

---

## Guiding Principles

- Build the smallest reusable execution model before adding many more one-off operators.
- Prefer recursive planning over handler special-casing.
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
**Status: In progress**

#### Current repo state
- [x] PromQL AST parsing and support analysis exist
- [x] Some local execution exists for top-level `sum(...)`
- [x] Some execution helpers already exist for:
  - [x] grouping key generation
  - [x] current `__name__` handling for `sum` aggregation
  - [x] current NaN/null conversion helpers
- [ ] Handlers still contain top-level feature special-cases
- [ ] No recursive planner/evaluator exists yet
- [ ] Current `sum(...)` support is not yet expressed as reusable plan nodes

#### Why now
This is the main prerequisite for nearly everything else. Without it, the code will keep accreting special-cases for each new feature.

#### Depends on
- Nothing

#### Scope
- [ ] Replace top-level handler special-cases with a recursive AST planner/evaluator
- [ ] Introduce plan nodes for:
  - [ ] delegated leaf expression
  - [ ] local aggregate
  - [ ] local binary op
  - [ ] local label transform
  - [ ] local histogram op
  - [ ] time modifier
- [ ] Introduce typed runtime values:
  - [ ] scalar
  - [ ] vector
  - [ ] matrix
- [ ] Introduce shared execution helpers:
  - [ ] label-set normalization
  - [x] grouping key generation
  - [x] basic current `__name__` handling
  - [ ] timestamp alignment rules as a shared evaluator concern
  - [x] basic current NaN/null handling
- [ ] Move current `sum(...)` support onto the new planner path

#### Test gates
- [x] Unit tests for AST support classification exist
- [ ] Unit tests for AST-to-plan lowering
- [ ] Unit tests for runtime value conversions
- [x] Existing easy integration tests pass unchanged
- [x] Existing medium `sum(...)` integration tests pass on the current implementation path
- [ ] Existing medium `sum(...)` integration tests pass on the future planner path

#### Exit criteria
- [ ] New features can be added as plan/eval nodes instead of handler branches

---

### Phase 2 — Generalize aggregation framework
**Status: In progress**

#### Current repo state
- [x] A local aggregation path exists for `sum`
- [x] `sum by (...)` works
- [x] `sum without (...)` works
- [x] Instant aggregation works
- [x] Range/matrix aggregation works by timestamp for `sum`
- [ ] No reducer framework exists yet for adding more operators cleanly
- [ ] No aggregators other than `sum` are implemented yet

#### Why now
Aggregation unlocks a large amount of dashboard compatibility and is a prerequisite for histogram-heavy query patterns.

#### Depends on
- [ ] Phase 1

#### Recommended internal order
- [x] `sum`
- [ ] `count`
- [ ] `min`
- [ ] `max`
- [ ] `avg`
- [ ] `topk`
- [ ] `bottomk`
- [ ] `count_values`

#### Scope
- [ ] Turn local aggregation into a reusable operator framework
- [x] Reuse current grouping/label semantics for:
  - [x] `by (...)`
  - [x] `without (...)`
- [x] Current Prometheus-like label behavior for `sum`:
  - [x] drop `__name__` where appropriate
  - [x] preserve only requested grouping labels for `by (...)`
  - [x] remove listed labels and `__name__` for `without (...)`
- [x] Support both for `sum`:
  - [x] instant vector aggregation
  - [x] matrix/range aggregation by timestamp
- [ ] Extend the framework to other aggregators

#### Test gates
- [x] Unit tests for some grouping behavior exist
- [ ] Unit tests for reducer semantics per operator
- [ ] Medium integration tests for:
  - [ ] `count(...)`
  - [ ] `avg(...)`
  - [ ] `min(...)`
  - [ ] `max(...)`
  - [ ] `topk(...)`
  - [ ] `bottomk(...)`
- [x] Existing `sum(...)` tests remain green

#### Exit criteria
- [ ] New aggregators are added by plugging into a reducer framework, not custom code paths

---

### Phase 3 — Add scalar support and pointwise binary ops
**Status: Not started**

#### Current repo state
- [x] Unsupported binary operators fail explicitly today
- [ ] No scalar execution exists
- [ ] No vector-scalar arithmetic exists
- [ ] No vector-scalar comparison exists

#### Why now
This is simpler than vector-vector matching, but it unlocks many common dashboard expressions.

#### Depends on
- [ ] Phase 1

#### Scope
- [ ] Add scalar execution and vector-scalar binary semantics
- [ ] Scalar literals
- [ ] Unary `+` and `-`
- [ ] Vector-scalar arithmetic:
  - [ ] `+`
  - [ ] `-`
  - [ ] `*`
  - [ ] `/`
  - [ ] `%`
  - [ ] `^`
- [ ] Vector-scalar comparisons:
  - [ ] `==`
  - [ ] `!=`
  - [ ] `>`
  - [ ] `<`
  - [ ] `>=`
  - [ ] `<=`
- [ ] `bool` comparison mode

#### Example queries unlocked
- [ ] `up == 0`
- [ ] `rate(...[5m]) * 100`
- [ ] `sum(...) / 60`

#### Test gates
- [ ] Unit tests for scalar coercion
- [ ] Unit tests for arithmetic and comparison semantics
- [ ] Medium integration tests for representative vector-scalar queries
- [ ] Explicit tests for `bool` output behavior

#### Exit criteria
- [ ] Binary operator semantics exist for scalar-involved queries and can be reused later for vector-vector ops

---

### Phase 4 — Implement label mutation helpers
**Status: Not started**

#### Current repo state
- [x] `label_replace` fails explicitly as unsupported today
- [x] `label_join` would fail explicitly as unsupported today
- [ ] No label mutation implementation exists

#### Why now
These are medium-complexity, high-value compatibility features that do not require vector matching.

#### Depends on
- [ ] Phase 1

#### Scope
- [ ] Implement `label_replace`
- [ ] Implement `label_join`
- [ ] Regex handling and replacement behavior
- [ ] Label overwrite/creation semantics
- [ ] Label deletion edge behavior if replacement yields empty value, if applicable to chosen compatibility behavior

#### Test gates
- [ ] Unit tests for regex capture/replacement semantics
- [ ] Unit tests for label mutation edge cases
- [ ] Medium integration tests for common dashboard label transforms

#### Exit criteria
- [ ] Common label-rewrite dashboard queries no longer fail as unsupported

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

- [ ] Phase 1 — recursive planner/evaluator — **In progress**
- [ ] Phase 2 — broader aggregations — **In progress (`sum` only)**
- [ ] Phase 3 — scalar + vector-scalar ops — **Not started**
- [ ] Phase 4 — label mutation helpers — **Not started**
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

### Contract invariants
- [x] HTTP API remains unchanged
- [x] Successful responses remain Prometheus-style JSON
- [x] Unsupported features fail explicitly and consistently for the current implemented subset
- [x] Implementation language remains hidden behind the HTTP contract

---

## Immediate Next Steps

### Recommended next implementation step
- [ ] Phase 1: recursive planner/evaluator

### Recommended first acceptance target after that
- [ ] Finish the Phase 2 aggregation framework so `count`, `min`, `max`, and `avg` can land without more handler special-casing

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
