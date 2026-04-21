# 03 — `predict_linear`

## Function

`predict_linear(v range-vector, t scalar)` returns, per series in `v`,
the value `t` seconds after the evaluation timestamp, extrapolated by
simple linear regression over the samples in the window.

Reference: Prometheus `promql/functions.go`, `funcPredictLinear` and
`linearRegression`.

## Status — same-slice local+native

This item lands **same-slice local+native**. The earlier "out of scope
by default" framing was pessimistic: working through the SQL shows the
native rendering decomposes into

1. a **windowed-arrays source** that yields, per `(series, grid_ts)`,
   the samples inside `(grid_ts - range_ms, grid_ts]` as two parallel
   arrays (timestamps and values), and
2. a single `arrayReduce('simpleLinearRegression', ...)` call plus
   Prometheus' constant-series short-circuit.

The windowed-arrays source is reusable — `mad_over_time`
([02-tier-2-moderate.md](./02-tier-2-moderate.md)) and
[05-holt-winters.md](./05-holt-winters.md) both want the same primitive.
That reusability is the argument for same-slice delivery: the hard part
(the source) is shared, so paying for it once unlocks three features.

Real-world usage remains narrow (mostly `predict_linear(disk_free[1h],
3600)` disk-fill alerts), but the implementation cost is no longer
disproportionate to that usage once the shared primitive exists.

## Local implementation

### Scope

- input is a range vector and a scalar `t` (seconds)
- output is an instant vector with the predicted value per series
- drops the metric name
- `< 2` samples in window: series absent from output
- constant series (all values equal, non-Inf): returns the constant
- constant series where the value is `±Inf`: returns `NaN`
- intercept time in Prometheus is the **evaluation timestamp**, so
  samples inside the window have negative relative `x`, and the
  prediction at `t` seconds after eval is `slope*t + intercept`

### Implementation sketch

New file `internal/promshim/exec/predict.go`:

```go
func ApplyPredictLinear(
    duration float64,
    input model.RuntimeValue,
    params EvalParams,
) (model.VectorValue, error) {
    matrix, ok := input.(model.MatrixValue)
    if !ok {
        return model.VectorValue{}, unsupportedf(
            "predict_linear requires matrix input, got %T", input)
    }
    evalTs := params.EvaluationTime.UnixMilli()
    out := make([]model.InstantSample, 0, len(matrix.Series))
    for _, series := range matrix.Series {
        if len(series.Values) < 2 {
            continue
        }
        slope, intercept := linearRegression(series.Values, evalTs)
        predicted := slope*duration + intercept
        out = append(out, model.InstantSample{
            Metric:    model.DropMetricName(series.Metric),
            Timestamp: evalTs,
            Value:     predicted,
        })
    }
    return model.VectorValue{Samples: out}, nil
}
```

The `linearRegression` helper must match Prometheus' implementation in
`promql/functions.go` bit-for-bit — it uses Kahan summation and has a
constant-series short-circuit returning `(0, initY)` (or `(NaN, NaN)`
if `initY` is `±Inf`). Copy the upstream implementation; do not derive
from scratch.

### Analyzer

