# Logical IR — Phase 1–3 design

**Status:** proposed
**Date:** 2026-04-23
**Scope:** `internal/promshim/` native-SQL lowering path (tier 2)
**Horizon:** 6–12 months until ClickHouse native PromQL (tier 1) covers enough surface to retire this code

## Context and motivation

Promshim is a bridge. Its purpose is to serve PromQL against ClickHouse until upstream tier 1 (native PromQL in ClickHouse) catches up. Current 4-tier priority: whole-query delegation → native SQL lowering → subtree pushdown → full local execution. Tier 2 (native SQL lowering) is where this design operates.

Today tier 2 has nine load-bearing architectural constraints that block pass-style optimization:

1. SQL is strings, not algebra. `sqlb.RawLit{V: "arrayFilter(...)"}` is the vocabulary. No relational IR.
2. Renderer organized by PromQL surface (`range.go`, `histogram.go`, `clamp.go`, ...), not by relational operation. Shared subexpressions are duplicated inline.
3. No intermediate logical IR between the Prometheus AST and SQL emission. No middle where "constant fold," "drop redundant grouping," "push filter under aggregate" could live as rules.
4. Routing is capability-based, not cost-based.
5. Tier boundaries are hard. A query with one unsupported leaf drops wholesale to the next tier.
6. Selector/Aggregation contracts are additive structs, not composable rules.
7. Tests pin exact SQL substrings. The test suite actively resists structural refactors.
8. No routing-distribution observability.
9. Plans are not cached.

This design takes on **clusters 1, 2, 3, 6, and 7** (the algebra gap and its test tax). Clusters 4, 5, 8, 9 (cost-based routing, partial delegation, observability, plan caching) are out of scope.

### Horizon framing

Promshim retires when tier 1 lands. Realistic remaining lifetime is 6–12 months. Any architectural investment must amortize inside that window. A substrate that takes three months to build pays back poorly against nine months of runtime. This design therefore targets a ~3–4 week architectural arc with payback starting immediately after, not a ground-up rewrite.

## Decision: Shape C — enriched PromQL-mirror IR, reactive upgrade path

After considering three IR shapes:

- **Shape A:** enrich today's PromQL-mirror nodes with relational metadata; keep node vocabulary PromQL-shaped.
- **Shape B:** build a bounded relational algebra (Scan/Filter/Project/Aggregate/Join/Window + satellite nodes for PromQL-specific functions).
- **Shape C:** Shape A now; Shape B reactively, only if a specific pass demands relational operators.

**Chosen: Shape C.** Shape A delivers a real IR (nodes carry schema/lineage/time domain; renderer consumes nodes directly; passes pattern-match) in weeks. Shape B would take months and may never be needed if Shape A's node vocabulary is sufficient. Shape C leaves the relational upgrade as a reactive move: introduce Filter/Project/Aggregate as *satellite* nodes next to the PromQL-mirror ones when (and only when) a pass can't be expressed on the existing vocabulary.

## Non-goals

- Cost-based routing or pass ordering.
- Plan-level memoization or caching.
- Upstream parser changes.
- Shape B migration (reactive only).
- Any change to the tier-1 delegation path, tier-3 subtree pushdown, or tier-4 local execution.

## Architecture

### Pipeline

```
parser.Expr  ─►  logical.ToLogical  ─►  logical.Node  ─►  renderer.Lower  ─►  emit/  ─►  SQL
                                            │
                                            ▼
                                    optimizer.Optimize (Phase 2+)
```

### New packages

- `internal/promshim/logical/` — the IR. Houses node types, enrichment (`logical.Analyze`), and `logical.ToLogical(parser.Expr) → logical.Node`.
- `internal/promshim/logical/opt/` — Phase 2 addition. Houses the `Pass` interface, fixpoint runner, and concrete passes.

### Renames and absorptions

- `internal/promshim/plan/` → `internal/promshim/logical/`. All 28 node types come along; exported names drop the redundant `Logical` prefix (`LogicalAggregationPlan` → `AggregationPlan`) since the package name now supplies it.
- `internal/promshim/local/BuildLogicalPlan` → `logical.ToLogical`. `local/` keeps a thin forwarding shim during migration.
- `internal/promshim/native/analysis*.go` → absorbed into `logical.Analyze`. `native.Analysis` becomes `logical.Analysis`.
- `internal/promshim/native/builder.go` + `NativeFragment` → deleted in Phase 3 once renderer dispatch is fully ported.

### Unchanged

- `parser.Expr` remains the input; upstream Prometheus parser is untouched.
- `emit/` (introduced in Phase 0) remains the SQL vocabulary. It grows as lowering-functions surface more primitives.
- `httpapi/`, `compliance/`, `harness/`, `storage/` — unchanged surface. They see a different package path for logical plan types.

## Node enrichment

