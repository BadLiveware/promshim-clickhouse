---
name: measuring-ch-optimizations
description: Use when evaluating a ClickHouse / native-SQL lowering commit that claims a rewrite landed, when wall-clock bench deltas sit in noise (<5%), when verifying a pushdown / CSE / alias claim, or when a matrix bench looks green but the underlying SQL changed.
type: technique
---

# Measuring ClickHouse query optimizations beyond wall-clock

## Overview

Wall-clock p50 from the matrix bench is a noisy signal. A 2–3% fixture-level
delta sits inside run-to-run variance, so commits that only *shuffle SQL text*
look identical to commits that actually *do less work*. ClickHouse exposes
sharper, lower-variance signals for free via `EXPLAIN` and
`system.query_log.ProfileEvents`. **Always check those before accepting an
optimization claim.**

## When to use

- A commit message claims "aliased X", "CSE'd Y", "pushed Z down", "reduced
  arrayMap sites" — verify it actually reached the executor.
- Matrix bench delta is inside noise but the author insists it's a win.
- `strategy_used` flipped in the matrix bench (silent fallback = hard
  regression; investigate before merging).
- A pushdown is expected to reduce scan work — confirm via storage counters,
  not latency.

**Don't reach for this when:** you're exploring, the claim is already
confirmed by a `SelectedRows` drop visible in the matrix, or the commit is
explicit plumbing/wiring with no runtime claim.

## First: verify the claim matches the diff

Before running any measurement tool, diff the commit message against
`git show <ref>` or `git diff`. A mismatched message — "CSE'd X" while the
patch actually adds a row-source fast path — is itself reject-or-rewrite
signal. No counter or EXPLAIN output can rescue a commit whose claim
doesn't describe the code. Only proceed to the signals below once the
claim is coherent with the patch.

## Native SQL builder evolution rule

When a ClickHouse optimization adds or changes a native SQL physical shape, keep
`internal/promshim/native/sqlb/` moving toward a typed ClickHouse SQL subset
without doing a big-bang renderer rewrite.

- Put semantic and physical choices in plan structs first: strategy, predicate
  placement, stale-marker placement, matched-series distinctness, join shape,
  aggregation shape, and settings assumptions.
- Add typed `sqlb` expressions/predicates/sources/ClickHouse helpers exactly as
  the new shape needs them. Do not build a general SQL parser or model unused
  ClickHouse syntax.
- Use raw SQL only as an explicit escape hatch for legacy or unsupported syntax;
  new optimization logic should prefer typed nodes so future rewrites can reuse
  it.
- Migrate path-by-path when optimization work touches a query family. Do not
  churn unrelated renderer paths just to reduce raw SQL counts.
- Treat renderer golden churn as a behavior signal: no churn for a pure builder
  migration, intentional churn only for a physical-shape change backed by
  correctness and ProfileEvents/benchmark evidence.

## Claim → required signal

Match the commit's *claim* to the signal that must move. If the claimed
signal doesn't move, the rewrite didn't do what the message says — even
if latency dropped for an unrelated reason.

| Claim shape | Signal that must move | How to read |
|---|---|---|
| "CSE'd X", "deduped Y", "killed arrayMap" | `FunctionExecute`, `ArrayMap` counter drops in `profile_events_sum` | `ch-profile-diff.sh` |
| "Aliased X", "shortened SQL" | `EXPLAIN SYNTAX` must still *differ* to be non-cosmetic | `ch-explain-diff.sh` — byte-identical = reject |
| "Pushed X down", "__name__ first filter", "matcher canonicalized" | `SelectedRows` / `SelectedBytes` drops | `ch-profile-diff.sh`; `EXPLAIN PLAN indexes=1` |
| "Fused A+B", "eliminated ARRAY JOIN", "row-source fast path" | `EXPLAIN PIPELINE` shows fewer stages; specific operator (e.g. `ArrayJoin`) absent in `EXPLAIN SYNTAX` | `ch-explain.sh` |
| "Skipped stale NaNs", "pruned series" | `SelectedRows` drops; `result_rows` unchanged | `ch-profile-diff.sh` |
| "Reduced memory" | `MemoryTrackerUsage` drops | `ch-profile-diff.sh` |
| "Bounded runaway CPU", "capped threads", "reduced noisy-neighbor risk" | `UserTimeMicroseconds` / `RealTimeMicroseconds` or thread-time drops without unacceptable p50/p95 regression | `ch-explain.sh` query-log summary/settings plus targeted max_threads comparison |
| "Fewer network roundtrips" | `X-Promshim-CH-Roundtrips` response header drops | matrix bench report |
| Any claim + `strategy_used` changed | **Hard regression signal.** Verify the claimed path still ran. | matrix bench `strategy` column |

