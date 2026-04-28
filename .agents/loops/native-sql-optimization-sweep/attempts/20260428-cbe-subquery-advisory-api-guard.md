# Attempt 20260428-cbe-subquery-advisory-api-guard

## Hypothesis

After introducing advisory metadata consumption in cost-policy routing, we should lock API-level explain visibility so advisory behavior cannot silently regress while selected/served strategy behavior remains unchanged.

## Implementation

Extended service explain regression coverage in `internal/promshim/service_test.go`:

- `TestQueryExplainIncludesSubqueryEstimateInputs` now also asserts:
  - `routing.advisory` contains `subquery_complexity=light`

Existing assertions continue to cover subquery estimate fields and complexity band.

## Validation

```bash
go test ./internal/promshim -run 'TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory metadata contract guard only (no routing behavior change).

Evidence: API-level test now verifies end-to-end advisory surfacing in query_explain output.

## Decision

Keep.

This hardens the first CBE diagnostics-consumption slice by ensuring advisory transparency remains stable at the externally consumed explain endpoint.
