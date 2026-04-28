# Attempt 20260428-cbe-estimate-input-subquery-complexity-band

## Hypothesis

After surfacing raw/derived subquery estimate inputs, a compact qualitative complexity band can make diagnostics more actionable for later CBE experiments without changing routing.

## Implementation

Extended `httpapi.QueryCostClass` with:

- `subqueryComplexityBand`

Added classifier helper:

- `classifySubqueryComplexityBand(workUnits, temporalFanout)`
- derives one of: `light`, `moderate`, `elevated`, `heavy`
- currently based on max(workUnits, temporalFanout) threshold bands

Integrated into `classifyQueryCost` when subquery range/step exist.

Updated tests:

- `internal/promshim/query_cost_class_test.go`
  - subquery case asserts `SubqueryComplexityBand == "light"`
- `internal/promshim/service_test.go`
  - `TestQueryExplainIncludesSubqueryEstimateInputs` now asserts API explain includes `subqueryComplexityBand`.

## Validation

```bash
go test ./internal/promshim -run 'TestClassifyQueryCostFamilies|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: instrumentation/diagnostic enrichment only. No routing/runtime behavior changes.

Evidence: classifier + API explain regression tests verify stable surfacing.

## Decision

Keep.

This completes the reflection-46 goal to derive more interpretable subquery complexity diagnostics from existing estimate metadata.
