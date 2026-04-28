# Attempt 20260428-reflection31-runtime-priority-reset

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed a bounded subquery thread-policy behavior change (binary-root scoping) with planner + service explain guards.
   - Unblocked focused runtime measurement by fixing corpus-window shape issues and adding fail-fast loader validation.
   - Captured stable focused benchmark/profile artifacts for mixed/nested binary subquery families.

2. **What's working well?**
   - The loop now has tighter measurement hygiene: benchmark rebuild discipline, corpus validation, and reproducible focused artifacts.
   - Explain and API-level diagnostics are aligned and easier to trust for policy-placement debugging.
   - Small, reviewable commits continue to keep risk low.

3. **What's not working or blocking progress?**
   - Recent iterations have skewed toward harness/test hardening; marginal runtime improvement work has slowed.
   - Current focused measurements show small prefer/force_supported deltas, so optimization signal-to-noise is limited for this narrow family.

4. **Should the approach be adjusted?**
   - Yes. Shift back to runtime-impact candidates now that harness reliability is restored.
   - Keep one guardrail: any runtime claim must include at least one non-wall-clock corroborating signal (query-log/profile events/memory).

5. **What are the next priorities?**
   - Pick one concrete runtime candidate in the subquery family (e.g., reduce unnecessary no-thread-cap spread or branch-specific setting side-effects) with explicit expected signal.
   - Run before/after focused benchmark using the now-valid corpus and include profile/memory summaries.
   - Keep additional harness hardening only as follow-up to observed measurement friction, not as primary loop output.

## Decision

Keep (reflection + priority reset, no code change).
