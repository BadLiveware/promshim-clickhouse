# 00 — Context and policy

## Ordering relative to the lowering plan

**The native SQL lowering plan is executed first.** This coverage plan
is queued behind it and does not begin bulk execution until the lowering
plan reaches its definition-of-done, or an explicit hand-off point is
declared.

See the "Execution order between the two plans" section in
[README.md](./README.md) for rationale and the narrow exception rules
(workload-blocked or trivial conformance-suite fixes).

The policy sections below still apply once this plan is active; the
land-local-first pattern depends on the lowering plan's Phase 6b
promotion gate being in place, which is the concrete reason for the
ordering.

## Why this plan is separate

The [native-sql-lowering-plan](../native-sql-lowering-plan/) explicitly
states as a non-goal (`01-context-and-guardrails.md:57`):

> do **not** chase full PromQL completeness before the common dashboard
> subset is fast and stable

That plan lowers the existing supported surface to native SQL. It takes
functions that already execute — usually on path 3 via
`internal/promshim/exec/` — and moves them to path 2 so ClickHouse does
the reduction.

This plan does the complementary job: it adds functions that the analyzer
currently rejects outright, so queries that use them stop 400-ing and
start executing. The two tracks share infrastructure (analyzer, logical
builder, diff harness) but make independent progress on orthogonal axes.

## Scope

In:

- scalar transform functions (math, clamp, trig, date/time, sort)
- aggregators the reducer registry is missing (`stddev`, `stdvar`,
  `quantile`, `group`)
- over-time stats (`stddev_over_time`, `stdvar_over_time`,
  `mad_over_time`, `present_over_time`)
- value-shape adapters (`scalar(v)`)
- metadata join functions (`info`)
- per-series windowed smoothing and counter behavior (`predict_linear`,
  `resets`, `holt_winters` / `double_exponential_smoothing`)

Out:

- native SQL lowering for any of the above — lives in the lowering plan
  once a local implementation exists and a workload wants it
- binary vector join native lowering — Phase 5 of the lowering plan
- subqueries under range functions — Fragment-IR limitation, lowering
  plan territory
- histogram native-type support (Prom 3 native histograms) — separate
  effort, not addressed here
- `info` function's dependency on a convention for which metric names
  are "info series" — that convention is OpenMetrics / Prom 3 territory
  and must be followed, not invented

## Non-goals

- do **not** add half-correct local implementations. Prometheus is the
  semantic oracle (see precedence below) and every new function ships
  with a diff test against it before the analyzer is opened up.
- do **not** expand the conformance harness surface without updating
  `harness/corpus/queries.json` in the same slice.
- do **not** let any coverage item land local-only by default. The
  lowering plan has completed before this plan runs, so infrastructure
  is ready and there is no reason to split into two slices. Local-only
  is an explicit fallback for items that stay hard even with
  infrastructure ready — today that fallback applies only to
  `holt_winters` / `double_exponential_smoothing` **if** `arrayFold`
  turns out to be unusable on the target ClickHouse version, and even
  then the item is marked activated-local-only with an upstream-UDF
  tracking note rather than quietly deferred.

## Policy — same-slice local+native

Because the lowering plan runs first (see ordering section above),
same-slice local+native is the default for every item in this plan.
The Phase 6b gate *pattern* still applies — native rendering ships
with a diff test against the local implementation — but it applies
within coverage-plan scope, not as a handoff to the lowering plan.

### Which items land same-slice local+native

- **Tier 1** ([01-tier-1-trivial.md](./01-tier-1-trivial.md)) — scalar
  math, clamp, trig, date/time, sort, aggregators, basic over-time
  stats. Single-expression renderer extensions; no drift surface.
- **Tier 2** ([02-tier-2-moderate.md](./02-tier-2-moderate.md)) —
  `scalar(v)`, `mad_over_time`, `info`. Custom pattern (CASE, nested
  quantile, metadata join) but infrastructure is ready.
- **`resets`** ([04-resets.md](./04-resets.md)) — pairwise array scan
  on resampled bucket. Mechanical post-lowering-plan; dedicated
  diff-test corpus for grid-boundary cases.
- **`predict_linear`** ([03-predict-linear.md](./03-predict-linear.md))
  — per-series linear regression. Uses a shared **windowed-arrays
  source** primitive plus `arrayReduce('simpleLinearRegression', ...)`.
- **`holt_winters` / `double_exponential_smoothing`**
  ([05-holt-winters.md](./05-holt-winters.md)) — iterative smoothing,
  expressed as a single `arrayFold` over the same shared windowed
  source once the first Prometheus iteration is algebraically folded
  into the initial state. Fallback: activated-local-only if
  `arrayFold` is unusable on the target ClickHouse version (with an
  upstream-UDF tracking note).

### Shared infrastructure

Tier 2's `mad_over_time`, `predict_linear`, and the holt-winters
smoothing item all share a **windowed-arrays source** that yields
per-`(series, grid_ts)` arrays of the samples inside the range window.
Specified in [03-predict-linear.md](./03-predict-linear.md), referenced
from the other two. Build it once; do not duplicate per function.

## Reference precedence

Same as the lowering plan, repeated here so this track can be executed
without switching tabs:

1. **Prometheus** — semantic authority.
   - [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)
   - [promql/functions.go](file:///home/fl/code/external/prometheus/promql/functions.go)
   - [promql/parser/parse.go](file:///home/fl/code/external/prometheus/promql/parser/parse.go)
2. **ClickHouse** — lowering-shape oracle, used only when a tier-1 item
   picks up a native rendering in the same slice.
3. **VictoriaMetrics** — sanity check for category edge cases (PromQL
   extensions, `range_*` families) but not a semantic authority.

## Relationship to the lowering plan

The lowering plan's `00-status-and-drift.md:186` parks "scalar
manipulations" on path 3 "indefinitely" as a *lowering-plan* position.
This coverage plan takes a tighter position: because it runs after the
lowering plan, same-slice local+native is cheap, so every coverage
item — tier 1, tier 2, `resets`, `predict_linear`, and
`double_exponential_smoothing` — lands natively in the same slice. The
only allowed local-only outcome is the `arrayFold` fallback noted
under holt_winters, and even that is a flagged activated-local-only
state, not a silent defer.

## Definition of done (plan-level)

This plan is complete when:

- every function in [01-tier-1-trivial.md](./01-tier-1-trivial.md)
  executes end-to-end with a Prometheus-parity diff test and a native
  rendering, in the same slice
- every function in [02-tier-2-moderate.md](./02-tier-2-moderate.md)
  executes end-to-end with a Prometheus-parity diff test and a native
  rendering diff-tested against the local implementation, in the same
  slice, with the per-function semantic notes resolved in code
- [04-resets.md](./04-resets.md) has shipped same-slice with its
  grid-boundary diff-test corpus green
- [03-predict-linear.md](./03-predict-linear.md) has shipped same-slice
  and the shared **windowed-arrays source** it introduces is consumed
  by `mad_over_time` and the holt-winters item rather than duplicated
- [05-holt-winters.md](./05-holt-winters.md) has shipped same-slice;
  the analyzer accepts both `holt_winters` and
  `double_exponential_smoothing`; if `arrayFold` proved unusable on
  the target ClickHouse version, the item is flagged
  activated-local-only with an upstream-UDF tracking note rather than
  silently deferred

Per-function acceptance criteria live in each tier file.
