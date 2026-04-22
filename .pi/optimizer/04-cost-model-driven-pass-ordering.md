# 04 — Cost-model-driven pass ordering & skipping

`FixedPassOrder` runs all nine optimizer passes unconditionally
(`optimizer.go:34-44`). Many are no-ops for simple expressions.
`applyJoinNormalizationDuplicateDetection` walks the tree looking for a
`BinaryVectorJoin` that `up{job="foo"}` simply doesn't contain;
`applyFunctionPatternRewrites` is a stub today and will grow to match
aggregation/range patterns that plain selectors never trigger;
`applyFlattenRedundantWrappers` recurses the whole tree even when no
`UnarySourceExpr` wrapper exists anywhere. Skip passes whose
preconditions are false based on a cheap shape fingerprint computed
once.

## Problem

The 80th-percentile PromQL query on a typical metrics deployment:

```promql
up{job="kubernetes-pods",namespace="monitoring"}
```

Fragment shape: single `FragmentKindLeafSource` + `Selector`. Of nine
passes:

- `applyTrivialExpressionNormalization` — scans for
  `UnarySourceExpr`; none exist; walks the tree for nothing.
- `applyEvaluationRangePropagation` — useful (computes bounds).
- `applyCommonMatcherInference` — useful (emits `__name__="up"`).
- `applyLabelPredicatePushdown` — useful.
- `applyProjectionPushdown` — useful.
- `applyFunctionPatternRewrites` — no aggregation, no range
  function; stub; no-op.
- `applyJoinNormalizationDuplicateDetection` — no join; returns
  `"not_applicable"` (`optimizer.go:222`) — after already walking.
- `applyFlattenRedundantWrappers` — walks; no wrappers; no-op.
- `applyFinalSQLShapingLateMaterialization` — useful.

Four of nine passes waste walks for a single-leaf fragment. Production
shims evaluate tens of thousands of such "simple selector" queries per
day (service-discovery, up-state alerts). Individually cheap; aggregated
with the clone cost from doc 03, visible under load.

A similar story for `rate(http_requests_total[5m])`: no binary join, no
label transform, no histogram, no clamp — all the respective passes are
wasted walks.

## Current behavior

- `optimizer.go:140-145` — unconditional loop over every pass.
- `optimizer.go:220-227` —
  `applyJoinNormalizationDuplicateDetection` early-returns on
  `"not_applicable"`, but only after the pass has been scheduled and
  the shape walk has run.
- No fragment-shape fingerprint exists today;
  `BaseSelectorSource`, `containsAggregationBoundary`,
  `containsLabelTransform` all traverse the tree independently when
  called.

## Proposed technique

**Part A — Fragment shape fingerprint.** Compute once at optimizer
entry (single post-order walk):

```go
type fragmentShape struct {
    HasAggregation, HasBinaryJoin, HasRangeFunction, HasSubquery   bool
    HasHistogram, HasLabelTransform, HasSortTransform              bool
    HasClampTransform, HasValueTransform, HasUnarySourceExpr       bool
    HasInfoJoin, HasAbsent, HasScalarConvert, HasSynthetic         bool
    SelectorCount, MaxDepth                                        int
}
```

~40 LOC. Cheaper than any single optimizer pass that would otherwise
walk the tree.

**Part B — Per-pass precondition predicates.**

```go
type optimizerPassSpec struct {
    ID     OptimizerPass
    Layer  OptimizerLayer
    Prereq func(fragmentShape) bool // nil = always run
    Apply  func(*optimizerState) error
}
```

| Pass | Precondition |
| --- | --- |
| `PassTrivialExpressionNormalization` | `s.HasUnarySourceExpr` |
| `PassEvaluationRangePropagation` | always |
| `PassCommonMatcherInference` | `s.SelectorCount > 0` |
| `PassLabelPredicatePushdown` | `s.SelectorCount > 0` |
| `PassProjectionPushdown` | `s.SelectorCount > 0` |
| `PassFunctionPatternRewrites` | `s.HasAggregation \|\| s.HasRangeFunction` |
| `PassJoinNormalizationDuplicateDetection` | `s.HasBinaryJoin` |
| `PassFlattenRedundantWrappers` | `s.HasUnarySourceExpr` |
| `PassFinalSQLShapingLateMaterialization` | always |

