# Native SQL lowering plan

This plan has been split into ordered plan chunks under:

- [.pi/native-sql-lowering-plan/](./native-sql-lowering-plan/)

Read them in this order:

1. [README.md](./native-sql-lowering-plan/README.md)
2. [00-status-and-drift.md](./native-sql-lowering-plan/00-status-and-drift.md) — current state vs original plan, read this before the phases
3. [01-context-and-guardrails.md](./native-sql-lowering-plan/01-context-and-guardrails.md)
4. [02-planner-and-fragment-model.md](./native-sql-lowering-plan/02-planner-and-fragment-model.md)
5. [03-optimizer-pipeline.md](./native-sql-lowering-plan/03-optimizer-pipeline.md)
6. [04-phase-1-analysis-scaffolding.md](./native-sql-lowering-plan/04-phase-1-analysis-scaffolding.md)
7. [05-phase-2-native-subtree-and-renderer.md](./native-sql-lowering-plan/05-phase-2-native-subtree-and-renderer.md)
8. [06-phase-3-selector-lowering.md](./native-sql-lowering-plan/06-phase-3-selector-lowering.md)
9. [07-phase-4-sql-optimizer-passes.md](./native-sql-lowering-plan/07-phase-4-sql-optimizer-passes.md)
10. [08-phase-5-join-and-vector-matching-lowering.md](./native-sql-lowering-plan/08-phase-5-join-and-vector-matching-lowering.md)
11. [09-phase-6-range-functions-and-subqueries.md](./native-sql-lowering-plan/09-phase-6-range-functions-and-subqueries.md)
12. [10-phase-7-rollout-and-shadow-comparison.md](./native-sql-lowering-plan/10-phase-7-rollout-and-shadow-comparison.md)
13. [11-validation-risks-and-definition-of-done.md](./native-sql-lowering-plan/11-validation-risks-and-definition-of-done.md)
14. [12-harness-parametrization.md](./native-sql-lowering-plan/12-harness-parametrization.md)

The split keeps the same overall intent as the original monolith, but organizes it into smaller execution-ordered plans with distinct task lists.
