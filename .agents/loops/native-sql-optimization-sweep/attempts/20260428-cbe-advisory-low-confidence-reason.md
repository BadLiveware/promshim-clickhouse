# Attempt 20260428-cbe-advisory-low-confidence-reason

## Hypothesis

Advisory/shadow decision transparency should also explain *why* `strict_low_confidence` was selected, without changing selected/served strategy behavior.

## Implementation

Extended routing advisory diagnostics in `internal/promshim/routing_policy.go`:

- added helper: `lowConfidenceAdvisory(reason)`
- for strict-low-confidence exits, attach advisory:
  - `low_confidence_reason=<reason>`

Applied to strict-low-confidence branches including:

- `predicted_win_below_margin`
- `family_gate_disabled`
- `candidate_serving_disabled`
- `strict_reference_already_local`
- `known_divergence`

Updated regression test:

- `internal/promshim/routing_policy_test.go`
  - `TestCostPreferRequiresFamilyGate` now asserts advisory includes
    `low_confidence_reason=family_gate_disabled`.

## Validation

```bash
go test ./internal/promshim -run 'TestCostPreferRequiresFamilyGate|TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory diagnostics enrichment only; no routing behavior change.

Evidence: low-confidence routing test now asserts reason-specific advisory while existing strategy-neutral tests stay green.

## Decision

Keep.

This further improves advisory/shadow explainability and maintains strict/selected strategy neutrality.
