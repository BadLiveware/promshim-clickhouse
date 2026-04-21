# 04 — `resets`

## Function

`resets(v range-vector)` returns, per series in `v`, the count of counter
resets observed in the range window. A reset is any sample `x[i]` where
`x[i] < x[i-1]`.

Reference: Prometheus `promql/functions.go`, `funcResets`.

## Why this is its own plan

Local implementation is trivial — a single pass over the window values.
The native side is a pairwise-compare-over-array pattern on the
resampled values:

```
arraySum(arrayMap((a,b) -> if(a<b, 1, 0),
                  arrayPopFront(values),
                  arrayPopBack(values)))
```

That pattern is mechanical once the lowering plan ships and
`timeSeries*ToGrid` plus array-function composition are established in
the renderer — which is the case by the time this coverage plan
executes. On that basis, **`resets` lands same-slice local+native**,
same treatment as tier 2. It earns its own file (rather than going
under [02-tier-2-moderate.md](./02-tier-2-moderate.md)) only because
the counter-reset-at-boundary corpus needs dedicated diff-test
attention — lose a reset at a bucket boundary and nobody notices until
a real workload hits the edge.

The Phase 6b gate pattern still applies: native rendering ships with a
differential test against the local implementation, in coverage-plan
scope.

## Local implementation

### Scope

- input is a range vector (counter semantics assumed)
- output is an instant vector with the reset count per series
- drops the metric name
- NaN handling: NaN does not count as either direction of comparison —
  skip NaN samples in the pairwise scan (verify against `funcResets`)
- empty window: series absent from output

### Implementation sketch

New function in `internal/promshim/exec/rangefunc.go`:

```go
func applyResetsMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
    out := make([]model.InstantSample, 0, len(matrix.Series))
    for _, series := range matrix.Series {
        if len(series.Values) == 0 {
            continue
        }
        resets := 0.0
        prev := math.NaN()
        for _, point := range series.Values {
            if math.IsNaN(point.Value) {
                continue
            }
            if !math.IsNaN(prev) && point.Value < prev {
                resets++
            }
            prev = point.Value
        }
        last := series.Values[len(series.Values)-1]
        out = append(out, model.InstantSample{
            Metric:    model.DropMetricName(series.Metric),
            Timestamp: last.Timestamp,
            Value:     resets,
        })
    }
    // sort as the rest of rangefunc.go does
    return model.VectorValue{Samples: out}, nil
}
```

Register in `localMatrixRangeFunctions`.

### Analyzer

Add `resets` to the `*_over_time` list in `AnalyzeRangeFunctionCall` in
`plan/promql.go` — the argument shape (single matrix) is identical.

## Native rendering (same slice)

```sql
arraySum(
  arrayMap(
    (a, b) -> if(a < b, 1, 0),
    arrayPopFront(values),
    arrayPopBack(values)
  )
)
```

applied to the resampled values array that a `timeSeries*ToGrid` bucket
produces.

The semantic sharp edge is **grid boundaries**. Reset counting on a
loose resample is subtle: an interpolated point between two raw samples
can mask a reset if the resample step elides a dip. To avoid drift,
either:

1. use a grid/resample mode that preserves raw-sample edges (no
   interpolation between observed values), or
2. argue upstream for a dedicated `timeSeriesResetsToGrid` primitive in
   the same family as the existing rate/delta variants

Option 1 is sufficient for the same-slice delivery and is what the diff
test gates against. Option 2 is a nice-to-have for query performance
once workloads prove the pattern is hot.

## Acceptance

- analyzer accepts `resets(<matrix>)`
- harness corpus has Prometheus-parity queries covering:
  - zero resets (monotonic counter)
  - single reset mid-window
  - multiple resets
  - reset at window boundary (first/last sample) — **must pass both
    locally and natively**
  - NaN interior samples
  - empty window
- native rendering ships in the same slice, diff-tested against the
  local implementation with particular attention to the boundary corpus
