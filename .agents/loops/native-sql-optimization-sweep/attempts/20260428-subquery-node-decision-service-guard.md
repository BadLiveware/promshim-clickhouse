# Attempt 20260428-subquery-node-decision-service-guard

## Hypothesis

After canonicalizing subquery-node `query_settings=no_thread_cap` explain metadata, we should lock API-level visibility so service explain responses cannot regress to root-only decision reporting.

## Baseline evidence

External explain artifact for:

```promql
rate((sum by (job) (up))[30m:1m])
```

still showed only a root-level row in `promshim-physical-decisions.tsv` from the running benchmark stack, which is expected when that stack is not rebuilt to latest local commits.

Artifact:

- `harness/artifacts/explain/20260428-subquery-node-visibility-baseline/`

Given environment drift risk, this iteration secures repository-level API behavior via service tests.

## Implementation

Added API regression coverage in `internal/promshim/service_test.go`:

- helper `findExplainNodeByKind(local.ExplainNode, kind)`
- new test `TestQueryRangeExplainIncludesSubqueryNodeThreadPreferenceDecision`

New assertions for query_range_explain on `rate((sum by (job) (up))[30m:1m])`:

- plan is native range explain
- subquery node exists
- subquery node contains `query_settings=no_thread_cap`
- reason equals `physical.ThreadPreferenceReasonSubqueryRateRows`

## Validation

```bash
go test ./internal/promshim -run 'TestQueryRangeExplainIncludes(SubqueryNodeThreadPreferenceDecision|PhysicalDecisions)$' -v

go test ./internal/promshim/local ./internal/promshim
```

All passed.

## Measurement for the claim

Claim type: explain-surface regression guard (no runtime performance claim).

Evidence is API-level structural assertion in service tests, which closes the gap between local planner tests and externally consumed explain responses.

## Decision

Keep.

This is a low-risk test hardening attempt that improves confidence before behavior-changing subquery preference propagation work.