Every `logical.Node` gets a sibling `logical.NodeInfo` entry in `logical.Analysis.Info[node]` with:

| Field | Type | Meaning |
|-------|------|---------|
| `Schema.Guaranteed` | set of label names | labels always present on output series at this node |
| `Schema.Possible` | set of label names | labels that may appear (e.g., produced by `label_replace`, join targets). |
| `TimeDomain` | enum: `Instant \| Range \| PointLookup \| Scalar` | which evaluation shape this node produces |
| `GroupingKey` | `[]string + Without bool` | for aggregations / joins, the grouping labels |
| `LabelLineage` | input→output label map | propagated from existing `native.Analysis` |
| `DropsMetric` | bool | whether `__name__` is dropped at this node |
| `TimeRequirements` | start/end/step needs | propagated from existing `native.Analysis` |

`Predicates` (proven label-matcher filters) is **deferred**. It is listed in the original contract to serve a future tag-late-join pass; Phase 2's trivial pass does not consume it, and YAGNI says add it with the first pass that needs it, not speculatively.

### Storage model — side map

`NodeInfo` lives in `logical.Analysis.Info[node]`, not as struct fields on the node itself.

- Nodes stay structurally identifiable by PromQL shape; two equivalent subtrees are equal without attribute noise.
- Passes that rewrite subtrees return new nodes without having to re-fill attributes in place. The optimizer rebuilds the Analysis after each rewriting pass.
- Phase 1 becomes a pure addition: existing node types untouched; Analysis grows new fields.

### Computation model

`logical.Analyze(root) → *logical.Analysis` is a single bottom-up walk. It replaces `native.Analyze`. The LoweringInfo fields that are renderer-concerned (`NativeLowerable`, `Fragment`, `NativeReason`) disappear from the post-Phase-3 API — the renderer dispatches on `NodeInfo` + node type directly.

## Renderer redesign

### Entry point

```go
type LoweringCtx struct {
    Analysis *logical.Analysis
    Params   RenderParams
    // query envelope, config, helpers...
}

func Lower(ctx LoweringCtx, node logical.Node) (RenderedQuery, error)
```

Internal dispatch is a type switch over `logical.Node`; each case calls a per-node-kind `lower*` function (e.g., `lowerAggregation`, `lowerBinary`, `lowerRangeFunction`).

### Per-kind lowering pattern

```go
func lowerAggregation(ctx LoweringCtx, n *logical.AggregationPlan) (RenderedQuery, error) {
    info := ctx.Analysis.Info[n]
    child, err := Lower(ctx, n.Child)
    if err != nil { return RenderedQuery{}, err }
    // compose SQL using emit/ primitives + info fields + child.SQL
}
```

Lowering functions are pure given `ctx`. No package-level state. No Fragment templating. SQL concatenation goes through `emit/` helpers, not through `sqlb.RawLit{V: "..."}` inline strings.

### File organization

- **Phase 1:** files stay at `native/renderer/*.go`. Each file's public entry point becomes a `lower*` function driven by `Lower(node, info)` dispatch rather than Fragment dispatch. Only a subset of node kinds is dispatched through the new path in Phase 1; the rest still route through Fragment.
- **Phase 3:** files reorganize by node kind (`lower_aggregation.go`, `lower_rangefunc.go`, `lower_binary.go`, `lower_histogram.go`). Files may relocate to `internal/promshim/logical/render/` to colocate with the IR. `NativeFragment` is deleted.

### emit/ growth during Phase 1–3

Every lowering-function-internal `sqlb.RawLit{V: "..."}` site is a candidate for `emit/`. Expected additions (non-exhaustive):

- `emit.TagsFromMetric(metric, tags)` — the `arrayConcat([('__name__', metric)], tags)` pattern.
- `emit.CountNaN(col)`, `emit.CountFinite(col)` — the NaN triad counters.
- `emit.TimestampParamBound(paramName)`, `emit.IntervalParam(paramName)` — parameterized time expressions.
- `emit.GridJoin(dataAlias, gridAlias, window)` — the grid-range INNER JOIN scaffolding.

`emit/` stays a flat vocabulary. No node types, no trees. It is the canonical phrasing of ClickHouse primitives so lowering functions never concatenate strings directly.

### Relationship to `storage/` callees

`storage/{selector_sql,join_sql,aggregation_rows,info_join_sql,sql}.go` continue to be called from lowering functions. They already use `emit/` as of Phase 0. No rewrite here — these are leaf builders.

### Coexistence period (Phase 1)

Phase 1 runs both dispatchers. The split is **hierarchical, not per-subtree**: a given root query is rendered entirely by `Lower` or entirely by Fragment, never mixed within one query. This prevents double-render divergence. Shadow mode + compliance catch any divergence at the query-result level.

## Pass infrastructure (Phase 2)

### Interface

