# Subquery hotspot design: `rate(sum(...)[5m:])`

## Target shape

Representative query family:

```promql
rate(sum(harness_requests_total{...})[5m:])
```

Current lowered shape (range mode) is dominated by:

1. subquery grid expansion
2. ASOF join over `timeSeriesData`
3. per-step window slicing with array materialization
4. rate calculation over window arrays

This path is correct but expensive for high-cardinality selectors that collapse to a no-group-label aggregation.

## Constraints

- Preserve Prometheus-visible semantics for `rate` and `sum` over counters.
- Keep explain/physical-decision contracts reviewable.
- Avoid silent strategy fallback regressions.
- Maintain current no-thread-cap guard behavior for subquery-over-aggregation family unless explicitly changed and revalidated.

## Candidate alternatives

### A) Constant-tag aggregation specialization (preferred first)

When `sum(...)` has no grouping labels and downstream tags collapse to a constant empty tuple array:

- detect this shape in planning/lowering metadata
- avoid generic tag-grouping pipeline where possible
- render a narrower SQL path with a constant tag payload and fewer per-row tag operations

**Expected signals**
- lower `queryDurationP50Ms` and `real_time_us`
- reduced memory (`memoryP50Bytes`)
- unchanged strategy (`native_sql`)

**Risk**
- medium: requires careful handling of metric-name stripping and empty-tag output contract

### B) Subquery-rate over aggregated rows streaming rate kernel

Introduce a dedicated SQL kernel for `rate` over aggregated rows produced by subquery child, minimizing repeated array transforms.

**Expected signals**
- lower function-execute counts
- lower user/system time

**Risk**
- high: touches rate semantics and may require broad fixture/compliance checks

### C) Physical-decision-only refinement (fallback)

No runtime SQL shape change; only improve branch decision surfacing.

**Expected signals**
- none (observability only)

**Risk**
- low, but not aligned with current runtime-priority goal

## Chosen first implementation slice

Start with **A (constant-tag aggregation specialization)** in a narrow guarded branch.

Guard conditions (initial):
- range-mode `rate` over subquery
- subquery child is aggregation with no `by`/`without` labels (single collapsed output tag set)
- existing native_sql lowering already selected

## Validation matrix

1. **Correctness/tests**
   - targeted renderer tests for new branch SQL shape
   - existing local planner explain tests still pass
   - targeted service explain tests for query_settings/decision placement still pass
2. **Runtime evidence**
   - focused bench corpus includes `draft_cand_0242...` plus adjacent subquery rows
   - compare before/after:
     - `shim p50`
     - `queryDurationP50Ms`
     - `memoryP50Bytes/P95`
     - `realTimeMicrosecondsP50`
     - `functionExecuteP50`
3. **Rollback criteria**
   - any correctness regression or explain contract drift not explicitly intended
   - runtime signals flat/noisy (<~2% with no corroborating metric movement)

## Scope boundary

This design slice does not include broad rate-kernel rewrites or CBE routing changes. It is a single guarded SQL-shape specialization for one hotspot family.
