# native-prewhere-pruning-audit

## Selection

- Selected date: 2026-04-25
- Git revision at selection: `e45b759`
- Layer: native SQL lowering / measurement
- Query family: high-read native selector/aggregation queries
- Status: `deferred`
- Decision: `deferred`

## Research source

- Seed row: `.pi/plans/layered-optimization-iteration/06-research-idea-seed.md`
- Final brief section: `.pi/feynman/outputs/promshim-optimization-ideas.md#9-prewhereprimary-pruning-audit-before-manual-sql-rewrites`

## Hypothesis

Before proposing any manual SQL PREWHERE rewrite, we can verify whether
ClickHouse already performs effective automatic pruning for current native SQL
shapes, using explain/index evidence plus query-log counters.

## Expected signal

Measurement/audit signal only (no behavior change in this attempt).

Expected measurable outcome:

- Explain plan/pipeline indicates MergeTree scans with primary key filtering on
  sampled high-read native queries.
- Query-log counters show non-zero pruning indicators (`SelectQueriesWithPrimaryKeyUsage`,
  `FilteringMarksWithPrimaryKeyMicroseconds`, `RowsReadByPrewhereReaders`,
  `SelectedMarks` << `SelectedMarksTotal` where applicable).
- Evidence supports one of:
  - `accept` (manual PREWHERE candidate not justified yet), or
  - `split/defer` with precise SQL-shape follow-up if pruning gaps are concrete.

## Baseline and context

Current context artifacts:

```text
harness/artifacts/ch-explain/native-prewhere-pruning-audit-selector/
harness/artifacts/ch-explain/native-prewhere-pruning-audit-agg-rate/
harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/
```

Initial observations from fresh captures:

- Selector range capture (`native-prewhere-pruning-audit-selector`) recorded:
  - `SelectQueriesWithPrimaryKeyUsage=2`
  - `RowsReadByPrewhereReaders=1675577`
  - `SelectedMarks=108`, `SelectedMarksTotal=15798`
- Aggregation+rate range capture (`native-prewhere-pruning-audit-agg-rate`) recorded:
  - `SelectQueriesWithPrimaryKeyUsage=2`
  - `RowsReadByPrewhereReaders=89668472`
  - `SelectedMarks=9092`, `SelectedMarksTotal=15798`

These suggest existing primary-key/prewhere pruning is active; next step is to
classify whether any high-value SQL-shape gap remains.

## Proof boundary

Audit-only attempt first:

- Collect and summarize explain + query-log evidence for selected query shapes.
- Do not change native SQL lowering in this pass unless pruning gaps are clear
  and bounded.
- Keep served behavior unchanged.

Excluded in this attempt:

- Manual PREWHERE injection in renderer output.
- CBE gate changes.
- Compliance allowlist changes.

## Risks and caveats

- Single-query evidence can overgeneralize; treat as shape-specific unless
  repeated across representative families.
- Query condition cache and mark cache can confound interpretation.
- Explain output can vary with ClickHouse version and planner internals.

## Rollback path

If this attempt stays audit-only: documentation-only rollback (normal revert of
notes/ledger updates).

If a bounded SQL-shape patch is later added: normal revert of renderer change.

## Predeclared validation commands

```bash
bash -n scripts/ch-explain.sh scripts/run-sweep.sh scripts/bench-artifact-summary.sh
./scripts/ch-explain.sh 'demo_cpu_usage_seconds_total{mode="idle"}' \
  --mode range --range-seconds 86400 --step 300 \
  --eval-time 2026-03-22T21:45:42Z \
  --shim-url http://localhost:29191 --ch-url http://localhost:28124 \
  --log-comment native-prewhere-pruning-audit-selector \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/ch-explain/native-prewhere-pruning-audit-selector
./scripts/ch-explain.sh 'sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))' \
  --mode range --range-seconds 604800 --step 3600 \
  --eval-time 2026-03-22T21:45:42Z \
  --shim-url http://localhost:29191 --ch-url http://localhost:28124 \
  --log-comment native-prewhere-pruning-audit-agg-rate \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/ch-explain/native-prewhere-pruning-audit-agg-rate
rg -n 'PrimaryKey|RowsReadByPrewhereReaders|SelectedMarks|SelectQueriesWithPrimaryKeyUsage|ReadFromMergeTree' \
  harness/artifacts/ch-explain/native-prewhere-pruning-audit-selector \
  harness/artifacts/ch-explain/native-prewhere-pruning-audit-agg-rate
git diff --check
```

## Notes

- Attempt 3 selected from backlog after attempt 2 acceptance.
- Fresh explain captures completed on benchmark endpoints with bounded
  `log_comment` values and native-lowering prefer mode.
## Decision

Deferred (negative evidence): sampled selector and aggregation+rate native SQL
shapes already show active primary-key/prewhere pruning in explain + query-log
counters, so this attempt does not justify a manual PREWHERE rewrite today.

## Evidence summary

- Selector sample: primary-key usage and strong mark reduction
  (`SelectedMarks=108` vs `SelectedMarksTotal=15798`) with non-zero
  `RowsReadByPrewhereReaders`.
- Aggregation+rate sample: primary-key usage and meaningful pruning signal
  (`SelectedMarks=9092` vs `SelectedMarksTotal=15798`) with high
  `RowsReadByPrewhereReaders`.
- Both plans show MergeTree reads with primary-key index analysis.

## Retry condition

Retry only when one of the following is observed in fresh shape-specific
captures:

- high-read native SQL families with weak/no primary-key pruning signal
  (`SelectedMarks` close to `SelectedMarksTotal`, no PK usage), or
- a reproducible SQL shape where explicit PREWHERE materially lowers read/mark
  counters without semantic risk.

Current follow-up should prefer non-invasive explain-only work
(`ir-semantic-dependency-classifier`) until such a gap is evidenced.

## Scope and rollback

- Scope: audit-only decision; no renderer or served-routing changes.
- Rollback: normal revert of decision/ledger documentation if superseded.
- Commit: `pending` (this attempt's semantic commit).
