# Attempt 20260428-subquery-node-preference-decision-surfacing

## Hypothesis

A safe first step before any subquery preference propagation behavior change is to surface nested subquery-level thread preference decisions in explain output. This should improve diagnosability without changing runtime SQL strategy.

## Baseline

From prior mapping attempt (`20260428-subquery-preference-propagation-mapping`), query:

```promql
rate((sum by (job) (up))[30m:1m])
```

showed only root-level physical decision metadata:

- `query_settings=no_thread_cap` at `data.plan`
- no child `subquery` node physical decision rows

Baseline artifacts:

- `harness/artifacts/explain/20260428-subquery-pref-propagation-baseline/`

## Implementation

Added explain-only nested decision annotation for subquery nodes that require step-grid behavior:

- `internal/promshim/local/explain.go`
  - add `annotateSubqueryPreferenceDecision()` in explain finalization
  - for `kind=subquery` + `lowering.needsSubqueryStepGrid=true`, append:
    - `kind=query_settings`
    - `strategy=no_thread_cap`
    - reason `subquery_step_grid_prefers_no_thread_cap`
    - guards `needs_subquery_step_grid`, `preserve_no_cap`
    - rejected alternative `set_max_threads`
  - no renderer/runtime planning or SQL lowering changes

Added regression test:

- `internal/promshim/local/planner_test.go`
  - `TestExplainPlanIncludesSubqueryNodeNoThreadCapDecision`

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim

go test -run TestExplainPlanIncludesSubqueryNodeNoThreadCapDecision -v ./internal/promshim/local
```

All passed.

## Measurement for the claim

Claim type: explainability/metadata surfacing (no runtime-performance claim).

Measured via structural explain regression evidence in unit tests:

- before: no subquery-node `physicalDecisions` row in mapped baseline
- after: test asserts subquery node includes `query_settings=no_thread_cap` with explicit reason and guards

Additional `ch-explain` run after code change (against current benchmark stack binary) remains useful for root-level sanity, but node-level surfacing is asserted by repository tests in this attempt.

## Decision

Keep.

This is a low-risk instrumentation slice that improves nested decision transparency and de-risks the next propagation behavior attempt.
