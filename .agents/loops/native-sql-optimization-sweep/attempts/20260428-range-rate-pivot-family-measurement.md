# Attempt 20260428-range-rate-pivot-family-measurement

## Hypothesis

After freezing the subquery served-local expansion path, the next high-cost family candidate should be validated with a focused measurable baseline before any behavior changes.

## Candidate family tested

Range-rate aggregation 7d shapes:

- `sum_rate_by_job_range_7d`
- `repeated_sum_rate_average_by_job_range_7d`

Corpus:

- `harness/corpus/iteration77-range-rate-hotspots-7d.json`

## Measurement run

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration77-range-rate-hotspots-7d.json \
  --eval-time 2026-03-22T22:11:57Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter77-range-rate-hotspots-7d \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict,cost_prefer \
  --include-prom false \
  --repeats 2 --warmup 1 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Results

- All rows served as `native_sql` with CH round-trips = 1.
- Runtime cluster was tight (roughly 40–46ms shim p50 / 40–43ms CH ms).
- Memory profile was also flat (~20.4MiB p95) across rows/modes.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter77-range-rate-hotspots-7d/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement gate).

This candidate family does not currently show obvious high-headroom mode/resource spread in this focused setup. Next measurable attempt should target a family/query set with larger observed variance or known resource pressure before proposing behavior changes.
