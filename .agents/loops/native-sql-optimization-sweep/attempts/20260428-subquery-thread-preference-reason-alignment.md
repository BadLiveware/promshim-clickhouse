# Attempt 20260428-subquery-thread-preference-reason-alignment

## Hypothesis

After adding subquery-node thread-preference decision surfacing, reason-code drift between renderer-applied decisions and explain-annotated child decisions can cause confusing diagnostics. Unifying reason codes through shared physical constants should improve explain consistency with zero runtime behavior change.

## Baseline evidence

Query:

```promql
rate((sum by (job) (up))[30m:1m])
```

Recent attempts showed root-level `query_settings=no_thread_cap` reason coming from renderer policy (`subquery_rate_over_aggregate_regresses_with_thread_cap`), while subquery-node explain annotation used a separate synthetic reason string.

Sanity artifact captured this iteration:

- `harness/artifacts/explain/20260428-subquery-node-reason-alignment/`

## Implementation

Introduced shared thread-preference reason constants in physical preferences and switched both runtime policy and explain annotation to them.

Changed files:

- `internal/promshim/native/physical/preferences.go`
  - add exported constants:
    - `ThreadPreferenceReasonDirectRangeAggregation`
    - `ThreadPreferenceReasonFusedRateAggregation`
    - `ThreadPreferenceReasonSubqueryRateRows`
- `internal/promshim/native/renderer/thread_cap_policy.go`
  - replace local hardcoded reason strings with shared physical constants.
- `internal/promshim/local/explain.go`
  - subquery-node explain annotation now uses `physical.ThreadPreferenceReasonSubqueryRateRows`.
- `internal/promshim/local/planner_test.go`
  - update subquery-node reason assertion to shared constant.
- `internal/promshim/native/physical/preferences_test.go`
  - update test fixture to shared constant.

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Measurement for the claim

Claim type: explain/metadata consistency only.

Evidence:

- Unit tests pass across local + renderer + physical packages.
- Explain artifact generation remains healthy for representative subquery shape.
- No SQL-shape/performance claim in this attempt.

## Decision

Keep.

This is a low-risk consistency improvement that reduces diagnostics drift before propagation-behavior changes.
