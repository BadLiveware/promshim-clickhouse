# Attempt 20260428-cbe-subquery-shadow-runtime-branch-evidence

## Hypothesis

After adding activation and blocked-path advisory diagnostics for the bounded subquery shadow branch, runtime captures should clearly demonstrate branch behavior transitions under cold vs warmed estimate states.

## Execution

1. Rebuilt benchmark runtime:

```bash
(cd harness/bench && docker compose up -d --build promshim)
```

2. Captured `cost_shadow` explain for subquery shape before warm-up:

- `harness/artifacts/explain/20260428-iter70-subquery-shadow-blocked.json`

3. Warmed the same query via `/api/v1/query` requests, then captured explain again:

- `harness/artifacts/explain/20260428-iter70-subquery-shadow-bypass.json`

## Observed behavior

- **Cold state**:
  - `decision=strict_missing_estimate`
  - `wouldSelect=native_sql`
  - advisory includes:
    - `subquery_complexity=light`
    - `missing_estimates=selector_stats`

- **Warmed state**:
  - `decision=shadow_only`
  - `wouldSelect=local`
  - advisory includes:
    - `subquery_complexity=light`
    - `shadow_subquery_cap_bypass=subquery`

## Interpretation

Runtime evidence now clearly shows the controlled behavior branch transition as estimate freshness changes, with strategy neutrality preserved.

(Explicit `shadow_subquery_cap_bypass_blocked=...` advisories remain specific to hard-cap blocked paths and are currently covered by unit tests; this runtime scenario exercised missing-estimate and bypass-activated paths.)

## Decision

Keep.

This provides concrete runtime confirmation that the first controlled behavior branch behaves as designed under both pre- and post-warm states.
