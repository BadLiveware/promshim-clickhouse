# 00 — Status and drift (as of 2026-04-21)

## Why this chunk exists

The rest of this plan was written before commits `b9b3767` ("Plan 1 done"),
`c8be729` ("Step"), `ffe42d9` ("Subquery [i]rate"),
`0d44c2b` ("Workaround inclusive vs exclusive left edge"), and
`0bae376` ("Last harness fixes") landed. The architecture and phase ordering
still hold, but the codebase took a different route through the first stretch
of work than the original Phase 1 described. This chunk records the drift so
the remaining phases can be executed against reality instead of the original
intent.

## The three execution paths that now exist

Every query the shim evaluates flows through one of:

1. **Delegated** — `delegatedExprPlan` in `internal/promshim/planner.go`.
   Sends the PromQL expression to the upstream PromQL-over-ClickHouse engine.
   Correctness oracle and broadest fallback.

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

## What Phase 1 actually delivered versus what it was supposed to deliver

Planned for Phase 1 (see `04-phase-1-analysis-scaffolding.md`):

- `NativeLoweringInfo` with lowerability and metadata
- `NativeFragment` with columns, lineage, metric-name state, required input
  time range
- label-lineage tracker
- evaluation-range requirement propagation scaffolding
- bottom-up lowerability analysis walk

Actually delivered (commit `b9b3767` and surrounding work):

- lowerability decisions are **embedded ad-hoc** inside
  `buildExecPlanWithContext` and `decideNativeAggregationPushdown` in
  `internal/promshim/planner.go`
- no fragment IR
- no label-lineage type
- no required-time-range propagation scaffolding

Phase 1 is therefore **functionally partially done** — enough to keep the
existing aggregation pushdown alive and to let the recent range-function and
subquery work land — but **structurally undelivered**: there are no reusable
types that Phases 2-6 can build on.

Phase 1's task list is still live. Re-read it as "extract what is currently
inlined in the planner into first-class types" rather than "add new
greenfield analysis".

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

1. **Phase 1 (revised)** — extract the analysis types that the original
   Phase 1 promised but that ended up inlined in the planner. This is now
   largely a **refactor** of existing planner code, not a greenfield
   addition. See `04-phase-1-analysis-scaffolding.md`.
2. **Phase 2** — unchanged in intent. Explicitly depends on Phase 1 type
   extraction landing first; the generalized `nativeSubtreePlan` cannot be
   introduced cleanly until `NativeLoweringInfo` and `NativeFragment` exist
   as first-class types.
3. **Phases 3-5** — unchanged.
4. **Phase 6 (split)**:
   - **6a** — admit new local range / counter functions only when the
     conformance harness requires them; every such addition carries a
     tracking note for native lowering.
   - **6b** — begin native lowering for the range functions already
     implemented locally, gated by the promotion policy above.
   See `09-phase-6-range-functions-and-subqueries.md`.
5. **Phase 7** — unchanged.

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
