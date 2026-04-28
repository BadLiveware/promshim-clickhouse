# Attempt 20260428-cbe-shadow-scorecard

## Hypothesis

A compact pre/post-warm-up scorecard can quantify decision-quality behavior for the first controlled shadow branch and guide whether to keep/tighten/expand.

## Scope

Representative matrix (4 query families × 2 policies):

- `subquery`: `rate(up[5m])[30m:1m]`
- `rate`: `rate(up[5m])`
- `binary`: `rate(up[5m]) + rate(up[5m])`
- `selector`: `up{job="api"}`
- policies: `cost_shadow`, `cost_prefer`

## Artifacts

- `harness/artifacts/explain/20260428-iter72-shadow-scorecard/scorecard.json`
- `harness/artifacts/explain/20260428-iter72-shadow-scorecard/summary.json`
- `harness/artifacts/explain/20260428-iter72-shadow-scorecard/summary.md`

## Summary

| phase | rows | fresh_rate | advisory_rate | missing_rate | shadow_local_rate |
|---|---:|---:|---:|---:|---:|
| cold | 8 | 0.75 | 0.62 | 0.25 | 0.50 |
| warm | 8 | 1.00 | 0.50 | 0.00 | 0.75 |

Interpretation:

- Warm-up eliminated missing estimates in this mini-corpus (`missing_rate 0.25 → 0.00`).
- Shadow local-candidate interpretation increased (`shadow_local_rate 0.50 → 0.75`) as freshness improved.
- Advisory rate dropped slightly because missing-estimate advisories disappeared post-warm while other advisories remained where expected.

## Decision

Keep (measurement/evidence).

The first controlled branch shows coherent state-dependent behavior with improved readiness post-warm. This supports keeping the branch and proceeding with tightly scoped next-step experiments, still under strategy-neutral constraints.
