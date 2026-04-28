# Attempt 20260428-cbe-advisory-missing-estimate-hint

## Hypothesis

The advisory/shadow CBE consumption slice should also explain *why* strict fallback occurs when estimates are missing, without changing selected/served strategy behavior.

## Implementation

Extended routing advisory generation for cost-policy decisions:

- when `missingEstimates` is non-empty in cost model decision path, add advisory:
  - `missing_estimates=<comma-separated-fields>`

This is emitted alongside existing advisory hints (e.g., subquery complexity) and does not alter decision selection.

Changed files:

- `internal/promshim/routing_policy.go`
- `internal/promshim/routing_policy_test.go`

## Validation

```bash
go test ./internal/promshim -run 'TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory diagnostics enrichment only; no routing behavior change.

Evidence: missing-estimate strict decision test now also asserts advisory includes `missing_estimates=selector_stats`.

## Decision

Keep.

This improves decision transparency in advisory/shadow mode while preserving strategy neutrality.
