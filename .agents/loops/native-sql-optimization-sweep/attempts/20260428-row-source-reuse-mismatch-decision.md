# Attempt 20260428-row-source-reuse-mismatch-decision

## Hypothesis

Explain output should expose a `row_source_reuse=not_reused` decision for repeated-candidate binary shapes whose operands differ, so users can tell why instant self-reuse was not selected.

Example target query:

```promql
rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[6h])
```

## Baseline

Before this attempt, the query above had no row-source-reuse decision metadata in explain output.

Before runtime signals:

- query_duration_ms: 246
- memory_usage: 255228971
- function_execute: 16814
- real_time_us: 6193304
- join_build_rows: 34632
- join_probe_rows: 5552664
- join_result_rows: 5552664

## Implementation

Changes:

- Updated mode-aware self-reuse decision builder to emit `not_reused` for repeated candidate operands with different subtree keys.
- Kept decision emission scoped to repeated-candidate families; non-candidate binary shapes still omit row-source-reuse decisions to avoid noisy metadata.
- Added test coverage for:
  - instant repeated-add decision (`instant_self_join`),
  - instant on-matching rejection (`not_reused` + matching-label reason),
  - instant different repeated operands rejection (`not_reused` + subtree mismatch reason).

Files changed:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`

## After evidence

After explain metadata for target query now includes:

- `row_source_reuse=not_reused`
- reason: `operands are different repeated subtree candidates`
- guards: `repeated_subtree_candidate_mismatch`
- rejected alternative: `instant_self_join:operands are different repeated subtree candidates`

After runtime signals (same query):

- query_duration_ms: 265
- memory_usage: 420419579
- function_execute: 14204
- real_time_us: 6527177
- join_build_rows: 34632
- join_probe_rows: 5552664
- join_result_rows: 5552664

These counters confirm runtime shape is effectively unchanged; this is an explainability/diagnostics improvement, not a performance-claim change.

## Validation

Commands run:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
```

Validation outcome:

- package tests passed

## Decision

Keep.

This is a low-risk observability improvement that improves decision transparency for repeated-candidate mismatches without broadening semantics or introducing runtime-behavior risk.
