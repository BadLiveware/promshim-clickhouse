# Attempt 20260428-cbe-advisory-matrix-eval-rebuilt

## Hypothesis

Re-running the advisory behavior matrix against a rebuilt runtime should resolve the previous capture mismatch and provide a reliable decision-quality snapshot for advisory consistency.

## Execution

1. Rebuilt/restarted benchmark promshim service:

```bash
(cd harness/bench && docker compose up -d --build promshim)
```

2. Re-ran bounded advisory matrix over representative query families (`subquery`, `rate`, `binary`, `selector`) and policies (`cost_shadow`, `cost_prefer`) via `query_explain`.

Artifacts:

- `harness/artifacts/explain/20260428-iter58-advisory-matrix-rebuilt/advisory-matrix.json`
- `harness/artifacts/explain/20260428-iter58-advisory-matrix-rebuilt/advisory-matrix.md`

## Findings

- Strategy neutrality held across rows (`selectedStrategy` remained aligned with strict behavior where expected).
- Advisory hints now appear consistently where intended:
  - subquery + cost_shadow: `subquery_complexity=light`, `missing_estimates=selector_stats`
  - rate + cost_shadow: `missing_estimates=selector_stats`
- Families/policies without advisory intent remained advisory-empty.

## Decision

Keep.

This completes the bounded advisory decision-quality evaluation tranche: advisory consistency is confirmed on rebuilt runtime, with no selected/served strategy changes.