## Thread caps are shape-specific evidence questions

Do not set a global `max_threads=4` default just because one query wastes CPU.
When profiling detailed native SQL shapes, explicitly compare the default,
`max_threads=8`, and `max_threads=4` when CPU runaway or noisy-neighbor risk is
part of the concern. Accept a cap only for shapes where ProfileEvents show a
meaningful CPU/RealTime/thread-time reduction without an unacceptable p50/p95
latency regression. Record both sides of the trade-off in the commit or PR.

Known caution: on profile-50k data, capping current windowed range-function
paths at 4 threads preserved several row timings but regressed the newly
row-oriented `subquery_rate_over_aggregate_5m_range_1d` path from about
3032 ms to about 3724 ms, moving it from parity to slower than Prometheus. Treat
that as a counterexample to blanket 4-thread caps; use typed execution
preferences only after shape-specific evidence.

## The signals, ranked

1. **`EXPLAIN SYNTAX`** — CH's own rewriter runs before the planner and
   already does CSE, constant folding, unnesting, alias resolution. **If two
   commits produce byte-identical `EXPLAIN SYNTAX`, the rewrite is cosmetic:
   the executor sees the same thing.** Highest-value check; ~2 s.
2. **`ProfileEvents` (system.query_log)** — `Map(String, UInt64)` attached to
   every finished query. Key counters:
   - `SelectedRows` / `SelectedBytes` / `ReadCompressedBytes` — storage-side
     work. Moves only on real pruning (matcher canonicalization, `__name__`
     first-filter, stale-NaN pushdown).
   - `FunctionExecute` — total function invocations. Drops when a rewrite
     actually kills executor work.
   - `RealTimeMicroseconds`, `UserTimeMicroseconds` — CPU. Far lower
     variance than wall-clock.
   - `ArraySort`, `ArrayFilter`, `ArrayMap`-family.
   - `NetworkSendBytes`, `OSReadChars`, `MemoryTrackerUsage`.
3. **`EXPLAIN PLAN indexes=1, actions=1, optimize=1`** — distinguishes
   "shorter SQL, same bytes read" (no-op) from "shorter SQL, fewer bytes
   read" (real win).
4. **`EXPLAIN PIPELINE json=1`** — processor graph. Isomorphic pipelines run
   identically.
5. **`EXPLAIN ESTIMATE`** — cheap per-source rows/bytes/parts estimate;
   flags pruning changes.
6. **`system.trace_log`** — CPU-sampled stacks (set
   `query_profiler_cpu_time_period_ns=10000000`). For structural commits
   where you need to see where CH spends time.

## Scripted workflows

Prefer `./scripts/run-sweep.sh` for benchmark/compliance sweeps and
cross-axis comparisons. It uses isolated benchmark ports/volumes so long-range
or higher-cardinality active-series data does not contaminate the compliance
fixture. Reach for lower-level scripts only for focused debugging, one-off
captures, or cases where `run-sweep.sh` cannot express the experiment.

All scripts live in `scripts/`. Run each script with `--help` for exact flags
and benchmark-stack examples instead of copying long invocations from this
skill.

### Single-query workbench: use `ch-explain.sh` first

For one-query investigation, prefer `./scripts/ch-explain.sh` over ad-hoc
`curl` plus `system.query_log` snippets. It handles both PromQL and concrete
ClickHouse SQL (`--sql`) and writes one artifact bundle with shim explain,
query-log rows, settings, ProfileEvents, clean SQL, and EXPLAIN variants.

Run `./scripts/ch-explain.sh --help` for full examples. After a capture, inspect
only the highest-signal files first:

- `README.md` — inline summary and artifact index.
- `query-log-summary.tsv` — duration, read rows/bytes, memory, `max_threads`,
  CPU time, `FunctionExecute`, and join row counters.
- `promshim-explain-summary.tsv` — routing/strategy/settings-profile summary
  for PromQL inputs.
- `qN/settings.tsv` and `qN/profile-events-top.tsv` — effective settings and
  the largest ProfileEvents.
- `qN/query-clean.sql` — SQL used for EXPLAIN/diffing.

If `ch-explain.sh` lacks a reusable field needed for query diagnosis, add it to
the script's artifact output instead of reintroducing one-off query-log snippets.

