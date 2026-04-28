# Attempt 20260428-rate-sum-collapsed-tags-guard-rejected

## Hypothesis

For `rate(sum(...)[5m:])` hotspot shapes, a collapsed-tag-set specialization in range rows fast path could reduce grouping overhead.

## Candidate tested

Prototype change attempted to route subquery `rate` fast-path SQL generation through a collapsed-tag-set grouping variant when aggregation had no grouping labels.

## Validation/measurement findings

- Focused tests passed with the prototype, but explain/SQL inspection showed the targeted `rate(sum(...)[5m:])` path does **not** use `buildRangeFunctionOverRowsSQL` (it remains on the windowed-rate path).
- Benchmark run for a focused one-query corpus (`harness/corpus/iteration38-cand0242.json`) stayed near baseline noise and provided no corroborating signal of path change.
- SQL evidence from `harness/artifacts/explain/20260428-iter38-cand0242-after/` confirmed unchanged `rate` shape characteristics.

## Decision

Reject/defer and revert prototype.

Reason: attempted optimization touched a path that is not active for the target hotspot (`rate`), so it is low-EV/no-op for the current objective.

## Next step

Target the actual `rate` windowed path in a bounded design-backed change, rather than extending non-rate rows fast-path machinery.
