# Attempt 20260428-reflection41-pivot-scope

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Delivered and locked binary-root thread-policy scoping behavior with planner/service explain guards.
   - Restored measurement reliability (corpus validation + bench repro hygiene) and captured repeatable hotspot artifacts.
   - Ran multiple bounded runtime prototypes on the top subquery hotspots and rejected low-signal/no-op candidates with evidence.

2. **What's working well?**
   - Fast fail/revert loop is preventing speculative regressions from landing.
   - Evidence quality is high: each candidate now has before/after explain and focused benchmark/profile context.
   - The loop is correctly enforcing corroborating non-wall-clock signals before accepting runtime claims.

3. **What's not working or blocking progress?**
   - Current hotspot candidates are yielding weak/noisy deltas for small SQL-shape adjustments.
   - We are spending more iterations disproving micro-optimizations than landing meaningful runtime wins.

4. **Should the approach be adjusted?**
   - Yes. Narrow to one stronger expected-value branch and stop testing adjacent micro-variants in parallel.
   - Prefer changes that can plausibly reduce window-materialization work class-wide, or else move to a different high-cost family with clearer headroom.

5. **What are the next priorities?**
   - Pick a single next branch with explicit "accept/reject" thresholds before coding.
   - If no branch clears expected-value bar from existing evidence, declare this hotspot tranche exhausted for now and pivot to the next family in the loop hypotheses.
   - Keep harness/docs churn minimal unless it unblocks a concrete runtime claim.

## Decision

Keep (reflection + scope-tightening pivot, no code change).