| Script | Use when |
|---|---|
| `run-sweep.sh` | Primary benchmark/compliance workflow and active-series/profile setup. Use named runs; artifacts live under `harness/artifacts/bench/sweeps/<run>/`. Run `--dry-run --estimate` before profile-50k/profile-500k, multi-profile, or broad corpus runs. |
| `ch-explain.sh` | First stop for a single-query deep dive. Supports PromQL and `--sql`; artifacts default to `harness/artifacts/explain/<ts>/`. |
| `run-bench.sh` | Focused benchmark-only run when a full sweep is unnecessary. Use explicit endpoints and named `--artifact-dir`; add `--clickhouse-profile summary|auto|processors` when batch ProfileEvents/processors matter. |
| `ch-profile-capture.sh` / `ch-profile-diff.sh` | Legacy/manual before-after ProfileEvents comparison. Preserve both sides under unique paths before diffing. |
| `ch-explain-diff.sh` | Commit-to-commit `EXPLAIN SYNTAX` verdict for a single PromQL when a claim is suspect. |
| `seed-long-range.sh` / `run-bench.sh --long-range` | Low-level debugging or unusual manual setups only; normal long-range setup/comparison should go through `run-sweep.sh`. |
| `bench-matrix.sh` | Render matrices from `harness/artifacts/bench/sweeps/<run>/manifest.json`; use `--per-query` for query-level rows. |

## Serialize measurement runs via the named-lock library