Loop:

```go
for _, pass := range optimizerPasses {
    if pass.Prereq != nil && !pass.Prereq(state.shape) {
        state.report.RulesApplied = append(state.report.RulesApplied, string(pass.ID)+":skipped")
        continue
    }
    state.report.RulesApplied = append(state.report.RulesApplied, string(pass.ID))
    if err := pass.Apply(state); err != nil {
        return nil, fmt.Errorf("applying optimizer pass %s: %w", pass.ID, err)
    }
}
```

Record skips so the audit log stays honest; suffix `:skipped` lets
regex consumers adapt mechanically.

**Part C — Minor reorder.** Move `PassEvaluationRangePropagation` to
slot #1 (it's always-on and produces bounds the caller needs) and keep
`PassFinalSQLShapingLateMaterialization` at #9. Everything else keeps
its relative order but is gated. This is the only reorder I believe is
principled today.

## Expected gain

For `up{job="kubernetes-pods"}`: 9 passes → 5. At ~100 ns per skipped
pass × 4 skipped passes = ~400 ns saved per query. At 5 k QPS per
replica, ~2 ms/s — small, but cumulative with docs 02 and 03.

For `rate(http_requests_total[5m])`: 9 → 6 (skip the two
`HasUnarySourceExpr`-gated, skip `HasBinaryJoin`-gated).
`FunctionPatternRewrites` finally runs on a real workload once it has
a body.

The more valuable benefit is **pass safety**: adding a new pass
becomes a compile-time demand to declare its precondition, forcing
authors to think about whether their pass is useful work on most
inputs.

## Risk / caveats

- **Precondition correctness.** A predicate that under-approximates
  silently regresses behaviour. Primary defense: every existing
  optimizer golden test must pass unchanged. Secondary: predicates
  must match exactly the first `nil`-check each pass does today.
- **Future non-trivial preconditions.** Passes like doc 02's CSE want
  "≥ 2 selectors plus structural duplication" — the shape struct
  won't capture the structural condition. Allow `Prereq` to be
  `nil` ("always run") for such cases.
- **Report stability.** Consumers of
  `OptimizationReport.RulesApplied` may expect all nine names. The
  `:skipped` suffix preserves a skip trail; document the suffix on
  the report type.
- **Shape staleness.** Current structural passes (`Trivial...`,
  `Flatten...`) only *remove* features, never add them — safe. If a
  future `mutationStructural` pass adds features, re-derive the
  shape after it:

  ```go
  if pass.Mutates == mutationStructural { state.shape = analyzeFragmentShape(state.fragment) }
  ```

- **Observability.** Skipped-pass logs grow slightly; use the
  structured suffix so filters can be updated cleanly.

## Implementation sketch

New file `internal/promshim/native/fragment_shape.go` with
`analyzeFragmentShape(f *NativeFragment) fragmentShape` over a shared
`visitFragment(f, visit func(*NativeFragment, depth int))` walker.
Factor that walker out of the three existing recursive helpers
(`BaseSelectorSource`, `containsAggregationBoundary`,
`containsLabelTransform`) so all four share one tree walk.

Store `shape` on `optimizerState`; populate immediately after the entry
clone; consult in the pass loop.

## Test coverage idea

- Unit: `TestFragmentShapeDetectsEveryFeature` — one fragment per
  feature, assert every flag fires.
- Unit: `TestFragmentShapeOnLeafSelector` — only `SelectorCount == 1`;
  serves as the "simplest input" regression gate.
- Unit: `TestOptimizerSkipsUnreachablePassesForLeafSelector` — assert
  `RulesApplied` contains four `:skipped` markers.
- Unit: `TestOptimizerSkipsJoinNormalizationForNonJoin` — on a
  range-function fragment, the join pass is recorded skipped.
- Unit: `TestOptimizerRecomputesShapeAfterStructuralPass` — build a
  fragment with a `UnarySourceExpr` wrapping a leaf; after the trivial
  normalization the flag flips false; any later gated pass sees the
  updated flag.
- Harness: compliance suite passes unchanged — pure scheduler change.
- Bench: `BenchmarkOptimizerLeafSelector` — expect ~30–50% faster on
  the simplest fragments where four passes are no-ops.
