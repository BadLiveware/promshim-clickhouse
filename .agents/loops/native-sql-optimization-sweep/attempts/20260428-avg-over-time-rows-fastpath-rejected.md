# Attempt 20260428-avg-over-time-rows-fastpath-rejected

## Hypothesis

Enabling range-mode rows fast path for `avg_over_time` (currently enabled for instant mode but not range mode) might reduce memory/runtime for subquery-heavy `avg_over_time(...[...:])` hotspot shapes.

## Candidate change tested

Proposed change:

- `internal/promshim/native/renderer/range.go`
- include `avg_over_time` in `canUseRangeFunctionRowsFastPath`

## Validation outcome

Focused package validation failed immediately after the candidate change:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim
```

Key failures showed expected range-window aggregate behavior/explain decisions no longer matched:

- `TestLowerHighOverlapAvgOverTimeRangeUsesDirectAggregate`
- `TestExplainPlanIncludesSparseRangeWindowPhysicalDecision`
- `TestQueryRangeExplainIncludesPhysicalDecisions`

This indicates the candidate is not a safe drop-in runtime optimization and would require broader semantics/decision-contract adjustments.

## Decision

Reject/defer and revert.

Candidate was reverted in this iteration; no code changes kept.

## Next step

If revisited, split into a larger plan with explicit correctness contract updates (range-window aggregate decision semantics + explain expectations) and dedicated before/after compliance/perf evidence.
