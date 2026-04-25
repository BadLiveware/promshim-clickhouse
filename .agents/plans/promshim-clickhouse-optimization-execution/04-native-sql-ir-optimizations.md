# 04. Native SQL and IR optimizations

## Purpose and scope

Implement native SQL and IR optimizations that make ClickHouse do less work or
move less data. This file is not satisfied by metadata-only work or narrower SQL
text alone. Each accepted optimization must show executor-visible evidence unless
it is explicitly recorded as rejected because ClickHouse already normalizes the
shape.

## Prerequisites

- Benchmark foundation and ClickHouse reference profile are stable.
- Promshim settings profile is fixed for the experiment or explicitly varied.
- Logical optimizer tracing and rollback already exist.
- Aggregation label projection exists and is recognized as an intermediate-width
  optimization, not a scan-reduction claim.

## Affected areas

- `internal/promshim/logical/`
- `internal/promshim/logical/opt/`
- `internal/promshim/native/analysis.go`
- `internal/promshim/native/optimizer.go`
- `internal/promshim/native/renderer/`
- `internal/promshim/storage/selector_sql.go`
- `internal/promshim/local/explain.go`
- Renderer golden tests under `internal/promshim/native/renderer/testdata/`
- Benchmark corpora for targeted families.

## Candidate queue

Work through candidates in this order. Stop a candidate when evidence shows it is
not useful, and record the rejection.

1. Exact selector time-bound pruning audit and tightening.
   - Verify all instant/range/rollup native paths use the tightest safe
     `required_start_ms`/`required_end_ms`.
   - Expected signal: reduced `SelectedRows`, `SelectedMarks`, `read_rows`, or
     `read_bytes` when current SQL is overly broad.
   - Rejection condition: EXPLAIN/query-log evidence shows current bounds are
     already exact for targeted rows.

2. Projection pruning beyond simple aggregation labels.
   - Extend required-label projection to selectors, transformations, simple
     aggregations, and histogram-safe families only where output label semantics
     are explicit.
   - Expected signal: lower intermediate width, result bytes, local transfer, or
     memory; not necessarily lower read rows.

3. Repeated selector/subtree reuse.
   - Use selector fingerprints to avoid duplicate ClickHouse scans or duplicate
     local work for identical subexpressions.
   - Expected signal: fewer ClickHouse round trips, fewer repeated scans, lower
     `FunctionExecute`/array counters, or lower local CPU.
   - Rejection condition: `EXPLAIN SYNTAX` and ProfileEvents prove ClickHouse
     already merges the work.

4. Range function SQL shape improvements.
   - Reduce array materialization/sorting/window work for `rate`,
     `avg_over_time`, and related functions where ClickHouse has better
     aggregate forms.
   - Expected signal: lower `FunctionExecute`, array-operation counters, memory,
     or CH millis under sparse and dense controls.

5. Aggregation pushdown refinement.
   - Improve direct aggregation paths only where NaN/label semantics are proven.
   - Expected signal: lower transfer, memory, or CH processing for aggregation
     families without correctness drift.

6. Histogram and vector-match families remain shadow-first.
   - Only optimize if a focused correctness suite exists for the exact semantics.

## Implementation tasks

1. Pick one candidate and write the expected-signal note.
   - Add a short artifact note naming target queries, expected signal,
     correctness risks, rollback gate, and negative controls.
   - Acceptance: the note identifies the baseline artifact path and exact query
     rows before code changes.

2. Capture pre-change EXPLAIN/ProfileEvents.
   - For each target query, capture promshim explain, rendered SQL, ClickHouse
     EXPLAIN SYNTAX/PLAN/PIPELINE, and query-log/ProfileEvents under a bounded
     log comment.
   - Acceptance: artifact directory can be inspected without rerunning queries.

3. Implement the optimization behind a rollback gate when serving behavior or SQL
   shape changes materially.
   - For IR passes, use trace names, skip reasons, and env/config rollback.
   - For renderer changes, update golden tests and targeted semantic tests.
   - For storage SQL changes, add SQL-builder tests and explain assertions.
   - Acceptance: unit tests fail without the change and pass with it.

4. Capture post-change evidence.
   - Repeat the exact pre-change captures using the same benchmark reference
     profile and settings profile.
   - Compare ProfileEvents and EXPLAIN, not just p50.
   - Acceptance: expected signal moves in the intended direction, or the
     candidate is reverted/rejected with evidence.

5. Run family sweeps and controls.
   - Run focused 7d sparse target sweep.
   - Run dense or long-range control when the change could alter route cliffs.
   - Run compliance if correctness-sensitive semantics changed.
   - Acceptance: memory summaries have zero missing comments and no unexplained
     errors.

6. Update docs and rollout notes.
   - Document accepted optimization in README/docs only as domain behavior, not
     as internal execution process.
   - If rejected, add a note to the artifact/outputs area explaining why.
   - Acceptance: docs name rollback controls and evidence artifacts for accepted
     changes.

## Validation tasks

Fast checks:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
git diff --check
```

Targeted evidence commands depend on the helper from file 01. Minimum manual
fallback for one query:

```bash
curl -sf -H 'X-Promshim-Log-Comment: <bounded-comment>' \
  '<promshim-url>/api/v1/query_range?query=<encoded>&start=<start>&end=<end>&step=<step>&native_lowering_mode=force_supported' \
  -o <artifact>/response.json
# Then SYSTEM FLUSH LOGS, query system.query_log by log_comment, and capture
# EXPLAIN SYNTAX/PLAN/PIPELINE for the executed SQL.
```

Focused sweep pattern:

```bash
./scripts/run-sweep.sh \
  --name native-opt-<family>-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer,force_supported,off \
  --corpus-set native --memory summary
```

Compliance gate when semantics changed:

```bash
./scripts/run-sweep.sh --name native-opt-<family>-compliance --skip-bench
```

## Exit criteria

- At least one native SQL/IR optimization is accepted with executor-visible
  evidence, or top-priority candidates are rejected with clear EXPLAIN/ProfileEvents
  proof.
- Accepted changes have tests, rollback controls where needed, and docs/artifact
  references.
- Compliance remains clean in prefer mode except existing allowed deviances.
- Evidence distinguishes SQL readability from actual executor work.

## Handoff to next file

Use accepted/rejected native optimization evidence to update CBE. If native SQL
remains slower for a family even after optimization, that family becomes a CBE
candidate for tier 3/4 serving or optimization.
