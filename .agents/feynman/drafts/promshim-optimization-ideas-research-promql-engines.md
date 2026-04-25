# Promshim optimization ideas: PromQL engine research notes

Task: Research Prometheus-compatible engine execution ideas that could inspire testable `promshim` candidates.

Status: done (candidate ideas, not recommendations).

## Evidence table

| # | Source | URL | Key claim | Type | Confidence |
|---|--------|-----|-----------|------|------------|
| 1 | Prometheus docs — Querying basics | https://prometheus.io/docs/prometheus/latest/querying/basics/ | PromQL range queries are equivalent to instant queries evaluated at equally spaced steps; instant selectors use newest sample within lookback; range vector windows are left-open/right-closed; staleness hides marked-stale series. | primary | high |
| 2 | Prometheus docs — Operators | https://prometheus.io/docs/prometheus/latest/querying/operators/ | Binary/vector matching, aggregation, IEEE-754/NaN handling, histogram sample behavior, metric-name dropping, and range-query ordering caveats constrain safe rewrites. | primary | high |
| 3 | Prometheus docs — Functions | https://prometheus.io/docs/prometheus/latest/querying/functions/ | Range functions have detailed semantics for extrapolation, counter resets, histograms, NaN/Inf, and ordering of rate/irate before aggregation. | primary | high |
| 4 | Prometheus docs — HTTP API | https://prometheus.io/docs/prometheus/latest/querying/api/ | Query APIs expose `lookback_delta`, warnings/infos, range query matrix responses, quoted special floats, and response ordering rules. | primary | high |
| 5 | promshim local range-function executor | file://./internal/promshim/local/planner_rangefunc.go | `executeRangeVectorPlan` iterates every step, runs the instant evaluator at that timestamp, and assembles a matrix by labelset. | primary / local source | high |
| 6 | promshim range chunking | file://./internal/promshim/local/range_strategy.go and file://./internal/promshim/local/chunking.go | promshim already chunks selected local range plans by points-per-series and merges matrices in timestamp order. | primary / local source | high |
| 7 | promshim selector estimates cache | file://./internal/promshim/selector_estimates.go | promshim already has a TTL cache for selector estimates keyed by matchers, time bounds, lookback, and offset. | primary / local source | high |
| 8 | Grafana Mimir docs — query-frontend | https://grafana.com/docs/mimir/latest/references/architecture/components/query-frontend/ | Mimir query-frontend splits long range queries, caches results, runs partial queries in parallel, and notes step alignment can violate PromQL conformance. | primary / vendor docs | high |
| 9 | Grafana Mimir docs — query sharding | https://grafana.com/docs/mimir/latest/references/architecture/query-sharding/ | Mimir shards suitable query subtrees across queriers; associative aggregations are shardable while functions such as `absent`, `histogram_quantile`, and `sort` are not; cardinality estimates can reduce shard counts. | primary / vendor docs | high |
| 10 | Grafana Labs blog — Mimir query performance | https://grafana.com/blog/2022/07/20/how-we-improved-grafana-mimir-query-performance-by-up-to-10x/ | Mimir reuses Prometheus's single-threaded PromQL engine for compatibility and uses time splitting plus query sharding to execute one query across cores/machines; reported up to 10x reductions are vendor self-reported. | self-reported | medium |
| 11 | Thanos docs — query-frontend | https://thanos.io/v0.40/components/query-frontend.md/ | Thanos query-frontend is stateless/horizontally scalable, splits range queries, caches results, can align ranges to step, excludes partial/warning responses from caching, and has max-freshness controls. | primary / vendor docs | high |
| 12 | Cortex docs — architecture/query-frontend | https://cortexmetrics.io/docs/architecture/#query-frontend | Cortex query-frontend queues, fairly schedules, splits multi-day queries, caches incomplete results by issuing missing subqueries, and can accelerate any Prometheus-API-compatible downstream. | primary / vendor docs | high |
| 13 | VictoriaMetrics docs — stream aggregation | https://docs.victoriametrics.com/victoriametrics/stream-aggregation/ | VictoriaMetrics can aggregate samples before storage to reduce query cost/series/samples, but stream aggregation ignores input timestamps and keeps state in process memory. | primary / vendor docs | high |
| 14 | VictoriaMetrics docs — MetricsQL | https://docs.victoriametrics.com/victoriametrics/metricsql/ | MetricsQL intentionally differs from PromQL for rollups/rate/increase, removes NaNs from output, auto-selects some lookbehind windows, and auto-wraps selectors in `default_rollup`. | primary / vendor docs | high |
| 15 | VictoriaMetrics MetricsQL optimizer code | https://github.com/VictoriaMetrics/metricsql/blob/master/optimizer.go | MetricsQL optimizer pushes common label filters through binary expressions into metric selectors when group/join modifiers make it safe. | primary code | high |
| 16 | VictoriaMetrics rollup result cache code | https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/app/vmselect/promql/rollup_result_cache.go | VictoriaMetrics rollup cache keys by expression/window/step/tag filters, stores compressed rollup series, tracks full/partial hits, skips recent data by cache timestamp offset, and supports reset/persistence. | primary code | high |
| 17 | VictoriaMetrics issue #10098 | https://github.com/VictoriaMetrics/VictoriaMetrics/issues/10098 | A reported instant rollup optimization changed `rate` results when rewriting through cached `increase`, showing rollup rewrites can be semantically unsafe. | primary issue report | medium |
| 18 | VictoriaMetrics issue #9762 | https://github.com/VictoriaMetrics/VictoriaMetrics/issues/9762 | A reported rollup-cache optimization applied `offset` twice for some rewritten rollups, showing modifier handling is a correctness risk. | primary issue report | medium |
| 19 | PromSketch paper — arXiv HTML | https://arxiv.org/html/2505.10560v1 | PromSketch identifies repeated scans and repeated computation over overlapping rule windows, proposes approximate intermediate-result caches, and reports large latency/cost reductions with bounded average error. | academic preprint | medium |
| 20 | PromSketch repository | https://github.com/Froot-NetSys/promsketch | PromSketch repository describes a Go implementation using exponential histograms and sketches as intermediate caches for Prometheus/VictoriaMetrics integrations. | primary code / self-reported | medium |
| 21 | promshim README | file://./README.md | promshim is documented as a PromQL compatibility layer exposing Prometheus query APIs over ClickHouse TimeSeries storage, with tiered execution and a compatibility-testing goal. | primary / local source | high |

