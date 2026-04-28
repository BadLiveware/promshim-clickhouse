# Attempt 20260428-reflection66-first-behavior-experiment-status

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed the advisory/diagnostic tranche and moved into the first controlled behavior experiment.
   - Landed a bounded `cost_shadow` behavior change for light/fresh subquery shapes with explicit guardrails.
   - Preserved served-strategy safety while enabling shadow candidate interpretation progress.

2. **What's working well?**
   - The tranche progression is now demonstrably effective: instrumentation → advisory → controlled behavior.
   - Guarded branch design plus focused tests kept risk low and reviewability high.
   - Strategy-neutral constraints prevented accidental user-visible routing flips.

3. **What's not working or blocking progress?**
   - We still need evidence on practical decision-quality impact of the new shadow candidate path (beyond unit-level behavior assertions).
   - Fresh-estimate availability remains uneven by policy/family combinations, which can limit scenario coverage.

4. **Should the approach be adjusted?**
   - Yes: keep behavior scope fixed and run a focused post-change matrix/evidence pass next, rather than immediately adding another behavior branch.

5. **What are the next priorities?**
   - Capture before/after routing matrix slices for the new subquery shadow-candidate condition and verify expected decision/candidate fields.
   - Confirm no unintended effects on non-target families.
   - If stable and valuable, define the next incremental behavior expansion boundary; otherwise revert or tighten guards.

## Decision

Keep (reflection, no code change).
