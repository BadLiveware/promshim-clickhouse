# Attempt 20260428-high-pressure-binary-rate-measurement-gate

## Hypothesis

After rejecting subquery served-local expansion, test a higher-pressure binary rate family to see if it offers measurable headroom for controlled behavior experiments.

## Measurement setup

Focused corpus:

- `harness/corpus/iteration78-high-pressure-rate-binary.json`
- rows:
  - `rate_ge_rate_5m_range_1d`
  - `rate_ge_bool_rate_5m_range_1d`

Command:

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration78-high-pressure-rate-binary.json \
  --eval-time 2026-03-22T22:11:57Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter78-high-pressure-rate-binary \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported,off \
  --routing-policies strict,cost_shadow,cost_prefer \
  --include-prom false \
  --repeats 2 --warmup 1 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Results

- Native paths (`prefer`/`force_supported`) stayed around ~27–31ms p50 with CH round-trips = 1.
- Local (`off`) was dramatically worse: ~1.2–1.3s p50 with CH round-trips = 289.
- Memory profile for native rows remained ~81.3MiB p95.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter78-high-pressure-rate-binary/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement gate).

This family is high-pressure but still strongly favors native serving over local by a large margin; behavior experiments that increase local serving here would likely regress resource usage. Next measurable candidate should target a different mechanism than local-serving expansion.
