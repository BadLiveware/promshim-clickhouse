# NativeFragment Retirement — Design Spec

**Date:** 2026-04-24
**Branch:** `feat/path2-native-sql-100-compliance`
**Goal:** Retire `NativeFragment` by porting the four remaining Fragment-body renderers and the scalar-involving `BinaryPlan` branch to read directly from `logical.Node`, then delete the Fragment tree, its builder, and its analyzer walk. End state: tier-2 lowering is a pure logical→SQL transform with no intermediate Fragment.

---

## Context

`renderer.Lower` dispatches `logical.Node` kinds to per-kind lowerers. 15 lowerers already direct-render from the logical node (leaf, scalar literal, pointwise, scalar builtin/convert, subquery, sort, label transforms, absent, info-join, unary, round, vector, scalar-trivial binary, vector-vector binary). Four lowerers still tap `native.BuildFragment(n, analysis)` and delegate to a Fragment-body renderer:

- `lowerHistogramProjection` → `renderHistogramProjectionFragment` (covers `histogram_count`, `histogram_sum`, `histogram_avg`, `histogram_stddev`, `histogram_stdvar`)
- `lowerHistogramFunction` → `renderHistogramFunctionFragment` (covers `histogram_quantile`, `histogram_fraction`, `histogram_quantiles`)
- `lowerRangeFunction` → `renderRangeFunctionFragment` (covers `*_over_time`, `rate`, `increase`, `delta`, `changes`, `deriv`, `quantile_over_time`, `predict_linear`, `holt_winters`)
- `lowerAggregation` → `renderAggregationFragment` (covers all aggregation ops with or without grouping; fuses naturally with range children via `tryRenderFusedRangeAggregationFragment`)

Plus `lower.go:146-151` — the scalar-involving branch of `lowerBinary` — still calls `native.BuildFragment` + `RenderFragment` generically.

After retirement: every lowerer direct-renders. `native.BuildFragment` does not exist. `NativeFragment`, `FragmentKind*`, and `native/builder.go` are deleted. `native.Analysis` survives as a slim per-logical-node render-hint map (LabelLineage, TimeRequirements, SourcePromQL provenance) with no `Fragment` field.

---

## Architecture

### Rendering path, post-retirement

```
request → parser → logical plan → analysis → Lower(ctx, node)
                                                │
                                                ├── type switch on logical.Node kind
                                                │
                                                └── per-kind lowerer
                                                     │
                                                     ├── builds selector from logical descendants via native.BuildSelectorSource
                                                     ├── (optionally) computes child-render requirements (tag-narrowing, anchor time, range bounds) from the logical tree
                                                     └── emits RenderedQuery via storage.Build*QuerySQL
```

No `NativeFragment` anywhere in the path.

### Tag-narrowing

Today, `histogram_*(foo_bucket)` with an aggregation child narrows the leaf selector to fetch only the `le` tag. The narrowing is stored Fragment-side on `SelectorSource.RequireFullTags` / `RequiredTagLabels` and applied by `narrowHistogramAggregationChildTags`, which walks and mutates the Fragment tree.

Post-retirement, narrowing is a render-time concern propagated parent→child via `RenderParams`:

```go
type RenderParams struct {
    // existing fields: Mode, EvaluationTimeMS, StartMS, EndMS, StepMS,
    // RequiredStartMS, RequiredEndMS, ResolveSourcePromQL...

    RequireFullTags    bool
    RequiredTagLabels  []string
}
```

A histogram-function renderer inspecting its child computes the narrowing decision and threads it through the child's `Lower(childCtx, childNode)` call. `narrowHistogramAggregationChildTags` becomes a pure computation returning the decision, not a tree mutator. `SelectorSource.RequireFullTags` / `RequiredTagLabels` on `native.SelectorSource` are deleted; the selector-render helpers read the values from `RenderParams` instead.

### Fused range+aggregation

`canFuseRangeAggregationFragment` (Fragment-deep) ports to `canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan) bool`, which inspects the logical child directly for the fusion shape. The fused renderer lands in the **range-function** port (step 3 of Phase A) so the aggregation port (step 4) can call into an already-ready fused helper.

