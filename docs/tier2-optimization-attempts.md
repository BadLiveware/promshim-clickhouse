# Tier-2 Native SQL Optimization Attempts

Compact log of the iterative optimization loops in `.agents/loops/`. Two
sequential loops, same methodology and branch (`feat/optimizing`):

- `tier2-optimization/` — attempts 001–046
- `tier2-native-optimization/` — attempts 047–079

## Methodology

- One bounded candidate per attempt; predeclared primary metric +
  secondary guardrails *before* measurement is interpreted.
- Default primary gate: normalized claim-row `FunctionExecute/query <= -8%`
  in both `prefer` and `force_supported` modes.
- Hard guardrails: strategy stays `native_sql`, ClickHouse roundtrips
  stable or lower, no unrelated native row regresses tracked counters
  by `> +10%`, `p50` neutral or better.
- Wall-clock-only deltas under ~5% are noise. `chMillis` is a transport
  signal; cannot override `p50` / ProfileEvents regressions.
- Sweeps: isolated benchmark stack via `scripts/run-sweep.sh`,
  `--corpus-set native`, `--shim-modes prefer,force_supported,off`,
  `--memory summary`. Rendered with `scripts/bench-matrix.sh`.

## Accepted optimizations (chain)

FunctionExecute/query deltas are normalized per-query, claim row, both native modes unless noted.

| # | Commit | Change | FunctionExecute/query | p50 | Notes |
|---|--------|--------|-----------------------|-----|-------|
| 028 | `b605e43` | Rate-scoped tuple-native sort (`arraySort(groupArray(...))`) for range-rate path | −11.67% / −11.63% | −1.9% / −1.4% | ChMillis −11% |
| 039 | `4c85dae` | Cancel `(x+x)/2 → x` (strict, implicit-1:1, `DropsMetric`) | −26.26% / −26.44% | −54.6% / −55.1% | |
| 040 | `56f61f0` | Direct selector-window aggregate for `rate` (after 039) | −20.65% / −20.70% on repeated-rate row; −13.59% / −13.60% on `rate_5m_range_1d` | −80.5% / −81.5% | SelectedRows −11%, SelectedBytes −11% |
| 041 | `07f2471` | Generalize to `(x+…+x)/n`, exact term-count divisors | −35.31% / −35.81% | −79.1% / −79.3% | SelectedRows −33%, ReadCompressedBytes −33% |
| 042 | `a149273` | Cancel reciprocal multiplier `(x+x+x+x)*0.25` | −39.40% / −40.00% | −88.0% / −88.1% | ChMillis −97% |
| 043 | `e412def` | Cancel unit-fraction `(x+…+x)*(1/4)`; reject `(2/8)` etc. | −39.94% / −40.01% | −88.3% / −88.7% | SelectedRows −37.5% |
| 044 | `a9adaab` | Generalize unit-fraction to non-power-of-two `(x+x+x)*(1/3)` | −35.73% / −35.53% | −77.4% / −79.4% | ReadCompressedBytes −33% |
| 064 | `c4886f4` | Replace staleness `argMax` with ASOF join; post-match stale-NaN filter preserved | −19.2% / −19.2% on `sum_by_job_range_7d` | −94.0% / −94.6% | SelectedRows −19%, ReadCompressedBytes −15% |
| 066 | `43360b4` | Correctness fix: disable direct `rate` aggregate shortcut for short windows | n/a (correctness-only) | perf regressed; recovered in 068 | Compliance clean |
| 068 | `5528994` | Re-enable direct `rate` aggregate for lookback `>= 60s` | `rate_5m_range_1d` −2.9% both modes | `rate_5m_range_1d` 537→99 ms; `sum_rate_by_job_range_7d` 1566→164 ms | Compliance clean |
| 069 | `0823203` | Instant-rate guard: `>= 60s` uses `deltaSumTimestamp` | histogram-quantile −0.5%; grouped −1.5% | histogram instant −12.3%; grouped −11.7% | `rate_1h_instant` −14% |
| 072 | `0dfcc44` | Direct histogram coalescing for instant `SUM by(…, le)` | bucket-only −0.4% / −0.7%; grouped −1.9% / −1.9% | bucket-only −14.6% / −16.5%; grouped −19.7% / −19.3% | SelectedRows stable (all wrappers still scan ~69M rows) |
| 079 | `73e39e3` | Schema-aware promoted tag column lowering (`PROM_SHIM_PROMOTED_TAG_COLUMNS`) | no fixture claim | no fixture claim | Deployment-gated; replaces `src.tags[…]` with direct column reads |

