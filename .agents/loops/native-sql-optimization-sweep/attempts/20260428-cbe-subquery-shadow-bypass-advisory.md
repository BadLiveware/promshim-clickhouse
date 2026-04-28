# Attempt 20260428-cbe-subquery-shadow-bypass-advisory

## Hypothesis

For the bounded subquery `cost_shadow` behavior branch, we should expose an explicit advisory when the subquery hard-cap bypass path is taken so decision-quality evidence is easier to audit.

## Implementation

Updated routing behavior diagnostics in `internal/promshim/routing_policy.go`:

- when `cost_shadow` uses the guarded subquery cap-bypass path, append advisory:
  - `shadow_subquery_cap_bypass=subquery`

Updated regression coverage:

- `internal/promshim/routing_policy_test.go`
  - `TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate` now asserts bypass advisory is present.

## Validation

```bash
go test ./internal/promshim -run 'TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate|TestCostPreferAddsCandidateServingDisabledAdvisory|TestCostPreferRequiresFamilyGate|TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement / evidence

Captured live explain artifacts (after rebuild + warm-up):

- `harness/artifacts/explain/20260428-iter68-subquery-shadow-advisory-warm.json`

Observed for subquery + cost_shadow:

- `decision=shadow_only`
- `wouldSelect=local`
- `advisory` includes:
  - `subquery_complexity=light`
  - `shadow_subquery_cap_bypass=subquery`

This confirms the controlled behavior branch plus explicit bypass rationale at runtime.

## Decision

Keep.

This improves interpretability of the first behavior experiment without changing served strategy behavior.