### `native.Analysis` survival shape

Today `FragmentInfo` wraps a Fragment plus per-node metadata. Post-retirement the `Fragment` field is removed; the struct is renamed `NodeInfo`. Surviving responsibilities:

- `LabelLineage` — used by the delegation classifier and planner rewrites
- `TimeRequirements` — used for required-bounds propagation in range/subquery renderers
- `SourcePromQL` provenance — used by `ResolveSourcePromQL`

The `Fragment`-producing walk in `analysis.go` (including the UnaryPlan branch at lines 62-103) is deleted. The remaining walk computes only the three fields above.

---

## Phase A — Port Fragment-body renderers

Ordering: **histogram projection → histogram function → range function → aggregation.** Rationale: smallest body first locks conventions cheaply; aggregation last so it can consume the already-ported fused-range helper.

### Per-body porting template

Each of the four bodies follows the same steps:

1. **Create `renderXxxLogical(cfg storage.QueryConfig, n *logicalpkg.XxxPlan, params RenderParams) (RenderedQuery, error)`** alongside the existing `renderXxxFragment`, mirroring its structure but consuming the logical node directly. Use:
   - `native.BuildSelectorSource(leaf.Expr)` on descendant leaves
   - `storage.BuildInstantSelectorQuerySQL` / `storage.BuildRangeSelectorQuerySQL` and friends for the final SQL
   - `logicalRangeRequiredBoundsForChild` (not `rangeRequiredBoundsForChild`) for child-bound computation
   - `logicalResolvedAnchorTimeMS` for `@` anchor descent
2. **Flip `lowerXxx` to call `renderXxxLogical`**, remove the `BuildFragment` tap.
3. **Migrate that kind's `rangeRequiredBoundsForChild` call sites** to `logicalRangeRequiredBoundsForChild`.
4. **Delete the Fragment kind and its `analysis.go` branch** IF no other consumer remains. Shared kinds (`FragmentKindUnarySourceExpr`, `FragmentKindBinaryScalarSourceExpr`) defer to the Phase B/C cleanup commit.
5. **Verify:** renderer unit tests, `go build ./internal/promshim/...`, `go test ./internal/promshim/native/renderer/...`.

### A1 — Histogram projection

**Fragment body:** ~300 lines in `renderer/histogram.go` (the `renderHistogramProjectionFragment` family, plus `renderClassicHistogramGroupsQuery`).

**Child kinds to handle:** the projection's child is always a histogram bucket leaf (direct `LeafExprPlan`) or an aggregation over buckets (`AggregationPlan` with `sum by`-shaped grouping). The logical renderer inspects `n.Child` and dispatches accordingly.

**Tag-narrowing:** the projection needs the `le` label. Thread `RequiredTagLabels=["le"]` and `RequireFullTags=false` into the child's `RenderParams` before calling the selector builder.

### A2 — Histogram function

**Fragment body:** ~350 lines in `renderer/histogram.go` (the `renderHistogramFunctionFragment` family).

**Child kinds to handle:** same as A1 — leaf bucket selector or aggregation-over-buckets. Reuse the child-render helper introduced in A1.

**Tag-narrowing:** identical to A1 — propagate `RequiredTagLabels=["le"]` via `RenderParams`.

**Function-specific branches:** `histogram_quantile(p, expr)`, `histogram_fraction(lo, hi, expr)`, `histogram_quantiles(name, p1, p2, ..., expr)`. The rendered CTE/aggregate shape depends on the function; port each branch by inspecting `n.Function` rather than `fragment.HistogramFunctionKind`.

### A3 — Range function

**Fragment body:** 938 lines in `renderer/range.go`.

**Child kinds to handle:** `LeafExprPlan` (direct range over a selector) and `SubqueryPlan` (range over a synthesized inner query). The logical renderer inspects `n.Child` and dispatches.

**Fused range+aggregation:** port `canFuseRangeAggregationFragment` to `canFuseRangeAggregationLogical` and `tryRenderFusedRangeAggregationFragment` to `tryRenderFusedRangeAggregationLogical` as part of this phase. The aggregation port (A4) calls into these.

