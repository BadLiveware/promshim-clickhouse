# Attempt 20260428-bench-corpus-range-validation

## Hypothesis

The recent benchmark HTTP 400 failures should be prevented earlier by corpus-level validation in the harness loader, so invalid `query_range` offset windows fail fast with an actionable message instead of surfacing as opaque runtime request errors.

## Baseline

With an invalid range corpus (`startOffsetSeconds > endOffsetSeconds`), `run-bench` previously proceeded to request execution and produced HTTP 400 row errors.

## Implementation

Added runtime corpus validation in `internal/promharness/corpus.go`:

- validate non-empty `name` and `query`
- validate endpoint is `query` or `query_range`
- for `query_range`:
  - require `endOffsetSeconds >= startOffsetSeconds`
  - require `stepSeconds > 0`
- return entry-indexed/name-aware error from `LoadQueryCorpus`

Added unit coverage:

- `internal/promharness/corpus_loader_test.go`
  - `TestLoadQueryCorpusRejectsInvalidRangeOffsets`

## Validation

```bash
go test ./internal/promharness -run TestLoadQueryCorpusRejectsInvalidRangeOffsets -v
go test ./internal/promshim/local ./internal/promshim/native/renderer ./internal/promshim
```

All passed.

## Measurement for the claim

Verified fail-fast behavior via bench invocation with an invalid temporary corpus:

```bash
./scripts/run-bench.sh --corpus /tmp/iter28-invalid-corpus.json ...
```

Now fails immediately with explicit loader error:

- `invalid query corpus entry #1 ("bad_range"): query_range requires endOffsetSeconds >= startOffsetSeconds ...`

instead of per-row HTTP 400 benchmark errors.

## Decision

Keep.

This unblocks faster harness triage and reduces wasted benchmark iterations for malformed query_range windows.
