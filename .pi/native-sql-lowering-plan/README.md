# Native SQL lowering plan — split index

This directory contains the native SQL lowering plan split into execution-ordered chunks.

## Reading / execution order

1. [01-context-and-guardrails.md](./01-context-and-guardrails.md)
   - why this work exists
   - preserve/add/non-goals
   - reference precedence
   - semantic safety rules

2. [02-planner-and-fragment-model.md](./02-planner-and-fragment-model.md)
   - target execution architecture
   - maximal-island planner model
   - fragment IR contracts
   - selector source strategy
   - target planner behavior examples

3. [03-optimizer-pipeline.md](./03-optimizer-pipeline.md)
   - staged RBO structure
   - optimizer passes in order
   - explain visibility requirements

4. [04-phase-1-analysis-scaffolding.md](./04-phase-1-analysis-scaffolding.md)
5. [05-phase-2-native-subtree-and-renderer.md](./05-phase-2-native-subtree-and-renderer.md)
6. [06-phase-3-selector-lowering.md](./06-phase-3-selector-lowering.md)
7. [07-phase-4-sql-optimizer-passes.md](./07-phase-4-sql-optimizer-passes.md)
8. [08-phase-5-join-and-vector-matching-lowering.md](./08-phase-5-join-and-vector-matching-lowering.md)
9. [09-phase-6-range-functions-and-subqueries.md](./09-phase-6-range-functions-and-subqueries.md)
10. [10-phase-7-rollout-and-shadow-comparison.md](./10-phase-7-rollout-and-shadow-comparison.md)
11. [11-validation-risks-and-definition-of-done.md](./11-validation-risks-and-definition-of-done.md)

## Recommended narrow first slice

If the goal is to prove the architecture quickly, prioritize this subset first:

1. Phase 1 analysis scaffolding
2. Phase 3 selector lowering
3. Phase 4 initial optimizer passes:
   - evaluation-range propagation
   - common matcher inference + label pushdown
   - projection pushdown with no `SELECT *`
4. explain improvements
5. shadow mode

That slice already proves the architecture on common dashboard shapes before full join and range-heavy support lands.
