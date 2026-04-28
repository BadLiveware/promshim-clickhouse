# Attempt 20260428-subquery-serving-measurement-gate

## Hypothesis

Before any expansion of subquery serving behavior, measure the target shape under native (`prefer/force_supported`) versus local (`off`) modes to verify whether local serving can plausibly improve execution resources.

## Measurement setup

Focused corpus:

- `harness/corpus/iteration74-subquery-serving-candidate.json`
- query: `rate(up[5m])[30m:1m]`

Command:

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration74-subquery-serving-candidate.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter74-subquery-serving-candidate \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,off,force_supported \
  --routing-policies strict,cost_prefer \
  --include-prom false \
  --repeats 5 --warmup 1 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Results

- `prefer` / `force_supported` (`native_sql`) shim p50: ~`12ms`
- `off` (`local`) shim p50: ~`124-128ms`
- local path shows much higher CH round-trips (`30` vs `1`) and significantly worse wall time.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter74-subquery-serving-candidate/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement gate).

This is strong measurable evidence that broadening subquery local serving for this shape would be regressive in wall-time and round-trips. Next behavior work should keep served strategy strict/native for this family and focus on advisory quality or narrower candidate gating rather than serving expansion.
