# Attempt 20260428-range-self-join

## Hypothesis

For range-mode binary vector expressions with identical operands and default one-to-one matching, promshim can avoid the second flattened range source and binary join. Rendering one flattened source and applying the binary operator to the same row should preserve semantics for `A + A` while reducing ClickHouse join work, memory, and CPU.

Initial query:

```promql
rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])
```

Profile:

- Benchmark stack profile: `7d profile-50k` present in Prometheus and ClickHouse.
- Query range: `2026-03-13T21:45:42Z..2026-03-14T21:45:42Z`, step `300s`.

## Baseline findings

The originally planned average target:

```promql
(sum by (job) (rate(demo_cpu_usage_seconds_total[1h])) + sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))) / 2
```

is already simplified by the logical `cancel_repeated_average` pass. Baseline artifact:

- `harness/artifacts/explain/20260428-row-source-reuse-baseline/`

That query rendered as a single sparse direct rate aggregation with no repeated binary source, so it is a guardrail rather than the best first runtime target.

The non-cancelled `rate + rate` query had existing materialized CTE row-source reuse, but still flattened the shared source twice and joined it to itself.

Baseline artifact:

- `harness/artifacts/explain/20260428-row-source-reuse-rate-plus-baseline/`

Baseline `query-log-summary.tsv` selected signals:

| Signal | Before |
|---|---:|
| query_duration_ms | 8617 |
| memory_usage | 4056171689 |
| read_rows | 698991425 |
| selected_rows | 699002969 |
| function_execute | 207106 |
| real_time_us | 290023641 |
| user_time_us | 47959768 |
| join_build_rows | 3347760 |
| join_probe_rows | 70117142 |
| join_result_rows | 70060536 |

## Implementation

Kept the optimization conservative:

- Added `storage.BuildRangeBinaryVectorSelfJoinSQL`, analogous to existing instant self-join SQL but preserving range output shape (`tags`, sorted `time_series`).
- Reused the existing `binaryVectorSelfReuseEligible` guard:
  - repeated operands have identical expression strings;
  - operator is `+`;
  - matching is default one-to-one;
  - no bool return;
  - native repeated-subexpression reuse is not disabled.
- Wired the range-mode binary vector branch to use the new range self-join builder.
- Added explain metadata: `row_source_reuse=range_self_join`.
- Added storage tests proving the range self-join has one flattened source and no binary join.

Changed files:

- `internal/promshim/storage/join_sql.go`
- `internal/promshim/storage/join_sql_test.go`
- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `.agents/plans/native-row-source-reuse-optimizer.md` target clarification

## After evidence

After artifact:

- `harness/artifacts/explain/20260428-row-source-reuse-rate-plus-after/`

After `query-log-summary.tsv` selected signals:

| Signal | Before | After | Direction |
|---|---:|---:|---|
| query_duration_ms | 8617 | 7361 | lower |
| memory_usage | 4056171689 | 3299962607 | lower |
| read_rows | 698991425 | 698968337 | roughly stable |
| selected_rows | 699002969 | 698968337 | roughly stable |
| function_execute | 207106 | 206039 | lower |
| real_time_us | 290023641 | 248337765 | lower |
| user_time_us | 47959768 | 44287979 | lower |
| join_build_rows | 3347760 | 11544 | much lower |
| join_probe_rows | 70117142 | 66778011 | lower |
| join_result_rows | 70060536 | 66724320 | lower |

Structural signal:

- Before: 2 `ARRAY JOIN` occurrences, materialized CTE referenced from both sides, binary join present.
- After: 1 `ARRAY JOIN` occurrence, no binary self-join, `row_source_reuse=range_self_join` in promshim explain summary.

Focused benchmark artifacts:

- `harness/artifacts/bench/standalone/20260428-row-source-reuse-self-join/`
- `harness/artifacts/bench/standalone/20260428-row-source-reuse-self-join-prom-check/`

Focused benchmark without Prom reference:

- `regressionCount: 0`
- Strategy histogram: `prefer:native_sql:4`, `force_supported:native_sql:4`
- Memory/profile artifacts written with no reported errors.

Prom reference check:

- `regressionCount: 0`
- `rate_plus_rate_5m_range_1d` remained `native_sql`, Prom p50 `7242.16ms`, shim p50 `8955.03ms` for one repeat. This is a correctness/structural smoke, not a final wall-clock claim.

## Validation

Commands run:

```bash
go test ./internal/promshim/storage ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/row-source-reuse-self-join-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-row-source-reuse-self-join --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
./scripts/run-bench.sh --corpus /tmp/row-source-reuse-self-join-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-row-source-reuse-self-join-prom-check --artifact-name bench-report.json --no-baseline --shim-modes prefer --routing-policies strict --include-prom true --repeats 1 --warmup 0 --memory off --matrix
```

Compliance result:

- prefer mode: 537 passed, 1 accepted tolerance, 0 failures.
- native mode: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root.
- Artifact: `harness/artifacts/compliance/20260428T170504Z/`

## Decision

Keep.

This is a bounded, conservative runtime optimization with correctness validation and strong low-variance ClickHouse signals: self-join build rows collapsed from millions to one row per series/timestamp group, memory and CPU signals moved down, and strategy stayed `native_sql`.

## Follow-ups

- Commit this accepted attempt.
- Continue row-source reuse work by adding typed/keyed eligibility metadata for less trivial repeated sources, especially repeated aggregations that are not already cancelled by logical optimization.
- Consider adding a benchmark-corpus row for `rate(...) + rate(...)` so this self-join path remains visible in future sweeps.
