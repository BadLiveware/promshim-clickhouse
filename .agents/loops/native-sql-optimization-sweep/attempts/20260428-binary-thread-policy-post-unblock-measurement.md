# Attempt 20260428-binary-thread-policy-post-unblock-measurement

## Hypothesis

With corpus-window validation and focused corpus fixes in place, we can now gather stable runtime evidence for binary-subquery thread-policy shapes and decide whether immediate additional behavior tuning is warranted.

## Baseline

Using the corrected focused corpus:

- `harness/corpus/iteration25-binary-thread-policy-smoke.json`

## Measurement run

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration25-binary-thread-policy-smoke.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter29-binary-thread-policy-measure \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict \
  --include-prom false \
  --repeats 3 --warmup 1 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Results

All rows executed as `native_sql` with no errors.

Observed medians (CH ms / shim p50 ms):

- mixed-root:
  - `force_supported`: 316 / 336.52
  - `prefer`: 322 / 339.64
- nested-binary:
  - `force_supported`: 188 / 196.53
  - `prefer`: 188 / 197.84

Profile highlights remained close across modes.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter29-binary-thread-policy-measure/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement-only).

No additional behavior change in this iteration. Current evidence shows stable execution and small mode deltas for the targeted binary-subquery family after recent scoping/unblock work.

Next step should target a new behavior candidate only if it has a clear expected-value path beyond this measured noise band.
