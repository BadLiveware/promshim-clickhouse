# Attempt 20260428-subquery-no-cap-rejected-alternative

## Hypothesis

For subquery-driven no-thread-cap decisions, explain output should explicitly show the suppressed thread-cap alternative so preference precedence is auditable during upcoming subquery-preference propagation work.

## Baseline evidence

Query:

```promql
rate((sum by (job) (demo_cpu_usage_seconds_total))[5m:1m]) + on(job) up{job="demo"}
```

Before decision metadata showed only:

- `query_settings=no_thread_cap`
- reason: `subquery_rate_over_aggregate_regresses_with_thread_cap`
- guards: `preserve_no_cap`
- rejected: empty

Baseline artifact:

- `harness/artifacts/explain/20260428-subquery-rate-agg-binary-before/`

## Implementation

Updated thread-preference decision rendering for `no_thread_cap` to include an explicit rejected alternative:

- `set_max_threads:suppressed by no-thread-cap preference`

Also made no-cap decision reason robust when unset by defaulting to `preserve_no_cap`.

Files changed:

- `internal/promshim/native/physical/decision.go`
- `internal/promshim/native/physical/preferences_test.go`

## After evidence

After artifact:

- `harness/artifacts/explain/20260428-subquery-no-cap-rejected-after/`

Now reports:

- `query_settings=no_thread_cap`
- rejected: `set_max_threads:suppressed by no-thread-cap preference`

Runtime posture unchanged (same query shape):

- before: `query_duration_ms=194`, `memory_usage=94328803`, `function_execute=682`
- after:  `query_duration_ms=199`, `memory_usage=94297395`, `function_execute=682`

## Validation

```bash
go test ./internal/promshim/native/physical ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Decision

Keep.

This improves explainability and preference-precedence transparency with no semantic or routing change.