```go
type Pass interface {
    Name() string
    Apply(logical.Node, *logical.Analysis) (logical.Node, bool, error)
    // returns: (possibly-rewritten root, changed?, err)
}
```

### Runner

```go
func Optimize(root logical.Node, passes []Pass) (logical.Node, *logical.Analysis, error) {
    for iter := 0; iter < maxIterations; iter++ {
        analysis := logical.Analyze(root)
        changed := false
        for _, p := range passes {
            next, didChange, err := p.Apply(root, analysis)
            if err != nil { return nil, nil, err }
            if didChange {
                root = next
                analysis = logical.Analyze(root)
                changed = true
            }
        }
        if !changed { return root, analysis, nil }
    }
    return nil, nil, fmt.Errorf("optimizer did not reach fixpoint in %d iterations", maxIterations)
}
```

- Fixed order (mirrors existing `FixedPassOrder`).
- Re-analysis after each rewriting pass so downstream passes see fresh enrichment.
- Fixpoint cap (e.g., 8 iterations) as safety against cycling rewrites.
- Passes are pure — no in-place node mutation.

### Phase 2 deliverable

One concrete, trivially-correct pass ships with the infrastructure. Candidate: **constant-fold-unary-negation** (`-(-x) → x`). Alternatives if the candidate doesn't surface first: drop `offset 0`, eliminate redundant `vector()` wrapping.

### Registration

Explicit slice:

```go
var defaultPasses = []Pass{
    constantFoldUnaryNegation{},
    // future passes appended in fixed order
}
```

No discovery, no plugin system.

### Interaction with `native/optimizer.go`

Today's `native/optimizer.go` has 9 Fragment-level passes. In Phase 2 the new node-level infrastructure runs *before* Fragment construction; Fragment-level passes continue to run where they do today. In Phase 3 the Fragment-level passes that can be expressed as node-level rewrites migrate; the rest collapse into lowering functions when Fragment is deleted.

### Rules sub-abstraction

**Not introduced.** A `Rule` (pattern-match + rewrite) sub-abstraction would be premature. If 30 passes of the same shape duplicate walk boilerplate later, a Rule abstraction can be distilled then.

## Test strategy

### Gates, in priority order

1. **Compliance** (`scripts/run-compliance.sh`). 538/539 (topk tie-break allowlisted). Runs after every commit.
2. **Bench** (`scripts/run-bench.sh`, `--matrix`, `--long-range`). Native-vs-Prom p50 ratios and long-range profiles must not regress beyond agreed thresholds (Phase 1: ±3%; Phase 3: ±5% per-query, no systematic regression). Runs after each phase, not per commit.
3. **Analysis unit tests** (new). Hand-built trees assert enrichment attributes. This is where IR correctness lives.
4. **Lowering unit tests** (migrated). One golden-SQL file per PromQL shape under `native/renderer/testdata/`. Byte-identical comparison. Updated via standard `-update` flag.

### Migration policy

- **Phase 1:** new Analysis tests added; existing renderer substring tests untouched.
- **Phase 3:** as each surface is ported, its existing substring tests are *replaced* by golden-SQL tests in the same commit. No parallel-maintenance period, no bulk-migration sprint.
- No custom tooling. Standard Go testdata pattern.

### What goldens don't cover

- Renderer performance — bench matrix is the signal.
- Semantic equivalence to Prometheus — compliance is the signal.
- Error message wording — tests assert error *kinds* (`IsBadData`, `IsUnsupported`), not strings.

### Phase 3 SQL churn is accepted

Every ported surface will produce phrasing changes (whitespace, alias names, subquery shapes) even when semantically identical. Compliance and bench stay green; the goldens absorb the churn. Review burden on Phase 3 commits is higher than normal — this is explicitly accepted for the porting window.

## Phase gates and acceptance

### Phase 1 — IR package + Node-driven dispatch (core subset)

**Ships:**

- `internal/promshim/logical/` package exists. Types promoted from `plan/` (renamed, `Logical` prefix dropped).
- `logical.ToLogical(parser.Expr) → logical.Node`. Replaces `local.BuildLogicalPlan`. `local/` forwarding shim marked deprecated.
- `logical.Analyze(root) → *logical.Analysis` implementing the Section 2 enrichment contract (without `Predicates`).
- `native/renderer/Lower(ctx, node) → RenderedQuery` exists and dispatches leaf, scalar literal, and binary-trivial nodes through the new path. All other nodes still route through Fragment.
- Analysis unit tests for enrichment.

**Gates:**

- `go build ./...` green.
- `go test ./internal/promshim/...` green.
- `scripts/run-compliance.sh` → 538/539.
- `scripts/run-bench.sh --matrix` → N/P ratios within ±3% of pre-Phase-1 baseline.
- No new allowlist entries in `harness/compliance/expected-failures.json`.

