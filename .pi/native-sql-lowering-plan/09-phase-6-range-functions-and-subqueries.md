# 09 — Phase 6 range functions and subqueries

## Status

This phase split in practice. See
[00-status-and-drift.md](./00-status-and-drift.md) for the long form.

Current status as of 2026-04-21:

- **Phase 6a is delivered.** The local range / counter implementations
  remain in `internal/promshim/exec/` as the correctness oracle, and the
  test-only oracle interfaces needed by native differential tests are in
  place.
- **Phase 6b is delivered for the current supported subset.** Native SQL
  lowering now covers the planned direct-selector and supported
  subquery-backed range-function subset for both instant and range mode,
  including the aggregate-over-time functions and the first counter
  subset.
- **Stable harness promotion has started.** The stable corpus in
  `harness/corpus/queries.json` now includes promoted Phase 6 native rows
  for:
  - `rate`
  - `increase`
  - `sum_over_time`
  - `count_over_time`
  - `rate(sum by (...) (...)[range:step])`
- **Optional promclick behavior is not a blocker for these promotions.**
  Rows that rely on functionality promclick does not support are marked
  with `"subjects": ["shim"]`; Prometheus remains the oracle in those
  runs.
- **Local retirement has not happened yet.** The local path stays in
  place as the oracle until the observation / retirement work described
  below is completed during rollout.
- **`quantile_over_time` is explicitly keep-local for now.** See
  [13-keep-local-quantile-over-time.md](./13-keep-local-quantile-over-time.md).
  It is intentionally excluded from the current native Phase 6 subset and
  does not count as an untracked native-lowering gap.

The policy from the status chunk still applies:

1. keep the local implementations as correctness oracle
2. add no net-new local range / counter functions unless the conformance
   harness strictly requires it
3. every future native lowering ships with a differential test against the
   local implementation; the local implementation may only be retired
   after a promotion window
4. transforms that are not range / counter functions (label mutation,
   `histogram_quantile`, `absent`, scalar helpers) stay on path 3
   indefinitely and are outside this policy

## Goal
Make subqueries and range functions participate fully in maximal-subtree
native lowering, while keeping the existing local implementations as the
correctness oracle until each native replacement is promoted.

## Scope
- normalized range-vector fragment lowering
- subquery step-grid expansion as part of native lowering (not as an
  execution barrier)
- lowering for the first range-heavy subset, starting with:
  - `last_over_time`
  - `sum_over_time`
  - `avg_over_time`
  - `min_over_time`
  - `max_over_time`
  - `count_over_time`
- later, the counter subset already implemented locally:
  - `rate`
  - `irate`
  - `increase`
  - `delta`
  - `changes`
  - `deriv`
- partial lowering around unsupported roots
- whole-root lowering when the range subtree is fully supported

## Phase 6a — contain and catalog the local path

1. **Inventory existing local range / counter implementations**
   - enumerate every file under `internal/promshim/exec/` that implements
     a range-vector or counter operator
   - for each, record:
     - Prometheus semantic rules it encodes (edge behavior, reset
       handling, extrapolation)
     - which upstream Prometheus reference the semantics track
     - any known divergences or workarounds
       (e.g. commit `0d44c2b` "Workaround inclusive vs exclusive left edge")

2. **Freeze the local surface**
   - explicit gate: no new range-vector or counter function lands locally
     unless the conformance harness requires it to progress
   - any such addition must carry a native-lowering tracking note

3. **Prepare local implementations as differential oracles**
   - expose a stable test-only interface that Phase 6b can call to compare
     sample-by-sample against future native renderings

## Phase 6b — distinct tasks for native lowering

1. **Implement normalized range-vector fragment lowering**
   - keep range fragments relational and tall internally
   - delay final matrix shaping until late finalization

2. **Implement subquery step-grid expansion**
   - expand subqueries without treating them as mandatory execution barriers
   - make required-time-range propagation honor subquery timing
   - reuse the grid / step semantics that the local subquery path already
     validated, but emit them as SQL rather than as Go iteration

3. **Implement the first native range-function subset**
   - start with `last_over_time`, `sum_over_time`, `avg_over_time`,
     `min_over_time`, `max_over_time`, `count_over_time`
   - keep function rewrite and lowering behavior tied to Prometheus
     semantics; ClickHouse is the lowering-shape oracle but not the
     semantic oracle
   - ship each with a differential test against the corresponding local
     implementation

4. **Lower the counter subset once the aggregate subset is stable**
   - `rate`, `irate`, `increase`, `delta`, `changes`, `deriv`
   - these are the highest-risk operators because of counter reset,
     extrapolation, and edge-inclusion rules; plan for an extended
     promotion window before retiring the local implementations

5. **Support mixed plans around unsupported roots**
   - lower maximal range / subquery child islands even when the root still
     executes locally (e.g. `histogram_quantile`, label mutation,
     `absent`)

## Promotion and retirement

Current checkpoint:

- The promoted Phase 6 stable-corpus rows now live in
  `harness/corpus/queries.json`.
- These rows are expected to stay green in Prometheus-vs-shim harness
  runs; use `--subjects shim` when validating this promotion set if
  optional promclick support is not relevant to the query.
- The local implementations are intentionally still present. This phase
  finishes native lowering and initial promotion, but does **not** retire
  the path-3 oracle yet.

For each operator moved from path 3 to path 2:

- differential test must run on the supported corpus for a promotion
  window (length decided at rollout, see
  [10-phase-7-rollout-and-shadow-comparison.md](./10-phase-7-rollout-and-shadow-comparison.md))
- native result is served only after the promotion window is green
- local implementation is retired only after the native result has been
  served for a further observation window with no regressions
- retirement PR removes the local file and its test; the native
  differential test against Prometheus / the delegated path becomes the
  primary correctness guarantee going forward

### Further retirement — up the execution ladder

The strategic endgame (see
[00-status-and-drift.md](./00-status-and-drift.md)) is that queries move
up the execution priority ladder as ClickHouse's native PromQL evaluator
on the TimeSeries engine gains coverage. A path-2 native lowering for a
range function becomes less load-bearing once upstream `prometheusQuery`
supports that function well enough that whole queries using it qualify
for rung 1 (entire-query delegation). The path-2 implementation is not
removed while there are still queries that combine it with
non-delegatable operators — those continue to use rung 2 — but its role
shifts from "primary evaluator" to "fallback for mixed plans". Track
each path-2 operator's upstream support status in the capability map
used by the entire-query classifier so the shift is visible in explain.

## Validation
- unit tests for time-window propagation
- integration tests for subquery examples
- **differential tests between path 3 (local) and path 2 (native) for every
  operator that exists on both paths**
- differential tests against delegated path / Prometheus where practical,
  especially for counter-reset and edge-inclusion behavior
- the differential harness itself must gain per-query time-window and
  range-step coverage before the first native range-function lowering
  ships — see
  [12-harness-parametrization.md](./12-harness-parametrization.md)
  (Layer 1 before the first native aggregate-over-time lowering, Layer 2
  before the first native `rate` / `increase` / `delta` lowering)
- promoted Phase 6 stable rows now live in `harness/corpus/queries.json`
  and are validated via the harness against Prometheus; rows that are not
  meaningful for optional promclick comparison are explicitly scoped to
  `"subjects": ["shim"]`
