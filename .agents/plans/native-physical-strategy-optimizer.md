# Native physical strategy optimizer plan

## Goal

Move native SQL physical-shape decisions out of ad-hoc renderer branches and into a small, typed physical strategy layer while keeping SQL construction in the existing renderer/storage packages.

This should preserve the validated wins from PR #12 and make future optimizations easier to compose, explain, and apply inside nested/subquery shapes.

## Current state

Recent optimizations are general by shape but selected inline during lowering/rendering:

- sparse direct aggregate for non-overlap `max_over_time`
- sparse direct aggregate for non-overlap `avg_over_time`
- sparse direct aggregate for non-overlap fused `sum(rate(...))`
- native-grid range functions for overlapping range functions
- cumulative avg path for some `avg_over_time` shapes
- selector strategy preferences (`ASOF` default, bucketed `argMax` opt-in)
- query settings preferences such as shape-specific `max_threads`

These decisions currently live across:

- `internal/promshim/native/renderer/range.go`
- `internal/promshim/native/renderer/range_logical.go`
- `internal/promshim/native/renderer/aggregation_range_fused_logical.go`
- `internal/promshim/storage/selector_sql.go`

The current `internal/promshim/logical/opt/` package is for logical rewrites and is not the right home for physical SQL-shape choices. The current `internal/promshim/native/optimizer.go` is mostly a report/pass scaffold and does not yet produce physical strategy decisions consumed by renderers.

## Non-goals

- Do not rewrite the renderer or storage SQL builders wholesale.
- Do not move SQL text construction into the optimizer.
- Do not add cost-based routing in the first pass.
- Do not broaden semantic coverage while moving decisions.
- Do not change validated default behavior without preserving benchmark/compliance evidence.

## Design direction

Add a small typed physical strategy module, for example:

```text
internal/promshim/native/physical/
```

or evolve `internal/promshim/native/optimizer.go` only if it can return concrete physical decisions rather than just reports.

The physical layer should answer questions like:

```go
type RangeWindowInput struct {
    Func string
    LookbackMS int64
    StepMS int64
    OffsetMS int64
    SourceKind SourceKind
    Mode native.RenderMode
    Preferences renderer.PhysicalPlanPreferences
    Config storage.QueryConfig
}

type RangeWindowStrategy string

const (
    RangeWindowStrategyWindowJoin RangeWindowStrategy = "window_join"
    RangeWindowStrategyDirectAggregate RangeWindowStrategy = "direct_aggregate"
    RangeWindowStrategySparseDirectAggregate RangeWindowStrategy = "sparse_direct_aggregate"
    RangeWindowStrategyCumulativeAvg RangeWindowStrategy = "cumulative_avg"
    RangeWindowStrategyNativeGrid RangeWindowStrategy = "native_grid"
)

type Decision struct {
    Strategy RangeWindowStrategy
    Reason string
    Guards []string
}
```

Renderers should call the strategy layer and then dispatch to existing SQL builders.

## Acceptance criteria

- Existing PR #12 benchmark/correctness behavior is preserved.
- Strategy decisions are unit-tested independently from SQL string tests.
- Renderer tests still verify representative SQL shapes.
- `go test ./internal/promshim/storage ./internal/promshim/native/renderer ./internal/promshim/logical ./internal/promshim/local ./internal/promshim/native` passes.
- Compliance remains unchanged except for known expected topk tie-order diff.
- Focused profile-50k artifacts show no regression for:
  - `max_over_time_gauge_by_3labels_1h_range_7d`
  - `avg_over_time_gauge_by_3labels_1h_range_7d`
  - `sum_rate_by_job_range_7d`
  - `repeated_sum_rate_average_by_job_range_7d`
  - `subquery_rate_over_aggregate_5m_range_1d`

## Tasks

### 1. Inventory existing physical decisions

Document every current inline physical choice and its guard conditions:

- range instant selector strategy:
  - ASOF default
  - bucketed `argMax` opt-in eligibility
- range-window aggregation strategy:
  - window join
  - direct aggregate
  - sparse direct aggregate
  - cumulative avg
  - native grid
- fused aggregation strategy:
  - native-grid sum aggregation
  - sparse direct rate aggregation
  - row-oriented aggregation fallback
- query settings:
  - set `max_threads`
  - explicit no-cap preservation

