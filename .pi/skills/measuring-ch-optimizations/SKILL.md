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
| "Fewer network roundtrips" | `X-Promshim-CH-Roundtrips` response header drops | matrix bench report |
| Any claim + `strategy_used` changed | **Hard regression signal.** Verify the claimed path still ran. | matrix bench `strategy` column |

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

All scripts live in `scripts/`. Run with `--help` for full flags.

| Script | Added cost | Use when |
|---|---:|---|
| `ch-profile-capture.sh --matrix` | ~1–2 s | Every bench run. Emits `harness/artifacts/ch-profile.json`: p50 `query_duration_ms`, `read_rows`, `read_bytes`, per-query `profile_events_sum`/`_avg` over repeats. Put it in the bench path unconditionally. **Overwritten each run** — preserve a baseline before re-capturing: `cp harness/artifacts/ch-profile.json harness/artifacts/ch-profile-<sha>.json`. |
| `ch-profile-diff.sh before.json after.json` | <1 s | After two preserved captures. Markdown table sorted by Δp50_ms plus per-query ProfileEvents deltas. Flags: `--min-delta-ms`, `--events`, `--format json`. |
| `ch-explain.sh '<promql>' --mode instant` | ~2 s | One-PromQL deep dive. Runs through shim, pulls the lowered SQL from `system.query_log`, dumps `EXPLAIN SYNTAX/PLAN/PIPELINE/ESTIMATE` to `harness/artifacts/ch-explain/<ts>/`. Skip flags available. |
| `ch-explain-diff.sh <ref-a> <ref-b> '<promql>'` | 30–180 s | Commit-to-commit verdict on a single PromQL. Builds/restarts the shim per ref; not for the inner loop. Prints "`EXPLAIN SYNTAX` is byte-identical" or "differs". |
| `seed-long-range.sh --profile {7d\|30d\|1y}` | 2–15 s | Once per `docker volume rm`. Seeds a non-overlapping window into the same `observability.prometheus` table, distinct pinned eval-time. |
| `run-bench.sh --long-range {7d\|30d\|1y\|all}` | 90 / 360 / 120 / ~570 s | Scan-work / partition-pruning signal. 7d = baseline `SelectedRows` signal, 30d = crosses `PARTITION BY toYYYYMM`, 1y = 12 partitions (use `--repeats 3 --warmup 1`). Skips Prom (backdated writes are CH-only). |

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

## Long-range profiles

Three profiles live in the same CH table in non-overlapping windows. Data is
additive and persists until `docker volume rm`.

| Profile | End-time | Window / step | ~Samples | Uncovers |
|---|---|---|---:|---|
| `7d` | `2026-03-22T21:45:42Z` | 7 d @ 15 s | ~5 M | Baseline scan-work; realistic PK-range shapes. |
| `30d` | `2026-02-22T21:45:42Z` | 30 d @ 60 s | ~5 M | Crosses `PARTITION BY toYYYYMM` (2 monthly partitions) — part-pruning regressions. |
| `1y` | `2025-03-22T21:45:42Z` | 365 d @ 300 s | ~14 M | 12 partitions — PK range scans, codec decode across many parts, planner per-part overhead. |

```bash
./scripts/seed-long-range.sh --profile 7d          # one-time per volume
./scripts/run-bench.sh       --long-range 7d       # query time only
```

## Manual fallbacks (not worth scripting)

- **`system.trace_log` flame graph** — append
  `SETTINGS query_profiler_cpu_time_period_ns=10000000, log_queries=1,
  log_profile_events=1` to captured SQL, then `SELECT arrayMap(x ->
  demangle(addressToSymbol(x)), trace) FROM system.trace_log WHERE
  query_id = '<id>'`. Pipe through `flamegraph.pl` for folded form.
- **Inline planner/executor trace** — re-run SQL with
  `?send_logs_level=trace` on the HTTP endpoint; streams index ranges,
  pipeline decisions, memory high-water-marks before results.
- **`system.processors_profile_log`** — per-processor timing inside the
  pipeline. Where to look when `EXPLAIN PIPELINE` and `ProfileEvents`
  disagree.
- **Direct pruning sanity check** — `SELECT avg(read_rows), avg(read_bytes),
  avg(query_duration_ms) FROM system.query_log WHERE query LIKE
  '%<marker>%'` before/after the commit.

## Common rationalizations

| Excuse | Reality |
|---|---|
| "The matrix bench is green, we're done." | Strategy may have silently flipped to fallback. Check `strategy_used`. |
| "p50 dropped 3%, ship it." | 3% is inside noise on the 10 m fixture. Check `EXPLAIN SYNTAX` diff and ProfileEvents. |
| "The SQL is clearly shorter, that's the win." | CH's own rewriter folds most text-level changes. Byte-identical `EXPLAIN SYNTAX` = cosmetic. |
| "`FunctionExecute` dropped 2×, ship it." | If latency is flat, you optimized a cold path. Still a correctness-of-claim signal; not a latency win. |
| "Running long-range is expensive, I'll skip it." | Data is pre-seeded; cost is query time only. 7 d is ~90 s — same budget as a single CI lint step. |
| "`ch-explain-diff.sh` takes minutes." | Reserve it for commits whose *claim* is suspect. Use `EXPLAIN SYNTAX` via `ch-explain.sh` (~2 s) for everything else. |
| "Wall-clock latency is what users see." | True — but optimization attribution requires ProfileEvents. Ship on latency; accept/reject *claims* on counters. |
| "The message says CSE; I'll just check the counters." | Check the *diff* first. If the patch doesn't actually do a CSE, no counter movement attributes to a CSE. Reject for the mismatch. |
| "I ran `ch-profile-capture.sh` once, I have my before/after." | Single capture = no before. The file is overwritten each run; `cp` the baseline under a `-<sha>.json` name before re-capturing or the diff is meaningless. |
| "My claim doesn't fit the signal table neatly." | Pick the closest row. If truly novel, state the expected signal *in the commit message* so review is decidable. |

## Red flags — stop and verify

- Accepting an "optimization" commit without running `ch-profile-diff.sh`.
- Matrix bench green + latency flat + no EXPLAIN diff checked.
- `strategy_used` change not investigated.
- Claim of "pushdown" without a `SelectedRows` or `SelectedBytes` drop.
- Long-range profile skipped for a commit that claims storage-side work.
- Commit message describes a different change than the diff (even if the
  diff looks good — the message is part of the review contract).
- Only one `ch-profile.json` on disk, claimed as before/after evidence.

**All of these mean: the commit's claim is unverified. Check the signals
before merging.**
