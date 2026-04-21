# PromQL coverage expansion plan — split index

This directory plans the work to **widen the set of PromQL functions the
shim accepts at all**. It is distinct from, and complementary to,
[../native-sql-lowering-plan/](../native-sql-lowering-plan/):

- **native-sql-lowering-plan** — deepens the slice. Takes functions that
  already execute somewhere (usually path 3, local evaluation) and lowers
  them to native ClickHouse SQL (path 2) so reduction happens in the
  database instead of in Go.
- **promql-coverage-plan** (this one) — widens the slice. Takes functions
  the analyzer currently rejects outright and adds them to the supported
  surface, landing them on path 3 first per the Phase 6a pattern.

The two tracks share a promotion pipeline: new functions land local with a
differential test against Prometheus; any future native rendering of the
same function ships with a differential test against the local
implementation, following the Phase 6b gate in the lowering plan.

## Execution order between the two plans

**The native SQL lowering plan is implemented first. This coverage plan
is queued behind it and does not start until the lowering plan reaches
its definition-of-done (or an explicit hand-off point).**

Rationale:

- finishing the lowering ladder on the currently-supported surface
  delivers a bigger real-world win than widening the surface with more
  functions that still go through path 3
- the land-local-first policy below depends on the lowering plan's
  Phase 6b promotion gate existing; if coverage runs first, tier-2 and
  tier-3 items accumulate as local-only debt with no defined path to
  native, which is exactly the asymmetry the lowering plan was written
  to prevent
- tier 1's same-slice exception (scalar transforms landing local+native
  together) assumes the renderer's scalar-transform wrapper from Phase 4
  already exists — which it does today, but the pattern is stable only
  once Phases 5 and 6b ship

Exceptions that may jump the queue:

- a production workload explicitly blocked by a missing function
- a conformance-suite baseline failure that turns out to be a trivial
  fix (tier-1 math / clamp / trig / date-time)

Both cases are decided per-request, not bulk — see
[00-context-and-policy.md](./00-context-and-policy.md) for the policy.

## Read this first

[00-context-and-policy.md](./00-context-and-policy.md) — why this plan
exists as a separate track, scope and non-goals, the land-local-first
policy, and the relationship to the lowering plan.

## Tier structure

Because this plan runs **after** the native SQL lowering plan completes,
the renderer, optimizer, and Phase 6b promotion-gate pattern are all in
place before coverage work starts. Every item in this plan lands
**same-slice local+native**; the native rendering ships with a diff
test against the local implementation, following the Phase 6b gate
pattern applied in coverage-plan scope.

1. [01-tier-1-trivial.md](./01-tier-1-trivial.md) — pointwise scalar math,
   clamp, trig, date/time, sort, the four missing aggregators, three
   over-time stats. Each one is ~1-5 lines in the renderer.

2. [02-tier-2-moderate.md](./02-tier-2-moderate.md) — `scalar(v)`,
   `mad_over_time`, `info`. Native rendering needs a custom pattern
   (cardinality CASE, quantile-based MAD, metadata join) but nothing
   architectural. `mad_over_time` shares the windowed-arrays source
   primitive introduced in [03-predict-linear.md](./03-predict-linear.md).

3. [04-resets.md](./04-resets.md) — counter reset counting. Pairwise
   array scan on the resampled bucket; mechanical once the lowering
   plan ships. Separate file only because the grid-boundary corpus
   wants dedicated diff-test treatment.

4. [03-predict-linear.md](./03-predict-linear.md) — per-series linear
   regression over a range window. Native rendering uses a shared
   **windowed-arrays source** primitive plus
   `arrayReduce('simpleLinearRegression', ...)`. The source is reused
   by `mad_over_time` and `double_exponential_smoothing`.

5. [05-holt-winters.md](./05-holt-winters.md) — iterative double
   exponential smoothing (Prom 3: `double_exponential_smoothing`). The
   first Prometheus iteration simplifies algebraically, which lets the
   native renderer use a single `arrayFold` over the same windowed
   source. Fallback to activated-local-only if `arrayFold` proves
   unusable on the target ClickHouse version.

## Cross-references

- **Subqueries under range functions** (`rate(sum_over_time(...)[5m:1m])`
  et al.) — currently rejected by the analyzer at
  `plan/promql.go:297-399`. This is a Fragment-IR limitation in the
  shim, not a missing function, so it belongs in
  [../native-sql-lowering-plan/09-phase-6-range-functions-and-subqueries.md](../native-sql-lowering-plan/09-phase-6-range-functions-and-subqueries.md),
  not here. Noted only so the gap is not forgotten.

- **Binary vector join native lowering** (Phase 5 of the lowering plan) is
  a separate track and not part of coverage. Joins already execute via the
  local evaluator.

- **Conformance suite** — [../promql-conformance-suite-plan.md](../promql-conformance-suite-plan.md)
  wires the upstream [prometheus/compliance](https://github.com/prometheus/compliance)
  `promql/` suite into the harness as a breadth signal. Re-baseline after
  each tier closes; conformance failures are tagged by owning tier so
  tier completion maps directly to measurable failure-count drops.

## Recommended execution order

1. Tier 1 as a single sweep — the analyzer, logical builder, exec, and
   renderer changes are uniform across the category.
2. The **windowed-arrays source primitive** (see
   [03-predict-linear.md](./03-predict-linear.md)) as a shared step,
   because `predict_linear`, `mad_over_time`, and
   `double_exponential_smoothing` all depend on it.
3. Tier 2 individually, each with its own diff-test corpus entry.
   `mad_over_time` picks up the shared source from step 2.
4. `predict_linear` riding the same source.
5. `resets` as its own slice with dedicated boundary-case diff tests.
6. `double_exponential_smoothing` / `holt_winters` last, since the
   `arrayFold` shape is the most novel and benefits from the earlier
   items having exercised the shared infrastructure.
