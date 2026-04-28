# Attempt 20260428-binary-subquery-bench-harness-unblock

## Hypothesis

The focused benchmark HTTP 400 failures were caused by an invalid corpus time-window shape (`startOffsetSeconds > endOffsetSeconds`) rather than unsupported query semantics.

## Baseline (failing)

Previous attempt (`20260428-subquery-binary-bench-harness-400`) used:

- `startOffsetSeconds: 3600`
- `endOffsetSeconds: 0`

and produced HTTP 400 for all rows/modes.

## Implementation

Updated focused corpus timing in:

- `harness/corpus/iteration25-binary-thread-policy-smoke.json`

Changes:

- `startOffsetSeconds: 0`
- `endOffsetSeconds: 3600`

Queries unchanged (mixed-root and nested-binary subquery shapes).

## Validation + measurement

Ran focused isolated benchmark:

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration25-binary-thread-policy-smoke.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter27-binary-thread-policy-smoke-fixed \
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

Results:

- no HTTP 400 failures
- all four rows served as `native_sql`
- mixed-root `ch_ms`: 309 (force_supported), 336 (prefer)
- nested-binary `ch_ms`: 184 (force_supported), 185 (prefer)

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter27-binary-thread-policy-smoke-fixed/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep.

This unblocks runtime measurement for the binary-subquery thread-policy family and validates that the prior 400 issue was a harness corpus-window bug.
