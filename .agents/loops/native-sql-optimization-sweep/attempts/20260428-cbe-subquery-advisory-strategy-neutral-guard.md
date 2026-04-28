# Attempt 20260428-cbe-subquery-advisory-strategy-neutral-guard

## Hypothesis

Now that advisory metadata is surfaced for subquery complexity, we should explicitly guard the contract that advisory consumption does not change selected/served strategy behavior in explain responses.

## Implementation

Extended `internal/promshim/service_test.go` in `TestQueryExplainIncludesSubqueryEstimateInputs` to assert:

- `routing.advisory` includes `subquery_complexity=light`
- `routing.strictStrategy == native_sql`
- `routing.selectedStrategy == native_sql`

This ensures advisory hints remain informational only.

## Validation

```bash
go test ./internal/promshim -run 'TestQueryExplainIncludesSubqueryEstimateInputs|TestCostShadowAddsSubqueryComplexityAdvisory|TestQueryExplainIncludesRoutingCostClass' -v
go test ./internal/promshim/local ./internal/promshim/native/renderer
```

All passed.

## Measurement for the claim

Claim type: API contract guard for advisory strategy neutrality; no routing behavior change.

Evidence: test now verifies advisory present while strategy selection remains unchanged.

## Decision

Keep.

This tightens the first advisory/shadow consumption slice with an explicit strategy-neutrality guarantee at the API surface.
