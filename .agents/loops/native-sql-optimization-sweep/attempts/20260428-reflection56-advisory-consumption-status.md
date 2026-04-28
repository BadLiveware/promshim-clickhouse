# Attempt 20260428-reflection56-advisory-consumption-status

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed and validated a full subquery estimate-diagnostics tranche (raw + derived fields).
   - Landed first consumption slice in advisory/shadow routing metadata.
   - Added API and policy guards proving advisory transparency while keeping selected/served strategy unchanged.

2. **What's working well?**
   - The current tranche model (instrumentation → advisory consumption → guards) is reducing risk and improving reviewability.
   - Regression coverage is strong across classifier, routing policy, and service explain surfaces.

3. **What's not working or blocking progress?**
   - We still have not run a bounded *decision-quality* evaluation showing that advisory diagnostics improve triage outcomes in practice.
   - Risk of continuing metadata polish without proving operational value.

4. **Should the approach be adjusted?**
   - Yes. Move from advisory field expansion to a compact decision-quality check: evaluate representative query families and confirm advisory outputs are present, consistent, and action-oriented.

5. **What are the next priorities?**
   - Run a small explain/routing artifact sweep over representative families (subquery, binary, non-subquery) under cost policies.
   - Verify advisory behavior matrix (present/absent, missing-estimate hints, complexity hints) matches expectations.
   - If the matrix is stable, propose the first controlled behavior experiment boundary; if not, fix advisory consistency gaps.

## Decision

Keep (reflection + execution-focus shift, no code change).
