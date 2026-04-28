# Attempt 20260428-binary-root-thread-policy-service-guard

## Hypothesis

After scoping no-thread-cap override away from binary roots, we need API-level regression coverage to lock the new root-vs-branch explain behavior and prevent drift.

## Baseline

Iteration 23 introduced behavior where:

- pure subquery-rate queries can still report root `query_settings=no_thread_cap`
- mixed binary-root queries should report no root query-setting decision while keeping subquery-branch `query_settings=no_thread_cap`

## Implementation

Updated `internal/promshim/service_test.go`:

- extended `TestQueryRangeExplainIncludesSubqueryNodeThreadPreferenceDecision` into a table test with two cases:
  1. pure subquery-rate root (expects root `query_settings` present)
  2. mixed binary root (expects root `query_settings` absent)
- both cases assert subquery node still has:
  - `query_settings=no_thread_cap`
  - reason `physical.ThreadPreferenceReasonSubqueryRateRows`

## Validation

```bash
go test ./internal/promshim -run TestQueryRangeExplainIncludesSubqueryNodeThreadPreferenceDecision -v
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim
```

All passed.

## Measurement for the claim

Claim type: explain/API regression guard only (no runtime perf claim).

Evidence is API-level structural assertions across both pure and mixed query families.

## Decision

Keep.

This locks the bounded behavior change from iteration 23 at the externally consumed explain endpoint.
