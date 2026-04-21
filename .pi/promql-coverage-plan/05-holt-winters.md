# 05 — `holt_winters` / `double_exponential_smoothing`

## Function

Prometheus 2.x: `holt_winters(v range-vector, sf scalar, tf scalar)`.

Prometheus 3.x: renamed to `double_exponential_smoothing(v, sf, tf)`.
The Prom 2 name was a misnomer — the function implements double (not
triple) exponential smoothing, hence the rename. Both names are
accepted; `holt_winters` dispatches to the same implementation as an
alias.

Reference: Prometheus `promql/functions.go`,
`funcDoubleExponentialSmoothing`.

## Status — same-slice local+native

This item lands **same-slice local+native**. The earlier "out of scope
by default" framing overstated the native cost: working through the SQL
shows it decomposes into

1. the same **windowed-arrays source** used by `predict_linear` and
   `mad_over_time` (see [03-predict-linear.md](./03-predict-linear.md)),
   and
2. a single `arrayFold` over the values array, with the first Prometheus
   loop iteration algebraically folded into the initial state so the
   fold body never branches on the iteration index.

Usage is still rare, and the Prom 3 rename signals upstream
ambivalence. But the marginal cost of adding it is small once the
shared source primitive exists, so it rides along rather than being
deferred indefinitely.

## Local implementation

### Scope

- input is a range vector, a smoothing factor `sf ∈ (0, 1)`, a trend
  factor `tf ∈ (0, 1)`
- output is an instant vector with the smoothed value per series at
  the end of the window
- drops the metric name
- validate `sf` and `tf` bounds; fail with a clear error outside `(0, 1)`
- `< 2` samples in window: series absent from output (matches Prometheus)
- exactly 2 samples: output is `v[1]` (the simplification below makes
  this fall out naturally)

### Prometheus iteration, simplified

Upstream's loop (paraphrased from `funcDoubleExponentialSmoothing`):

```
s1 = v[0]
b  = v[1] - v[0]
for i = 1 .. n-1:
    x  = sf * v[i]
    y  = (1 - sf) * (s1 + b)
    s0, s1 = s1, x + y
    b  = calcTrendValue(i-1, tf, s0, s1, b)
return s1
```

`calcTrendValue` returns `b` unchanged at `i = 0` (the first iteration),
otherwise `tf*(s1-s0) + (1-tf)*b`.

The first iteration (`i = 1`) simplifies algebraically:

- `x + y = sf·v[1] + (1-sf)·(v[0] + (v[1]-v[0])) = v[1]`
- `b` unchanged

So after the first iteration the state is `(s1 = v[1], b = v[1] - v[0])`,
and every subsequent iteration uses the general `calcTrendValue`
branch. This means the local and native implementations can both skip
the branch on `i = 0` by folding over `v[2..n]` with initial state
`(v[1], v[1] - v[0])`.

### Implementation sketch

New file `internal/promshim/exec/smoothing.go`:

```go
func ApplyDoubleExponentialSmoothing(
    sf, tf float64,
    input model.RuntimeValue,
) (model.VectorValue, error) {
    if sf <= 0 || sf >= 1 {
        return model.VectorValue{}, badDataf(
            "smoothing factor must be between 0 and 1 exclusive")
    }
    if tf <= 0 || tf >= 1 {
        return model.VectorValue{}, badDataf(
            "trend factor must be between 0 and 1 exclusive")
    }
    matrix, ok := input.(model.MatrixValue)
    if !ok {
        return model.VectorValue{}, unsupportedf(
            "double_exponential_smoothing requires matrix input, got %T", input)
    }
    out := make([]model.InstantSample, 0, len(matrix.Series))
    for _, series := range matrix.Series {
        if len(series.Values) < 2 {
            continue
        }
        s1 := series.Values[1].Value
        b := series.Values[1].Value - series.Values[0].Value
        for i := 2; i < len(series.Values); i++ {
            v := series.Values[i].Value
            newS1 := sf*v + (1-sf)*(s1+b)
            b = tf*(newS1-s1) + (1-tf)*b
            s1 = newS1
        }
        last := series.Values[len(series.Values)-1]
        out = append(out, model.InstantSample{
            Metric:    model.DropMetricName(series.Metric),
            Timestamp: last.Timestamp,
            Value:     s1,
        })
    }
    return model.VectorValue{Samples: out}, nil
}
```

Diff-test against upstream `funcDoubleExponentialSmoothing` with at
least one series where all three values differ — that catches any
off-by-one from the simplification above.

### Analyzer

Accept both names:

