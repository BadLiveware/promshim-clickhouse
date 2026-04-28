# Attempt 20260428-range-self-join-bool-comparison

## Hypothesis

Range self-reuse can safely include identical one-to-one repeated **bool comparisons** for range-function subtrees, for example:

```promql
rate(demo_cpu_usage_seconds_total[5m]) >= bool rate(demo_cpu_usage_seconds_total[5m])
```

Expected value: keep semantics while removing duplicated binary-side work.

## Baseline

Before change (`rate >= bool rate`, 1d range, 5m step):

- query_duration_ms: 8707
- memory_usage: 4045586437
- read_rows: 698991425
- selected_rows: 699002969
- function_execute: 208485
- real_time_us: 293214755
- user_time_us: 49422945
- join_build_rows: 3347760
- join_probe_rows: 70182299
- join_result_rows: 70060536

Structure before:

- 2 `ARRAY JOIN` occurrences
- 2 binary-side `INNER JOIN` branches
- no row-source reuse decision

## Implementation

Changed renderer eligibility to allow self-reuse when:

- operator is supported arithmetic or comparison,
- default one-to-one matching,
- identical expressions and identical repeated subtree key,
- bool is allowed only for comparison operators,
- existing repeated-subexpression disable env still blocks reuse.

Tests updated:

- bool comparison repeated range subtree now reuses one source,
- non-bool comparison reuse still works,
- non-range leaf arithmetic remains non-reused.

Benchmark corpus visibility:

- Added `rate_ge_bool_rate_5m_range_1d` to `harness/corpus/bench-native-lowering-7d.json`.

Files changed:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`
- `harness/corpus/bench-native-lowering-7d.json`

## After evidence

After change (`rate >= bool rate`, same shape):

- query_duration_ms: 7206
- memory_usage: 3044895581
- read_rows: 698968337
- selected_rows: 698968337
- function_execute: 207124
- real_time_us: 242960764
- user_time_us: 43638560
- join_build_rows: 11544
- join_probe_rows: 66819260
- join_result_rows: 66724320

Direction summary:

- duration: down
- memory: down
- CPU signals (`real_time_us`, `user_time_us`): down
- join build/probe/result rows: down significantly on build, down on probe/result
- read/selected rows: effectively stable for input size

Structure after:

- 1 `ARRAY JOIN`
- binary self-reuse expression present (`toFloat64(if((lhs.value >= lhs.value), 1, 0))`)
- `row_source_reuse=range_self_join` in explain decision metadata

## Validation

Commands executed:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/range-self-join-bool-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-range-self-join-bool --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
```

Validation outcomes:

- renderer/storage/local/native/promshim test packages: pass
- compliance prefer pass: 537 passed, 1 accepted tolerance, 0 failures
- compliance native pass: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root
- focused benchmark: no regressions, all rows native_sql in prefer/force_supported

## Decision

Keep.

This extension maintains conservative safety gates and shows material reductions in join-build, memory, and CPU signals for repeated bool comparison range-function expressions.