Two concurrent stack-using runs silently corrupt each other: they share
the compliance stack (`:29091`, `:28123`, `:29090`), the
`system.query_log` time-window (normalizeQuery aggregation merges both
runs' rows), and `harness/artifacts/*.json` (last writer wins).
`scripts/lib/run-lock.sh` enforces serialization at the shell so you
don't have to remember which tools conflict.

Locks are keyed by name:

- **`stack`** — exclusive access to stack-backed query windows. Taken by
  `run-bench.sh`, `run-compliance.sh`, `seed-long-range.sh`,
  `ch-explain.sh`, `ch-explain-diff.sh`, and `ch-profile-capture.sh`.
  Two runs with the same name refuse to race; the second exits 3 and
  points at the holder's pid/script.
- **`harness`** — orchestrator-level, held only by `run-harness.sh` so
  two full harness runs can't interleave. Inner phases take `stack`
  only while they're running, so external stack-users can grab the
  stack between phases.

Inheritance is per-name via `CHO_RUN_LOCK_HELD_<NAME>=1`, so nested scripted
calls compose without deadlock.

**What the lock does not cover:**

- Direct interactive work against the stack (`curl :29091/...`,
  `curl :29191/...`, `docker exec`, ad-hoc ClickHouse queries). You are the serializer.
- File-system races on `harness/artifacts/` from hand-written tools
  not routed through the scripts. `run-sweep.sh` also takes named locks for
  sweep/benchmark-stack operations, but manual interactive access can still
  contaminate query-log windows.
- Wall-clock noise from a warm-up pass or a busy host.

**If an artifact looks wrong, discard it.** Mixed artifacts have been
repeatedly mistaken for real signal. Check for stale locks
(`ls /tmp/ch-observability-*.lock`) and re-run from a quiet stack
rather than trying to subtract the noise.

## Reading the signals

| Signal shape | Verdict |
|---|---|
| Latency moved, `SelectedRows` constant | CPU win or noise — check `FunctionExecute` and CPU microseconds. |
| `SelectedRows` / `SelectedBytes` dropped | Real pruning win. |
| `FunctionExecute` dropped, everything else constant | Rewrite reached the executor; may be cold path if latency flat. |
| SQL text shorter, `EXPLAIN SYNTAX` identical, ProfileEvents identical | **Cosmetic. Reject the claim.** |
| `EXPLAIN SYNTAX` differs, storage + CPU counters identical | Syntactic change, same execution. Still cosmetic from runtime standpoint. |
| `SelectedRows` dropped, latency flat | Disk cache masking the I/O win. Re-run with `SET use_query_cache=0, max_threads=1`. |
| `strategy_used` flipped (`native_sql` → fallback) | **Hard regression.** Matrix-bench green hides silent breakage. |
| Query present in one capture, not the other | Shape changed entirely. Investigate — could be win, could be wrong-strategy fallback. |

## Long-range profiles and active-series targets

Long-range profiles (`7d`, `30d`, `1y`) live in non-overlapping time windows.
Sample volume scales with the active-series target; do not quote fixed sample
counts without naming the preset or target.

Use explicit active-series selectors in new commands:

- `--active-series-preset fast` — default fast target, about 5k active series.
- `--active-series-preset profile-50k` — profiling target, about 50k active series.
- `--active-series-preset profile-500k` — stress profiling target, about 500k active series.
- `--active-series N` — custom target.
- `--density ...` — deprecated compatibility alias only; avoid in new guidance.

Use `run-sweep.sh --dry-run --estimate` before broad, multi-profile, or higher
active-series runs. For exact setup/benchmark flags, run `./scripts/run-sweep.sh
--help`; keep low-level `seed-long-range.sh` / `run-bench.sh --long-range` for
script debugging or unusual manual setups.

## Manual fallbacks (not worth scripting)

- **`system.trace_log` flame graph** — append
  `SETTINGS query_profiler_cpu_time_period_ns=10000000, log_queries=1,
  log_profile_events=1` to captured SQL, then `SELECT arrayMap(x ->
  demangle(addressToSymbol(x)), trace) FROM system.trace_log WHERE
  query_id = '<id>'`. Pipe through `flamegraph.pl` for folded form.
- **Inline planner/executor trace** — re-run SQL with
  `?send_logs_level=trace` on the HTTP endpoint; streams index ranges,
  pipeline decisions, memory high-water-marks before results. If useful more
  than once, add it to `ch-explain.sh` instead of keeping a one-off command.
- **`system.processors_profile_log`** — per-processor timing inside the
  pipeline. Where to look when `EXPLAIN PIPELINE` and `ProfileEvents`
  disagree.
- **Direct pruning sanity check** — prefer `ch-explain.sh` or preserved
  benchmark profile artifacts. If you must query `system.query_log` directly,
  keep it a throwaway hypothesis check and move reusable fields into
  `ch-explain.sh`.

## Common rationalizations

| Excuse | Reality |
|---|---|
| "The matrix bench is green, we're done." | Strategy may have silently flipped to fallback. Check `strategy_used`. |
| "p50 dropped 3%, ship it." | 3% is inside noise on the 10 m fixture. Check `EXPLAIN SYNTAX` diff and ProfileEvents. |
| "The SQL is clearly shorter, that's the win." | CH's own rewriter folds most text-level changes. Byte-identical `EXPLAIN SYNTAX` = cosmetic. |
| "`FunctionExecute` dropped 2×, ship it." | If latency is flat, you optimized a cold path. Still a correctness-of-claim signal; not a latency win. |
| "Running long-range is expensive, I'll skip it." | Use `run-sweep.sh --dry-run --estimate`, choose the smallest active-series/corpus that tests the claim, and record the gap if a broader profile is deferred. |
| "`ch-explain-diff.sh` takes minutes." | Reserve it for commits whose *claim* is suspect. Use `ch-explain.sh` (~2 s + query time) for single-query SQL/settings/ProfileEvents/EXPLAIN evidence first. |
| "Wall-clock latency is what users see." | True — but optimization attribution requires ProfileEvents. Ship on latency; accept/reject *claims* on counters. |
| "The message says CSE; I'll just check the counters." | Check the *diff* first. If the patch doesn't actually do a CSE, no counter movement attributes to a CSE. Reject for the mismatch. |
| "I ran `ch-profile-capture.sh` once, I have my before/after." | Single capture = no before. The file is overwritten each run; `cp` the baseline under a `-<sha>.json` name before re-capturing or the diff is meaningless. |
| "My claim doesn't fit the signal table neatly." | Pick the closest row. If truly novel, state the expected signal *in the commit message* so review is decidable. |
| "The stack lock is in the way; I'll `rm` it to unblock." | The lock is held for a reason. Check `ls /tmp/ch-observability-*.lock` and the pid in the file; only remove if that process is actually gone. |
| "I'll poke the stack via curl/docker while a bench runs." | The named lock doesn't guard interactive access. You're adding rows to the same `query_log` window the bench is aggregating. Wait. |
| "The artifact looks a bit weird but I can work around it." | Don't. Mixed artifacts have been repeatedly mistaken for real signal. Discard and re-run from a quiet stack. |

## Red flags — stop and verify

- Accepting an "optimization" commit without running `ch-profile-diff.sh`.
- Matrix bench green + latency flat + no EXPLAIN diff checked.
- `strategy_used` change not investigated.
- Claim of "pushdown" without a `SelectedRows` or `SelectedBytes` drop.
- Long-range or higher-active-series profile skipped for a commit that claims storage-side work.
- Running long-range or higher-active-series benchmarks against compliance ports instead of the
  isolated benchmark stack.
- Commit message describes a different change than the diff (even if the
  diff looks good — the message is part of the review contract).
- Only one `ch-profile.json` on disk, claimed as before/after evidence.
- Interactive `curl`/`docker exec` against the stack while a bench or
  capture is running — the named lock only guards scripts, not ad-hoc
  probes, and the probes still land in the same `query_log` window.
- Capture artifact with a surprising number of normalized queries (e.g.
  ~25 expected, 200+ actual) — stack was not quiet. Discard and re-run.

**All of these mean: the commit's claim is unverified. Check the signals
before merging.**
