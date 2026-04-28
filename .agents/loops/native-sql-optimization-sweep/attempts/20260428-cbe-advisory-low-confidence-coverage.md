# Attempt 20260428-cbe-advisory-low-confidence-coverage

## Hypothesis

After adding low-confidence advisory hints, we should verify coverage across more than one low-confidence reason so advisory diagnostics remain consistent and actionable.

## Implementation

Added/updated routing-policy regression coverage in `internal/promshim/routing_policy_test.go`:

- `TestCostPreferRequiresFamilyGate` (existing) continues to assert
  - `low_confidence_reason=family_gate_disabled`
- new `TestCostPreferAddsCandidateServingDisabledAdvisory` asserts
  - decision/reason: `strict_low_confidence` + `candidate_serving_disabled`
  - advisory contains `low_confidence_reason=candidate_serving_disabled`

No routing behavior changes.

## Validation

```bash
go test ./internal/promshim -run 'TestCostPreferRequiresFamilyGate|TestCostPreferAddsCandidateServingDisabledAdvisory|TestCostShadowDecisionStaysStrictOnMissingEstimate|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesSubqueryEstimateInputs|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: advisory diagnostics coverage hardening only; no routing/runtime behavior change.

Evidence: low-confidence advisory hints are now tested for multiple reasons, improving consistency confidence.

## Decision

Keep.

This strengthens bounded advisory decision-quality coverage ahead of any controlled routing behavior experiments.
