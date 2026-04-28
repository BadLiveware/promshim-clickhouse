# Attempt 20260428-subquery-cost-prefer-expansion-rejected

## Hypothesis

After enabling bounded `cost_shadow` subquery local-candidate interpretation, the next possible expansion would be enabling `cost_prefer` served local override for the same light/fresh subquery family.

## Evidence used

From iteration 74 focused measurement (`rate(up[5m])[30m:1m]`):

- Native (`prefer` / `force_supported`) shim p50: ~12ms
- Local (`off`) shim p50: ~124–128ms
- CH round-trips: native `1` vs local `30`

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter74-subquery-serving-candidate/`

## Evaluation

Given a ~10x latency gap and much higher round-trips for local serving on the target shape, promoting subquery local execution to served behavior in `cost_prefer` would be expected to regress measurable execution resources.

## Decision

Reject/defer expansion.

Do **not** expand subquery behavior from shadow-only candidate interpretation to served local override at this time.

## Next step

Keep current bounded shadow branch and shift measurable behavior experiments to families where local serving has empirical headroom (or where native has measured resource regressions).
