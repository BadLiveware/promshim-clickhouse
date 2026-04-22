# 03 — Stop cloning at every pass

`OptimizeFragment` deep-copies the fragment tree on entry
(`optimizer.go:133`), and two of its nine passes
(`normalizeTrivialSourceExpressions`, `flattenRedundantWrappers`)
rebuild it again. Several other passes are pure reads. For a moderate
query we pay 3–4× the allocation cost we actually need. Classify passes
as mutating vs. analytical, clone once at the boundary, and operate in
place.

## Problem

Consider `sum by (job) (rate(http_requests_total{env="prod"}[5m]))` —
three fragment nodes (Aggregation → RangeFunction → Selector). On one
`OptimizeFragment` invocation:

1. `BuildFragment` → `CloneFragment` once (`builder.go:23`).
2. `OptimizeFragment` entry → `CloneFragment` once (`optimizer.go:133`).
3. `applyTrivialExpressionNormalization` →
   `normalizeTrivialSourceExpressions` → recursive `CloneFragment`
   (`optimizer.go:246`).
4. `applyCommonMatcherInference` — mutates `selector.InferredMatchers`
   in place (`optimizer.go:185`).
5. `applyLabelPredicatePushdown` — mutates `selector.PushedMatchers`
   via `mergeMatchers` (`optimizer.go:195`).
6. `applyProjectionPushdown` — mutates selector fields in place
   (`optimizer.go:205`, `521`).
7. `applyFunctionPatternRewrites` — currently a no-op.
8. `applyJoinNormalizationDuplicateDetection` — writes only to
   `state.report`.
9. `applyFlattenRedundantWrappers` → `flattenRedundantWrappers` →
   recursive `CloneFragment` again (`optimizer.go:286`).

That's four deep tree clones per optimize. Only the entry clone
(`optimizer.go:133`) is genuinely needed — the analysis cache
(`Analysis.byNode`) owns the pre-optimizer tree and would leak mutations
back if we dropped it. The remaining three clones produce identical
trees 99% of the time because the structural rewrites only fire on
`UnarySourceExpr` nodes that are no-ops in the first place.

Each `CloneFragment` on a 3-node tree is ~6 heap allocations (one per
fragment + typed-pointer struct + slice copies). Four clones × ~6 = ~24
allocations per optimize; most are pure waste.

Separately, `mergeMatchers` (`optimizer.go:867`) and the
`labels.MustNewMatcher` call inside it allocate a fresh `*labels.Matcher`
for every entry in `PushedMatchers`. If `Matchers`, `InferredMatchers`,
and `PushedMatchers` all contain `env="prod"`, we hold three
pointer-distinct copies of the same struct.

## Current behavior

- `optimizer.go:133` — single top-level `CloneFragment`.
- `optimizer.go:242-280` — `normalizeTrivialSourceExpressions` clones
  every visited fragment.
- `optimizer.go:282-320` — `flattenRedundantWrappers` clones again.
- `optimizer.go:169-232` — both are wired via
  `state.fragment = helper(state.fragment)` so the return-value
  protocol itself *requires* producing a new tree.
- `optimizer.go:195-203` — `applyLabelPredicatePushdown` routes through
  `mergeMatchers` which calls `labels.MustNewMatcher` per input.

## Proposed technique

**Part A — Classify passes.** Add a `Mutates fragmentMutationKind` tag
to `optimizerPassSpec`:

- `mutationNone` — read-only (writes to `state.report`):
  `applyEvaluationRangePropagation`, `applyFunctionPatternRewrites`,
  `applyJoinNormalizationDuplicateDetection`,
  `applyFinalSQLShapingLateMaterialization`.
- `mutationSelectorFields` — mutates selector fields on the existing
  selector: `applyCommonMatcherInference`,
  `applyLabelPredicatePushdown`, `applyProjectionPushdown`.
- `mutationStructural` — may swap typed pointers / collapse wrappers:
  `applyTrivialExpressionNormalization`,
  `applyFlattenRedundantWrappers`.

Clone once on entry (preserved for safety vs. `Analysis.byNode`), then
run every pass in place:

```go
state := &optimizerState{ fragment: CloneFragment(fragment), ... }
for _, pass := range optimizerPasses { pass.Apply(state) }
```

Rewrite the two structural passes as in-place walks:

```go
func normalizeTrivialSourceExpressionsInPlace(f *NativeFragment) {
    if f == nil { return }
    if f.Aggregation != nil { normalizeTrivialSourceExpressionsInPlace(f.Aggregation.Source) }
    // ...recurse into every typed child...
    if f.Kind == FragmentKindUnarySourceExpr &&
        f.ValueExpr == "{value}" && f.TagsExpr == "{tags}" && !f.DropsMetric {
        f.Kind = FragmentKindLeafSource
    }
}
```

No `CloneFragment`. The only rewrite that matters (`UnarySourceExpr` →
`LeafSource`) is a single field write.