## Findings and candidate ideas

### 1) Exact range-result splitting and cache, with a no-cache freshness tail

- **Source-backed note:** Prometheus defines a range query as repeated instant-query evaluation at equally spaced timestamps, so exact time splitting is semantically natural if split boundaries preserve the original step grid and merge order [1]. Mimir, Thanos, and Cortex all use query frontends that split long range queries, execute partial queries in parallel, and cache partial results [8][11][12]. Mimir explicitly warns that aligning queries to step can violate PromQL conformance, so promshim should avoid mutating caller start/end/step unless a compatibility-off mode is explicit [8]. Thanos excludes partial-response and warning-bearing results from caching and has max-freshness controls, which is a useful guardrail for recently changing data [11].
- **promshim layer candidate:** HTTP/query service range path plus local/native evaluator result rendering. This is closest to a small in-process query-frontend in front of the existing planner/evaluator [5][6].
- **Expected proof signal:** Repeated 7d/30d/1y dashboard-range queries show lower p50/p95 latency, fewer ClickHouse round trips, and unchanged differential results against the existing strict path [4][5].
- **Correctness risks:** Start/end inclusivity, step-grid preservation, `lookback_delta`, stale series, special-float JSON strings, matrix series ordering, warnings/infos, and native histogram response shape all need exact handling [1][4]. Caching recent windows can return stale answers if data arrives late, so a freshness tail like Thanos/VictoriaMetrics use should be considered [11][16].
- **First experiment shape:** Add an opt-in in-memory range-result cache keyed by query string or normalized AST, start, end, step, lookback_delta, native-lowering mode, table/database, ClickHouse version, and feature flags. Split only on exact step boundaries; never align user times. Cache only chunks ending before `now - freshness_tail`. Exclude responses with errors, warnings, infos, or partial fallback. Validate through `promshim-promql-compliance`, dashboard corpus replay, and a repeated-query benchmark.

### 2) Rolling-window reuse inside local range-function execution

