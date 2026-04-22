# Measuring the native SQL lowering — methodology

The matrix bench reports wall-clock p50 per PromQL fixture. That is too
noisy a signal to evaluate single-commit optimizations: a 2–3% fixture-level
delta sits inside run-to-run variance, so commits that *shuffle SQL text*
look indistinguishable from commits that actually *do* less work.

This doc describes sharper signals ClickHouse gives us for free, the
tooling that captures them, and the manual steps for the bits that are not
worth scripting.

Baseline loop time is ~25 s. The tooling here adds roughly:

| Tool                           | Added time | When to use                    |
|--------------------------------|-----------:|--------------------------------|
| `ch-profile-capture.sh`        |   ~1–2 s   | every bench run (drop-in)      |
| `ch-profile-diff.sh`           |   <1 s     | after comparing two captures   |
| `ch-explain.sh`                |   ~2 s/call| deep-dive on one fixture       |
| `ch-explain-diff.sh`           |  30–180 s  | bisecting a single commit      |
| `seed-long-range.sh`           |   2–15 s   | once per `docker volume rm`    |
| `run-bench.sh --long-range 7d` |    ~90 s   | storage-side pruning signal    |
| `run-bench.sh --long-range 30d`|   ~360 s   | partition-crossing signal      |
| `run-bench.sh --long-range 1y` |   ~120 s   | part-pruning / yearly scans    |

All within the 2-orders-of-magnitude budget on the 25 s baseline loop.
Long-range bench times assume the data is already seeded (persistent in
the docker volume); they measure query time only, not ingest.

---

## The signals, ranked

1. **`EXPLAIN SYNTAX`** — ClickHouse's own syntactic rewriter runs before
   the planner and already does CSE, constant folding, common-subquery
   unnesting, and alias resolution. If two commits' lowered SQL produce
   byte-identical `EXPLAIN SYNTAX` output, whatever textual "optimization"
   happened between them is cosmetic: the executor sees the same thing. This
   is the single highest-value check you can run, and it is dirt cheap.
2. **`ProfileEvents` from `system.query_log`** — a `Map(String, UInt64)`
   attached to every finished query. Keys of interest:
   - `RealTimeMicroseconds`, `UserTimeMicroseconds`, `SystemTimeMicroseconds`
     — CPU breakdown. Far lower variance than wall-clock.
   - `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes` — storage-side
     work. Moves only when you actually prune (matcher canonicalization,
     `__name__` first filtering, stale-NaN pushdown).
   - `FunctionExecute` — total function invocations across the execution
     pipeline. If a commit claims to kill two `arrayMap(...)` sites, this
     number drops.
   - `ArraySort`, `ArrayFilter`, `ArrayMap`-family counters.
   - `NetworkSendBytes`, `OSReadChars`, `MemoryTrackerUsage` — I/O +
     memory footprint.
3. **`EXPLAIN PLAN indexes=1, actions=1, optimize=1`** — shows which primary-
   key ranges were scanned and what planner actions survived. This tells
   the difference between "shorter SQL, same bytes read" (a no-op) and
   "shorter SQL, fewer bytes read" (a real win).
4. **`EXPLAIN PIPELINE json=1`** — the processor graph. If two SQL shapes
   compile to isomorphic pipelines they will run identically.
5. **`EXPLAIN ESTIMATE`** — estimated rows/bytes/parts per data source;
   cheap to diff; flags when pruning changed.
6. **`system.trace_log`** — CPU-sampled stacks when you set
   `query_profiler_cpu_time_period_ns=10000000`. Use for structural commits
   where you need to see where ClickHouse is spending time; skip for
   alias-only commits.

---

## Scripted workflows

### 1. Every bench run: capture ProfileEvents

Drop-in wrapper that adds ~1–2 s to a `run-bench.sh` invocation. Emits
`harness/artifacts/ch-profile.json` alongside `bench-report.json`.

```bash
./scripts/ch-profile-capture.sh --matrix --repeats 10
```

The output file contains one entry per **normalized SQL shape**
(`normalizeQuery(query)` groups the 10 repeats into one row), with:

- p50 / min / p90 `query_duration_ms`
- p50 `read_rows`, `read_bytes`, `result_rows`, `memory_bytes`
- `profile_events_sum` — `Map(String, UInt64)` summed over all repeats
- `profile_events_avg` — per-key average over repeats

Baseline strategy: commit a `harness/bench/ch-profile-baseline.json` next
to the existing `baseline.json`, and gate optimization commits against it
the same way.

### 2. After an optimization commit: diff against the previous capture

```bash
./scripts/ch-profile-diff.sh \
  harness/artifacts/ch-profile-before.json \
  harness/artifacts/ch-profile-after.json
```

Output is a Markdown table sorted by Δp50_ms, plus per-query ProfileEvents
breakdowns for non-zero deltas. Flags:

- `--min-delta-ms N` — suppress rows whose latency barely moved.
- `--events K1,K2,...` — narrow the ProfileEvents columns to what you
  care about (default: CPU/IO counters). Pass `--events ''` for none.
