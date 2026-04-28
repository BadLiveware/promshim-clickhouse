# Attempt 20260428-cbe-estimate-tranche-closure

## Hypothesis

After the recent estimate-input additions (`subqueryRangeMs`, `subqueryStepMs`, `subqueryPointsPerEval`, `subqueryOverlapSlots`, `subqueryWorkUnits`, `subqueryComplexityBand`), the instrumentation tranche should be explicitly closed so the loop can move to controlled behavior experiments instead of continuing metadata expansion.

## Evidence considered

Recent iterations added and validated:

- raw subquery timing envelope fields
- derived points/overlap/work indicators
- qualitative complexity band
- classifier + API explain regression coverage for each field

All were behavior-neutral and now provide a coherent subquery complexity diagnostic set.

## Decision

Keep (scope decision, no code change): **close the current estimate-input instrumentation tranche**.

## Next step

Start a bounded controlled CBE behavior experiment that *consumes* current diagnostics (e.g., shadow-only candidate interpretation or explain/routing advisory selection rationale) with explicit no-behavior-change guardrails first, then evaluate whether a safe behavior change is justified.