- **Source-backed note:** promshim's local range-function path currently loops from `start` to `end`, evaluates the same expression at each step, and appends each instant vector into a matrix [5]. PromSketch identifies repeated data scans and repeated computation over overlapping windows as bottlenecks for periodic range/rule queries [19]. Prometheus range-vector selection uses a left-open/right-closed interval, so an incremental window implementation must evict/include samples exactly at the correct boundaries [1].
- **promshim layer candidate:** `internal/promshim/local` range-function execution and storage selector layer, initially for exact float-only rollups such as `count_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` [3][5].
- **Expected proof signal:** For dense long-range rollup queries, one scan per series plus incremental aggregation should reduce ClickHouse rows re-read per step and reduce CPU time while matching the current evaluator exactly [3][5].
- **Correctness risks:** `rate`, `increase`, `delta`, `deriv`, `quantile_over_time`, `changes`, histograms, mixed float/histogram ranges, stale markers, `offset`, `@`, and subquery grids have extra semantics and should not be included in the first exact rolling implementation [1][3][17][18].
- **First experiment shape:** Implement a disabled-by-default `rolling_range_rollups` path for `count/sum/min/max/avg_over_time(metric[window])` with plain selectors only. Build golden tests around boundary samples at `t-window`, stale series, NaN, Inf, and sparse samples. Compare against the existing per-step executor and reference Prometheus before benchmarking.

### 3) Rollup-result cache for safe exact subexpressions, not algebraic rewrites first

- **Source-backed note:** VictoriaMetrics has a rollup result cache keyed by expression, window, step, and tag filters, with compressed entries, full/partial hit accounting, persistence/reset support, and a policy to skip too-recent data [16]. Reported VictoriaMetrics issues show that rewriting `rate` through `increase` and mishandling `offset` can produce wrong answers, so a promshim cache should initially cache exact evaluated subexpressions rather than applying algebraic transformations [17][18].
- **promshim layer candidate:** Logical/native plan memoization for evaluated rollup subtrees, separate from final HTTP response caching [5][16].
- **Expected proof signal:** Repeated queries with identical heavy rollup subtrees should show cache-hit metrics and latency reduction even when final queries differ above the rollup subtree [16].
- **Correctness risks:** Cache keys must include `offset`, `@`, start/end/step, lookback, selector matchers, table/database, native-lowering mode, and any feature that changes evaluation [1][4][18]. Caching `NaN`/`Inf` and empty vectors must match Prometheus JSON behavior and staleness behavior [1][4].
- **First experiment shape:** Cache only full exact results of a narrow whitelist: `sum_over_time`, `count_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` over plain selectors with no `offset`, no `@`, no subquery, and no histograms. Add metrics for hit/miss/skip reasons. Keep the current evaluator as oracle through shadow comparison.

### 4) Safe label-filter pushdown through binary expressions

- **Source-backed note:** MetricsQL's optimizer pushes common label filters into metric selectors across binary expressions, with explicit handling for `on`, `ignoring`, `group_left`, `group_right`, set operators, aggregations, and label-manipulation functions [15]. Prometheus binary operators and vector matching require exact one-to-one or many-to-one semantics, and unmatched vector elements are dropped for arithmetic/comparison operations [2].
- **promshim layer candidate:** Logical planner / native SQL lowering, before ClickHouse selector SQL is built [2][15].
- **Expected proof signal:** Binary queries with selective labels should scan fewer series/rows in ClickHouse and produce byte-identical vectors/matrices to the unoptimized plan [2][15].
- **Correctness risks:** `or`, `unless`, `absent`, `absent_over_time`, `bool`, many-to-one matching, metric-name dropping, histogram operands, and label-rewriting functions can change whether a pushed filter is legal [2][15].
- **First experiment shape:** Add an explain-only optimizer that annotates candidate pushed filters for arithmetic one-to-one binary expressions with identical common equality matchers. Then enable for a tiny whitelist and differential-test against the existing planner.

### 5) Selector-hash sharding for associative aggregation subtrees

- **Source-backed note:** Mimir shards suitable query portions by splitting series into hash shards and merging partial results; it documents associative aggregations such as `sum`, `min`, `max`, `count`, and `avg` as shardable while functions such as `absent`, `histogram_quantile`, `sort`, and `sort_desc` are not shardable [9]. The Mimir blog explains that this keeps Prometheus engine compatibility while parallelizing a query across cores and machines, with performance gains reported by Grafana Labs [10].
- **promshim layer candidate:** Native SQL lowering / storage selector layer, where ClickHouse can evaluate hash predicates over series identity labels and merge associative partials [9].
- **Expected proof signal:** High-cardinality `sum/count/min/max/avg by (...)` queries show lower wall-clock latency at modest shard counts without increasing total rows read too much [9][10].
- **Correctness risks:** Series hash partitioning must be deterministic and complete, `avg` needs sum/count merge semantics, output label ordering must remain stable, and non-associative functions must be excluded [2][9]. Sharding can increase backend load and query count, so it needs bounds [9].
- **First experiment shape:** Implement opt-in `N`-way hash sharding for `sum by (...) (selector)` and `count by (...) (selector)` only. Use a ClickHouse hash over a canonical labelset string. Merge partial vectors locally. Sweep `N in {2,4,8}` on high-cardinality fixtures and compare strict results.

