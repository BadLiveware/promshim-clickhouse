# Attempt 20260428-subquery-avg-over-time-rows-path-rejected

## Hypothesis

Enabling `avg_over_time` on the subquery-child range rows fast path (without changing leaf-path decisions) might reduce memory/latency for the `draft_cand_0416...` subquery hotspot.

## Candidate change tested

Prototype (reverted in-iteration):

- `internal/promshim/native/renderer/range_logical.go`
- In subquery-child range branch, route `avg_over_time` through `buildRangeFunctionOverRowsSQL` alongside existing fast-path functions.

## Correctness validation

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim
```

Passed.

## Measurement

Focused hotspot corpus benchmark after prototype:

- corpus: `harness/corpus/iteration33-subquery-hotspots.json`
- artifact: `harness/artifacts/bench/standalone/20260428-iter40-subquery-hotspots-after-avgrows/`

Compared with baseline artifact:

- `harness/artifacts/bench/standalone/20260428-iter33-subquery-hotspots/`

Observed:

- `draft_cand_0416...` memory remained ~`81.3MiB` p95 (no meaningful reduction)
- `draft_cand_0416...` latency did not improve consistently (shim/CH slightly higher in sampled run)
- other rows remained near-noise with no clear corroborating win

## Decision

Reject/defer and revert.

No convincing runtime or memory win with corroborating signals; prototype removed.