**Bound propagation:** every `rangeRequiredBoundsForChild` call site inside range.go flips to `logicalRangeRequiredBoundsForChild`.

**Verify additions:** run the full bench matrix (`scripts/run-bench.sh --matrix`) because range is the single largest body and most bench-weighted surface.

### A4 — Aggregation

**Fragment body:** ~500 lines across `renderer/aggregation.go` and `renderer/aggregation_range_fused.go` (129 lines).

**Child kinds to handle:** `LeafExprPlan`, `UnaryPlan`, `BinaryPlan` (scalar-involving), `RangeFunctionPlan`/`RatePlan`/etc. (triggers fusion). `renderAggregationSource` today supports 3 Fragment kinds (LeafSource, UnarySourceExpr, BinaryScalarSourceExpr); the logical version dispatches on the child's `logical.Node` type instead.

**Fusion call:** delegates to the `tryRenderFusedRangeAggregationLogical` helper ported in A3.

**Completes Phase A.** At this commit: refresh `harness/bench/baseline.json`, confirm compliance stays at 538/539.

---

## Phase B — Scalar-involving `BinaryPlan` direct-render

`lower.go:146-151` currently reads:

```go
if lhsInfo.TimeDomain == logicalpkg.DomainScalar || rhsInfo.TimeDomain == logicalpkg.DomainScalar {
    fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
    if err != nil { return RenderedQuery{}, err }
    return RenderFragment(ctx.Config, fragment, ctx.Params)
}
```

**Port shape.** Build a `lowerBinaryScalarInvolving(ctx, n)` that:

1. Identifies the scalar side via `lhsInfo.TimeDomain == DomainScalar` / `rhsInfo.TimeDomain == DomainScalar`.
2. Lowers the non-scalar side via `Lower(ctx, nonScalarChild)` to produce its `RenderedQuery`.
3. Folds the scalar side via `lowerScalarLiteral` / `lowerScalarBuiltin` / `lowerScalarConvert` to produce the scalar value or SQL fragment.
4. Wires the binary op through `renderValueTransformFromSource` (the same helper the unary SUB path already uses).
5. Returns the finalized `RenderedQuery`.

This eliminates the last consumer of `FragmentKindBinaryScalarSourceExpr`. The shared Fragment kinds (`FragmentKindUnarySourceExpr`, `FragmentKindBinaryScalarSourceExpr`) can now be deleted in the Phase C trailing commit.

**Verify:** `scripts/run-compliance.sh`, renderer tests, the `harness/corpus/binary_scalar_*` fixtures specifically.

---

## Phase C — Delete Fragment machinery

Single cleanup commit after Phase B. Deletes:

- `FragmentKind*` constants in `native/types.go`
- `NativeFragment` struct and all its fields
- `native.BuildFragment`, `native.CloneFragment`, `native.RenderFragment` (if it still exists as a wrapper)
- The Fragment-producing branches in `native/analysis.go` (including the UnaryPlan branch at lines 62-103)
- The `Fragment` field on `native.FragmentInfo`; rename `FragmentInfo` → `NodeInfo`
- `renderer/source.go`'s `renderSourceFragment` and `forceFragmentFullTags`
- The `renderXxxFragment` functions for each of the four ported bodies
- The `SelectorSource.RequireFullTags` / `RequiredTagLabels` fields on `native.SelectorSource` (moved to `RenderParams` in Phase A)
- The Fragment-dispatch fallback in `local/native_subtree.go` — with `errUnsupportedLowerNode` no longer reachable from any lowerer, the fallback branch disappears. `IsUnsupportedByLower` stays (still used for non-lowering routing decisions elsewhere).

**Grep gate before commit:**
```bash
grep -r "NativeFragment\|FragmentKind\|BuildFragment\|RenderFragment" internal/promshim/
```
Expected: zero matches outside of deleted files.

---

## Verification gates

### Per commit (all phases)

- `go build ./internal/promshim/...`
- `go test ./internal/promshim/native/renderer/...`
- `go test ./internal/promshim/...`

### End of Phase A

