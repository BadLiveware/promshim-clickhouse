# Attempt 20260428-reflection71-shadow-branch-status

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed instrumentation and advisory consumption tranches for subquery-aware CBE diagnostics.
   - Landed the first controlled behavior branch in `cost_shadow` for light/fresh subquery shapes.
   - Added activation + blocked-path advisories and captured runtime evidence showing expected cold→warm transition behavior.

2. **What's working well?**
   - Branch guardrails are effective: behavior change is bounded and served strategy remains unchanged.
   - Explain/routing artifacts now provide actionable, traceable diagnostics for why decisions occur.
   - The loop’s evidence discipline (tests + rebuilt-runtime checks) is preventing ambiguous conclusions.

3. **What's not working or blocking progress?**
   - We have branch-correctness and transparency evidence, but limited quantitative outcome evidence on *decision quality improvement* over a broader query set.
   - Current artifacts are mostly representative samples rather than a compact scored comparison baseline.

4. **Should the approach be adjusted?**
   - Yes: move from branch-shape validation to a bounded decision-quality scoring pass across a stable mini-corpus.

5. **What are the next priorities?**
   - Define a small fixed evaluation set and compute comparable metrics (advisory presence, missing-estimate rate, shadow-local candidacy rate) pre/post warm-up.
   - Use this scorecard to decide whether to keep branch as-is, tighten guards, or expand cautiously.
   - Avoid adding new branch logic until this scorecard exists.

## Decision

Keep (reflection, no code change).
