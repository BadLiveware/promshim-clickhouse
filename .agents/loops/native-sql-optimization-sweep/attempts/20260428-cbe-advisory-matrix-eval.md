# Attempt 20260428-cbe-advisory-matrix-eval

## Hypothesis

A bounded decision-quality check should show advisory metadata behavior is consistent across representative query families/policies while selected/served strategy remains unchanged.

## Evaluation matrix

Captured `query_explain` routing snapshots for representative families under `cost_shadow` and `cost_prefer`:

- subquery: `rate(up[5m])[30m:1m]`
- rate: `rate(up[5m])`
- binary: `rate(up[5m]) + rate(up[5m])`
- selector: `up{job="api"}`

Artifacts:

- `harness/artifacts/explain/20260428-iter57-advisory-matrix/advisory-matrix.json`
- `harness/artifacts/explain/20260428-iter57-advisory-matrix/advisory-matrix.md`

## Findings

- Selected/strict strategies remained unchanged for all sampled rows (good).
- Live matrix showed `advisory` empty for all rows, including cases where repository tests expect advisory hints for missing estimates/subquery complexity.
- Repository tests validating advisory generation passed:
  - `TestCostShadowDecisionStaysStrictOnMissingEstimate`
  - `TestCostShadowAddsSubqueryComplexityAdvisory`

## Interpretation

This indicates an environment/runtime mismatch for the live explain matrix capture (likely stale running service binary) rather than a source-level logic failure.

## Decision

Split/defer.

Decision-quality evaluation is incomplete until matrix capture reflects the current code build. Next iteration should rerun the matrix against a rebuilt/restarted target service and confirm advisory presence/consistency there.
