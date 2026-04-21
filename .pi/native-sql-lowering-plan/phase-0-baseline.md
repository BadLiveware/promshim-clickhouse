# Phase 0 — Baseline guardrails, touchpoints, and starter corpus contract

## Why this file exists

The split native SQL lowering plan starts its numbered implementation phases at
Phase 1. Phase 1 has since shipped (see
[`00-status-and-drift.md`](./00-status-and-drift.md) and
[`04-phase-1-analysis-scaffolding.md`](./04-phase-1-analysis-scaffolding.md)),
but the pre-Phase-1 groundwork captured here remains the baseline reference
for every later phase.

In this repo, `phase 0` means the concrete preparatory work called out in
[`01-context-and-guardrails.md`](./01-context-and-guardrails.md):

1. freeze semantic authority and rewrite safety,
2. inventory the current planner/explain/SQL touchpoints,
3. define the starter Prometheus-backed differential corpus.

This file turns those prep items into a repo-local execution baseline so later
native-lowering changes have a stable reference point.

## Semantic authority and rewrite policy

Native lowering in this repo follows a strict authority order:

1. **Prometheus semantics first**
2. **ClickHouse lowering shape second**
3. **optimizer structure references third**

### Prometheus is the correctness oracle

For any semantic question, review against the Prometheus sources referenced in
[`01-context-and-guardrails.md`](./01-context-and-guardrails.md):

- vector matching legality and duplicate-series failures
- metric-name preservation/drop behavior
- range / subquery timing and evaluation windows
- lookback / offset / staleness rules
- counter reset and extrapolation semantics for `rate`/`irate`/`increase`

### ClickHouse is the lowering-shape oracle

Use the first-party ClickHouse Prometheus-to-SQL implementation as the primary
reference for:

- selector lowering shape
- evaluation-range propagation
- fragment/result-shape contracts
- late SQL finalization patterns
- range/window lowering mechanics

### Rewrite safety rules

These rules are phase-0 policy and should remain in force for follow-up work:

- no native rewrite ships without explicit reasoning against Prometheus
  semantics
- no native rewrite ships without differential coverage against Prometheus or
  the delegated path
- no blanket string-rewrite shortcuts such as textual `sum(rate(...))` pattern
  substitution
- current local range/subquery implementations remain the correctness fallback
  until native replacements are proven by differential tests
- native-lowering PRs must explain unsupported boundaries rather than silently
  approximating them

## Current repo touchpoints (authoritative starting inventory)

### 1. Logical planning

Current logical-plan construction lives in:

- `internal/promshim/logical_builder.go`
- `internal/promshim/plan/logicaltypes.go`
- `internal/promshim/plan/promql.go`

Important current facts:

- the repo already has a non-trivial logical plan tree for selectors,
  aggregations, binary/unary operators, label transforms, range functions, and
  subqueries
- lowerability is **not** represented as first-class analysis metadata yet
- range/subquery timing requirements are encoded in executor behavior rather
  than planner metadata

### 2. Strategy selection and execution planning

Current execution-path selection lives in:

- `internal/promshim/planner.go`
- `internal/promshim/aggregation_pushdown.go`
- `internal/promshim/plan_context.go`
- `internal/promshim/delegated_support.go`

Current behavior snapshot:

- delegated path: `delegatedExprPlan`
- native path: `nativeAggregationPlan`
- local path: `internal/promshim/exec/*` via the various `local*Plan` nodes
- `buildExecPlanWithContext(...)` is where logical nodes become
  delegated/native/local execution nodes, now reading lowerability from
  the `native.Analysis` produced by Phase 1 rather than recomputing it
  inline
- `decideNativeAggregationPushdown(...)` remains the aggregation-specific
  decision, but now consumes the shared analysis output
- `buildNativeAggregationSource(...)` performs the current special-case
  source extraction for aggregation pushdown

The inlined lowerability seam that the original plan flagged as a Phase 1
refactor target has been resolved. The remaining seam — a
general-purpose `nativeSubtreePlan` — is Phase 2's concern.

### 3. Native SQL entry points that exist today

Current repo-owned SQL rendering lives in:

- `internal/promshim/storage/sql.go`

The only current native PromQL-related SQL builders are:

