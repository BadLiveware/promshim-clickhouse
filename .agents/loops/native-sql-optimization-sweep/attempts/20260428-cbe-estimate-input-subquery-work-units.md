# Attempt 20260428-cbe-estimate-input-subquery-work-units

## Hypothesis

To make existing subquery estimate inputs more actionable without routing changes, add one compact derived indicator: `subqueryWorkUnits`.

## Implementation

Extended `httpapi.QueryCostClass` with:

- `subqueryWorkUnits`

Derived in query-cost classification when subquery range+step are known:

- `subqueryWorkUnits = subqueryPointsPerEval * max(selectorCount, 1)`

This reuses existing surfaced fields and provides a compact subquery complexity magnitude for explain/routing diagnostics.

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case asserts `SubqueryWorkUnits == 31` for `rate(up[5m])[30m:1m]`
- `internal/promshim/service_test.go`
  - `TestQueryExplainIncludesSubqueryEstimateInputs` asserts API explain includes `subqueryWorkUnits`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation-only diagnostic enrichment; no routing/runtime behavior claim.

Evidence: classifier + API explain tests verify stable derived estimate surfacing.

## Decision

Keep.

This advances the reflection-46 goal by deriving a more interpretable subquery complexity signal from existing estimate fields while preserving current behavior.
