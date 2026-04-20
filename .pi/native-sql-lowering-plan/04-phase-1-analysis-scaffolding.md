# 04 — Phase 1 analysis scaffolding

## Goal
Introduce a generic native-lowering analysis pass without changing most runtime behavior yet.

## Scope
- add native output kinds and fragment metadata types
- add bottom-up lowerability analysis over existing `logicalPlan` nodes
- add label-lineage tracking
- add evaluation-range requirement propagation scaffolding
- extend explain to expose lowerability and reasons

## Distinct tasks

1. **Add native type definitions**
   - create native output kinds
   - create `NativeLoweringInfo`
   - create fragment metadata skeletons

2. **Implement lowerability analysis walk**
   - walk existing logical-plan nodes bottom-up
   - classify lowerable vs non-lowerable nodes
   - attach explicit reasons for unsupported nodes

3. **Add label-lineage tracking**
   - represent original / copied / mutated / dropped / synthetic / unknown labels
   - make later predicate pushdown depend on this data

4. **Add evaluation-range analysis scaffolding**
   - compute required input range metadata
   - thread it through analysis results even before selector lowering is complete

5. **Expose analysis in explain**
   - add lowerability and fallback reasons to explain output

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
