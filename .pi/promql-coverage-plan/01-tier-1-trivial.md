# 01 — Tier 1: trivial coverage additions

## Goal

Add the PromQL functions that are currently analyzer-rejected but are
mechanical to implement both locally and natively. Each of these:

- has a direct ClickHouse equivalent (for the native side) or is a pure
  sort/grouping operation
- has no temporal state, no per-series history, no metadata dependency
- is a one-to-one sample transform, a sort of an existing vector, or a
  reducer over an existing aggregation axis

Per policy in [00-context-and-policy.md](./00-context-and-policy.md),
tier 1 items may land local and native in the same slice because there
is no drift surface.

## Scope

### Scalar math (pointwise)

| Function | ClickHouse |
|----------|------------|
| `abs` | `abs` |
| `ceil` | `ceil` |
| `floor` | `floor` |
| `sgn` | `sign` |
| `exp` | `exp` |
| `ln` | `log` |
| `log2` | `log2` |
| `log10` | `log10` |
| `sqrt` | `sqrt` |

### Clamp family

| Function | ClickHouse |
|----------|------------|
| `clamp(v, lo, hi)` | `greatest(lo, least(hi, v))` |
| `clamp_min(v, lo)` | `greatest(v, lo)` |
| `clamp_max(v, hi)` | `least(v, hi)` |

Prometheus drops the metric name on the output of `clamp*`.

### Trigonometry

`sin`, `cos`, `tan`, `asin`, `acos`, `atan`, `sinh`, `cosh`, `tanh`,
`asinh`, `acosh`, `atanh`, `deg`, `rad`.

ClickHouse has native implementations of all of these; `deg`/`rad` map
to `degrees`/`radians`.

### Time / date

| Function | Behavior |
|----------|----------|
| `pi()` | constant |
| `time()` | evaluation timestamp as float seconds |
| `timestamp(v)` | per-sample timestamp as float seconds |
| `scalar(v)` | **not in this tier — see [02-tier-2-moderate.md](./02-tier-2-moderate.md)** |
| `minute(v?)` | `toMinute` over sample timestamp or `time()` |
| `hour(v?)` | `toHour` |
| `day_of_week(v?)` | Prom: 0=Sun, 1=Mon, …; ClickHouse `toDayOfWeek` is 1=Mon, 7=Sun — **adjust** |
| `day_of_month(v?)` | `toDayOfMonth` |
| `day_of_year(v?)` | `toDayOfYear` |
| `days_in_month(v?)` | `toDaysInMonth` |
| `month(v?)` | `toMonth` |
| `year(v?)` | `toYear` |

`day_of_week` semantic mismatch is the one thing to get right here —
Prometheus returns `0..6` with Sunday=0; ClickHouse returns `1..7` with
Monday=1. Map explicitly in both the local and native implementations.

### Sort

| Function | Behavior |
|----------|----------|
| `sort(v)` | ascending by value |
| `sort_desc(v)` | descending by value |
| `sort_by_label(v, labels…)` | ascending lexicographic by given labels |
| `sort_by_label_desc(v, labels…)` | descending |

Sort is only meaningful on an instant vector at the query root. The
analyzer should accept it only when the surrounding context does not
re-order (i.e. it is effectively the outermost operation, or inside
another construct that preserves order-by-input, of which PromQL has
very few).

### Aggregators

| Aggregator | ClickHouse reducer |
|------------|---------------------|
| `stddev` | `stddevPop` |
| `stdvar` | `varPop` |
| `quantile(φ, v)` | `quantile(φ)(v)` |
| `group` | constant 1 per group |

These are additions to `newAggregateReducer` in
`internal/promshim/exec/aggregate.go`, and additions to the
aggregation-pushdown allowlist in
`internal/promshim/native/optimizer.go` so the native path can reduce in
ClickHouse.

`quantile` takes a scalar parameter via `AggregateExpr.Param`; the local
path already plumbs this for `topk`/`bottomk`, so the wiring exists.

### Over-time stats

| Function | Local implementation | Native pattern |
|----------|----------------------|----------------|
| `stddev_over_time` | reduce `stddevPop` over series values | `arrayReduce('stddevPop', values)` over resampled matrix |
| `stdvar_over_time` | reduce `varPop` over series values | `arrayReduce('varPop', values)` |
| `present_over_time` | `1` if any sample in range, else absent | `length(values) > 0 ? 1 : NULL` |

