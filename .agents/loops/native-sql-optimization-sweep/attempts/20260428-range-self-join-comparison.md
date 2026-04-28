# Attempt 20260428-range-self-join-comparison

## Hypothesis

Range self-reuse should safely extend from repeated arithmetic range-function operands to repeated non-bool comparisons under identical one-to-one matching, for example:

```promql
rate(demo_cpu_usage_seconds_total[5m]) >= rate(demo_cpu_usage_seconds_total[5m])
```

Expected value: preserve semantics while reducing duplicated binary-side join/build work and memory/CPU for repeated comparison shapes.

## Baseline

Baseline explain capture:

- `harness/artifacts/explain/20260428-range-self-join-compare-before/`

Baseline query-log signals:

| Signal | Before |
|---|---:|
| query_duration_ms | 8685 |
| memory_usage | 4034302245 |
| read_rows | 698991425 |
| selected_rows | 699002969 |
| function_execute | 206683 |
| real_time_us | 292427191 |
| user_time_us | 47966745 |
| join_build_rows | 3347760 |
| join_probe_rows | 70156605 |
| join_result_rows | 70060536 |

Structure before:

- 2 `ARRAY JOIN` occurrences.
- 2 binary-side `INNER JOIN` branches.
- No `row_source_reuse` decision for the comparison shape.

## Implementation

Changes:

- Extended `binaryVectorSelfReuseEligible` operator support from arithmetic-only to include non-bool comparison operators:
  - `==`, `!=`, `>`, `<`, `>=`, `<=`
- Preserved conservative gates:
  - disable env still honored,
  - default one-to-one matching only,
  - `return bool` explicitly blocked,
  - operands must be identical,
  - both sides must be repeated CSE subtree candidates with identical subtree key.
- Updated decision guard text to generic supported-operator / repeated-subtree wording.
- Added renderer tests:
  - repeated range comparison uses one flattened source,
  - bool comparison does not reuse,
  - existing non-range leaf arithmetic no-reuse guard remains.
- Added benchmark corpus visibility row:
  - `rate_ge_rate_5m_range_1d` in `harness/corpus/bench-native-lowering-7d.json`.

Changed files:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`
- `harness/corpus/bench-native-lowering-7d.json`

## After evidence

After explain capture:

- `harness/artifacts/explain/20260428-range-self-join-compare-after/`

After query-log signals:

| Signal | Before | After | Direction |
|---|---:|---:|---|
| query_duration_ms | 8685 | 7099 | lower |
| memory_usage | 4034302245 | 3023742564 | lower |
| read_rows | 698991425 | 698968337 | roughly stable |
| selected_rows | 699002969 | 698968337 | roughly stable |
| function_execute | 206683 | 205961 | lower |
| real_time_us | 292427191 | 239284230 | lower |
| user_time_us | 47966745 | 43938903 | lower |
| join_build_rows | 3347760 | 11544 | much lower |
| join_probe_rows | 70156605 | 66821138 | lower |
| join_result_rows | 70060536 | 66724320 | lower |

Structure after:

- 1 `ARRAY JOIN`.
- 1 remaining `INNER JOIN` (series-source join), no duplicated binary-side join.
- SQL contains `lhs.value >= lhs.value`.
- Explain decision includes `row_source_reuse=range_self_join` with supported operator / repeated-subtree guards.

## Validation

Commands run:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/range-self-join-compare-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-range-self-join-compare --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
```

Results:

- Go tests passed for renderer/storage/local/native/promshim packages.
- Compliance passed:
  - prefer: 537 passed, 1 accepted tolerance, 0 failures.
  - native: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root.
- Focused benchmark passed with no regressions:
  - strategy histogram: `prefer:native_sql:4`, `force_supported:native_sql:4`.
  - repeated comparison row stayed `native_sql` in both modes.

## Decision

Keep.

This extension remains conservative and preserves correctness while reducing duplicated binary-side work for repeated non-bool comparison range-function shapes.

## Follow-ups

- Evaluate whether selected bool-comparison self-reuse can be made safe and valuable with explicit semantics guards.
- Add typed explain metadata for eligibility/rejection paths (not only applied strategy).