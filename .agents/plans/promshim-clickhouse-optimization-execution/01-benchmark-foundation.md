# 01. Benchmark foundation

## Purpose and scope

Build a measurement base that can support real tuning decisions. This file does
not optimize behavior by itself; it makes the benchmark matrix, artifact capture,
and baseline comparisons strong enough that later ClickHouse, promshim settings,
native SQL, CBE, and tier 3/4 changes can be judged from evidence.

## Prerequisites

- Current branch includes query-log correlation, settings profile provenance,
  logical optimizer metadata, and rollout docs.
- `running-sweep` and `measuring-ch-optimizations` playbooks must be followed
  before benchmark/sweep claims.

## Affected areas

- `harness/corpus/*.json`
- `scripts/run-sweep.sh`
- `scripts/run-bench.sh`
- `scripts/bench-matrix.sh`
- `cmd/promshim-bench`
- `internal/promharness`
- `harness/artifacts/sweeps/`
- Optional helper scripts under `scripts/` for EXPLAIN/query-log captures.

## Workload families to cover

Create a compact but representative tuning corpus. The corpus should be separate
from broad compliance and should include bounded rows for:

- instant selectors: plain, equality matcher, regex matcher;
- range selectors: short and long query_range selector outputs;
- range functions: `rate`, `increase`, `avg_over_time`, `max_over_time`;
- aggregations: `sum by`, `avg by`, `max without`, high-cardinality grouping;
- aggregation over range functions: `sum by(job)(rate(...))` and histogram
  bucket aggregation;
- repeated subexpressions: repeated `rate` and repeated selectors;
- vector matching and histogram rows as shadow/negative controls;
- local-heavy rows where Prometheus/local currently beats native SQL;
- native-heavy rows where ClickHouse should be the reference candidate.

## Implementation tasks

1. Add a tuning corpus.
   - Create `harness/corpus/bench-optimization-tuning.json` for short fixture
     runs and `harness/corpus/bench-optimization-tuning-7d.json` for long-range
     benchmark stack runs.
   - Keep `category` values bounded and family-oriented.
   - Include `compareMode` only where structural comparison is needed because
     minor float/text differences are expected.
   - Acceptance: `python3 -m json.tool` succeeds and the corpus covers all
     workload families above.

2. Add artifact metadata checks.
   - Extend bench/sweep validation if needed so reports expose settings profile,
     routing fields, strategy, CH round trips, CH millis, and log-comment memory
     summary status.
   - Acceptance: a small smoke bench can be checked with one `jq` command that
     reports row count, settings profiles, strategy histogram, and missing log
     comments.

3. Add or standardize focused EXPLAIN/ProfileEvents capture.
   - Prefer reusing existing helpers if present (`scripts/ch-explain.sh`,
     `scripts/ch-profile-capture.sh`, `scripts/ch-profile-diff.sh`).
   - If helpers are insufficient, add a small script that takes a promshim URL,
     query, mode/policy/profile, and output directory, then writes:
     - promshim explain JSON;
     - rendered SQL;
     - executed ClickHouse SQL from query_log when available;
     - EXPLAIN SYNTAX, PLAN, PIPELINE;
     - query-log JSON rows filtered by log comment.
   - Acceptance: the helper works for `sum by (job) (demo_cpu_usage_seconds_total)`
     and `rate(demo_cpu_usage_seconds_total[1h])`.

4. Capture strict baseline artifacts.
   - Run a named 7d sparse baseline using the optimization tuning corpus.
   - Run a focused dense control only for rows that complete within the timeout;
     split out rows that time out into a separate `slow-control` artifact rather
     than hiding them.
   - Run a 30d/1y sparse baseline if seeded data is present; otherwise record
     `bench-status` evidence that the dataset is missing.
   - Acceptance: each completed baseline has a manifest, bench report, matrix,
     and memory summary with zero missing log comments.

5. Record baseline interpretation.
   - Add an artifact note under `harness/artifacts/sweeps/<run>/notes.md` or a
     repo-local `outputs/` note that identifies candidate route cliffs, timeouts,
     and families that are too noisy for wall-clock-only decisions.
   - Acceptance: note names the next candidate experiments for files 02-06.

## Validation tasks

Run:

```bash
python3 -m json.tool harness/corpus/bench-optimization-tuning.json >/dev/null
python3 -m json.tool harness/corpus/bench-optimization-tuning-7d.json >/dev/null
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --dry-run --estimate --name promshim-optimization-foundation-dry-run
```

For any live baseline run, inspect:

```bash
./scripts/bench-matrix.sh --sweep <manifest.json> --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' <memory-summary.json>
```

## Exit criteria

- Tuning corpus exists and covers the targeted family spread.
- At least one named 7d sparse baseline exists for the tuning corpus.
- Dense and long-range availability is explicitly known from artifacts.
- Query-log correlation is complete for completed benchmark rows.
- Baseline notes identify the first experiments for ClickHouse deployment tuning,
  promshim settings, native SQL/IR, CBE, and tier 3/4 work.

## Handoff to next file

Use the baseline artifact names and notes as the input to ClickHouse deployment
tuning. Do not change ClickHouse server configuration before the baseline is
preserved.
