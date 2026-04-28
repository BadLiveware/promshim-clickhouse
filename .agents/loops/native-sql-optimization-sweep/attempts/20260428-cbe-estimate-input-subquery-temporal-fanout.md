# Attempt 20260428-cbe-estimate-input-subquery-temporal-fanout

## Hypothesis

To make subquery estimate diagnostics more interpretable for later CBE, add a compact derived temporal fanout indicator from existing fields, without changing routing behavior.

## Implementation

Extended `httpapi.QueryCostClass` with:

- `subqueryTemporalFanout`

Derived during cost classification when subquery range/step are known:

- `subqueryTemporalFanout = subqueryPointsPerEval * max(rangePointsPerSeries, 1)`

This estimates how many subquery time-slices are traversed across query evaluation granularity.

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case asserts `SubqueryTemporalFanout == 31` for `rate(up[5m])[30m:1m]` in instant endpoint context.
- `internal/promshim/service_test.go`
  - `TestQueryExplainIncludesSubqueryEstimateInputs` now asserts API explain includes `subqueryTemporalFanout`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation-only estimate enrichment; no routing/runtime behavior changes.

Evidence: classifier and API regression tests validate stable surfacing of the new field.

## Decision

Keep.

This continues the reflection-46 plan by deriving useful complexity diagnostics from existing metadata rather than adding unrelated raw fields.
