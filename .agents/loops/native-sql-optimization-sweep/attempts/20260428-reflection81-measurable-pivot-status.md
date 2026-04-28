# Attempt 20260428-reflection81-measurable-pivot-status

## Reflection checkpoint

1. **What has been accomplished so far?**
   - Completed and stabilized the bounded subquery `cost_shadow` behavior experiment with clear advisory transparency and strategy-neutral guarantees.
   - Established measurable gates that rejected unsafe/low-value expansions (subquery and high-pressure binary local-serving paths).
   - Performed pivot-family scouting and identified processing-family high-cost candidates for next optimization focus.

2. **What's working well?**
   - Measurable gate discipline is working: expansion decisions are now data-driven and reversible.
   - Advisory + branch diagnostics are strong enough to explain behavior decisions without changing served strategy.
   - The loop now avoids drifting into infra-only churn without performance relevance.

3. **What's not working or blocking progress?**
   - Recent measured pivots mainly rejected candidate directions rather than landing new resource wins.
   - We still need one concrete implementation candidate in the processing family that is likely to lower absolute CPU/memory/time, not just compare modes.

4. **Should the approach be adjusted?**
   - Yes: switch from “mode comparison gates” to one tightly scoped SQL-shape optimization candidate in the highest-pressure processing query.

5. **What are the next priorities?**
   - Select `processing_avg_memory_1h_by_job_type_range_24h_7d` (largest memory pressure) as the next implementation target.
   - Create a bounded design/implementation slice aimed at reducing repeated source work or window-materialization overhead.
   - Require before/after evidence on at least: CH queryDuration p50, memory p95, and one corroborating engine signal (e.g., functionExecute).

## Decision

Keep (reflection, no code change).
