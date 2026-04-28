# Attempt 20260428-binary-root-thread-policy-scoping

## Hypothesis

Global root-level no-thread-cap suppression for any query containing `rate(subquery-over-aggregation)` is over-broad for binary-root shapes. Scoping the suppression away from binary roots should preserve subquery safety metadata on the relevant branch while avoiding forced root-level policy overrides.

## Baseline evidence

Mixed-root query:

```promql
sum(avg_over_time(up[1h])) + sum(rate((sum by (job) (up))[5m:1m]))
```

Before this change (baseline mapping attempt), explain showed root-level decision:

- `data.plan query_settings=no_thread_cap`
- reason `subquery_rate_over_aggregate_regresses_with_thread_cap`

Baseline artifact:

- `harness/artifacts/explain/20260428-mixed-root-subquery-thread-policy-baseline/`

## Implementation

Changed `internal/promshim/native/renderer/thread_cap_policy.go`:

- `suppressThreadCapForPlan` now skips global no-cap override when root node is a binary plan.
- Keeps existing no-cap override for non-binary roots.
- Retains branch-local subquery/rate suppression through existing range-lowering path.

Updated regression expectations in `internal/promshim/local/planner_test.go`:

- binary-root subquery shapes no longer require a root `query_settings` decision.
- non-binary subquery-rate-over-aggregation still expects `query_settings=no_thread_cap`.

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Measurement for the claim

Rebuilt benchmark promshim to avoid stale-code artifacts:

```bash
(cd harness/bench && docker compose up -d --build promshim)
```

Post-change explain artifacts:

- Mixed root: `harness/artifacts/explain/20260428-mixed-root-thread-policy-after-rebuild/`
  - `query_settings=no_thread_cap` moved from root to subquery branch node path (`data.plan.children.1.children.0.children.0`).
- Nested binary: `harness/artifacts/explain/20260428-nested-binary-thread-policy-after-rebuild/`
  - `query_settings=no_thread_cap` appears on subquery branch node path (`data.plan.children.0.children.0`).

This confirms bounded behavior shift from root-global to branch-local decision surfacing for binary roots.

## Decision

Keep.

This is a narrow behavior change aligned with the subquery propagation pivot, with preserved correctness validation and explicit before/after evidence.
