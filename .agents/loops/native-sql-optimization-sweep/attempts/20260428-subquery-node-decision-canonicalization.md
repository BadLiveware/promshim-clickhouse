# Attempt 20260428-subquery-node-decision-canonicalization

## Hypothesis

Subquery-node explain annotations should reuse the canonical `ThreadPreferenceDecision` constructor so rejected alternatives and guard semantics stay aligned with runtime thread-preference decisions.

## Baseline

Prior subquery-node surfacing used a handcrafted `physical.Decision` payload in local explain finalization. This risked drift from canonical no-thread-cap decision formatting (especially `rejected` reason text/structure).

## Implementation

- `internal/promshim/local/explain.go`
  - replaced handcrafted subquery-node no-thread-cap decision with:
    - `physical.ThreadPreferenceDecision(ThreadPreference{Mode: ThreadPreferenceNoCap, ReasonCode: ThreadPreferenceReasonSubqueryRateRows})`
  - prepended subquery-specific guard `needs_subquery_step_grid` while preserving canonical no-cap guards/rejected alternative.
- `internal/promshim/local/planner_test.go`
  - strengthened `TestExplainPlanIncludesSubqueryNodeNoThreadCapDecision` assertions for:
    - guard prefix includes `needs_subquery_step_grid`
    - canonical rejected alternative `set_max_threads` + `suppressed by no-thread-cap preference`.

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Measurement for the claim

Claim type: explain metadata canonicalization only (no runtime/SQL claim).

Evidence is test-level structural verification that child-node decision now shares canonical rejected semantics with root/runtime no-cap decision generation.

## Decision

Keep.

Low-risk consistency hardening before further subquery propagation behavior work.
