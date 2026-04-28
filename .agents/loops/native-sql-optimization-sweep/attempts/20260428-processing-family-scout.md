# Attempt 20260428-processing-family-scout

## Hypothesis

After rejecting local-serving expansion paths, identify a new higher-headroom family by running a focused processing-corpus scout and selecting top absolute resource-cost shapes for the next measurable optimization attempt.

## Execution

Ran processing scout benchmark:

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/bench-processing-7d.json \
  --eval-time 2026-03-22T22:11:57Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter79-processing-scout \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer \
  --routing-policies strict \
  --include-prom false \
  --repeats 1 --warmup 0 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Findings

Highest absolute-cost rows in this scout:

- `processing_histogram_quantile_1h_instant_7d`
  - CH p50 ~142ms, memory p95 ~81.4MiB
- `processing_avg_memory_1h_by_job_type_range_24h_7d`
  - CH p50 ~123ms, memory p95 ~366.4MiB (largest memory pressure)
- `processing_histogram_quantile_1h_range_24h_7d`
  - CH p50 ~115ms, memory p95 ~81.4MiB

All rows stayed `native_sql` with CH round-trips = 1.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter79-processing-scout/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement scout).

This identifies a better next measurable target family (processing/histogram+range-memory pressure) with clearer absolute resource headroom than recent subquery-serving branches.
