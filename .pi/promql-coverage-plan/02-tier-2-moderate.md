# 02 — Tier 2: moderate coverage additions

## Goal

Add the three functions whose local implementations are straightforward
but whose native lowerings need a custom pattern beyond a one-line
function mapping.

Because this plan runs **after** the native SQL lowering plan completes
(see [README.md](./README.md)), the renderer, optimizer, and Phase 6b
promotion gate are all in place before coverage work starts. Tier-2
items therefore land **same-slice local+native**, extending the
same-slice exception already applied to tier 1. The native rendering
still has to clear a diff test against the local implementation — that
is the Phase 6b *pattern*, applied in coverage-plan scope; it is not
deferred to the lowering plan.

## Items

### `scalar(v)`

**Semantics** (Prometheus `functions.go`):

- input is an instant vector
- if the vector contains exactly one series, output is that series'
  value as a scalar
- if the vector is empty or contains two or more series, output is `NaN`
- the output has no labels, since a scalar carries none

**Local implementation** (`internal/promshim/exec/transform.go`):

```go
func ApplyScalar(input model.RuntimeValue, params EvalParams) (model.ScalarValue, error) {
    vector, ok := input.(model.VectorValue)
    if !ok {
        return model.ScalarValue{}, unsupportedf(
            "scalar() requires vector input, got %T", input)
    }
    ts := scalarEvalTimestamp(params)
    switch len(vector.Samples) {
    case 1:
        return model.ScalarValue{Timestamp: ts, Value: vector.Samples[0].Value}, nil
    default:
        return model.ScalarValue{Timestamp: ts, Value: math.NaN()}, nil
    }
}
```

`evaluator` already returns a `ScalarValue` from downstream plan nodes,
so the wiring is a new case in the leaf-call dispatch.

**Why it is tier 2 (native side)**

Native rendering needs to count series at query time and branch on that
count:

```sql
SELECT
  CASE
    WHEN count() = 1 THEN any(value)
    ELSE cast('nan' as Float64)
  END AS scalar_value
FROM (<inner vector SQL>)
```

That is one level of wrapping and a runtime COUNT, which is why it is
not tier 1 (a pure expression wrapper) but also not tier 3 (no windowed
or iterative state).

**Acceptance**

- analyzer accepts `scalar(<instant vector>)`
- local diff test covers: single-series, multi-series, empty-vector
  inputs
- output is type `ScalarValue`, not `VectorValue`, so the evaluator's
  result-shape switch is exercised
- native rendering ships in the same slice, guarded by a diff test
  against the local implementation

### `mad_over_time`

**Semantics** (Prometheus 2.46+ `functions.go`, `funcMadOverTime`):

- input is a range vector
- output per series: the **median absolute deviation** of the samples
  in the range, i.e. the median of `|xᵢ − median(x)|` for the window
- drops the metric name
- NaN/Inf handling: NaN in input propagates — see Prometheus' handling
  for the exact policy (first NaN poisons or is skipped? check
  `functions.go` before writing the test corpus — this is the detail
  that separates "correct" from "close enough")

**Local implementation** (`internal/promshim/exec/rangefunc.go`):

Two-pass over the window:

1. sort values, take the median
2. map to absolute deviations, sort again, take the median

Reuse `calculateQuantileFromValues(0.5, values)` which already lives in
`rangefunc.go`. Only the second pass needs to allocate.

**Why it is tier 2 (native side)**

Needs a nested quantile over the array of absolute deviations inside a
grid bucket, wrapped around the shared **windowed-arrays source**
defined in [03-predict-linear.md](./03-predict-linear.md) — the same
per-`(series, grid_ts)` `window_values` array used by `predict_linear`
and `double_exponential_smoothing`:

```sql
SELECT
    fingerprint,
    metric_name,
    grid_ts,
    multiIf(
        length(window_values) = 0,
            CAST(NULL AS Nullable(Float64)),
        arrayReduce(
            'quantileExact(0.5)',
            arrayMap(
                x -> abs(x - arrayReduce('quantileExact(0.5)', window_values)),
                window_values
            )
        )
    ) AS value
FROM <windowed-arrays source>
```

Correct and not a function-map one-liner, but the only custom piece is
the nested `arrayReduce` — the source itself is the shared primitive,
not a per-function build.

**Acceptance**

- analyzer accepts `mad_over_time`
- local diff test covers: monotonic series, constant series, series
  with NaN, empty windows, single-point windows (MAD is 0)
- native rendering ships in the same slice, guarded by a diff test
  against the local implementation

### `info`

**Semantics** (Prometheus 3, `functions.go`, `funcInfo`):

- input is an instant vector and an optional label filter
- looks up the **info series** (typically `target_info`, but any metric
  whose name ends in `_info` or is convention-marked as an info series)
  with matching identifying labels
- copies non-conflicting labels from the info series onto the input
  series, returning the enriched vector
- fails if any input series matches multiple info series on the same
  identifying-label set (duplicate-info ambiguity)
- implementation in upstream Prom 3 relies on the `info` metadata
  registry introduced alongside the feature — read Prom 3
  `functions.go` and the `info` RFC before locking in behavior

**Local implementation**

Reuses existing vector-matching infrastructure in
`internal/promshim/exec/vector_matching.go`. The essence is:

1. load the info series relevant to the input vector's identifying
   labels (this is a storage read, same contract as a selector)
2. run a `group_left(info_labels…)` style join with explicit matchers
3. detect the duplicate-info case and raise `ErrDuplicateLabelsetTimestamps`
   or a dedicated `info` error

The storage read for info series goes through the same `QueryConfig` /
selector path as any other metric selector, just with an implicit
`__name__="target_info"` (or convention) matcher.

**Why it is tier 2 (native side)**

Native lowering is a LEFT JOIN against the info metric's latest sample
per identifying-label group, keyed on those labels, with conflict
detection. The shape is a standard join but the **convention for which
metrics count as info series** is a policy input to the renderer, not a
mechanical expression rewrite. That is the moderate-work part.

**Unresolved before implementation**

- which metric-name convention the shim adopts (`_info` suffix,
  explicit registry, both)
- whether info series lookup respects the query's time window or always
  uses "latest before eval time" — Prom 3's answer is "latest before
  eval time with staleness"
- how `info` composes with `rate`, `sum`, etc. around it — read Prom 3
  test suite for composition cases

**Acceptance**

- analyzer accepts `info(<instant vector>, { labels… })` and the
  zero-arg-label form
- local diff test against Prom 3 covers:
  - simple enrichment case
  - label conflict (input label wins)
  - duplicate-info series failure
  - info series absent (input passes through unchanged)
- metric-name convention documented in the code next to the implementation
- native rendering ships in the same slice, guarded by a diff test
  against the local implementation; the info-series convention is a
  renderer-level config, not a hardcoded literal

## Validation

- per-function diff tests in the harness corpus, each tagged with the
  tier-2 label
- unit tests per-function covering the edge cases listed above
- explain surfaces each function's fragment kind (new kinds:
  `FragmentKindScalarConvert`, `FragmentKindMADOverTime`,
  `FragmentKindInfoJoin`)

## Definition of done

- all three functions execute locally with Prometheus-parity diff tests
- all three have native renderings with diff tests against the local
  implementation, using the Phase 6b gate pattern in coverage-plan scope
- `mad_over_time` consumes the shared windowed-arrays source defined
  in [03-predict-linear.md](./03-predict-linear.md), not a parallel
  build
- explain surfaces each function's fragment kind
