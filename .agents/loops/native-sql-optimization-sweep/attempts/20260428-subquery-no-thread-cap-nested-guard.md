# Attempt 20260428-subquery-no-thread-cap-nested-guard

## Hypothesis

As we pivot toward subquery preference propagation, we should lock in existing no-thread-cap behavior when `rate(subquery-over-aggregation)` appears under a larger binary root. This protects current execution preferences while future planner/renderer work expands around subqueries.

## Baseline evidence

`ch-explain` for:

```promql
rate((sum by (job) (demo_cpu_usage_seconds_total))[5m:1m]) + on(job) up{job="demo"}
```

(range mode, prefer, strict) reports:

- `query_settings=no_thread_cap`
- reason: `subquery_rate_over_aggregate_regresses_with_thread_cap`

Artifact:

- `harness/artifacts/explain/20260428-subquery-rate-agg-binary-before/`

## Implementation

No runtime behavior change.

Added planner/explain regression coverage by extending the existing decision test matrix with a nested binary wrapper case:

- `rate(sum by (job) (up)[5m:1m]) + on(job) up`

This asserts `query_settings=no_thread_cap` remains present in `physicalDecisions` even when the subquery shape is not the root node.

File changed:

- `internal/promshim/local/planner_test.go`

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Decision

Keep.

This is a low-risk, high-signal guardrail for upcoming subquery-focused optimization work: it preserves a known-safe execution preference and prevents accidental drift when subquery logic is composed into larger expression roots.
