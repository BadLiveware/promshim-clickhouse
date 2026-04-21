# 00 — Status and drift (as of 2026-04-21)

## Why this chunk exists

The rest of this plan was written before commits `b9b3767` ("Plan 1 done"),
`c8be729` ("Step"), `ffe42d9` ("Subquery [i]rate"),
`0d44c2b` ("Workaround inclusive vs exclusive left edge"), and
`0bae376` ("Last harness fixes") landed. The architecture and phase ordering
still hold, but the codebase took a different route through parts of the
first stretch of work than the original plan described, and the strategic
framing around why the native path exists at all has since sharpened. This
chunk records both so the remaining phases can be executed against reality
instead of the original intent.

## Strategic intent — this shim is a bridge

The shim exists only because ClickHouse's native PromQL evaluator on the
TimeSeries engine is not production-ready yet: the engine is still behind
`allow_experimental_time_series_table = 1`, is not available in ClickHouse
Cloud, has incomplete operator coverage, and is going through breaking
schema evolution (see upstream PR #99083, which renames the inner target
from `DATA` to `SAMPLES` and reshapes outer columns). The destination is
PromQL evaluated directly against ClickHouse via `prometheusQuery` /
`prometheusQueryRange`; this shim fills the gap until upstream catches up.

Implications that shape the rest of the plan:

1. **Storage target is the ClickHouse TimeSeries engine**, not the OTel
   exporter's MergeTree schema. ClickHouse staff (vitlibar, nikitamikhaylov)
   are actively building the PromQL semantic layer on TimeSeries in 2026 —
   math/comparison operators, quantile, `limitk`/`topk`/`bottomk`,
   date/time functions. Targeting TimeSeries means the shim shrinks as
   upstream coverage grows. The OTel schema is where ClickStack/HyperDX
   live, but HyperDX sidesteps PromQL entirely, so routing our native
   lowering there would make this shim permanent.

2. **Execution priority ladder.** Queries should run on the highest rung
   they qualify for:

   1. **Entire-query delegation** — full AST → path 1 as one
      `prometheusQuery` call. No shim-side processing. Goal state.
   2. **Native SQL only** — path 2 owns the whole execution and returns
      reduced results. May use `prometheusQuery` as an inner source for
      selectors. Reduction happens in ClickHouse.
   3. **Local execution with native-SQL matrix source** — path 2 emits
      selector SQL with pushed filters; path 3 iterates the matrix in
      Go. Strictly better than rung 4 for any selector Phase 3 can
      lower — label matchers hit ClickHouse indexes, no
      `prometheusQuery` wrapper overhead.
   4. **Local execution with delegated matrix source** — path 1 returns
      a matrix via `prometheusQuery`, path 3 iterates. Today's default
      for range functions. All samples cross the wire.

   Composition is asymmetric: path 2 can take path 1 as an inner SQL
   source; path 3 can consume matrices from either path 1 or path 2;
   path 2 cannot consume path 3 mid-query (SQL cannot accept Go values
   back).

   Two delegation mechanisms coexist: **maximum delegatable subtree** is
   the primitive that rungs 2-4 use to source data (it cannot be
   forbidden — path 3 depends on it); **entire query delegation** is the
   dedicated top-rung mode where the whole AST qualifies and the query
   bypasses the shim entirely. A whole-AST classifier keyed on a
   ClickHouse-version capability map decides whether the top rung
   applies; it does not forbid subtree delegation in the non-qualifying
   case.

3. **Retirement direction is up the ladder.** Phase 6b moves path-3
   operators up to path 2 (native lowering of range functions). Growth
   in upstream PromQL coverage moves queries from path 2 up to full
   delegation. Value of an upstream PromQL feature is measured by the
   fraction of real-world queries that become fully delegatable when it
   lands — a feature that only lets more subtrees delegate but doesn't
   let whole queries qualify is less valuable than one that tips a
   whole family of queries onto the top rung.

4. **PR #99083 and similar upstream changes are moving targets.** Selector
   SQL over `timeSeriesData` / `timeSeriesTags` / `timeSeriesMetrics` must
   go through an adapter layer keyed on ClickHouse version, not hardcoded
   column names, so a ClickHouse upgrade is a config change rather than
   a refactor.

## The three execution paths that now exist

Every query the shim evaluates flows through one of:

1. **Delegated** — `delegatedExprPlan` in `internal/promshim/planner.go`.
   Sends the PromQL expression to the upstream PromQL-over-ClickHouse engine
   via `prometheusQuery` / `prometheusQueryRange`. Used in two ways (see
   strategic intent above): as the top-rung **entire-query delegation**
   mode when the whole AST qualifies, and as the **subtree-delegation
   primitive** that paths 2 and 3 use to source data for larger plans.
   Correctness oracle and destination for every construct upstream can
   evaluate.

2. **Native SQL** — `nativeAggregationPlan` in `internal/promshim/planner.go`.
   Emits repo-owned ClickHouse SQL via
   `storage.BuildInstantAggregationQuerySQL` /
   `storage.BuildRangeAggregationQuerySQL`. Still special-cased for
   aggregation over a delegatable leaf; the generalized `nativeSubtreePlan`
   described in Phase 2 does not exist yet.

3. **Local execution** — `internal/promshim/exec/*.go`. Pulls a range-vector
   matrix from a child plan (usually the delegated path) and iterates over
   samples in Go. Home of the range-function, subquery, and label-mutation
   work added in the commits listed above: `rate`, `irate`, `increase`,
   `delta`, `changes`, `deriv`, `*_over_time`, histogram helpers, `absent`,
   label mutation, vector matching, transforms, subquery step-grid
   evaluation.

