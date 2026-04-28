# Attempt 20260428-subquery-hotspot-sql-shape-triage

## Hypothesis

For the current highest-latency subquery hotspot (`draft_cand_0242...`), SQL-shape triage can identify a concrete runtime candidate worth implementing next.

## Baseline evidence

Captured explain/SQL for:

```promql
rate(sum(harness_requests_total{job=~"api",namespace=~"blue"})[5m:])
```

Artifacts:

- `harness/artifacts/explain/20260428-iter35-cand0242-baseline/`

Observed shape:

- root + child both report `query_settings=no_thread_cap`
- SQL is dominated by subquery grid expansion + ASOF join + windowed-array rate computation over aggregated rows
- aggregated tags collapse to a constant empty tuple array (`CAST([], 'Array(Tuple(String, String))')`) due `sum(...)` without grouping labels

## Candidate evaluation

Potential optimization ideas from this shape (e.g., constant-tag special-casing, narrower grouped-path SQL, alternate rate-over-rows shape) are non-trivial and likely to alter established range-window/rate physical decision contracts.

Given the previous failed `avg_over_time` rows-fastpath trial and existing explain/contract tests, a safe single-iteration runtime code change here would be speculative without a dedicated design slice.

## Decision

Split/defer (no code change).

This iteration produces concrete SQL-shape evidence and narrows the next runtime candidate to a design-backed implementation plan rather than a risky direct edit.

## Next step

Create a bounded design/implementation slice for the `sum(...)` no-group-label subquery/rate path with explicit correctness constraints and expected profile-event/memory signals before coding.
