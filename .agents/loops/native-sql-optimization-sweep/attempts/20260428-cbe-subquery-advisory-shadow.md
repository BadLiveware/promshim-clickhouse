# Attempt 20260428-cbe-subquery-advisory-shadow

## Hypothesis

A bounded advisory/shadow experiment can consume existing subquery complexity diagnostics without changing selected/served strategy.

## Implementation

Added a new optional routing metadata field:

- `routing.advisory[]`

For cost-model policies, populated advisory when subquery complexity band exists:

- `subquery_complexity=<band>`

This is attached in routing decision assembly and does not alter decision/selected/served strategy.

Changed files:

- `internal/promshim/httpapi/router.go`
- `internal/promshim/routing_policy.go`
- `internal/promshim/routing_policy_test.go`

## Validation

```bash
go test ./internal/promshim -run 'TestCostShadow(AddsSubqueryComplexityAdvisory|DecisionStaysStrictOnMissingEstimate)|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory metadata consumption only; no routing behavior change.

Evidence:

- New unit test `TestCostShadowAddsSubqueryComplexityAdvisory` verifies advisory appears while decision semantics remain strict/shadow behavior.
- Existing strict-missing-estimate behavior test continues to pass.

Note: live `query_explain` artifact capture against benchmark stack may not reflect latest local code unless that service is rebuilt/restarted; authoritative validation for this iteration is from repository tests.

## Decision

Keep.

This executes the planned first consumption experiment: diagnostics are now used in advisory routing context while served strategy remains unchanged.
