# Native SQL lowering plan

This plan has been split into ordered plan chunks under:

- [.pi/native-sql-lowering-plan/](./native-sql-lowering-plan/)

Read them in this order:

1. [README.md](./native-sql-lowering-plan/README.md)
2. [01-context-and-guardrails.md](./native-sql-lowering-plan/01-context-and-guardrails.md)
3. [02-planner-and-fragment-model.md](./native-sql-lowering-plan/02-planner-and-fragment-model.md)
4. [03-optimizer-pipeline.md](./native-sql-lowering-plan/03-optimizer-pipeline.md)
5. [04-phase-1-analysis-scaffolding.md](./native-sql-lowering-plan/04-phase-1-analysis-scaffolding.md)
6. [05-phase-2-native-subtree-and-renderer.md](./native-sql-lowering-plan/05-phase-2-native-subtree-and-renderer.md)
7. [06-phase-3-selector-lowering.md](./native-sql-lowering-plan/06-phase-3-selector-lowering.md)
8. [07-phase-4-sql-optimizer-passes.md](./native-sql-lowering-plan/07-phase-4-sql-optimizer-passes.md)
9. [08-phase-5-join-and-vector-matching-lowering.md](./native-sql-lowering-plan/08-phase-5-join-and-vector-matching-lowering.md)
10. [09-phase-6-range-functions-and-subqueries.md](./native-sql-lowering-plan/09-phase-6-range-functions-and-subqueries.md)
11. [10-phase-7-rollout-and-shadow-comparison.md](./native-sql-lowering-plan/10-phase-7-rollout-and-shadow-comparison.md)
12. [11-validation-risks-and-definition-of-done.md](./native-sql-lowering-plan/11-validation-risks-and-definition-of-done.md)

The split keeps the same overall intent as the original monolith, but organizes it into smaller execution-ordered plans with distinct task lists.
