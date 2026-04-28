# Attempt 20260428-cbe-first-behavior-boundary-scope

## Hypothesis

The cleanest way to start the first controlled CBE behavior experiment is to scope it to combinations that already satisfy fresh-estimate readiness, instead of waiting for full cross-policy freshness parity.

## Evidence basis

From the warmed/rebuilt advisory matrix (`20260428-iter63-advisory-matrix-warmed`):

- `cost_prefer` rows for `subquery`, `rate`, `binary` showed fresh cache state (`estimateSource=cache`, `fresh=true`) in representative checks.
- `cost_shadow` subquery still hit `strict_missing_estimate` / `selector_stats` missing.

## Scoped experiment boundary (proposed)

- Include: `cost_prefer` + representative families where estimate freshness is confirmed.
- Exclude: `cost_shadow` subquery rows until selector-estimate freshness parity is resolved.
- Guardrails:
  - no broad family enablement changes
  - explicit rollback condition on any strategy divergence or missing-estimate regressions
  - retain advisory-only traces to compare before/after candidate decisions

## Decision

Keep (scope decision, no code change).

This unblocks entry to a tightly bounded behavior experiment next iteration while respecting readiness criteria and avoiding confounded shadow-path gaps.