**Part B — Intern matcher pointers.** A per-query
`matcherInterner` keyed on `Type|Name|Value`:

```go
type matcherInterner struct{ byKey map[string]*labels.Matcher }
func (m *matcherInterner) intern(x *labels.Matcher) *labels.Matcher {
    key := x.Type.String() + "\x00" + x.Name + "\x00" + x.Value
    if existing, ok := m.byKey[key]; ok { return existing }
    m.byKey[key] = x
    return x
}
```

Route `CloneMatchers` (once at build time) and `mergeMatchers` through
the interner. Identical matchers across `Matchers`, `InferredMatchers`,
and `PushedMatchers` become pointer-equal; `cloneSelectorSource` still
copies the slice header but reuses matcher pointers.

## Expected gain

For the 3-node example: 4 deep clones × ~6 allocs → 1 clone × ~6 allocs;
~75% cut in optimizer-layer allocations. Combined with doc 02's plan
cache (which skips `OptimizeFragment` entirely on hit), this is the
next-tier win whenever the cache misses.

Under `go test -bench` on the optimizer, expect `allocs/op` down ~3× on
trees with depth ≥ 3 and nil effect on shallow trees. In `pprof` under
load, `runtime.mallocgc` and `runtime.scanobject` time on the planner
path should drop proportionally; GC cycles under steady QPS happen
measurably less often.

Matcher interning adds a smaller second gain: a 40-panel dashboard
sharing `env="prod"` goes from one matcher alloc per panel to one per
query lifetime.

## Risk / caveats

- **Mutation hazards.** The entire reason clone exists today is
  defensive. Preserve the entry clone at `optimizer.go:133` — only
  subsequent passes go in-place. Add a package comment: "Passes
  receive a cloned fragment; mutate freely; never hold pointers into
  the original analysis tree."
- **Matcher interning and downstream mutation.** If any consumer
  mutates a `*labels.Matcher` in place, interning aliases the change.
  `labels.Matcher` is conventionally immutable upstream; audit callers
  once, document the invariant.
- **Structural rewrites.** Today's two structural passes are
  idempotent and don't need the pre-rewrite form of a sibling. A
  future pass that reads the whole pre-rewrite tree and then swaps
  should tag itself `mutationStructuralWithSnapshot` and clone just
  inside that pass. Do not reintroduce the blanket clone.
- **Tests that rely on pointer inequality.** Any test that asserts
  `state.fragment != original` will need updating — intent was always
  structural correctness, not pointer identity.

## Implementation sketch

```go
type fragmentMutationKind int
const (
    mutationNone fragmentMutationKind = iota
    mutationSelectorFields
    mutationStructural
)

type optimizerPassSpec struct {
    ID      OptimizerPass
    Layer   OptimizerLayer
    Mutates fragmentMutationKind
    Apply   func(*optimizerState) error
}

func OptimizeFragment(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (*OptimizedFragment, error) {
    state := &optimizerState{
        fragment: CloneFragment(fragment), // once, defensively.
        report:   &OptimizationReport{FunctionCatalog: append([]string(nil), functionRewriteCatalog...)},
        info:     info, ctx: ctx,
        interner: newMatcherInterner(),
    }
    internMatchersInFragment(state.fragment, state.interner)
    for _, pass := range optimizerPasses {
        state.report.RulesApplied = append(state.report.RulesApplied, string(pass.ID))
        if err := pass.Apply(state); err != nil {
            return nil, fmt.Errorf("applying optimizer pass %s: %w", pass.ID, err)
        }
    }
    state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, semanticBarriersForFragment(state.fragment)...)
    return &OptimizedFragment{Fragment: state.fragment, Report: state.report}, nil
}
```

The two structural passes become in-place walks — mechanical transforms
of today's clone-based versions: drop the `normalized := CloneFragment(...)`
binding, operate on the incoming pointer. ~80 lines of code change total.

`applyLabelPredicatePushdown` funnels through the interner:
`selector.PushedMatchers = state.interner.internSlice(mergeMatchers(...))`.

## Test coverage idea

- Unit: `TestOptimizerAllocationCount` — `testing.AllocsPerRun` on a
  representative fragment; assert an upper bound (e.g. ≤ 10) that
  today's code fails.
- Unit: `TestOptimizerFragmentDoesNotShareWithAnalysis` — mutate the
  returned fragment; the analysis-owned original must stay untouched.
- Unit: `TestInPlacePassesIdempotent` — run the structural helpers
  twice; second call is a no-op and allocates zero.
- Unit: `TestMatcherInternerCollapsesIdentical` — two selectors with
  the same matcher share a pointer after interning.
- Unit: `TestMatcherInternerDoesNotAliasAcrossQueries` — interner is
  per-`optimizerState`; two serial `OptimizeFragment` calls don't share.
- Golden regression: every existing optimizer golden test passes
  unchanged.
- Bench: `BenchmarkOptimizeFragmentWide` on a ~20-node fragment; track
  `B/op` and `allocs/op` pre- and post-change.
