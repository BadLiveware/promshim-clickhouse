# Attempt 20260428-range-self-join-arithmetic

## Hypothesis

The new range self-join path should generalize from `A + A` to identical one-to-one arithmetic operands (`+`, `-`, `*`, `/`, `%`, `^`) for repeated range-function subtrees. Reusing one flattened source should reduce duplicated join/build work for expressions such as:

```promql
rate(demo_cpu_usage_seconds_total[5m]) * rate(demo_cpu_usage_seconds_total[5m])
```

without changing semantics, strategy selection, or compliance.

## Baseline

Baseline artifact (before this attempt):

- `harness/artifacts/explain/20260428-range-self-join-mul-before/`

Key baseline signals (`query-log-summary.tsv`):

| Signal | Before |
|---|---:|
| query_duration_ms | 8711 |
| memory_usage | 4055860420 |
| read_rows | 698991425 |
| selected_rows | 699002969 |
| function_execute | 207850 |
| real_time_us | 293298166 |
| user_time_us | 48105874 |
| join_build_rows | 3347760 |
| join_probe_rows | 70162320 |
| join_result_rows | 70060536 |

Structure before:

- 2 `ARRAY JOIN` occurrences.
- 2 binary-side `INNER JOIN` paths.
- No `row_source_reuse` physical decision for multiply case.

## Implementation

Changes:

- Expanded native binary self-reuse eligibility from `ADD` only to arithmetic ops: `ADD`, `SUB`, `MUL`, `DIV`, `MOD`, `POW`.
- Added safety gate to avoid broad leaf-expression rewrites: require both operands to be repeated CSE subtree candidates (range-function family) via matching `cseSubtreeKey`.
- Updated range-mode physical decision wording/guards to `arithmetic_operator`.
- Added renderer tests:
  - range multiply self-reuse for repeated rate source,
  - no self-reuse for leaf arithmetic (`up * up`) to keep scope conservative.
- Added benchmark corpus visibility row:
  - `rate_plus_rate_5m_range_1d` in `harness/corpus/bench-native-lowering-7d.json`.

Changed files:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`
- `harness/corpus/bench-native-lowering-7d.json`

## After evidence

After artifact:

- `harness/artifacts/explain/20260428-range-self-join-mul-after/`

Key after signals (`query-log-summary.tsv`):

| Signal | Before | After | Direction |
|---|---:|---:|---|
| query_duration_ms | 8711 | 6919 | lower |
| memory_usage | 4055860420 | 3027569371 | lower |
| read_rows | 698991425 | 698968337 | roughly stable |
| selected_rows | 699002969 | 698968337 | roughly stable |
| function_execute | 207850 | 206642 | lower |
| real_time_us | 293298166 | 233424519 | lower |
| user_time_us | 48105874 | 42968448 | lower |
| join_build_rows | 3347760 | 11544 | much lower |
| join_probe_rows | 70162320 | 66819034 | lower |
| join_result_rows | 70060536 | 66724320 | lower |

Structure after:

- 1 `ARRAY JOIN`.
- 1 remaining `INNER JOIN` (series-source join), no duplicated binary side join.
- SQL includes `lhs.value * lhs.value`.
- `promshim-physical-decisions.tsv` includes:
  - `row_source_reuse = range_self_join` with `arithmetic_operator` guard.

## Validation

Commands run:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/range-self-join-arithmetic-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-range-self-join-arithmetic --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
```

Results:

- Go tests passed for changed/affected packages.
- Compliance passed:
  - prefer: 537 passed, 1 accepted tolerance, 0 failures.
  - native: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root.
  - artifact: `harness/artifacts/compliance/20260428T171610Z/`
- Focused benchmark passed with no regressions:
  - artifact: `harness/artifacts/bench/standalone/20260428-range-self-join-arithmetic/`
  - strategy histogram: `prefer:native_sql:4`, `force_supported:native_sql:4`

## Decision

Keep.

The generalization preserved correctness and strategy behavior while reducing duplicated binary-side work for repeated arithmetic range-function expressions.

## Follow-ups

- Add typed/keyed explain metadata for self-reuse eligibility/rejection (beyond applied strategy only).
- Evaluate whether selective comparison-op self-reuse is worth pursuing separately with NaN-sensitive guards and fixtures.