Add `predict_linear` to `plan/promql.go`. Argument shape is
`(matrix, scalar)`. The scalar must be a literal at plan time (follow
Prometheus' restriction).

## Native rendering

### Windowed-arrays source

The renderer needs a source that yields, per `(series, grid_ts)`, two
parallel arrays of samples inside `(grid_ts - range_ms, grid_ts]`. The
edge convention mirrors Prometheus and follows the left-edge
inclusive-vs-exclusive workaround in commit `0d44c2b`.

**Option A — upstream primitive (preferred)**: argue for
`timeSeriesGroupArraysToGrid` in the same family as existing
`timeSeriesRateToGrid`/`timeSeriesDeltaToGrid`. Signature:

```
timeSeriesGroupArraysToGrid(start_ms, end_ms, step_ms, range_ms, ts, value)
    -> Array(Tuple(grid_ts Int64, timestamps Array(Int64), values Array(Float64)))
```

This also unblocks `mad_over_time` and `double_exponential_smoothing`
without each having to build the same source separately.

**Option B — pragmatic today (while Option A lands)**: a subquery with
`ARRAY JOIN` over a generated grid and `groupArrayIf` over samples:

```sql
SELECT
    fingerprint,
    metric_name,
    grid_ts,
    groupArrayIf(unix_milli, unix_milli > grid_ts - {range_ms:Int64}
                          AND unix_milli <= grid_ts) AS window_timestamps,
    groupArrayIf(value,       unix_milli > grid_ts - {range_ms:Int64}
                          AND unix_milli <= grid_ts) AS window_values
FROM metrics.samples
ARRAY JOIN range(
    {start_ms:Int64},
    {end_ms:Int64} + {step_ms:Int64},
    {step_ms:Int64}
) AS grid_ts
WHERE unix_milli >  {start_ms:Int64} - {range_ms:Int64}
  AND unix_milli <= {end_ms:Int64}
  AND metric_name = {metric:String}
  AND <label predicates>
GROUP BY fingerprint, metric_name, grid_ts
```

The `ARRAY JOIN range(...)` trick replicates each sample across every
grid bucket whose window it could fall into; `groupArrayIf` then filters
to the per-bucket inclusion window. Not beautiful, but correct, and it
runs with what exists today.

### Predict-linear expression

Wrapping the windowed-arrays source:

```sql
SELECT
    fingerprint,
    metric_name,
    grid_ts,
    multiIf(
        length(window_values) < 2,
            CAST(NULL AS Nullable(Float64)),
        -- Constant-series short-circuit, non-Inf case
        arrayAll(v -> v = window_values[1], window_values)
          AND NOT isInfinite(window_values[1]),
            window_values[1],
        -- Constant-series short-circuit, Inf case
        arrayAll(v -> v = window_values[1], window_values)
          AND isInfinite(window_values[1]),
            CAST('nan' AS Float64),
        -- General case
        tupleElement(lr, 1) * {duration:Float64} + tupleElement(lr, 2)
    ) AS value
FROM (
    SELECT
        fingerprint,
        metric_name,
        grid_ts,
        window_values,
        window_timestamps,
        arrayReduce(
            'simpleLinearRegression',
            arrayMap(t -> (t - grid_ts) / 1000.0, window_timestamps),
            window_values
        ) AS lr
    FROM <windowed-arrays source above>
)
```

Notes:

- `simpleLinearRegression` in ClickHouse returns `Tuple(k Float64, b Float64)`
  where `y = k·x + b`. That matches Prometheus' `(slope, intercept)`
  return order from `linearRegression`.
- `x = (t - grid_ts) / 1000.0` places `x = 0` at eval time, matching
  Prometheus' `interceptTime = enh.Ts` convention. All in-window
  samples have negative `x`.
- The prediction is `slope·duration + intercept` because `x = duration`
  is `duration` seconds after eval time.
- Rows where the `multiIf` returns NULL must be filtered out at the
  outer projection so the series is absent from the output (Prometheus
  does not emit a point for `<2`-sample windows).

### Diff-test surface

Diff test the native rendering against the local implementation on:

- upward-sloping series — slope sign and magnitude match
- downward-sloping series — same
- noisy linear series — both implementations pick the same slope
  (same Kahan/sum-reduce ordering matters)
- `duration = 0` — should equal `intercept`, not the last raw sample
- constant series — both short-circuit, no regression computed
- constant-Inf series — both return NaN
- fewer than 2 samples — series absent from both outputs
- NaN interior samples — propagation policy matches upstream

## Acceptance

- analyzer accepts `predict_linear(<matrix>, <scalar>)`
- harness corpus has Prometheus-parity queries covering the diff-test
  surface above
- local implementation matches upstream `funcPredictLinear` bit-for-bit
  (Kahan summation, constant-series short-circuit, Inf handling)
- native rendering ships same-slice, diff-tested against the local
  implementation; the windowed-arrays source is shared with
  `mad_over_time` and `double_exponential_smoothing`
- if Option A (upstream primitive) is not yet available when this
  lands, Option B (ARRAY JOIN subquery) is used, with a tracking note
  for the Option A swap once upstream merges the primitive
