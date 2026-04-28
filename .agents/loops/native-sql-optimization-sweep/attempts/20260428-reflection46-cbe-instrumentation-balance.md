# Attempt 20260428-reflection46-cbe-instrumentation-balance

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed the scoped thread-policy branch and intentionally closed its low-signal hotspot tranche.
   - Shifted to estimate-input plumbing for later CBE and successfully surfaced subquery estimate fields (`subqueryRangeMs`, `subqueryStepMs`, `subqueryPointsPerEval`, `subqueryOverlapSlots`) through classifier + API explain paths.
   - Kept all instrumentation increments behavior-neutral with passing focused regression coverage.

2. **What's working well?**
   - The loop is now producing coherent, additive estimate metadata with low risk.
   - Reflection checkpoints are successfully preventing drift into unbounded micro-optimization loops.
   - Tests are keeping explain-surface contracts stable as metadata evolves.

3. **What's not working or blocking progress?**
   - We are approaching diminishing returns from adding estimate fields without yet consuming them for decision quality.
   - Runtime impact remains indirect until estimate inputs are integrated into evaluable candidate signals/cost interpretation.

4. **Should the approach be adjusted?**
   - Yes: transition from "add more fields" to "make current fields useful".
   - Next step should be one bounded synthesis artifact or helper that combines exposed subquery estimate inputs into clearer cost-shaping context (still without changing routing).

5. **What are the next priorities?**
   - Add one explain-visible derived estimate summary for subquery complexity using existing fields (e.g., normalized subquery workload indicator), or a compact classifier helper consumed by routing diagnostics.
   - Ensure no routing decision changes yet; keep this as instrumentation/diagnostic quality work.
   - Validate through classifier + API tests, then reassess whether enough estimate observability exists to start controlled CBE behavior experiments.

## Decision

Keep (reflection + prioritization shift, no code change).