```go
case "holt_winters", "double_exponential_smoothing":
    return AnalyzeDoubleExponentialSmoothingCall(call)
```

The analyzer validates `(matrix, scalar, scalar)` with literal scalar
arguments. `sf`, `tf` must be bound at plan time; reject non-literal
scalars.

## Native rendering

### Windowed-arrays source

Same primitive as [03-predict-linear.md](./03-predict-linear.md):
either the preferred upstream `timeSeriesGroupArraysToGrid` or the
pragmatic `ARRAY JOIN range(...)` + `groupArrayIf` subquery today. Do
not duplicate the source per function — render it once and share.

### Smoothing expression

```sql
SELECT
    fingerprint,
    metric_name,
    grid_ts,
    multiIf(
        length(window_values) < 2,
            CAST(NULL AS Nullable(Float64)),
        length(window_values) = 2,
            window_values[2],
        tupleElement(
            arrayFold(
                (acc, v) -> (
                    -- new_s1 = sf*v + (1-sf)*(s1_prev + b_prev)
                    {sf:Float64} * v
                      + (1 - {sf:Float64}) * (tupleElement(acc, 1) + tupleElement(acc, 2)),
                    -- new_b  = tf*(new_s1 - s1_prev) + (1-tf)*b_prev
                    {tf:Float64} *
                      (({sf:Float64} * v
                        + (1 - {sf:Float64}) * (tupleElement(acc, 1) + tupleElement(acc, 2)))
                       - tupleElement(acc, 1))
                    + (1 - {tf:Float64}) * tupleElement(acc, 2)
                ),
                arraySlice(window_values, 3),
                (window_values[2], window_values[2] - window_values[1])
            ),
            1
        )
    ) AS value
FROM <windowed-arrays source>
```

Notes:

- `arraySlice(window_values, 3)` takes elements from index 3 onwards in
  ClickHouse's 1-indexed arrays. That corresponds to Prometheus'
  `i = 2..n-1` loop after the first iteration has been folded into the
  initial state.
- The initial state `(window_values[2], window_values[2] - window_values[1])`
  encodes the post-first-iteration `(s1 = v[1], b = v[1] - v[0])`
  (again accounting for 1-indexing).
- `new_s1` is computed twice in each fold step — once to emit as the
  first tuple slot, once inside the `new_b` expression. `arrayFold`
  has no `let`-binding. This is a real wart but each duplicated
  expression is six FLOPs, and the fold runs per grid point, not per
  sample, so practical cost is negligible.
- `{sf:Float64}` and `{tf:Float64}` are query parameters bound by the
  renderer. The analyzer validates the bounds before emission.
- The `length = 2` branch returns `window_values[2]` to match the
  `v[1]` output of the fold with an empty tail (`arraySlice` returns an
  empty array, `arrayFold` returns the initial state unchanged, then
  `tupleElement(..., 1) = window_values[2]`).

### Fallback if `arrayFold` proves buggy on the target version

ClickHouse's `arrayFold` is recent and has had corner cases around
accumulator closure capture. If a target version rejects the above:

1. materialize the accumulator through `arrayCumSum`-style tricks is
   **not** sufficient — double exponential smoothing has nonlinear
   coupling between `s1` and `b`, so it cannot be expressed as a
   cumulative reduction over a single-pass array function
2. the correct fallback is a server-side UDF (`timeSeriesDoubleExpSmoothingToGrid`)
   in the same family as `timeSeriesRateToGrid`. That is upstream work
   and blocks this item. Mark as activated-local-only until the UDF
   lands.

### Diff-test surface

- constant series — smooths to the constant
- linear upward trend — smoothed trend equals the raw slope
- linear downward trend — same
- sharp step change — smoothed response damps over a few points; exact
  values must match upstream bit-for-bit
- `sf` near `0` (heavy smoothing) and near `1` (almost pass-through)
- `tf` near `0` (trend frozen) and near `1` (trend tracks aggressively)
- two-sample window — output = last value
- fewer than two samples — series absent
- invalid `sf` / `tf` → clean error from both local and native paths

## Acceptance

- analyzer accepts `double_exponential_smoothing` and `holt_winters`
  (alias) with the correct argument shape
- local implementation matches upstream `funcDoubleExponentialSmoothing`
  bit-for-bit, including the two-sample edge and factor-bound errors
- harness corpus covers the diff-test surface above, with both function
  names tested and equivalence asserted
- native rendering ships same-slice, diff-tested against the local
  implementation, sharing the windowed-arrays source with
  `predict_linear` and `mad_over_time`
- if `arrayFold` is unusable on the target ClickHouse version, item is
  marked activated-local-only with an upstream-UDF tracking note