### 6) Cost/cardinality-aware parallelism and cache admission using existing selector estimates

- **Source-backed note:** Mimir can use cached cardinality observations to reduce query shard counts, and documents that estimates are used only to reduce shard count, not raise it beyond configured bounds [9]. promshim already has a TTL selector-stats cache keyed by matchers, start/end, lookback, and offset, and uses it to fill cost-class estimates [7].
- **promshim layer candidate:** Routing policy, query-cost classifier, cache admission, and future sharding/concurrency knobs [7][9].
- **Expected proof signal:** Heavy queries receive sharding/cache treatment while low-cardinality queries avoid overhead; benchmark traces should show fewer regressions from over-parallelizing small queries [7][9].
- **Correctness risks:** Cardinality estimates are performance hints, not semantic inputs; stale or missing estimates must fall back to conservative behavior [7][9].
- **First experiment shape:** Add a planner decision trace that records estimated series/samples, chosen chunk size, cache admission decision, and future shard count. Use existing selector probes to compare estimated versus observed series on the benchmark corpus before changing execution.

### 7) Optional precomputed rollups / recording-rule-like ClickHouse materializations

- **Source-backed note:** Prometheus docs recommend pre-recording expensive expressions via recording rules when ad-hoc graphing is too slow [1]. VictoriaMetrics stream aggregation pre-aggregates samples before storage to reduce stored samples or series and to accelerate downstream queries, but it ignores input timestamps and holds aggregation state in process memory [13]. PromSketch also frames intermediate precomputation as a way to avoid repeated scans and repeated computation over overlapping windows [19].
- **promshim layer candidate:** Not core transparent PromQL first; better as an opt-in rewrite/catalog layer over ClickHouse materialized views or precomputed rollup tables [1][13].
- **Expected proof signal:** A small set of dashboard-heavy rollups rewritten to materialized data should reduce query latency and scanned bytes [1][13].
- **Correctness risks:** This is not transparent unless freshness, backfill, staleness, label identity, and exact PromQL range semantics are enforced [1][13]. Stream-style aggregation caveats around timestamps and restart state are a warning for any promshim-adjacent precompute service [13].
- **First experiment shape:** Manually create one ClickHouse materialized view for a known corpus query such as `sum by (job)(rate(...[5m]))`. Add a feature-flagged exact-query rewrite only for that string or normalized AST. Validate against reference Prometheus over windows with late samples and missing series.

### 8) Approximate intermediate caches as non-default exploratory mode only

- **Source-backed note:** PromSketch reports approximate intermediate-result caches for window aggregation queries, covering a subset of Prometheus aggregation-over-time functions and trading accuracy for latency/cost [19][20]. Promshim's README states that promshim is a PromQL compatibility layer for ClickHouse-backed metrics and aims at Prometheus-compatible query API behavior, so approximate answers should not be silently mixed into default compatibility mode [21].
- **promshim layer candidate:** Separate experimental endpoint or explicit query parameter for approximate dashboard mode, not the default `/api/v1/query` or `/api/v1/query_range` path [4][19].
- **Expected proof signal:** Approximate mode can be measured by latency reduction versus exact promshim and error versus reference exact answers [19].
- **Correctness risks:** Approximation breaks exact PromQL semantics, warnings/infos need API design, and alerting/rule consumers may be unsafe users for approximate answers [4][19].
- **First experiment shape:** Do not implement first. If pursued, prototype outside the compatibility path with explicit response metadata and a corpus limited to `quantile_over_time`/heavy over-time dashboards.

## Semantic caveats checklist for all candidates