**Rollback:** revert commits. No data migration, schema change, or external-surface change.

**Estimate:** ~1 week.

### Phase 2 — Pass infrastructure + one trivial pass

**Ships:**

- `internal/promshim/logical/opt/` package with `Pass` interface and `Optimize` fixpoint runner.
- One concrete pass.
- `httpapi/` pipeline calls `Optimize` between `ToLogical` and render.
- Unit tests for the pass and the runner (fixpoint, error propagation, no-change early exit).

**Gates:**

- Compliance 538/539.
- Bench unchanged (trivial pass should not move p50 materially).
- Trivial pass demonstrably fires on a corpus query (explain plan shows the rewrite).

**Estimate:** ~2–3 days.

### Phase 3 — Port remaining renderer dispatch; delete NativeFragment

**Ships:**

- Every PromQL shape ported from Fragment dispatch to Node dispatch.
- File reorganization by node kind.
- `NativeFragment` type deleted. `native.BuildFragment` deleted. Fragment-producing `native/analysis*.go` files deleted.
- `native.Analysis` fully replaced by `logical.Analysis`.
- Substring-pinning renderer tests replaced by golden-SQL tests.
- `emit/` grows to its steady-state vocabulary.

**Gates:**

- Compliance 538/539.
- Bench: ±5% per-query permitted; no systematic regression across the matrix.
- Long-range bench (7d/30d/1y) green.
- `grep -r NativeFragment internal/promshim/` returns no matches in code (comments allowed).

**Rollback:** Phase 3 is a sequence of per-surface commits. Any single surface's port can be reverted independently.

**Estimate:** ~2 weeks.

## Risks and escape hatches

### R1 — NativeFragment fields don't all map to NodeInfo

Some Fragment fields (e.g., `ValueExpr: "{value}"` templating in `SelectorSource`) may be rendering-local state, not enrichment. **Escape:** let `LoweringCtx` grow. Don't force-fit every Fragment field onto `NodeInfo` if it's really a rendering concern.

### R2 — Schema ambiguity for label creators

`label_replace`, `count_values`, `info` joins, `histogram_quantiles` produce labels whose presence depends on runtime conditions. **Escape:** the two-valued `Schema.Guaranteed / Schema.Possible` already addresses this in the enrichment contract (Section 2).

### R3 — `Predicates` listed but unused in Phase 2

**Escape:** deferred out of Phase 1 (noted in enrichment section). Added with the first pass that consumes it.

### R4 — Golden-SQL churn in Phase 3

**Escape:** goldens may be "accepted on first write" in Phase 3 porting commits; review focuses on lowering logic. Subsequent changes to goldens are reviewed normally. This is a conscious, time-boxed loosening of review standard.

### R5 — Shape A hits its ceiling mid-Phase 3

A surface resists clean lowering on PromQL-mirror nodes. **Escape:** introduce a single relational-shaped satellite node (e.g., `Filter` or `Project`) for that surface. `ToLogical` can produce either shape based on source AST. Not a full Shape B migration. If this happens 2–3 times, that's the signal for a deliberate Shape B follow-on.

### R6 — Coexistence double-render

Phase 1's split renders some queries via `Lower`, others via Fragment. **Escape:** hierarchical split (whole-query level, never per-subtree). Shadow mode + compliance catch any divergence at query-result level.

### R7 — Tier 1 lands early

ClickHouse ships full PromQL in 3 months instead of 9. **Escape:** horizon risk is equal for any work in `internal/promshim/`. Phase 1's IR has better ROI per week than surgical passes because every subsequent pass costs days. The horizon doesn't change the choice; it just means stop work at the first signal tier 1 is landing.

### R8 — Phase 3 stalls in dual-dispatch state

Other work interrupts; half-ported renderer persists. **Escape:** per-surface Phase 3 commits are independently green. A half-ported state is acceptable for arbitrary time. Fragment deletion is the final Phase 3 commit; it's only taken when every surface is through `Lower`.

## Out of scope (future work)

- Phase 4 — First real optimization pass (tag-late-join). Separate spec once Phase 1–3 lands.
- Cost-based routing (point 4 in the diagnosis).
- Plan-level memoization/caching.
- Shape B (relational algebra upgrade). Reactive only.
- Routing-distribution observability.

## Success criteria (end of Phase 3)

- Every rendered SQL fragment flows through `emit/` vocabulary; no direct `sqlb.RawLit{V: "..."}` in lowering files.
- Every lowering function dispatches on `logical.Node` type and reads enrichment from `logical.Analysis`.
- `NativeFragment` is deleted from the codebase.
- Compliance 538/539.
- Bench within agreed tolerances vs baseline.
- At least one pass (Phase 2's trivial pass) is running in production evaluation.
- Adding a new pass requires only editing one file under `internal/promshim/logical/opt/` — not porting changes across renderer surfaces.