- `BuildInstantAggregationQuerySQL(...)`
- `BuildRangeAggregationQuerySQL(...)`

Important limitation to preserve in reviews:

- today’s native path still uses `prometheusQuery(...)` /
  `prometheusQueryRange(...)` as the inner source and wraps that with repo-owned
  aggregation SQL
- this is useful as a bridge, but it is **not yet** the target native subtree
  lowering architecture described in Phases 2-7

### 4. Explain and HTTP touchpoints

Current explain/API touchpoints live in:

- `internal/promshim/explain.go`
- `internal/promshim/service.go`
- `internal/promshim/httpapi/router.go`
- `internal/promshim/service_test.go`

Current behavior snapshot:

- `/api/v1/query_explain` and `/api/v1/query_range_explain` expose the chosen
  execution plan
- normal query endpoints also support `explain=1`
- explain now surfaces chosen strategy/kind/reason/estimate plus the
  `LoweringInfo.ExplainInfo()` projection (lowerability, fallback reason,
  fragment kind, aggregation eligibility, required lookback/offset,
  subquery step-grid flag, label lineage) for Phase 1–tracked nodes

### 5. Local semantic oracle

Current local execution lives in:

- `internal/promshim/exec/*.go`
- `internal/promshim/planner_test.go`
- `internal/promshim/service_test.go`

Current policy baseline:

- local execution remains the correctness oracle for already-landed range
  functions, label mutation, joins/vector matching, histogram helpers, and
  subquery behavior
- native-lowering work must not silently bypass or weaken these semantics
- where native and local implementations coexist, differential coverage is the
  promotion gate

### 6. Differential harness touchpoints

Current Prometheus-vs-promshim comparison infrastructure lives in:

- `cmd/promharness-compare/main.go`
- `internal/promharness/compare.go`
- `internal/promharness/corpus.go`
- `internal/promharness/types.go`
- `harness/corpus/queries.json`
- `scripts/run-harness.sh`
- `harness/README.md`

Important current facts:

- corpus rows already support query/range timings, expected errors, and
  `compareMode`
- `InferCompareMode(...)` already defaults rate-family shapes to structural
  comparison when exact numeric parity is not expected
- the stable corpus is broad, but phase-0 needs a **smaller starter set** that
  is explicitly aligned to the native-lowering roadmap

## Starter differential corpus contract

Phase 0 adds a focused native-lowering starter corpus. That corpus should be:

- small enough to run frequently while Phases 1-4 are in flight
- broad enough to cover the first semantic families the roadmap cares about
- explicit about whether a query expects exact or structural comparison

### Required starter buckets

The starter corpus should contain at least one Prometheus-backed comparison for
all of:

1. **selectors**
   - plain selector
   - selector with label matchers
   - selector with offset and/or range-query shape
2. **aggregations**
   - plain aggregation over a selector
   - aggregation over unary/scalar-transformed selector
   - range-query form of the same aggregation family
3. **joins / vector matching**
   - one representative vector-vector arithmetic query
   - one representative `group_left` or `group_right` query
4. **counters / rate family**
   - at least one `rate(...)`
   - at least one aggregation-over-rate shape
   - structural comparison is acceptable where exact numeric parity is not yet a
     stable contract
5. **subqueries**
   - one matrix-root selector subquery
   - one local-child subquery
   - one matrix-consuming query over a subquery

### Starter corpus intent

This corpus is **not** the full stable parity corpus and **not** the later
shadow corpus. It is the narrow baseline used to keep the native-lowering work
honest while the planner and fragment-analysis layers are still being built.

## Phase-0 validation baseline

The minimum validation bar for the phase-0 artifacts is:

- corpus files load successfully through `internal/promharness/corpus.go`
- corpus rows have unique names and valid endpoints
- documentation points to real current code paths
- existing `go test ./internal/promshim/... ./internal/promharness/...` remains
  green

## Out of scope for phase 0

Phase 0 does **not**:

- introduce `LoweringInfo` or fragment IR types (Phase 1; now delivered)
- change runtime execution-path selection
- add new native SQL lowering behavior
- change explain envelopes
- add shadow execution controls

Those begin in the numbered phases after this baseline is in place.
