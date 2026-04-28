# Attempt 20260428-cbe-controlled-subquery-shadow-evidence

## Hypothesis

After enabling bounded subquery shadow-candidate interpretation, rebuilt-runtime matrix evidence should confirm the intended behavior branch triggers for target shape(s) without changing served strategy.

## Execution

1. Rebuilt benchmark promshim runtime:

```bash
(cd harness/bench && docker compose up -d --build promshim)
```

2. Warmed representative queries.
3. Captured query_explain routing matrix for representative families (`subquery`, `rate`, `binary`, `selector`) across `cost_shadow` and `cost_prefer`.

Artifacts:

- `harness/artifacts/explain/20260428-iter67-behavior-eval-rebuilt/matrix.json`
- `harness/artifacts/explain/20260428-iter67-behavior-eval-rebuilt/matrix.md`

## Findings

Target branch confirmation:

- `subquery + cost_shadow` now yields:
  - `decision=shadow_only`
  - `wouldSelect=local`
  - `strict=native_sql`, `selected=native_sql` (served behavior unchanged)
  - advisory includes `subquery_complexity=light`

Non-target families remain consistent with existing expectations:

- `rate/binary + cost_shadow`: shadow-only local candidate interpretation
- `cost_prefer` rows remain strict-low-confidence or strict-over-cap as before
- selector rows remain strict-low-confidence delegated

## Decision

Keep.

This completes the post-change evidence pass for the first controlled behavior branch and confirms intended bounded impact with strategy neutrality preserved.
