# Attempt 20260428-cbe-subquery-bypass-blocked-advisory

## Hypothesis

For the bounded subquery shadow-candidate behavior branch, explainability should cover both activation and blocked cases so operators can tell why a cap bypass did or did not happen.

## Implementation

Extended `internal/promshim/routing_policy.go`:

- when subquery cap bypass path is blocked under `cost_shadow` + hard-cap outcome, attach advisory:
  - `shadow_subquery_cap_bypass_blocked=<reason>`

Implemented reason helper that distinguishes blocked causes (e.g. `complexity_not_light`, stale/missing estimates, non-subquery cap-hit shape).

Updated tests in `internal/promshim/routing_policy_test.go`:

- `TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate` (activation path remains)
- new `TestCostShadowAddsSubqueryBypassBlockedAdvisory` (blocked path advisory asserted)

## Validation

```bash
go test ./internal/promshim -run 'TestCostShadowAllowsLightFreshSubqueryAsShadowCandidate|TestCostShadowAddsSubqueryBypassBlockedAdvisory|TestCostPreferAddsCandidateServingDisabledAdvisory|TestCostPreferRequiresFamilyGate|TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory decision transparency only; no selected/served strategy changes.

Evidence: explicit test coverage now validates both bypass-activated and bypass-blocked advisory branches.

## Decision

Keep.

This completes the first behavior branch’s explainability envelope by making blocked reasons as observable as successful bypass activation.
