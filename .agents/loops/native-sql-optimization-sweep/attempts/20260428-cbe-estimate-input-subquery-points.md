# Attempt 20260428-cbe-estimate-input-subquery-points

## Hypothesis

A useful next estimate-input field for later CBE is subquery points-per-evaluation (`subqueryRangeMs/subqueryStepMs + 1`) surfaced in routing cost class metadata, without altering routing behavior.

## Implementation

Extended `httpapi.QueryCostClass` with:

- `subqueryPointsPerEval`

Populated in cost classification when subquery range and step are known:

- `SubqueryPointsPerEval = (SubqueryRangeMS / SubqueryStepMS) + 1`

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case asserts `SubqueryPointsPerEval == 31` for `rate(up[5m])[30m:1m]`
- `internal/promshim/service_test.go`
  - `TestQueryExplainIncludesSubqueryEstimateInputs` now asserts API explain includes `subqueryPointsPerEval`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation metadata only; no routing/runtime behavior claim.

Evidence: classifier and API-level tests verify stable surfacing of the new derived estimate input.

## Decision

Keep.

This incrementally strengthens estimate-input observability for later CBE planning while keeping behavior unchanged.
