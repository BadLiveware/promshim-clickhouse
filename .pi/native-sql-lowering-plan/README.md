# Native SQL lowering plan — split index

This directory contains the native SQL lowering plan split into execution-ordered chunks.

## Read this first

[00-status-and-drift.md](./00-status-and-drift.md) records where the
codebase actually is (as of 2026-04-21) versus where this plan originally
assumed it would be, and captures the strategic framing (this shim is a
bridge until ClickHouse's native PromQL on TimeSeries is production-ready;
delegation is whole-query-or-nothing; retirement direction is path 2 →
path 1 at the query level). The architecture still holds. Phase 1 has
shipped; Phase 6 split into 6a/6b, with 6b still pending — both
interpreted through that chunk.

If you want the pre-Phase-1 execution baseline for this repo, also read
[phase-0-baseline.md](./phase-0-baseline.md). It freezes the semantic
guardrails, inventories the current touchpoints, and points at the focused
starter differential corpus used for this roadmap.

## Reading / execution order

1. [00-status-and-drift.md](./00-status-and-drift.md)
   - strategic intent (bridge to upstream PromQL on TimeSeries)
   - current state of the codebase
   - the three execution paths (delegated / native SQL / local), with
     delegation defined as whole-query-or-nothing
   - Phase 1 delivered
   - policy for local vs native range functions
   - adapter-layer requirement for TimeSeries inner-table column shapes

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

Phase 1 is done. To prove the architecture end-to-end, prioritize this
subset next:

1. Phase 2 — generalized `nativeSubtreePlan` consuming the delivered
   `LoweringInfo` / `NativeFragment` types
2. Phase 3 selector lowering, with the TimeSeries column-shape adapter
   layer noted in [00-status-and-drift.md](./00-status-and-drift.md)
3. Phase 4 initial optimizer passes:
   - evaluation-range propagation
   - common matcher inference + label pushdown
   - projection pushdown with no `SELECT *`
4. explain improvements
5. shadow mode with whole-AST capability gating

That slice already proves the architecture on common dashboard shapes before full join and range-heavy support lands.
