# Attempt 20260428-cbe-estimate-freshness-prep-check

## Hypothesis

A bounded estimate-freshness preparation pass (query warm-up + matrix rerun) may satisfy the remaining readiness-gate item for controlled behavior experiments.

## Preparation performed

Issued repeated query requests for representative shapes to warm selector stats cache:

- `rate(up[5m])[30m:1m]`
- `rate(up[5m])`
- `rate(up[5m]) + rate(up[5m])`
- `up{job="api"}`

Then reran advisory matrix on rebuilt runtime.

Artifacts:

- `harness/artifacts/explain/20260428-iter63-advisory-matrix-warmed/advisory-matrix.json`
- `harness/artifacts/explain/20260428-iter63-advisory-matrix-warmed/advisory-matrix.md`

## Findings

- `cost_prefer` rows for subquery/rate/binary now show `estimateSource=cache, fresh=true` where expected.
- `cost_shadow` for subquery still reports `strict_missing_estimate` with `missing_estimates=selector_stats` (`estimateSource=none, fresh=false`).
- selector family remains strict/none as expected under current strict strategy constraints.

## Decision

Split/defer behavior-experiment entry.

Readiness improved but is not fully satisfied for shadow-path subquery estimate freshness; controlled behavior experiment should wait until this inconsistency is either resolved or explicitly scoped out of the first experiment boundary.

## Next step

Define first behavior experiment boundary to include only policy/family combinations with confirmed fresh estimates, or add a targeted fix for subquery shadow estimate availability.