## Coverage / harness enablers (accepted, no perf claim)

| # | Commit | What |
|---|--------|------|
| 005 | `3d3eb36` | Added repeated-range corpus row to make claim measurable |
| 049 | `c59bdb0` | `repeated_sum_rate_average_by_job_range_7d` row |
| 050 | `b9218dc` | Optimizer test: aggregate repeated avg collapses via `cancel_repeated_average` |
| 052 | `9c8204a` | `histogram_quantile_by_job_1h_instant` (grouped histogram guard) |
| 053 | `2ef42cd` | Renderer test: grouped histogram preserves non-bucket labels, drops `le`/`__name__` |
| 055 | `18aad2c` | `repeated_sum_rate_average_by_job_6h_instant` (instant counterpart) |
| 057 | `499ffa5` | Optimizer tests for reciprocal/unit-fraction aggregate operands |
| 058 | `01b0397` | `repeated_sum_rate_average_by_job_mul_6h_instant` |
| 061 | `bf838db` | V2 bench artifact response-phase fields (`headerP50Ms`, `bodyDrainP50Ms`, p95) |
| 076 | `dbb2895` | Renderer test: grouped histogram quantile uses `histogram_function_child_direct_child_rows` |

## Rejected (durable anti-repeat lessons)

FunctionExecute/query is normalized per-query, claim row. Gate was `<= −8%` unless noted.

| # | Idea | FunctionExecute/query | p50 | Other signals / guardrail |
|---|------|----------------------|-----|--------------------------|
| 001 | Row-oriented range agg over identity selector | +1.06% prefer; SelectedRows ±0% | n/a | SelectedBytes −0.05%, ReadCompressedBytes −0.46% — all noise |
| 008 | Range self-reuse (normalized, gate −10%) | −7.68% / −7.34% | (not measured) | SelectedRows/Bytes −7.4% both modes |
| 010 | Range self-reuse (re-attempt, gate −8%) | −5.25% / −5.22% | (not measured) | SelectedRows/Bytes −5.2% both modes |
| 012 | Histogram Stage-1 SQL-shape rewrite | +0.01% / +0.05% | (not measured) | SelectedRows/Bytes ±0% |
| 013 | Histogram Stage-1 only-`le` extraction | −0.05% / +0.00% | (not measured) | SelectedRows/Bytes ±0% |
| 016 | Constant-folding scalar wrapper composition | −0.12% / −0.12% | (not measured) | SelectedRows ±0%, ReadCompressedBytes +0.08% / −0.13% |
| 017 | Identity `arrayMap` elision in range-agg subqueries | −0.02% / +0.02% | (not measured) | SelectedRows/Bytes ±0% |
| 018 | Histogram bucket-index no-slice (`arraySlice` elision) | +0.23% / +0.35% | (not measured) | SelectedRows/Bytes ±0% — both work and latency regressed |
| 019 | Selector-identity SUM NaN-wrapper elimination | −0.05% / +0.02% | (not measured) | SelectedRows/Bytes ±0% |
| 021 | Direct selector-window agg for `rate` (early, pre-039) | −2.62% / −2.58% | −81.2% / −81.1% | ReadCompressedBytes −0.6%/−1.1%; work below gate despite large p50 win |
| 022 | Range-rows fast-path for `rate` | −4.91% / −4.73% | +85.4% / +83.9% (regression) | ChMillis −35%/−44%; p50 regressed severely |
| 024 | Histogram dual-array sort prep | −1.03% / −0.67% | (not measured) | Guardrail: `repeated_rate_average_5m_range_1d` SelectedRows/ReadCompressedBytes >+10% |
| 026 | `(x+x)/2 → x` cancellation (first isolated try, gate −8%) | −6.47% / −6.49% | −54.6% / −55.1% | ChMillis −84%/−82%; later accepted as 039 with compounded retest |
| 030 | Terminal time-series sort lambda elision | +2.81% / +2.71% | +2.7% / −2.1% | ChMillis +3.5% / −1.1% |
| 032 | Selector-window rate-kernel compaction | +5.04% / +4.94% | (not measured) | ChMillis −1.2% / −2.3% — work regressed despite shorter SQL |
| 034 | Renderer `x + x → x*2` collapse | +0.42% / +0.32% | −53.9% / −53.7% | ChMillis −78%/−80%; work counter regressed despite large p50/CH-ms wins |
| 036 | Range scalar-transform composition / cancellation | +2.47% / +2.33% | worse both modes | ChMillis +93/+88 ms worse |
| 038 | Range subtree dedup → one-source self-join | +1.78% / +1.20% | −51.1% / −50.7% | ChMillis −52%; guardrail: `rate_5m_range_1d` FunctionExecute +11.9%/+11.5% |
| 045 | Direct rows-based range selector agg `sum by(job)(...)` | +0.61% / +0.48% | −6.0% / −7.6% | ReadCompressedBytes +0.91%/−0.12%; work regressed slightly |
| 046 | Direct-rate full-tag carry (avoid tag rejoin) | +36.27% / +36.44% | +788% / +813% | Guardrails: `rate_5m_range_1d` +14.6%, repeated-rate rows +17.6% |
| 047 | Project grouped range-rate child labels to `job` | +11.79% / +11.72% | −2.3% / +6.1% | SelectedBytes ±0%; work regressed |
| 048 | Scout: 047 + tag carry | +4.09% / +4.00% | +387% / +427% | Guardrails: multiple rows >+18% FunctionExecute |
| 075 | Guarded non-overlap range-rate bucketization | −2.0% to −3.6% | mixed ±1.2% (noise) | ChMillis flat; storage boundary risk vs. weak signal |

