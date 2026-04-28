# Attempt 20260428-cbe-readiness-gate-checklist

## Goal

Apply the readiness-gate pivot from iteration 61 and decide whether to enter the first controlled behavior experiment now.

## Readiness checklist

1. **Advisory consistency across representative families/policies**
   - Status: ✅ met (rebuilt-runtime matrix confirms intended advisory presence/absence patterns).
2. **Strategy-neutral advisory contract verified**
   - Status: ✅ met (API + routing tests assert strict/selected unchanged where advisory is present).
3. **Low-confidence reason transparency**
   - Status: ✅ met (reason-specific advisory coverage exists for multiple strict-low-confidence paths).
4. **Estimate availability sufficient for controlled serving experiment in target family**
   - Status: ❌ not met (subquery paths still frequently land in `strict_missing_estimate` / stale estimate conditions in sampled matrix rows).
5. **Bounded rollback path for first behavior experiment documented**
   - Status: ✅ met (existing loop policy + attempt template supports explicit revert criteria).

## Decision

Split/defer behavior experiment entry this iteration.

Readiness gate is **not fully met** due to estimate availability/freshness for target subquery cases. Entering a behavior experiment now would confound evaluation with missing-estimate strict fallbacks rather than test the intended decision logic.

## Next step

Run a bounded estimate-freshness preparation slice for representative subquery shapes (cache warm-up + re-check matrix) and only then start the first controlled behavior experiment.
