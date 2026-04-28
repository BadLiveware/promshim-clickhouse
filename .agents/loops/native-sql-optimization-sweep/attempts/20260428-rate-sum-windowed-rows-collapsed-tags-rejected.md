# Attempt 20260428-rate-sum-windowed-rows-collapsed-tags-rejected

## Hypothesis

For `rate(sum(...)[5m:])` hotspot shapes, specializing the windowed-rows builder for collapsed tag sets (no aggregation grouping labels) may reduce grouping overhead and memory.

## Candidate tested

Prototype (reverted in-iteration):

- added collapsed-tag-set mode to windowed-rows SQL builder
- for subquery aggregation with no grouping labels, grouped windowed rows by `eval_ts` only with constant tags

## Validation

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim
```

Passed during prototype.

## Measurement

Focused one-query corpus benchmark after prototype:

- corpus: `harness/corpus/iteration38-cand0242.json`
- artifact dir: `harness/artifacts/bench/standalone/20260428-iter39-cand0242-after-shape/`

Observed (after vs prior run on same corpus family):

- `force_supported` shim p50: `90.75ms` (vs prior ~`89.86ms`)
- `prefer` shim p50: `91.19ms` (vs prior ~`90.16ms`)
- CH p50 also did not improve
- memory signals were not consistently improved

## Decision

Reject/defer and revert.

No convincing runtime win with corroborating metrics for this prototype; keep existing path unchanged.

## Next step

Return to design alternatives that more directly reduce expensive window/array work rather than micro-shaping grouping in the current kernel.
