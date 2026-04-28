# Attempt 20260428-cbe-controlled-subquery-shadow-candidate

## Hypothesis

Within the scoped readiness boundary, we can run the first controlled behavior experiment by allowing a **shadow-only** local candidate for low-complexity, fresh-estimate subquery shapes, while keeping served strategy unchanged.

## Implementation

Updated `internal/promshim/routing_policy.go`:

1. Added a bounded cap-bypass helper for shadow mode only:
   - `allowSubqueryShadowCapBypass(...)`
   - applies only when:
     - family is `subquery`
     - `HasSubquery=true`
     - estimate state is fresh (`Fresh=true`, no missing/stale)
     - `SubqueryComplexityBand == "light"`
     - only cap hit is `subquery`

2. Applied bypass in cost-model decision path for `RoutingPolicyCostShadow` only.

3. Extended local candidate eligibility to include bounded subquery instant family:
   - `localCandidateFamily`: adds `subquery` with strict shape constraints.

4. Added provisional subquery family cost baseline in `familyBases` for candidate-cost comparability.

## Validation

Added and ran tests:

- `TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate`
- existing advisory/low-confidence/query-explain tests

```bash
go test ./internal/promshim -run 'TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate|TestCostPreferAddsCandidateServingDisabledAdvisory|TestCostPreferRequiresFamilyGate|TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: controlled behavior experiment in **shadow candidate selection only**.

Evidence:

- new test verifies decision remains `shadow_only` with `WouldSelect=local` under bounded fresh/light subquery conditions.
- selected/served strategy behavior remains unchanged by policy design for shadow mode.

## Decision

Keep.

This is the first bounded behavior experiment after readiness gating: it changes candidate interpretation in shadow mode for a narrowly constrained subquery case without changing served strategy.
