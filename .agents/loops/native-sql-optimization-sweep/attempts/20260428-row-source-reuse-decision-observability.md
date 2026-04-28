# Attempt 20260428-row-source-reuse-decision-observability

## Hypothesis

Row-source reuse explainability should improve if range binary lowering emits a typed `row_source_reuse` decision not only when reuse is applied (`range_self_join`) but also when an identical repeated operand shape is detected and reuse is rejected by a guard.

Primary observability target:

```promql
rate(demo_cpu_usage_seconds_total[5m]) + on(job) rate(demo_cpu_usage_seconds_total[5m])
```

Expected outcome: explain output includes `row_source_reuse=not_reused` with a concrete guard reason, enabling reviewers to understand why reuse did not apply.

## Baseline

Before this attempt, repeated-but-non-default matching shapes had no row-source-reuse decision metadata in explain output.

Baseline capture (before instrumentation change):

- query: `rate(...) + on(job) rate(...)`
- explain summary had no `row_source_reuse` entry
- HTTP execution still failed with duplicate-match guard (expected for this shape under current semantics)

## Implementation

Changes:

- Added `buildRangeSelfReuseDecision` in `lower_binary_vector_join.go` to produce a typed decision for repeated range-mode binary operands.
- Decision now covers:
  - applied strategy: `range_self_join`
  - rejection strategy: `not_reused` with guard reason and rejected alternative
- Reused existing conservative conditions for eligibility:
  - default one-to-one matching,
  - supported operator,
  - bool allowed only for comparison operators,
  - identical operand expression,
  - identical repeated subtree key,
  - env rollback gate respected.
- Range binary path now attaches the decision metadata in both applied and not-applied repeated-operand cases.
- Added renderer tests that assert decision metadata for:
  - bool comparison reuse (`row_source_reuse=range_self_join`),
  - leaf arithmetic non-reuse (`row_source_reuse=not_reused`).

Files changed:

- `internal/promshim/native/renderer/lower_binary_vector_join.go`
- `internal/promshim/native/renderer/lower_binary_vector_join_test.go`

## After evidence

Explain observability target after change:

- query: `rate(...) + on(job) rate(...)`
- `promshim-explain-summary.tsv` includes `row_source_reuse=not_reused`
- `promshim-physical-decisions.tsv` includes:
  - `kind=row_source_reuse`
  - `strategy=not_reused`
  - `reason=range self-reuse currently requires default one-to-one matching labels`
  - `guards=matching_labels_not_default`
  - `rejected=range_self_join:range self-reuse currently requires default one-to-one matching labels`

Applied-path sanity after change (no runtime regression expected):

- repeated bool comparison (`rate >= bool rate`) still reports `row_source_reuse=range_self_join`
- focused benchmark strategy histogram remains all `native_sql` for tested rows.

## Validation

Commands executed:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/storage ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
./scripts/run-bench.sh --corpus /tmp/range-self-join-bool-corpus.json --eval-time 2026-03-14T21:45:42Z --prom-url http://localhost:29190 --shim-url http://localhost:29191 --ch-url http://localhost:28124 --artifact-dir harness/artifacts/bench/standalone/20260428-range-self-join-bool-v2 --artifact-name bench-report.json --no-baseline --shim-modes prefer,force_supported --routing-policies strict --include-prom false --repeats 1 --warmup 0 --memory summary --clickhouse-profile summary --matrix
```

Validation outcomes:

- renderer/storage/local/native/promshim package tests: pass
- compliance prefer: 537 passed, 1 accepted tolerance, 0 failures
- compliance native: 537 passed, 1 accepted tolerance, 0 diff failures, 0 unsupported root
- focused benchmark: regressionCount 0 and all tested rows `native_sql` in prefer/force_supported

## Decision

Keep.

This attempt improves decision transparency without broadening semantics, and it preserves correctness and benchmark posture.

## Reflection checkpoint summary

1. **Accomplished so far**
   - Built and validated a sequence of row-source reuse optimizations (add, arithmetic, non-bool comparison, bool comparison) with compliance-safe rollouts.
   - Added commit-quality evidence discipline (self-contained metrics/validation).

2. **Working well**
   - Repeated-source optimizations consistently reduce `join_build_rows`, memory, and CPU signals.
   - The attempt pipeline (baseline → change → compliance → benchmark → commit) is stable and repeatable.

3. **Not working / blockers**
   - Explain transparency for non-applied reuse was previously missing for some repeated shapes.
   - Some vector-matching shapes still error at runtime due duplicate-series semantics; this attempt documents rather than changes that behavior.

4. **Approach adjustment**
   - Prioritize observability and explicit rejection reasons before expanding runtime behavior further.

5. **Next priorities**
   - Add typed eligibility/rejection metadata coverage across remaining repeated-source families.
   - Then move to subquery preference propagation and estimate plumbing.
