# 04 — Phase 1 analysis scaffolding

## Status — delivered

Phase 1 shipped. Kept here as acceptance record and as a pointer to the
types Phase 2 onward depend on.

## What was delivered

The native-lowering analysis layer lives at `internal/promshim/native/`.
It separates **does this lower?** from **what execution plan does it
get?**:

- `types.go`
  - `OutputKind` (`unknown` / `scalar` / `instant_vector` / `range_matrix`)
  - `FragmentKind` (`leaf_source` / `unary_source_expression` /
    `binary_scalar_source_expression`)
  - `NativeFragment` — kind, output kind, source PromQL, value/tags
    expressions, metric-drop flag
  - `AggregationSupport` — eligibility + reason + source fragment
  - `TimeRequirements` — lookback, offset, `NeedsSubqueryStepGrid`
  - `LoweringInfo` — the per-node analysis record (originally named
    `NativeLoweringInfo` in the plan; renamed because the `native`
    package prefix made the redundancy obvious)
  - `Analysis` with `Analyze(plan)` and `InfoFor(node)` lookups
  - `ExplainInfo` projection for JSON-safe explain rendering
- `analysis.go` — bottom-up walk populating `LoweringInfo` per node
- `lineage.go` — `LabelLineage` with original / copied / mutated /
  dropped / synthetic / unknown / wildcard semantics, and an explain
  projection
- evaluation-range requirements threaded via `TimeRequirements` on every
  `LoweringInfo`

The planner now reads lowerability from `Analysis.InfoFor(...)` rather
than recomputing it inline. Explain surfaces lowerability, fallback
reasons, fragment kind, aggregation eligibility, required lookback and
offset, subquery step-grid flag, and label lineage.

## What this unblocks

- Phase 2's generalized `nativeSubtreePlan` can consume `LoweringInfo`
  and `NativeFragment` directly — no further refactor gate
- Phase 3 selector lowering can rely on `LabelLineage` for predicate
  pushdown safety
- Phase 4 optimizer passes can read `TimeRequirements` for
  evaluation-range propagation
- Phase 6b differential tests can compare local and native results with
  shared metadata about required input ranges

## Acceptance criteria, met

- aggregation-pushdown decisions pass through the analysis layer rather
  than being recomputed from the AST inline
- explain output includes `nativeLowerable`, `reason`, `fragmentKind`,
  `aggregationPushdownEligible`, `requiredLookbackSeconds`,
  `requiredOffsetSeconds`, `needsSubqueryStepGrid`, and `labelLineage`
  for each node where applicable
- existing aggregation-pushdown corpus produces unchanged strategy
  selections and unchanged explain output for the aggregation-pushdown
  rows (regression guard)
- unit tests exist in `internal/promshim/native/analysis_test.go` for
  lowerability classification, lineage, and time-requirement propagation
