# 09 — Phase 6 range functions and subqueries

## Status

This phase split in practice. See
[00-status-and-drift.md](./00-status-and-drift.md) for the long form.

- **Path 3 (local) implementations of the first range-function subset have
  already landed** under `internal/promshim/exec/` (`rate`, `irate`,
  `increase`, `delta`, `changes`, `deriv`, `*_over_time`, histogram
  helpers), along with local subquery step-grid evaluation. This work
  unblocked the conformance harness but took these operators off the
  native lowering track that the original Phase 6 described.
- **Path 2 (native SQL) lowering for these same operators has not
  started**. That is the remaining, harder part of this phase.

The phase is therefore restructured into 6a (contain path 3) and 6b
(deliver path 2). The policy from the status chunk applies:

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
