# 13 — Keep local: `quantile_over_time`

## Decision

`quantile_over_time` remains on **path 3 (local execution)** for now.
It is an explicit **keep-local** decision, not an untracked omission.

## Why this stays local

1. **Semantic risk is high relative to current value**
   - `quantile_over_time` is sensitive to raw-sample window contents,
     NaN handling, ordering, and edge inclusion.
   - A native SQL lowering would need to match the existing local oracle's
     behavior over both direct range selectors and subquery-produced
     matrices.
   - The current native Phase 6 subset already covers the higher-value
     aggregate-over-time and counter/rate-family operators used more often
     in promoted corpus work.

2. **Current dashboard/rollout value is low compared to cost**
   - The dashboard-focused shortlist has only a small `range-function`
     slice, and `quantile_over_time` is not currently a high-leverage
     blocker for the promoted native rollout path.
   - Keeping it local does not block native lowering of the more valuable
     direct-selector and subquery-backed range/counter subset already in
     scope.

3. **The local implementation is already a good semantic oracle**
   - `internal/promshim/exec/rangefunc.go` implements the current behavior.
   - tests in `internal/promshim/exec/rangefunc_test.go` and harness rows in
     `harness/corpus/queries.json` already exercise the local path.
   - Until there is a strong reason to ship native lowering, the local path
     is the lower-risk place to preserve correctness.

## Scope consequence

- `quantile_over_time` does **not** count as missing native-lowering work
  for the current supported native Phase 6 subset.
- It is exempted from the "replace every path-3 range/counter operator"
  expectation by this explicit keep-local note.
- Explain and analysis should report it as intentionally local rather than
  "not yet implemented" in a vague sense.

## Revisit triggers

Revisit this decision only if at least one of these becomes true:
- the common dashboard differential subset shows `quantile_over_time` as a
  material/high-frequency family,
- ClickHouse gains an upstream primitive that makes exact window-quantile
  lowering straightforward and reviewable,
- or rollout data shows that keeping it local is a meaningful performance
  bottleneck.

## Oracle / fallback policy

- the local implementation remains the semantic authority in-repo
- delegated PromQL / Prometheus remain the external oracle where practical
- if native lowering is ever attempted later, it must ship with explicit
  differential tests against the current local implementation before any
  promotion