- `--format json` — emit machine-readable JSON for regression gates.

**Interpretation:**
- latency moved, `SelectedRows` constant → CPU win (or noise).
- `SelectedRows` dropped → real pruning win.
- `FunctionExecute` dropped with everything else constant → the rewrite
  actually reached the executor.
- nothing moved outside noise → the rewrite is cosmetic; reject the
  commit claim.
- a query present in one capture but not the other → the shape changed
  entirely (could be a win; could mean fallback to a different strategy;
  always investigate).

### 3. On-demand deep dive: EXPLAIN for one PromQL

```bash
./scripts/ch-explain.sh 'rate(demo_cpu_usage_seconds_total[5m])' --mode instant
./scripts/ch-explain.sh 'sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))' \
  --mode range --range-seconds 600 --step 30
```

Runs the PromQL through the shim (`:29091`), then pulls the lowered SQL(s)
from `system.query_log` and dumps `EXPLAIN SYNTAX/PLAN/PIPELINE/ESTIMATE`
for each. Output: `harness/artifacts/ch-explain/<timestamp>/`.

Skip flags available if a particular EXPLAIN variant is slow or noisy:
`--skip-plan`, `--skip-pipeline`, `--skip-estimate`, `--skip-syntax`.

### 4. Commit-to-commit: diff EXPLAIN for a single PromQL

The high-value check for commits whose message claims "aliased X" or
"CSE'd Y". Runs the same PromQL against two refs in temp git worktrees,
captures EXPLAIN for each, diffs.

```bash
./scripts/ch-explain-diff.sh ca85ea6 HEAD 'avg_over_time(demo_cpu_usage_seconds_total[5m])'
```

The script prints a per-artifact diff summary and a verdict:

- `EXPLAIN SYNTAX is byte-identical` → the commit is textual only;
  ClickHouse folded the rewrite. Reject-or-revert unless the commit was
  pre-wiring for a later change.
- `EXPLAIN SYNTAX differs` → the rewrite reaches the executor. Check the
  plan/pipeline diff to see whether bytes read or processor shape changed.

Note: this script rebuilds and restarts the shim container per ref, so it
runs in the 30–180 s range on a warm docker cache. Not something to put
inside the tight loop — run it on demand when a commit's claim is in
question.

### 5. Long-range bench profiles (7d / 30d / 1y)

The default 10 m compliance fixture is fine for CPU / alias / function-call
signals, but its `SelectedRows` numbers are tiny — a commit that claims to
prune storage work won't move the needle. For scan-work signal you need
data that matches real dashboard ranges.

Three named profiles live in the same `observability.prometheus` table in
non-overlapping time windows, each with its own pinned eval-time and its
own bench corpus:

| Profile | End-time                  | Window / step | ~Samples | Seed time |
|---------|---------------------------|---------------|---------:|----------:|
| `7d`    | `2026-03-22T21:45:42Z`    | 7 d @ 15 s    |    ~5 M  |    ~5 s   |
| `30d`   | `2026-02-22T21:45:42Z`    | 30 d @ 60 s   |    ~5 M  |    ~5 s   |
| `1y`    | `2025-03-22T21:45:42Z`    | 365 d @ 300 s |   ~14 M  |   ~15 s   |

Seed a profile once (data persists in the docker volume until you
`docker volume rm`), then run the matching bench corpus:

```bash
./scripts/seed-long-range.sh --profile 7d
./scripts/run-bench.sh --long-range 7d

./scripts/seed-long-range.sh --profile 30d
./scripts/run-bench.sh --long-range 30d

./scripts/seed-long-range.sh --profile 1y
./scripts/run-bench.sh --long-range 1y --repeats 3
```

`--long-range` skips Prom readiness (Prom was bypassed — CH has its own
out-of-order semantics and accepts backdated writes directly via port
29092) and pins `--eval-time` to the profile's window. The three corpora
(`harness/corpus/bench-native-lowering-{7d,30d,1y}.json`) use range sizes
that match typical Grafana dashboard spans for that window:

- 7 d: `rate[1h..1d]`, `avg_over_time[1d]`, range queries over 1 d..7 d.
- 30 d: `rate[6h..7d]`, `avg_over_time[7d]`, range queries over 30 d.
- 1 y: `rate[1d..30d]`, `avg_over_time[30d]`, range queries over 1 y.

What each profile uncovers:

- **7 d** — baseline scan-work signal. Enough data that `SelectedRows`
  moves meaningfully, queries touch the PK range in realistic shapes.
- **30 d** — crosses the `PARTITION BY toYYYYMM` boundary (2 monthly
  partitions). Catches regressions in part pruning / planner partition
  decisions that 7 d queries fully contained in one partition would miss.
- **1 y** — 12 monthly partitions. Stresses primary-key range scans,
  codec decode across many parts, and the planner's per-part overhead.
  Use `--repeats 3 --warmup 1` to keep loop time under a few minutes.

Data is additive — seed all three once, query any of them at will.

---

## Manual steps (things not worth scripting)

### Per-query CPU sampling (`system.trace_log`)