Output: a short code-facing inventory in the plan or a new package comment.

### 2. Create the physical strategy package

Add a small package or module with no SQL rendering responsibilities.

Initial API should cover only already-validated decisions:

- choose range selector strategy
- choose range-window aggregate strategy
- choose fused range aggregation strategy
- choose query settings policy

Keep inputs explicit and typed. Avoid stringly maps.

### 3. Move decision helpers first, not SQL builders

Move or wrap existing decision helpers:

- `preferDirectSelectorWindowJoin`
- `preferDirectSelectorWindowAggregate`
- `resolveRangeWindowAggregateStrategy`
- `canUseNativeGridRangeFunction`
- sparse direct rate eligibility
- thread cap selection helpers

Renderer code should still call existing storage builders such as:

- `BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags`
- `BuildRangeWindowSelectorCumulativeAvgRowsQuerySQLWithFinalTags`
- `BuildRangeNativeGridSelectorSumAggregationQuerySQLWithFinalTags`

### 4. Add independent decision tests

Add tests for pure strategy selection, for example:

- `max_over_time`, lookback 1h, step 1h, offset 0 -> sparse direct aggregate
- `max_over_time`, lookback 1h, step 5m -> window join / existing overlap path
- `avg_over_time`, lookback 1h, step 1h, offset 0 -> sparse direct aggregate
- `avg_over_time`, lookback 1h, step 1m -> cumulative avg or current measured path
- `sum(rate(...))`, lookback 1h, step 1h, offset 0 -> sparse direct rate aggregation
- `sum(rate(...))`, lookback 5m, step 1m -> native grid
- subquery rate-over-aggregation -> no-cap preserved

### 5. Wire renderer branches to the strategy layer

Replace inline branching in:

- `range.go`
- `range_logical.go`
- `aggregation_range_fused_logical.go`

with calls to the physical strategy layer.

Keep diffs mechanical: decision moves only, SQL shape should remain identical for representative tests.

### 6. Expose decisions in explain output

Add strategy decision fields where useful so `/api/v1/query_explain` and `ch-explain.sh` artifacts can answer:

- which physical strategy was chosen
- why it was chosen
- which guard conditions were satisfied
- which alternatives were rejected, if cheap to record

This can start as compact strings; it does not need a full trace UI.

### 7. Validate focused profile-50k rows

Run focused benchmarks before and after the move using existing artifacts as comparison:

- `harness/artifacts/bench/standalone/avg-over-time-timeout-fix-sparse/bench-report.json`
- `harness/artifacts/bench/standalone/predict-linear-constant-fix/bench-report.json`
- `harness/artifacts/bench/standalone/sparse-rate-nonoverlap-iter3/bench-report.json`
- `harness/artifacts/bench/standalone/profile-50k-final-gap-check/bench-report-native.json`

Expected outcome: no strategy regression, no reintroduced timeout/unsupported rows.

### 8. Leave room for future optimizer work

After decisions are centralized, future work can add:

- repeated subexpression detection and row-source reuse
- subquery physical preference propagation
- cardinality/step/lookback estimate inputs
- cost-based choice between native-grid, sparse direct aggregate, local execution, and subtree pushdown
- explainable CBE reports

Do not implement these in the first refactor unless explicitly scoped.

## Risks

- Moving decision code can silently change which renderer branch handles a shape.
- Strategy names can imply semantics they do not guarantee; keep names physical and precise.
- Over-generalizing sparse bucket logic can break overlapping or offset windows.
- Mixing logical rewrites and physical decisions can make correctness harder to reason about.

## Validation commands

Minimum before commit:

```bash
go test ./internal/promshim/storage ./internal/promshim/native/renderer ./internal/promshim/logical ./internal/promshim/local ./internal/promshim/native
```

If renderer behavior changed materially:

```bash
./scripts/run-compliance.sh
```

Focused benchmark examples:

```bash
./scripts/run-bench.sh \
  --corpus /tmp/slow-rate-corpus.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/physical-strategy-refactor \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer \
  --routing-policies strict \
  --include-prom true \
  --repeats 1 \
  --warmup 0 \
  --clickhouse-profile summary \
  --matrix
```

Use named artifacts and do not run profile-50k/long-range benchmarks against compliance ports.