## Deferred / split (no code change, recorded for future selection)

| # | Topic | Why deferred |
|---|-------|--------------|
| 002 | Repeated-rate expression simplification | Lowered SQL already uses native self-reuse |
| 003 | Histogram quantile outlier | Needs dedicated pipeline plan + semantics validation |
| 004 | Range self-reuse fast path | Corpus lacked claim-specific row (became 005) |
| 006/007/009 | Methodology: queryCount drift, normalization, threshold calibration | Fixed comparison protocol, no code |
| 011 | Histogram quantile Stage-1 plan | Captured explain; bounded plan written |
| 014/015 | Constant folding | No corpus coverage (added in 015), candidate later rejected at 016 |
| 020/023/025/027/029/031/033/035/037 | Mechanism-selection planning steps | Each captured fresh explain, picked next family |
| 051 | Histogram quantile (post-049) | High-complexity pipeline; no safe bounded change |
| 054/056/059 | Repeated-avg / grouped-histogram baselines | Match existing accepted shape; no follow-up |
| 060 | `sum_by_job_range_7d` | Same shape as 045 reject; needs new mechanism |
| 062 | Response-phase profiling | Top high-p50 rows are pre-header dominated; body drain not the bottleneck |
| 063 | ASOF join scout (CH-side) | Materially different from 045; promising — became 064 |
| 065 | Post-ASOF compliance | Surfaced `rate(...[15s])` diff; led to 066 fix |
| 067 | Post-rate-fix baseline | Documented perf regression to recover via 068 |
| 070/071 | Histogram stage profiling | Wrappers all scan ~69M rows; need scan reduction or interpolation rewrite |
| 073/074 | Range-rate residual scout | Same guarded direct-rate SQL; only Attempt 046 family had room and is anti-repeat |
| 077/078 | Plateau reflection | No safe bounded follow-up without fresh diagnosis |

## Avoid list (durable)

- Exact retries on the same baseline of: 045, 046, 047, 048.
- Broad algebra rewrites without semantic proof: `x+0`, `x*1`, `(x*k)/k`,
  `x-x`, `x/x`.
- Alias-only / wrapper-only / lambda-only / SQL-prettification claims
  without executor-visible evidence.
- Carrying full tags into high-cardinality window grouping.
- Tier 3/4 routing optimization as a primary target before tier-2 is improved.
- Expanding `harness/compliance/expected-failures.json` for shim bugs
  or missing features.

## Active hypotheses for future attempts

1. Narrow repeated-expression normalization may still unlock physical-path
   wins; keep `DropsMetric` / implicit-matching / term-count guards strict.
2. Profiling-guided IR/logical rewrites preferred over renderer/storage
   surgery for the same hot component.
3. Direct CH aggregate/window primitives win when they remove arrays,
   joins, groups, or repeated range windows — not merely when SQL is shorter.
4. Compound scout mode (2–4 related rejected ideas) acceptable with a
   concrete synergy hypothesis; ablate before committing.

## Where to look

- Per-attempt detail: `.agents/loops/<loop>/attempts/NNN-*.md`
- Sweep artifacts: `harness/artifacts/bench/sweeps/<run-name>/`
- Explain artifacts: `harness/artifacts/explain/<run-name>/` and `harness/artifacts/explain-diff/<run-name>/`
- Canonical loop protocol/guardrails: `.agents/loops/tier2-native-optimization/loop.md`