Path 3 is legitimate for transforms that cannot reasonably be expressed in
SQL (for example, most label mutations or `histogram_quantile`). The drift
is that **range functions and subqueries**, which the original plan put on
path 2, also ended up on path 3.

## Phase 1 — delivered

Phase 1 has shipped. The analysis scaffolding described in
`04-phase-1-analysis-scaffolding.md` now exists at
`internal/promshim/native/`:

- `types.go` — `LoweringInfo` (originally written as `NativeLoweringInfo`
  in the plan; renamed to `LoweringInfo` since it lives in the `native`
  package and the prefix was redundant), `NativeFragment`, `OutputKind`,
  `FragmentKind`, `AggregationSupport`, `TimeRequirements`, `Analysis`
  with `byNode` lookup, and an `ExplainInfo` projection for explain
  surfacing
- `analysis.go` — bottom-up `Analyze` walk over `planpkg.LogicalPlan`
- `lineage.go` — `LabelLineage` tracking
- evaluation-range requirements carried via `TimeRequirements` on each
  `LoweringInfo`

The aggregation-pushdown decision in the planner now reads from the
analysis pass rather than computing lowerability inline. Explain surfaces
lowerability, fallback reasons, fragment kind, aggregation eligibility,
lookback/offset, and label lineage.

Phase 2 and later consume these types as first-class inputs. There is no
remaining Phase 1 refactor debt; task-list items from the original Phase 1
doc should be read as "delivered" unless `04-phase-1-analysis-scaffolding.md`
explicitly calls out otherwise.

## What "Phase 6" work landed on the wrong path

Commits `ffe42d9` (subquery `[i]rate`), `c8be729` (step grid),
`0d44c2b` (edge semantics), and the matching code in
`internal/promshim/exec/` implemented range functions and subqueries as
**local execution (path 3)**. The original Phase 6 intended these to be
**native SQL lowerings (path 2)**, likely via ClickHouse window functions
over the time-series tables.

Local execution:

- matches Prometheus semantics closely (edge rules, reset handling)
- unblocks the conformance harness
- transfers every raw sample in the range across the wire before reducing

Native lowering:

- pushes the reduction into ClickHouse so only per-step results return
- has to re-derive `rate` / `irate` / `increase` / extrapolation rules in
  SQL, which is where correctness bugs will land
- is still the end goal of Phase 6

### Policy for the remainder of the work

1. **Keep the existing local implementations in place** as the correctness
   fallback. They are the in-repo Prometheus oracle for differential tests
   against future native renderings.
2. **Do not add any net-new range-vector or counter function on path 3**
   unless the conformance harness needs it to make progress. If one has to
   land locally to unblock the harness, it must ship with a tracking note
   for native lowering.
3. **Treat every local range / counter implementation as "awaiting native
   lowering".** Each is a candidate for Phase 6b below.
4. **Reject silent path migrations.** A PR that re-implements a local
   function natively must carry an explicit differential test against the
   local implementation. The local implementation may only be removed after
   the differential test has been green for a promotion window.

The risk this policy guards against is **semantic drift between paths 2 and
3** — two implementations of `rate` that then have to stay bit-for-bit
identical forever. The asymmetry is deliberate: local is where semantics are
learned, native is where they ship.

Transforms that are not range or counter functions (label mutation,
`histogram_quantile`, `absent`, most scalar manipulations) stay on path 3
indefinitely and are outside this policy.

## What still applies, unchanged, from the rest of the plan

- the target architecture: logical plan -> lowerability analysis ->
  maximal-subtree selection -> fragment IR -> staged RBO passes ->
  final SQL shaping -> execution
- reference precedence: Prometheus (semantic) > ClickHouse (lowering shape)
  > VictoriaMetrics / DataFusion / Calcite (optimizer mechanics)
- phase ordering, with the revisions below

## Revised phase ordering

1. **Phase 1** — delivered. See the "Phase 1 — delivered" section above and
   `04-phase-1-analysis-scaffolding.md` for acceptance detail.
2. **Phase 2** — unblocked. The `LoweringInfo` / `NativeFragment` types
   required by the generalized `nativeSubtreePlan` now exist as first-class
   types in `internal/promshim/native/`. See
   `05-phase-2-native-subtree-and-renderer.md`.
3. **Phases 3-5** — unchanged in structure, but Phase 3 selector lowering
   must go through an adapter for TimeSeries inner-table column shapes
   rather than hardcoding names, because upstream PR #99083 is actively
   reshaping them (see strategic intent above).
4. **Phase 6 (split)**:
   - **6a** — admit new local range / counter functions only when the
     conformance harness requires them; every such addition carries a
     tracking note for native lowering.
   - **6b** — begin native lowering for the range functions already
     implemented locally, gated by the promotion policy above.
   See `09-phase-6-range-functions-and-subqueries.md`.
5. **Phase 7** — unchanged in intent. Capability gating for delegation is
   a whole-AST classifier keyed on ClickHouse version, not a subtree scorer
   (see strategic intent above). See
   `10-phase-7-rollout-and-shadow-comparison.md`.

## Addendum to the definition of done

In addition to the criteria in
`11-validation-risks-and-definition-of-done.md`:

- every range or counter function implemented on path 3 has either been
  replaced by a native lowering on path 2 or carries an explicit "keep
  local" design note explaining why native lowering is not feasible
- differential tests exist between path 2 and path 3 implementations
  wherever both exist
- no net-new path-3 range / counter function ships without either a
  native-lowering tracking note or a "keep local" design note
