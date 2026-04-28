# Attempt 20260428-fused-rate-threadcap-decision-surfacing

## Hypothesis

Fused range-rate aggregation already applies a thread-cap guardrail (`max_threads=4`), but explain metadata should explicitly surface that setting decision (`query_settings=set_max_threads`) for observability parity with other physical decisions.

## Baseline evidence

Query:

```promql
sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))
```

Before this attempt:

- explain decisions: empty for `query_settings`
- query-log: `max_threads=4` already present

Baseline artifacts:

- `harness/artifacts/explain/20260428-query-settings-threadcap-rate-before/`

## Implementation

Plumbed thread-cap setting decisions from renderer thread-cap policy helpers into rendered fragment physical decisions for:

- direct range aggregation path
- fused range-rate aggregation path

Added helper to convert computed thread preferences into decision rows and attached those decisions where thread-cap settings are generated.

Also extended planner explain tests to assert `query_settings=set_max_threads` for fused rate aggregation.

Files changed:

- `internal/promshim/native/renderer/thread_cap_policy.go`
- `internal/promshim/native/renderer/aggregation_logical.go`
- `internal/promshim/native/renderer/aggregation_range_fused_logical.go`
- `internal/promshim/local/planner_test.go`

## After evidence

After this attempt for the same query:

- explain decisions now include `query_settings=set_max_threads` with reason `fused_rate_aggregation_cpu_guardrail` and guard `asof_guardrail`
- query-log continues to show `max_threads=4`

After artifact:

- `harness/artifacts/explain/20260428-query-settings-threadcap-rate-after/`

Runtime posture unchanged (observability-only delta):

- before: `query_duration_ms=515`, `memory_usage=242731401`, `function_execute=34572`
- after:  `query_duration_ms=527`, `memory_usage=237883622`, `function_execute=34562`

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Decision

Keep.

This closes an explainability gap by aligning reported `physicalDecisions` with already-applied query settings, without changing semantics or routing.