`mad_over_time` is **not** in this tier (quantile-based, needs more
care); see [02-tier-2-moderate.md](./02-tier-2-moderate.md).

## Implementation pattern

Each function needs changes in a fixed set of places.

### 1. Analyzer — `internal/promshim/plan/promql.go`

Most of these are "pure per-sample" transforms. Extend
`isSupportedLeafFunction` for the scalar math / clamp / trig / date-time
/ pi / time / timestamp families:

```go
case "abs", "ceil", "floor", "sgn",
     "exp", "ln", "log2", "log10", "sqrt",
     "clamp", "clamp_min", "clamp_max",
     "sin", "cos", "tan", "asin", "acos", "atan",
     "sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
     "deg", "rad",
     "pi", "time", "timestamp",
     "minute", "hour",
     "day_of_week", "day_of_month", "day_of_year", "days_in_month",
     "month", "year":
    return true
```

Sort variants need their own analyzer entries because they are order-
preserving root operations (not leaf functions) — add a `switch` arm
alongside `label_replace` that validates the argument shape.

Aggregators go through the logical builder's `AggregateExpr` handling,
not `isSupportedLeafFunction`. The fix is in
`plan/promql.go` around the aggregation switch and in the reducer
registry in `exec/aggregate.go`.

Over-time stats go in the existing `AnalyzeRangeFunctionCall` list next
to `sum_over_time` and friends.

### 2. Logical builder — `internal/promshim/logical_builder.go`

Most of these can reuse the existing `rangeFunctionPlan` /
`leafCallPlan` paths. Sort needs its own small plan type because it is
a post-processing step over the inner vector.

### 3. Local execution — `internal/promshim/exec/`

- `transform.go` — extend for the pointwise scalar / clamp / trig /
  date-time functions. Metric-name drop rules follow Prometheus
  `functions.go`: scalar math and clamp drop the name; `time()`,
  `timestamp()`, `pi()` return dimensionless metrics with no name;
  date-time functions drop the name.
- `aggregate.go` — add `stddevReducer`, `stdvarReducer`,
  `quantileReducer`, `groupReducer`, and their cases in
  `newAggregateReducer`.
- `rangefunc.go` — add `stddev_over_time`, `stdvar_over_time`,
  `present_over_time` to `localMatrixRangeFunctions` with accompanying
  `applyXxxMatrix` functions.
- a new `sort.go` file for the four sort variants.

### 4. Native renderer — `internal/promshim/native/renderer.go`

Scalar transforms wrap an existing fragment's value column in the
mapped ClickHouse function. Clamp unfolds to nested `greatest`/`least`.
Trig and date-time map directly. Sort renders as an outer `ORDER BY`.

### 5. Optimizer — `internal/promshim/native/optimizer.go`

Extend `PassFunctionPatternRewrites` (currently limited to range/counter
functions) with a generic scalar-transform rewrite that wraps the input
fragment. This is the only pass affected.

Extend `PassJoinNormalizationDuplicateDetection`'s aggregation allowlist
to include the four new aggregators.

### 6. Harness corpus — `harness/corpus/queries.json`

Add at least one query per function, ideally spanning:
- instant vector input
- range vector input (for over-time stats)
- label-dropping versus label-preserving metric behavior
- edge-case inputs: NaN, +Inf, -Inf, empty vectors

## Validation

- **Differential** — every function has at least one Prometheus-parity
  query in the harness corpus. The harness compare step must pass on
  both the shim's local path and the native path where rendered.
- **Unit** — `exec/transform_test.go`, `exec/aggregate_test.go`, and
  new `exec/rangefunc_test.go` cases cover NaN / infinity / empty /
  metric-name semantics.
- **Renderer snapshot** — the native renderer emits the expected
  ClickHouse expression for each. Snapshot tests go in the native
  package.
- **`day_of_week` explicit test** — one test per weekday covering the
  Prom 0-indexed vs ClickHouse 1-indexed mapping.

## Definition of done

- analyzer accepts every function listed above
- harness corpus has Prometheus-parity coverage for every function
- native rendering exists and is covered by snapshot tests for every
  function except sort (sort may be root-only and can remain local if
  native sort introduces a plan-shape wart)
- explain output labels each function with its fragment kind
- no local implementation of a tier-1 function ships without its native
  rendering, per the same-slice exception in
  [00-context-and-policy.md](./00-context-and-policy.md)
