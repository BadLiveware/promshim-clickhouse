# Attempt 20260428-subquery-preference-propagation-mapping

## Hypothesis

Before changing subquery preference propagation behavior, we need a concrete baseline proving what explain currently exposes for nested subquery shapes. If nested decision metadata is missing, the highest-value next step is instrumentation/surfacing (split), not routing logic changes.

## Query and baseline evidence

Query (range mode):

```promql
rate((sum by (job) (up))[30m:1m])
```

Captured baseline with:

```bash
scripts/ch-explain.sh 'rate((sum by (job) (up))[30m:1m])' \
  --mode range \
  --range-seconds 3600 \
  --step 30 \
  --eval-time 2026-03-14T21:45:42Z \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 --ch-user default --ch-pass otel \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/explain/20260428-subquery-pref-propagation-baseline
```

Artifacts:

- `harness/artifacts/explain/20260428-subquery-pref-propagation-baseline/promshim-explain-summary.tsv`
- `harness/artifacts/explain/20260428-subquery-pref-propagation-baseline/promshim-physical-decisions.tsv`
- `harness/artifacts/explain/20260428-subquery-pref-propagation-baseline/q1/query-clean.sql`

Observed:

- Strategy remains `native_sql`.
- Physical decision metadata surfaces only root-level `query_settings=no_thread_cap` with reason `subquery_rate_over_aggregate_regresses_with_thread_cap`.
- No nested node-level decision rows are currently emitted for child subquery/source preference choices.

## Implementation

No code change in this iteration.

This attempt is an evidence-gathering split to avoid speculative routing edits without decision observability for nested nodes.

## Validation

Structural validation via `ch-explain` artifacts succeeded (query lowered and served as `native_sql`).

No runtime claim in this iteration; no benchmark/compliance run required.

## Decision

Split/defer.

Next high-value attempt should add typed child/node-level preference decision surfacing for subquery families (without changing strategy selection), then evaluate propagation behavior with before/after explain deltas.
