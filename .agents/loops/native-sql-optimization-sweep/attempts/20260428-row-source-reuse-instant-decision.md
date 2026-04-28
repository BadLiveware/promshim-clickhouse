# Attempt 20260428-row-source-reuse-instant-decision

## Hypothesis

Row-source reuse decision observability should be consistent across render modes. Instant-mode repeated range-function binary shapes should emit typed `row_source_reuse` decisions (applied and rejected) just like range mode.

## Baseline

Before this attempt, instant repeated shape explain output did not include row-source reuse decisions.

Example baseline query:

```promql
rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[1h])
```

Before signals:

- `physical_decisions` in explain summary: empty
- query_duration_ms: 136
- memory_usage: 251614040
- function_execute: 9976
- real_time_us: 3334915

## Implementation

Changes:

- Unified self-reuse decision builder into mode-aware helper:
  - `buildSelfReuseDecision(..., mode)`
- Instant-mode binary join now attaches `row_source_reuse` decisions:
  - `instant_self_join` when applied
  - `not_reused` with guard reason when rejected for repeated candidate shapes
- Range-mode path switched to same shared helper (no behavior regression).
- Added renderer tests for instant-mode decision metadata:
  - repeated instant add emits `instant_self_join`
  - repeated instant with `on(job)` emits `not_reused` with matching-label reason

Files changed:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`

## After evidence

Applied-path example after change:

```promql
rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[1h])
```

After explain metadata:

- `row_source_reuse=instant_self_join`
- reason: identical one-to-one repeated range-function operands share one instant source
- guards include `instant_mode`

Applied-path metrics (same query):

- query_duration_ms: 136 -> 133
- memory_usage: 251614040 -> 231505327
- function_execute: 9976 -> 7131
- real_time_us: 3334915 -> 3091185

Rejected-path example after change:

```promql
rate(demo_cpu_usage_seconds_total[5m]) + on(job) rate(demo_cpu_usage_seconds_total[5m])
```

After explain metadata:

- `row_source_reuse=not_reused`
- reason: range self-reuse currently requires default one-to-one matching labels
- rejected alternative: `range_self_join` with same reason

## Validation

Commands run:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/instant-self-reuse-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-instant-self-reuse --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
```

Validation outcomes:

- package tests passed
- compliance remained clean:
  - prefer: 537 passed, 1 accepted tolerance, 0 failures
  - native: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root
- focused instant benchmark:
  - strategy histogram `prefer:native_sql:3`, `force_supported:native_sql:3`
  - regressionCount 0

## Decision

Keep.

This is a low-risk observability and consistency improvement that preserves runtime behavior and keeps compliance/benchmark posture stable while making instant-mode reuse decisions explainable.
