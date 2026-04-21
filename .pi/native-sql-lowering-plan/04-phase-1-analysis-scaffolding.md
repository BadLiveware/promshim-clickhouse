# 04 — Phase 1 analysis scaffolding

## Status

Parts of this phase have **already shipped, but in a different shape** than
originally planned. See
[00-status-and-drift.md](./00-status-and-drift.md). Specifically:

- lowerability decisions for the aggregation-pushdown case exist, but they
  are inlined inside `buildExecPlanWithContext` and
  `decideNativeAggregationPushdown` in `internal/promshim/planner.go`
- no `NativeLoweringInfo`, `NativeFragment`, or label-lineage types exist
- no required-time-range propagation scaffolding exists
- evaluation-range needs for range functions and subqueries are handled
  locally inside `internal/promshim/exec/` rather than carried through the
  planner as metadata

Phase 1 is therefore now primarily a **refactor**: lift the lowerability
decisions that are currently entangled with execution-plan construction
into first-class types that Phases 2-6 can build on. Treat the task list
below as "extract and formalize", not "write from scratch".

## Goal
Introduce a generic native-lowering analysis pass as a reusable layer,
separating **does this lower?** from **what execution plan does it get?**.

## Scope
- add native output kinds and fragment metadata types
- add bottom-up lowerability analysis over existing `logicalPlan` nodes
- add label-lineage tracking
- add evaluation-range requirement propagation scaffolding
- extend explain to expose lowerability and reasons
- migrate the existing aggregation-pushdown decision onto the new analysis
  types without changing its runtime behavior

## Distinct tasks

1. **Add native type definitions**
   - create native output kinds
   - create `NativeLoweringInfo`
   - create fragment metadata skeletons

2. **Implement lowerability analysis walk**
   - walk existing logical-plan nodes bottom-up
   - classify lowerable vs non-lowerable nodes
   - attach explicit reasons for unsupported nodes
   - replace the ad-hoc checks currently inlined in
     `buildExecPlanWithContext` / `decideNativeAggregationPushdown` with
     lookups on the new `NativeLoweringInfo`

3. **Add label-lineage tracking**
   - represent original / copied / mutated / dropped / synthetic / unknown labels
   - make later predicate pushdown depend on this data

4. **Add evaluation-range analysis scaffolding**
   - compute required input range metadata
   - thread it through analysis results even before selector lowering is complete
   - reconcile with the step / grid handling currently implemented locally
     for range functions and subqueries, so the same metadata can drive
     native lowering in Phase 6b

5. **Expose analysis in explain**
   - add lowerability and fallback reasons to explain output
   - surface why each subtree chose delegated / native / local, not only the
     aggregation-pushdown path that is visible today

## Migration notes

- **Do not change the runtime behavior of `nativeAggregationPlan` in this
  phase.** It stays as the only native execution node for now. The goal is
  only that its inputs come from the new analysis pass.
- Keep the new analysis read-only for Phase 1; it informs decisions but
  does not yet own execution-plan selection. Top-down strategy selection
  and the generalized `nativeSubtreePlan` belong to Phase 2.
- Existing local range-function and subquery plans in
  `internal/promshim/exec/` stay in place. The analysis pass should
  annotate them with evaluation-range / lineage metadata without
  substituting a native lowering.

## Likely files
- new:
  - `internal/promshim/native/analysis.go`
  - `internal/promshim/native/types.go`
  - `internal/promshim/native/lineage.go`
  - `internal/promshim/native/time_requirements.go`
- touched:
  - `internal/promshim/planner.go`
  - `internal/promshim/explain.go`

## Validation
- unit tests for lowerability classification
- unit tests for label-lineage propagation
- unit tests for required-time-range propagation
- regression test that the existing aggregation-pushdown selections do not
  change shape after the refactor (same queries choose the same strategy,
  same explain output where applicable)
