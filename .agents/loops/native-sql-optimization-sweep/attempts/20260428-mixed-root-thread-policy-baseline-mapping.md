# Attempt 20260428-mixed-root-thread-policy-baseline-mapping

## Hypothesis

A bounded behavior-oriented subquery preference propagation attempt should start from mixed-root thread-policy evidence, because global no-thread-cap suppression may mask branch-specific thread-cap opportunities.

## Baseline evidence

Query (range mode):

```promql
sum(avg_over_time(up[1h])) + sum(rate((sum by (job) (up))[5m:1m]))
```

Captured with:

```bash
scripts/ch-explain.sh 'sum(avg_over_time(up[1h])) + sum(rate((sum by (job) (up))[5m:1m]))' \
  --mode range --range-seconds 3600 --step 30 \
  --eval-time 2026-03-14T21:45:42Z \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 --ch-user default --ch-pass otel \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/explain/20260428-mixed-root-subquery-thread-policy-baseline
```

Observed:

- root strategy: `native_sql`
- physical decision: only `query_settings=no_thread_cap`
- reason: `subquery_rate_over_aggregate_regresses_with_thread_cap`

Artifacts:

- `harness/artifacts/explain/20260428-mixed-root-subquery-thread-policy-baseline/`

## Implementation

No code change this iteration.

## Validation/measurement

This is a behavior-attempt preparation slice. It produced baseline structure needed for next-step runtime comparison and did not claim a runtime improvement.

## Decision

Split/defer.

Next attempt should implement one minimal behavior variant (scoped thread-cap policy propagation for mixed-root families), then measure before/after query-log/ProfileEvents from rebuilt benchmark stack and keep only if regression risk stays controlled.
