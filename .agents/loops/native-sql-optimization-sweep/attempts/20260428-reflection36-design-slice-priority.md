# Attempt 20260428-reflection36-design-slice-priority

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Landed and validated bounded binary-root thread-policy scoping behavior.
   - Hardened explain/service regression guards around root-vs-branch query-settings decisions.
   - Unblocked and stabilized focused benchmark measurement workflows for subquery families.
   - Identified current hotspot candidates and gathered concrete SQL-shape evidence for `rate(sum(...)[5m:])`.

2. **What's working well?**
   - Evidence-first loop discipline is preventing risky speculative edits.
   - Harness reliability is significantly improved (fail-fast corpus validation + reproducible focused artifacts).
   - Decisions are now easier to audit via aligned planner/service explain signals.

3. **What's not working or blocking progress?**
   - Runtime wins are currently bottlenecked by contract-sensitive SQL-shape complexity in the top hotspot.
   - Recent iterations have produced several measurement/triage outcomes but limited accepted runtime behavior improvements.

4. **Should the approach be adjusted?**
   - Yes. Shift from incremental trial edits to a bounded design-first slice for the `rate(sum(...)[5m:])` hotspot, including explicit correctness/decision-contract impact before implementation.
   - Continue rejecting low-signal tweaks where corroborating metrics remain flat/noisy.

5. **What are the next priorities?**
   - Write a compact implementation design note for the hotspot path with:
     - candidate SQL-shape alternatives,
     - required explain/decision contract updates,
     - concrete expected perf/memory signals,
     - validation matrix (targeted tests + focused bench + profile checks).
   - Implement only the smallest design-backed variant with clear rollback criteria.

## Decision

Keep (reflection + execution-shape reset, no code change).
