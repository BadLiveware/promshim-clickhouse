# Attempt 20260428-direct-agg-threadcap-decision-guard

## Hypothesis

After surfacing thread-cap setting decisions in fused/direct aggregation paths, direct range aggregation should have explicit regression coverage so `query_settings=set_max_threads` remains visible in explain output.

## Baseline evidence

Query:

```promql
sum by (job) (up)
```

Range-mode explain shows:

- `query_settings=set_max_threads`
- reason: `direct_range_aggregation_cpu_guardrail`
- guard: `asof_guardrail`

Baseline artifact:

- `harness/artifacts/explain/20260428-direct-agg-threadcap-check/`

## Implementation

No runtime behavior change.

Added planner/explain regression coverage for direct range aggregation thread-cap decision:

- `direct range aggregation applies thread-cap guardrail setting`

File changed:

- `internal/promshim/local/planner_test.go`

## Validation

```bash
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/storage ./internal/promshim
```

All passed.

## Decision

Keep.

This locks the newly surfaced query-settings decision contract for direct aggregation and prevents explainability regressions.
