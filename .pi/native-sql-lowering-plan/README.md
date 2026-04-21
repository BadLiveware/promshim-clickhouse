# Native SQL lowering plan — split index

This directory contains the native SQL lowering plan split into execution-ordered chunks.

## Read this first

[00-status-and-drift.md](./00-status-and-drift.md) records where the
codebase actually is (as of 2026-04-21) versus where this plan originally
assumed it would be. The architecture still holds; Phase 1 and Phase 6 have
drifted and are now interpreted through that chunk.

If you want the pre-Phase-1 execution baseline for this repo, also read
[phase-0-baseline.md](./phase-0-baseline.md). It freezes the semantic
guardrails, inventories the current touchpoints, and points at the focused
starter differential corpus used for this roadmap.

## Reading / execution order

1. [00-status-and-drift.md](./00-status-and-drift.md)
   - current state of the codebase
   - the three execution paths (delegated / native SQL / local)
   - what Phase 1 actually delivered and what it still owes
   - policy for local vs native range functions

2. [01-context-and-guardrails.md](./01-context-and-guardrails.md)
   - why this work exists
   - preserve/add/non-goals
   - reference precedence
   - semantic safety rules

3. [02-planner-and-fragment-model.md](./02-planner-and-fragment-model.md)
   - target execution architecture
   - maximal-island planner model
   - fragment IR contracts
   - selector source strategy
   - target planner behavior examples

4. [03-optimizer-pipeline.md](./03-optimizer-pipeline.md)
   - staged RBO structure
   - optimizer passes in order
   - explain visibility requirements

5. [04-phase-1-analysis-scaffolding.md](./04-phase-1-analysis-scaffolding.md)
6. [05-phase-2-native-subtree-and-renderer.md](./05-phase-2-native-subtree-and-renderer.md)
7. [06-phase-3-selector-lowering.md](./06-phase-3-selector-lowering.md)
8. [07-phase-4-sql-optimizer-passes.md](./07-phase-4-sql-optimizer-passes.md)
9. [08-phase-5-join-and-vector-matching-lowering.md](./08-phase-5-join-and-vector-matching-lowering.md)
10. [09-phase-6-range-functions-and-subqueries.md](./09-phase-6-range-functions-and-subqueries.md)
11. [10-phase-7-rollout-and-shadow-comparison.md](./10-phase-7-rollout-and-shadow-comparison.md)
12. [11-validation-risks-and-definition-of-done.md](./11-validation-risks-and-definition-of-done.md)
13. [12-harness-parametrization.md](./12-harness-parametrization.md)
    - time-window / range-step / dataset-shape layers for the differential harness
    - tied to Phase 6b rollout, not a blocker for Phase 1

## Recommended narrow first slice

If the goal is to prove the architecture quickly, prioritize this subset first:

1. Phase 1 analysis scaffolding (now a **type-extraction refactor** of
   lowerability logic currently inlined in the planner — see
   [00-status-and-drift.md](./00-status-and-drift.md))
2. Phase 3 selector lowering
3. Phase 4 initial optimizer passes:
   - evaluation-range propagation
   - common matcher inference + label pushdown
   - projection pushdown with no `SELECT *`
4. explain improvements
5. shadow mode

That slice already proves the architecture on common dashboard shapes before full join and range-heavy support lands.