- `scripts/run-compliance.sh` — expect 538/539 (only the allowlisted `topk-tie-break-ordering`)
- `scripts/run-bench.sh --matrix` — no regression >+10% on any query vs pre-Phase-A baseline; refresh `harness/bench/baseline.json`
- `scripts/run-harness.sh` — all corpora pass

### End of Phase B+C

- `scripts/run-compliance.sh` — 538/539
- `scripts/run-bench.sh --matrix --long-range all` — write fresh `harness/bench/baseline.json`
- `grep -r NativeFragment internal/promshim/` returns zero matches
- `scripts/run-harness.sh` — all corpora pass

---

## Risks and mitigations

### R1 — Tag-narrowing miss

**Risk:** A narrowing site is overlooked during a histogram port, causing the shim to read too-wide label sets for bucket queries.

**Impact:** Correctness-silent performance regression; rendered SQL still returns correct values but reads more columns than needed.

**Detection:** compliance suite's `histogram_quantile`/`histogram_count` rows diverge byte-for-byte from Prometheus (narrowing affects the generated column list, visible in SQL); bench matrix surfaces the performance delta on `histogram_*` queries.

**Mitigation:** The Phase A verification gate runs the full bench matrix; any narrowing regression is visible in the `histogram_quantile_*` and `histogram_count_*` rows.

### R2 — External `native.Analysis.Fragment` consumer

**Risk:** Code outside `renderer/` and `local/native_subtree.go` reads `FragmentInfo.Fragment`. Phase C deletion breaks it silently.

**Impact:** Build failure at Phase C commit.

**Mitigation:** Pre-commit grep gate:
```bash
grep -rn "\.Fragment\b" internal/promshim/ --include='*.go' | grep -v '_test.go\|renderer/\|native_subtree.go\|native/analysis.go\|native/builder.go\|native/types.go'
```
Must return zero matches before deleting. If matches exist, port them first.

### R3 — Fused range+aggregation shape drift

**Risk:** The fusion structural check encodes assumptions about the Fragment's child shape (specifically `Aggregation → RangeFunction`, plus scalar-on-one-side variants). Porting it to logical must preserve the exact structural check or byte-identical SQL breaks on fused queries.

**Impact:** Bench regression or compliance divergence on fused queries like `rate(foo[5m]) / sum by(x) (rate(foo[5m]))`.

**Mitigation:** The A3 and A4 commits each run `scripts/run-bench.sh` and `scripts/run-compliance.sh`; the `aggregation_rate_fused_*` corpus rows are the canary.

### R4 — `RenderParams` field bleed

**Risk:** Adding `RequireFullTags` / `RequiredTagLabels` to `RenderParams` means every lowerer sees them, including lowerers that should never set them. A lowerer that forwards its own `ctx.Params` unchanged to a child that expects *different* narrowing gets the parent's narrowing incorrectly.

**Impact:** Correctness divergence on nested histogram-function queries.

**Mitigation:** Convention enforced in the porting template: renderers that propagate narrowing MUST construct a fresh `RenderParams` for child calls, explicitly setting the tag fields. Renderers that don't care MUST pass through unchanged. Add a renderer unit test case per body: "parent narrowing does not leak to unrelated child."

---

## Out of scope

- Tier 3/4 (subtree pushdown, full local executor) — per `CLAUDE.md`, new work lives only in tiers 1/2.
- Delegation classifier / tier-1 capability map — untouched.
- `harness/` / `scripts/` — only the bench baseline file refreshes.
- Chart, SSO, Thanos compat — unchanged.

---

## Success criteria

1. `grep -r NativeFragment internal/promshim/ --include='*.go'` returns zero matches.
2. `scripts/run-compliance.sh` reports 538/539 (allowlisted `topk-tie-break-ordering` only).
3. `scripts/run-bench.sh --matrix --long-range all` produces a refreshed baseline with no query >+10% over the pre-retirement baseline.
4. `go test ./internal/promshim/...` passes.
5. `renderer.Lower` never calls `native.BuildFragment` and never inspects a `*NativeFragment`.
6. `native.Analysis` survives with `NodeInfo` (no `Fragment` field); the Fragment-producing walk is gone.