- Preserve range-query evaluation timestamps exactly; Prometheus says a range query is the same PromQL expression evaluated at each step [1].
- Preserve instant-selector lookback and stale-series disappearance rules [1].
- Preserve range-vector left-open/right-closed window boundaries [1].
- Include `lookback_delta` in any cache key because the API exposes it per query [4].
- Preserve warnings and info annotations because the API can return them with successful data [4].
- Treat `NaN`, `+Inf`, and `-Inf` as Prometheus JSON strings at the API boundary [4].
- Do not assume vector output order except where Prometheus documents it; range-vector series are sorted by metric, while instant vectors are generally not guaranteed without sorting functions [4].
- Be conservative around native histograms because Prometheus has type-specific binary, aggregation, and function behavior [1][2][3][4].
- Do not rewrite `rate`, `increase`, or `delta` algebraically until counter-reset, extrapolation, offset, and boundary behavior are proven against reference Prometheus [3][17][18].
- Do not align start/end to step in exact compatibility mode, because Mimir documents this as a PromQL conformance violation [8].

## Coverage Status

- **Checked directly:** Prometheus semantics/API/function/operator docs; Mimir, Thanos, Cortex query-frontend/sharding docs; VictoriaMetrics MetricsQL, stream aggregation, optimizer, rollup cache code; relevant VictoriaMetrics rollup-cache correctness issues; PromSketch paper HTML and repository; local promshim README, range execution, chunking, selector-estimate code [1]-[21].
- **Checked with academic search:** `alpha search` surfaced `Approximation-First Timeseries Monitoring Query At Scale` / PromSketch, then I read the arXiv HTML and repository before summarizing [19][20].
- **Uncertain / needs follow-up:** I did not inspect ClickHouse TimeSeries-specific query-planning internals for hash-sharding feasibility; candidates involving materialized views need ClickHouse-side validation. I did not verify whether promshim has other cache code outside the local files cited here.
- **Blocked:** None.

## Sources

1. Prometheus, “Querying basics” — https://prometheus.io/docs/prometheus/latest/querying/basics/
2. Prometheus, “Operators” — https://prometheus.io/docs/prometheus/latest/querying/operators/
3. Prometheus, “Functions” — https://prometheus.io/docs/prometheus/latest/querying/functions/
4. Prometheus, “HTTP API” — https://prometheus.io/docs/prometheus/latest/querying/api/
5. promshim local range-function executor — file://./internal/promshim/local/planner_rangefunc.go
6. promshim range chunking — file://./internal/promshim/local/range_strategy.go and file://./internal/promshim/local/chunking.go
7. promshim selector estimates cache — file://./internal/promshim/selector_estimates.go
8. Grafana Mimir, “query-frontend” — https://grafana.com/docs/mimir/latest/references/architecture/components/query-frontend/
9. Grafana Mimir, “query sharding” — https://grafana.com/docs/mimir/latest/references/architecture/query-sharding/
10. Grafana Labs, “How we improved Grafana Mimir query performance by up to 10x” — https://grafana.com/blog/2022/07/20/how-we-improved-grafana-mimir-query-performance-by-up-to-10x/
11. Thanos, “Query Frontend” — https://thanos.io/v0.40/components/query-frontend.md/
12. Cortex, “Architecture — Query frontend” — https://cortexmetrics.io/docs/architecture/#query-frontend
13. VictoriaMetrics, “Streaming aggregation” — https://docs.victoriametrics.com/victoriametrics/stream-aggregation/
14. VictoriaMetrics, “MetricsQL” — https://docs.victoriametrics.com/victoriametrics/metricsql/
15. VictoriaMetrics/metricsql, `optimizer.go` — https://github.com/VictoriaMetrics/metricsql/blob/master/optimizer.go
16. VictoriaMetrics/VictoriaMetrics, `rollup_result_cache.go` — https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/app/vmselect/promql/rollup_result_cache.go
17. VictoriaMetrics issue #10098, “vmselect: instant rollup optimization produces different results” — https://github.com/VictoriaMetrics/VictoriaMetrics/issues/10098
18. VictoriaMetrics issue #9762, “Instant query with rollup optimization and an offset modifier may return incorrect cached results” — https://github.com/VictoriaMetrics/VictoriaMetrics/issues/9762
19. Zeying Zhu et al., “Approximation-First Timeseries Monitoring Query At Scale” — https://arxiv.org/html/2505.10560v1
20. Froot-NetSys, “PromSketch: Approximation-First Timeseries Query at Scale” — https://github.com/Froot-NetSys/promsketch
21. promshim README — file://./README.md
