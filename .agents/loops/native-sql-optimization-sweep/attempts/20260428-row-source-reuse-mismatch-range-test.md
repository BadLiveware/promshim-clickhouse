# Attempt 20260428-row-source-reuse-mismatch-range-test

## Hypothesis

Range mode should keep the same row-source-reuse mismatch explainability contract as instant mode for repeated-candidate operand mismatches.

## Baseline evidence

Range explain for:

```promql
rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[6h])
```

already reports:

- `row_source_reuse=not_reused`
- reason: `operands are different repeated subtree candidates`
- guard: `repeated_subtree_candidate_mismatch`
- rejected alternative: `range_self_join:...`

## Implementation

No runtime behavior change.

Added missing renderer unit coverage to lock this contract in range mode:

- `TestLowerBinaryVectorJoinMarksRangeNotReusedForDifferentRepeatedOperands`

File changed:

- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`

## Validation

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
```

All passed.

## Decision

Keep.

This is a low-risk regression guard that preserves explainability behavior for range mismatch shapes and prevents future drift between instant/range decision metadata.
