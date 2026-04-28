# Attempt 20260428-cbe-estimate-input-subquery-fields

## Hypothesis

A small instrumentation-first step for later CBE is to expose explicit subquery estimate inputs in routing cost class metadata, without changing routing behavior.

## Implementation

Added two fields to `httpapi.QueryCostClass`:

- `subqueryRangeMs`
- `subqueryStepMs`

Populated in `classifyQueryCost` while walking `parser.SubqueryExpr` nodes:

- max observed subquery range (ms)
- max observed explicit subquery step (ms)

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case now asserts `SubqueryRangeMS` and `SubqueryStepMS`.
- `internal/promshim/service_test.go`
  - new `TestQueryExplainIncludesSubqueryEstimateInputs` asserts API explain routing class includes the new fields for `rate(up[5m])[30m:1m]`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation metadata only (no routing/runtime behavior change).

Evidence: API-level explain test verifies the new estimate-input fields are surfaced and stable for a subquery shape.

## Decision

Keep.

This advances the estimate-input hypothesis for later CBE while preserving current routing decisions.
