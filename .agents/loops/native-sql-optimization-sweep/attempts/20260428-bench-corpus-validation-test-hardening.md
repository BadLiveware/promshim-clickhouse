# Attempt 20260428-bench-corpus-validation-test-hardening

## Hypothesis

After adding fail-fast corpus validation, we should harden regression coverage for adjacent invalid-input classes so harness UX stays explicit and stable instead of regressing back to late runtime failures.

## Baseline

Validation logic exists for:

- invalid `query_range` offset ordering
- non-positive `query_range` step
- unsupported endpoint

Only offset-order case had direct loader unit coverage.

## Implementation

Expanded `internal/promharness/corpus_loader_test.go` with two additional tests:

- `TestLoadQueryCorpusRejectsNonPositiveRangeStep`
- `TestLoadQueryCorpusRejectsUnsupportedEndpoint`

## Validation

```bash
go test ./internal/promharness -run TestLoadQueryCorpusRejects -v
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim
```

All passed.

## Measurement for the claim

Claim type: harness validation hardening (no runtime optimization claim).

Evidence is broader unit-level guard coverage around loader-side invalid corpus handling.

## Decision

Keep.

This reduces risk of benchmark-debug regressions while keeping optimization loops focused on measurable runtime work.