When a structural commit needs a flame graph, enable the sampler for a
single run by appending a `SETTINGS` clause. The `ch-explain.sh`-captured
SQL can be modified and replayed:

```sql
-- Paste the captured SQL, then append:
SETTINGS
  query_profiler_real_time_period_ns = 10000000,
  query_profiler_cpu_time_period_ns  = 10000000,
  log_queries = 1,
  log_profile_events = 1
```

Then query the trace:

```sql
SELECT
    arrayMap(x -> demangle(addressToSymbol(x)), trace) AS frames,
    count() AS samples
FROM system.trace_log
WHERE event_time >= now() - INTERVAL 5 MINUTE
  AND trace_type = 'CPU'
  AND query_id = '<the query_id from system.query_log>'
GROUP BY frames
ORDER BY samples DESC
LIMIT 50
FORMAT PrettyCompactNoEscapes;
```

ClickHouse must have been started with permission to resolve symbols
(default in the shipped image). Output is a flat top-of-stack histogram;
pipe it into `flamegraph.pl` if you want the folded form.

### Trace-level query execution details

For one-off debugging, re-run the SQL with `send_logs_level='trace'`:

```bash
curl -sfS -u default:otel --data-binary @-  \
  'http://localhost:28123/?send_logs_level=trace' <<<"<the SQL>"
```

ClickHouse will stream planner/executor trace inline before the results.
Useful for seeing which index ranges were selected, what streams were
pipelined, and at what point memory tracker high-water-marks were hit.

### `system.processors_profile_log`

Per-processor timing inside the pipeline. Enabled via the existing
`system-logs-ttl.xml` config (1 h retention). Query shape:

```sql
SELECT
    processor_name,
    count(),
    sum(elapsed_us),
    sum(input_bytes),
    sum(output_bytes)
FROM system.processors_profile_log
WHERE event_time >= now() - INTERVAL 5 MINUTE
  AND query_id = '<target query_id>'
GROUP BY processor_name
ORDER BY sum(elapsed_us) DESC
```

This is the right place to look when `EXPLAIN PIPELINE` and `ProfileEvents`
disagree on where time went.

### Verifying storage-side pruning directly

When you suspect a matcher/metric-name pushdown commit, sanity-check by
running:

```sql
SELECT
    round(avg(read_rows))        AS avg_rows,
    round(avg(read_bytes))       AS avg_bytes,
    round(avg(query_duration_ms)) AS avg_ms
FROM system.query_log
WHERE event_time >= now() - INTERVAL 10 MINUTE
  AND type = 'QueryFinish'
  AND query LIKE '%<unique marker from the SQL>%'
```

before and after the commit. Storage-side work is the strongest signal
that a pushdown actually landed — much harder to confuse with noise than
wall-clock.

---

## When the signals disagree

- SQL text shortened, `EXPLAIN SYNTAX` identical, ProfileEvents identical
  → cosmetic rewrite. **This describes commits 3–5 on the native-SQL
  optimizer branch as of 2026-04-22.**
- `EXPLAIN SYNTAX` differs, `SelectedRows` identical, CPU counters identical
  → syntactic change that compiles to the same execution. Still cosmetic
  from a runtime standpoint.
- `SelectedRows` or `SelectedBytes` dropped, latency flat → disk cache masked
  the I/O win; run with `SET use_query_cache=0, max_threads=1` and repeat.
- `FunctionExecute` dropped by 2× but latency flat → the functions you
  removed weren't on the hot path. Either you optimized something cold,
  or the remaining work dominates. Useful as a correctness-of-claim check
  even when it doesn't translate to end-user latency.
- `strategy_used` flipped from `native_sql` to anything else in the
  matrix bench → the commit broke the query silently; the fallback path
  made the matrix look green. **Treat this as a hard regression.**

---

## Overhead budget reminder

- `ch-profile-capture.sh`: piggybacks on the bench's own runs — only reads
  `system.query_log` afterward. Cost is one flush + one aggregation
  SELECT, ~1–2 s. Use on every run.
- `ch-profile-diff.sh`: pure jq. <1 s.
- `ch-explain.sh`: one PromQL + four EXPLAIN queries. ~2 s per invocation.
- `ch-explain-diff.sh`: builds and restarts the shim container once per
  ref. 30–180 s; keep out of the inner loop.
- `seed-long-range.sh`: one-time per docker volume. 2–15 s depending on
  profile. Data persists until `docker volume rm`.
- `run-bench.sh --long-range {7d|30d|1y|all}`: ~90 s (7d), ~360 s (30d),
  ~120 s (1y with `--repeats 3 --warmup 1`), ~570 s combined (`all`).
  Query time only — data is already on disk. Run when you need scan-work
  signal that the 10 m fixture can't provide.

Put `ch-profile-capture.sh` in the bench path unconditionally. Reserve
`ch-explain.sh`/`ch-explain-diff.sh` for suspicious commits or claim
verification. Run `--long-range` profiles when you need scan-work or
partition-pruning signal — data is pre-seeded, so cost is query time only.
