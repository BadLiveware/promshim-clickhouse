# Attempt 20260428-reflection26-harness-debug-priority

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Landed a bounded behavior change for binary-root thread-policy scoping (global no-cap suppression no longer forced at binary roots).
   - Added planner and service-level explain regression guards to lock root-vs-branch query-settings behavior.
   - Preserved correctness signals across local/native package tests during the behavior shift.

2. **What's working well?**
   - The loop is producing reviewable, low-risk commits with explicit evidence trails.
   - Explain artifacts plus service tests now give strong observability for thread-policy propagation outcomes.
   - Rebuild discipline reduced stale-image confusion when comparing explain behavior.

3. **What's not working or blocking progress?**
   - Focused `run-bench` measurement for mixed/nested binary subquery shapes currently fails with HTTP 400, blocking runtime-impact validation for this family.
   - Without fixing this harness/query-shape gap, we risk over-indexing on explain metadata without confirming perf direction.

4. **Should the approach be adjusted?**
   - Yes. Prioritize a bounded harness-debug slice next: isolate exact 400 cause in bench invocation path and adapt corpus/query form (or bench driver handling) so this shape can be measured reliably.
   - Defer additional behavior tuning until this measurement path is unblocked.

5. **What are the next priorities?**
   - Reproduce one failing row outside bench to capture exact API parse/error payload and identify incompatibility source.
   - Patch either corpus/query encoding or bench request construction for these binary subquery shapes.
   - Re-run the focused smoke corpus and only proceed with further optimization claims once non-error timing rows are available.

## Decision

Keep (reflection + execution-priority pivot, no code change).
