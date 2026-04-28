# Attempt 20260428-subquery-binary-bench-harness-400

## Hypothesis

After binary-root thread-policy scoping, we should run a focused benchmark smoke for mixed/nested binary subquery shapes to check for obvious routing/perf regressions.

## Setup

Read benchmark playbook (`running-sweep`) and used isolated benchmark stack endpoints.

Created focused corpus:

- `harness/corpus/iteration25-binary-thread-policy-smoke.json`

Queries target mixed-root and nested-binary subquery shapes.

## Commands run

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration25-binary-thread-policy-smoke.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter25-binary-thread-policy-smoke \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict \
  --include-prom false \
  --repeats 1 --warmup 0 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Result

All focused rows returned `HTTP 400` in both `prefer` and `force_supported` modes, so no runtime comparison signal was produced.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter25-binary-thread-policy-smoke/bench-report.json`
- `harness/artifacts/bench/standalone/20260428-iter25-binary-thread-policy-smoke/memory-summary-bench-report.json`

## Decision

Defer/split.

This iteration identifies a measurement harness gap for this query family (bench invocation returns HTTP 400 despite successful `ch-explain` runs on related shapes). Next step is a bounded harness-debug attempt to capture exact failing request/response details and adapt corpus/query form accordingly before claiming perf outcomes.
