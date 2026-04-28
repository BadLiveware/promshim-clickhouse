# Attempt 20260428-subquery-hotspot-candidate-mapping

## Hypothesis

Before another runtime behavior change, we should map a slightly broader subquery-only hotspot set and identify where runtime signals are large enough to justify tuning.

## Implementation

Created focused corpus from `common-dashboard-subset` subquery rows:

- `harness/corpus/iteration33-subquery-hotspots.json` (3 queries)

## Measurement run

```bash
./scripts/run-bench.sh \
  --corpus harness/corpus/iteration33-subquery-hotspots.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/20260428-iter33-subquery-hotspots \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict \
  --include-prom false \
  --repeats 2 --warmup 1 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Results

- All rows ran as `native_sql` with no errors.
- prefer vs force_supported deltas are small across rows (again near noise scale).
- One query stands out by absolute cost among this set:
  - `draft_cand_0242_rate_family_subquery_aggregation_selector` at ~85ms CH p50.
- Memory outlier among this set:
  - `draft_cand_0416_rate_family_range_function_subquery_aggregation_selector` with ~81.3MiB p95 memory in both modes.

Artifacts:

- `harness/artifacts/bench/standalone/20260428-iter33-subquery-hotspots/bench-report.json`
- `.../memory-summary-bench-report.json`
- `.../clickhouse-profile-bench-report.json`

## Decision

Keep (measurement-only candidate mapping).

No code change this iteration. This narrows the next runtime candidate to query shapes with larger absolute cost/memory signal instead of continuing to optimize low-delta mode differences.
