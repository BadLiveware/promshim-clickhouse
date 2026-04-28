# Attempt 20260428-cbe-estimate-input-subquery-overlap

## Hypothesis

For later CBE planning, subquery overlap pressure should be explicit in routing cost class metadata. A derived `subqueryOverlapSlots` field can expose this without any routing change.

## Implementation

Extended `httpapi.QueryCostClass` with:

- `subqueryOverlapSlots`

Derived in cost classification when subquery range and step are known:

- `SubqueryOverlapSlots = float64(SubqueryRangeMS) / float64(SubqueryStepMS)`

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case asserts `SubqueryOverlapSlots == 30` for `rate(up[5m])[30m:1m]`
- `internal/promshim/service_test.go`
  - `TestQueryExplainIncludesSubqueryEstimateInputs` now asserts API explain includes `subqueryOverlapSlots`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation-only estimate-input surfacing; no routing/runtime behavior claim.

Evidence: classifier and API-level tests pass with stable field values.

## Decision

Keep.

This incrementally enriches CBE-oriented estimate observability while preserving current behavior.
