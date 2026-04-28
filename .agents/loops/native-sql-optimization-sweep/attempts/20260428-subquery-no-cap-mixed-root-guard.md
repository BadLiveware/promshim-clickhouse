# Attempt 20260428-subquery-no-cap-mixed-root-guard

## Hypothesis

When a query contains both:
- a shape that can request thread-cap guardrails, and
- a `rate(subquery-over-aggregation)` shape that must preserve no-thread-cap,

the final physical preference should remain `query_settings=no_thread_cap`.

## Baseline evidence

`ch-explain` for:

```promql
sum(avg_over_time(demo_cpu_usage_seconds_total[1h])) + sum(rate((sum by (job) (demo_cpu_usage_seconds_total))[5m:1m]))
```

(range mode, prefer, strict) reports:

- `query_settings=no_thread_cap`
- reason: `subquery_rate_over_aggregate_regresses_with_thread_cap`

Artifact:

- `harness/artifacts/explain/20260428-subquery-no-cap-mixed-scalar-before/`

## Implementation

No runtime behavior change.

Added planner/explain regression coverage that locks no-cap precedence in a mixed-root shape:

- `subquery no-cap suppresses thread-cap candidates in mixed root`

File changed:

- `internal/promshim/local/planner_test.go`

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Decision

Keep.

This is a bounded subquery-preference propagation guardrail that preserves known-safe execution preference behavior as we pivot toward deeper subquery-focused optimization work.
