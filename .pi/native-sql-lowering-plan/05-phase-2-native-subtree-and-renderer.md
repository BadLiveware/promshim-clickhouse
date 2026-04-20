# 05 — Phase 2 native subtree and renderer skeleton

## Goal
Replace the aggregation-specific native node with a general native subtree execution node.

## Scope
- introduce `nativeSubtreePlan`
- introduce `NativeFragment` builder + renderer
- keep the initial supported subset small:
  - selectors
  - scalar literals
  - unary arithmetic
  - scalar/vector arithmetic
  - simple aggregations (`sum`, `count`, `min`, `max`, `avg`)
- re-express current native aggregation pushdown through the general native subtree mechanism

## Distinct tasks

1. **Introduce `nativeSubtreePlan`**
   - generalize execution away from `nativeAggregationPlan`
   - preserve explain integration

2. **Add fragment builder skeleton**
   - lower the supported subset into internal fragment IR
   - keep final SQL rendering separate from lowering

3. **Re-route current simple aggregation pushdown**
   - make current aggregation native path a special case of the new general mechanism

4. **Keep subset intentionally narrow**
   - avoid broad operator support before the planner/fragment contracts are solid

## Validation
- adapt existing native aggregation tests to the new architecture
- add SQL rendering snapshot tests
- add explain tests showing `native_sql` subtree selection
