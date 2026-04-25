# Candidate optimization ideas for promshim

## Executive summary

This brief is a source-backed idea generator for promshim's iterative optimization search plan, which ranks optimization candidates, runs one experiment at a time, and repeats after accept/reject/defer decisions [1]. It is **not exhaustive and not authoritative**. Every idea below should enter `harness/artifacts/optimization-backlog.md` as a hypothesis to test, because the local ranking plan defines backlog rows around benefit, breadth, evidence readiness, correctness risk, implementation cost, rollbackability, expected signal, and next action rather than around committed roadmap status [2].

A high-priority first-pass pattern is to improve the **optimization loop itself** before adding many narrow optimizations: attach compact ClickHouse proof signatures to benchmark rows, add optimizer-pass/rewrite trace metadata, and extract richer CBE features from IR and artifacts [4, 5, 6, 7, 8, 11, 16]. These are measurement and decision-quality foundations rather than direct query-speed rewrites [5, 6, 7, 8, 11]. Their priority is a subjective first-pass ranking based on breadth and evidence usefulness, not a measured benchmark conclusion.

Promising direct runtime hypotheses are: exact rolling-window reuse for local range functions, exact rollup-result or range-chunk caching with a freshness tail, safer IR dependency analysis for projection/pushdown/reuse, and a query-condition-cache experiment for repeated selective ClickHouse filters [13, 14, 17, 18, 19, 23, 24, 25, 26, 29]. These need careful Prometheus-semantics boundaries: range query timestamps, lookback/staleness, left-open/right-closed windows, NaN/Inf, histograms, vector matching, offsets, and subquery grids [17, 20, 21, 22].

## How to use these ideas

Benefit, risk, and breadth scores below are qualitative triage scores, not measured benchmark results. For each candidate, add a backlog row with [2]:

- candidate ID;
- layer;
- breadth score;
- expected non-p50 signal;
- baseline artifact path or command;
- correctness risks;
- first experiment; and
- decision status.

Rejected/deferred ideas should go into `harness/artifacts/optimization-negative-results.md` with retry conditions so they are not repeated without new evidence [3].

## Highest-value fundamental candidates

### 1. Add a ClickHouse proof signature to benchmark rows

**Candidate ID:** `bench-clickhouse-proof-signature`
**Layer:** benchmark/artifact foundation
**Breadth:** 3 — strengthens most future SQL/settings/CBE decisions.

ClickHouse exposes runtime fields in `system.query_log`, including duration, read rows/bytes, memory, `ProfileEvents`, changed settings, `log_comment`, `query_id`, projections, query-cache usage, and `normalized_query_hash` [6]. ClickHouse also documents event names in `system.events`, including selected marks/parts/ranges, PREWHERE readers, index analysis, and query/condition-cache hits [7]. For per-benchmark-row proof, use per-query `system.query_log.ProfileEvents`; treat `system.events` as the event-name reference or as server-wide context, not as row-level evidence [6, 7]. The local repository already has `ch-explain.sh`, which issues PromQL through promshim, captures lowered SQL via `system.query_log`, and writes EXPLAIN/query-log artifacts [10]. The local repository also has `ch-profile-capture.sh`, which aggregates `system.query_log` ProfileEvents, duration, read rows/bytes, result rows, and memory into JSON [11].

**Hypothesis:** If each benchmark result row carries a compact proof signature, ranking and reviewing optimizations becomes less dependent on ad-hoc artifact inspection [5, 6, 7, 11].

**Expected signal:** The measurement target is evidence quality rather than direct query speed. Each benchmark row should be able to show normalized SQL hash or bounded query identity, changed settings profile when `log_query_settings=1` is enabled, `read_rows`, `read_bytes`, selected marks/ranges where available, `FunctionExecute`, memory, projection usage, and cache usage [6, 7, 11]. Missing query-log rows or ambiguous joins should mean `unverified`, not zero work.

**First experiment:** Add a post-bench aggregation that joins benchmark rows to `system.query_log` by bounded log comment and emits a filtered proof map per row [6, 10, 11]. Run it on an existing optimization sweep and check whether every benchmark row has a corresponding log-comment-backed proof artifact [6, 10, 11]. Define one-to-many behavior for benchmark rows that issue multiple ClickHouse statements.

**Instrumentation caveats:** `system.query_log.Settings` needs ClickHouse query-setting logging to be enabled to capture changed settings [6]. `system.query_log` can be node-local in distributed/cloud topologies, so single-node benchmark assumptions should be recorded in the manifest or proof sidecar [6]. Cache state, log flush timing, background merges, query retries, and manual stack access can contaminate proof signatures [5, 6, 7].

**Risks:** Artifact size and sensitive data leakage are operational risks because query-log rows can include query text and related metadata [6]. Keep raw PromQL/labels out of unbounded metric labels and prefer bounded IDs/hashes [4, 6, 10].

### 2. Add optimizer rule budget and rewrite trace instrumentation

**Candidate ID:** `ir-rewrite-trace-budget`
**Layer:** IR optimizer / artifact foundation
**Breadth:** 3 — helps all future IR/native/local rewrites.

DataFusion's optimizer source says rule order matters, new rules are expensive because they are run for all queries, and new rules should use aggressive no-op paths when possible [12]. Calcite and DataFusion expose optimizer concepts through named rules/passes in their public documentation [13, 15]. promshim already requires stable rewrite names, preconditions, preserved invariants, expected physical signals, skipped reasons, high-risk exclusions, CBE interactions, and rollback controls when serving is affected [4].

**Hypothesis:** Measuring pass hit rate, no-op reasons, and optimizer time will help prioritize fundamental rewrites and prevent optimizer overhead from exceeding execution savings [4, 12].

**Expected signal:** Explain/artifact fields show pass name, applied/skipped reason, node count inspected, changed fingerprint, iteration count, and optimizer time [4, 12]. The first useful output is a bounded trace, not a runtime-speed claim [4, 12].

**First experiment:** Instrument existing IR passes only, without adding new rewrites [4, 12]. Compare traces over the optimization corpus and identify high-cost/no-op or high-hit pass opportunities [4, 12].

**Risks:** Trace fields must be bounded and stable because optimizer-contract artifacts require stable names, skipped reasons, and rollback controls [4]. Avoid leaking raw query text, labels, or tenant values [4, 6].

### 3. Build a semantic dependency classifier for projection, pushdown, and reuse

**Candidate ID:** `ir-semantic-dependency-classifier`
**Layer:** IR metadata shared by native SQL, subtree pushdown, and local execution
**Breadth:** 3 — one fundamental primitive can support many later optimizations.

Calcite's `PushProjector` pushes expressions only when they depend on a single input and preserves expressions that should not be split [13]. DataFusion filter pushdown moves filters earlier only when doing so preserves results, and its source gives limit-crossing as an unsafe case [14]. For promshim, dependency analysis must include PromQL-specific facts: time bounds, lookback, offsets, label-set production, vector matching, histograms, staleness, and subquery grids [4, 17, 20, 21, 22].

**Hypothesis:** A shared semantic dependency classifier will reduce one-off eligibility checks and enable safer broader classes of projection, CSE, pushdown, and local memoization [4, 13, 14].

**Expected signal:** Initially explain-only: candidate nodes have bounded dependency facts and rejection reasons [4]. Later runtime signals include fewer scans, fewer round trips, less transfer, or lower `FunctionExecute` for optimizations that use the classifier [5, 6, 7, 11].

**First experiment:** Add an explain-only classifier for existing repeated-subexpression and aggregation-projection cases [4]. Check whether it reproduces current allow/reject decisions before using it to broaden any optimization [4].

**Risks:** Under-modeling PromQL dependencies can produce wrong results because PromQL semantics include lookback/staleness, vector matching, histogram behavior, range-function semantics, and API-level special-value handling [17, 20, 21, 22]. Treat missing/unknown dependency facts as reference-required or no-rewrite [4, 16].

### 4. Add CBE feature extraction beyond family strings

**Candidate ID:** `cbe-ir-feature-extraction`
**Layer:** CBE calibration and candidate ranking
**Breadth:** 3 — improves future route choice and candidate ranking.

Local docs distinguish semantic optimizer invariants from physical signals and CBE interactions [4]. Local rollout guidance requires strict/reference safety, `cost_shadow` before `cost_prefer`, local family gates, estimates, caps, confidence, and preserved artifacts for served CBE changes [16]. Optimizer systems such as Calcite and DataFusion operate on structured plan/expression representations rather than only on substring-like family names [13, 15]. CBE may improve if calibration classes record range width, output points, selector count, grouping labels, histogram buckets, vector matching, and observed rows/bytes as non-semantic features [4, 5, 16].

**Hypothesis:** More structured feature extraction can split misleading broad families and make CBE recommendations safer and more explainable [4, 16].

**Expected signal:** Calibration artifacts include feature medians and split classes [16]. Prediction-error or fallback-reason improvements should be evaluated only in later shadow/warmed-prefer artifacts [16].

**First experiment:** Add feature extraction to calibration output only, without serving changes [16]. Regenerate calibration from named sweeps and inspect which families should split [2, 16].

**Risks:** Physical hints must remain non-semantic [4, 16]. Missing/stale features must fail safe to strict/reference behavior [16].

## Direct runtime candidates worth testing

### 5. Exact rolling-window reuse for local range functions

**Candidate ID:** `local-rolling-range-rollups`
**Layer:** tier 4 local execution, possibly CBE candidate quality
**Breadth:** 2–3 depending on function coverage.

Prometheus defines range queries as repeated instant evaluations at equally spaced steps, and Prometheus defines range-vector windows as left-open/right-closed [17]. The local promshim range-function executor currently iterates each step, runs the instant evaluator at that timestamp, and assembles a matrix by label set [18]. PromSketch identifies repeated scans and repeated computation over overlapping windows as a bottleneck, but PromSketch's proposed intermediate caches are approximate rather than exact [19]. The reusable idea for promshim is therefore exact rolling-window reuse, not approximate answer substitution [19, 35].

**Hypothesis:** For exact float-only rollups over plain selectors, maintaining an incremental sample window may reduce repeated local work over dense/long range queries [17, 18, 19, 20].

**Expected signal:** Support would require lower local CPU/allocations, fewer decoded sample operations, lower ClickHouse milliseconds or round trips if repeated reads are avoided, and unchanged output versus the current evaluator and Prometheus reference behavior [17, 18, 20, 22].

**First experiment:** Start with `count_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` over plain selectors, because these are range-over-time functions with simpler exact aggregation requirements than counter/extrapolation or quantile functions [20]. Exclude `rate`, `increase`, `delta`, `deriv`, `quantile_over_time`, `changes`, histograms, offsets, `@`, and subqueries until exact semantics are separately proven [17, 20, 21, 27, 28].

**Risks:** Boundary samples at `t-window`, staleness, NaN/Inf, sparse samples, and mixed histogram/float behavior are correctness risks under Prometheus range-vector, function, operator, and API semantics [17, 20, 21, 22].

### 6. Exact rollup-result or range-chunk cache with freshness tail

**Candidate ID:** `exact-rollup-result-cache`
**Layer:** local/subtree execution and possibly HTTP/query service
**Breadth:** 2–3 if cache key and freshness machinery are reusable.

Mimir, Thanos, and Cortex split/cache range queries in query frontends [23, 24, 25]. VictoriaMetrics has a rollup-result cache keyed by expression/window/step/tag filters and skips too-recent data, but public VictoriaMetrics issues report wrong answers from rollup optimization/rewrite paths involving `rate`/`increase` and `offset` [26, 27, 28]. These issue reports are failure-mode cautions, not proof that all rollup caches are unsafe. For promshim, exact evaluated-result caching is safer than algebraic rewrite caching only if the cache key and semantic equivalence contract are complete [22, 26, 28, 35].

**Hypothesis:** Caching exact evaluated rollup results or exact range chunks for historical windows may speed repeated dashboard queries [23, 24, 25, 26]. Sharing cached heavy subtrees across different query ASTs is a separate promshim-specific hypothesis that requires an explainable equivalence/key design before implementation.

**Expected signal:** Support would require cache hit/miss/skip metrics, lower p50/p95 on repeated historical queries, fewer ClickHouse reads or less local CPU, and unchanged strict/reference results [22, 23, 24, 25, 26].

**First experiment:** Cache only exact full results of a narrow whitelist such as `sum_over_time`, `count_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` over plain selectors, no offset, no `@`, no subquery, no histograms, and only chunks ending before a freshness tail [17, 20, 22, 24, 26, 28]. Do not initially cache arbitrary subtrees shared across different queries.

**Risks:** Cache key completeness is the primary correctness risk; relevant inputs include query/AST, matchers, table/database, start/end/step, request `lookback_delta`, offset, response-limit behavior where applicable, native mode, settings profile, ClickHouse version, promshim/IR version, Prometheus parser/semantics version, feature flags, and any future tenant/auth context [17, 22, 26, 28]. Incorrect keys can return stale or wrong results [22, 24, 26, 28].

### 7. Query-condition-cache profile for repeated selective filters

**Candidate ID:** `settings-query-condition-cache-profile`
**Layer:** promshim ClickHouse session settings
**Breadth:** 2 — family/profile-level setting experiment.

ClickHouse query condition cache stores per-filter/per-granule skip bits for repeated selective filters on mostly immutable data [29]. It requires analyzer support, is controlled by `use_query_condition_cache`, is not retained across restarts, and exposes hit/miss counters [29]. promshim already has a `repeated_selective` profile name and currently skips enabling query-condition/result-cache settings unless evidence gates are met [9].

**Hypothesis:** Repeated selective historical PromQL queries may benefit from condition-cache hits with lower final-result staleness risk than ClickHouse result caching, but the cache is still stateful and measurement-sensitive [29, 33].

**Expected signal:** Support would require warm runs with `QueryConditionCacheHits`, fewer relevant read/mark/index-analysis counters or lower duration, and no correctness drift [7, 29]. Cold/control runs should isolate condition-cache misses from OS page cache, ClickHouse mark cache, result query cache, projection/pruning changes, and benchmark ordering effects [7, 29, 33].

**First experiment:** Add an explicit experimental profile or benchmark axis enabling `use_query_condition_cache` only when version/analyzer support is detected [9, 29]. Run cold/warm pairs for repeated selector/matcher-heavy queries with cache-state notes [7, 29].

**Risks:** Statefulness can contaminate measurement, analyzer support is a prerequisite, restart volatility can erase the cache, and cache hits can obscure unrelated pruning changes in optimization tests [29].

### 8. Safe label-filter pushdown through simple binary expressions

**Candidate ID:** `ir-binary-label-filter-pushdown`
**Layer:** IR/native SQL
**Breadth:** 2 — family-level if limited to safe binary forms.

VictoriaMetrics MetricsQL optimizer pushes common label filters into metric selectors across binary expressions under safety checks [30]. Prometheus binary/vector matching semantics constrain this heavily: `on`, `ignoring`, group modifiers, set operators, bool comparisons, metric-name dropping, and histogram behavior can all change legality [21].

**Hypothesis:** For one-to-one arithmetic binary expressions, a matcher inferred from an enclosing safe context or copied from one side to the other may reduce scan work only when the other side does not already have an equivalent matcher and Prometheus vector-matching semantics prove the extra matcher cannot remove valid output [21, 30]. If both sides already contain identical equality matchers, re-pushing the same matcher is a no-op and should be rejected as a candidate.

**Expected signal:** Support would require lower `SelectedRows`/`SelectedBytes`/`read_rows`, unchanged output labels/values, and explicit skipped reasons for unsafe or no-op binary shapes [6, 7, 21, 30].

**First experiment:** Implement explain-only candidate annotation with concrete before/after examples. Example accepted shape should identify the origin of the additional matcher and prove that output is unchanged. Only after differential tests should a tiny whitelist be enabled [4, 21, 30].

**Risks:** Vector matching, set ops, `or`/`unless`, absent functions, label mutation, histograms, and bool comparisons are risk areas because Prometheus operators define shape-specific matching, dropping, and histogram behavior [21].

### 9. PREWHERE/primary-pruning audit before manual SQL rewrites

**Candidate ID:** `native-prewhere-pruning-audit`
**Layer:** native SQL lowering / measurement
**Breadth:** 2 — can identify multiple SQL-shape opportunities.

ClickHouse automatically moves eligible filters to PREWHERE by default through `optimize_move_to_prewhere`, and the PREWHERE guide describes measuring effects through bytes read and EXPLAIN/logs [31]. Primary-key and skip-index pruning can be inspected with `EXPLAIN PLAN indexes=1` [8]. Before forcing manual PREWHERE, promshim can audit whether generated SQL lets ClickHouse perform the expected I/O reduction [8, 31].

**Hypothesis:** Some native lowered SQL shapes may block automatic PREWHERE or effective primary pruning, and detecting those cases can reveal better SQL-shape candidates [8, 31].

**Expected signal:** Support would require control runs with `optimize_move_to_prewhere=false` or EXPLAIN comparisons that show whether PREWHERE is active [31]. `RowsReadByPrewhereReaders`, `RowsReadByMainReader`, `SelectedMarks`, `read_bytes`, and `EXPLAIN indexes=1` should change or explain lack of movement [6, 7, 8, 31].

**First experiment:** Add a `ch-explain` mode or notes workflow for top high-read native queries comparing default vs PREWHERE-disabled settings [8, 10, 31]. If auto-PREWHERE is absent for a shape, inspect SQL before adding manual PREWHERE [31].

**Risks:** Manual PREWHERE can be cosmetic or harmful because ClickHouse already has automatic PREWHERE movement and engine/version-specific behavior can matter [31].

### 10. Selector-hash sharding for associative aggregations

**Candidate ID:** `native-associative-hash-sharding`
**Layer:** native SQL / subtree execution / CBE
**Breadth:** 2 if limited to associative aggregation families.

Mimir shards suitable query subtrees and documents associative aggregations such as `sum`, `min`, `max`, `count`, and `avg` as shardable while functions such as `absent`, `histogram_quantile`, and sorting are not [32]. Mimir uses cardinality estimates to reduce shard counts rather than raise them beyond configured bounds [32]. Mimir's architecture is distributed across queriers and storage shards, so this is only an analogy for promshim; it is not evidence that external fan-out improves a single ClickHouse backend.

**Hypothesis:** For high-cardinality associative aggregation queries, hash-sharded execution might reduce wall-clock latency by parallelizing work only if ClickHouse's own parallelism is insufficient or saturated and external fan-out does not merely increase backend load [21, 32].

**Expected signal:** Support would require lower wall-clock for high-cardinality aggregations, bounded extra query count, unchanged results, no regression for low-cardinality queries, and evidence that total backend work/load remains acceptable [21, 32].

**First experiment:** Before implementing sharding, capture `EXPLAIN PIPELINE`, current `max_threads`, query-log/ProfileEvents, backend CPU/load, and total query count for target high-cardinality aggregation queries [6, 8, 21, 32]. Only if the baseline suggests under-parallelization should an opt-in `N`-way sharding prototype for `sum by (...) (selector)` and `count by (...) (selector)` be tested with `N={2,4,8}` [21, 32].

**Risks:** Increased backend load, merge correctness, `avg` sum/count semantics, deterministic hashing, output ordering, and cap behavior are risk areas under Prometheus operator semantics and Mimir's shard-bounding model [21, 32].

## Ideas to reject or keep out of default paths for now

### Default result query cache

ClickHouse result query cache can serve repeated `SELECT` results, but the ClickHouse docs describe it as transactionally inconsistent and TTL-based [33]. It can mask real query work in optimization measurements because cache hits can replace underlying execution evidence [6, 7, 33]. Keep it out of default optimization measurement and default PromQL serving [33, 35]. If tested, treat it as a separate dashboard-replay freshness experiment [22, 33, 36].

### Approximate intermediate caches

PromSketch proposes approximate intermediate caches for time-series queries, but promshim is documented as a PromQL compatibility layer [19, 34, 35]. Approximate answers should not enter default `/api/v1/query` or `/api/v1/query_range`, whose Prometheus API behavior includes exact response semantics, warnings/infos, and special-float JSON conventions [22, 35]. If approximate execution is ever explored, it should be an explicit approximate mode with response metadata and error measurement [19, 22, 34, 35].

### Transparent precomputed rollups/materialized views

Prometheus recording rules precompute frequently needed or computationally expensive expressions and can make repeated dashboard queries faster [36]. Transparent rewrites to precomputed rollups require freshness, backfill, label identity, and exact range semantics before they can preserve PromQL behavior [17, 20, 21, 22]. Start with explicit manual experiments or optional operator guidance rather than transparent default rewrites [1, 17, 20, 21, 22, 36].

## Suggested first backlog rows

Benefit and risk values in this table are subjective first-pass triage scores for backlog ordering, not benchmark measurements [2].

| Candidate ID | Layer | Breadth | Benefit | Risk | First action |
|---|---|---:|---:|---:|---|
| `bench-clickhouse-proof-signature` | artifact foundation | 3 | 3 | 1 | Experiment: add proof signature to existing benchmark reports [6, 7, 10, 11]. |
| `ir-rewrite-trace-budget` | IR/artifacts | 3 | 2 | 1 | Experiment: instrument existing passes only [4, 12]. |
| `ir-semantic-dependency-classifier` | IR/shared | 3 | 3 | 2 | Explain-only experiment reproducing current decisions [4, 13, 14, 17, 20, 21, 22]. |
| `cbe-ir-feature-extraction` | CBE/calibration | 3 | 2 | 1 | Add non-serving calibration fields [4, 16]. |
| `local-rolling-range-rollups` | tier 4 local | 2–3 | 3 | 3 | Prototype exact whitelist on dense/long-range controls [17, 18, 20]. |
| `exact-rollup-result-cache` | local/query service | 2–3 | 3 | 3 | Prototype historical exact whitelist with freshness tail [23, 24, 25, 26]. |
| `settings-query-condition-cache-profile` | session settings | 2 | 2 | 2 | Cold/warm repeated-selective profile experiment [9, 29]. |
| `ir-binary-label-filter-pushdown` | IR/native | 2 | 2 | 3 | Explain-only concrete before/after annotation first [21, 30]. |
| `native-prewhere-pruning-audit` | native/measurement | 2 | 2 | 1 | Audit high-read native queries before manual rewrites [8, 10, 31]. |
| `native-associative-hash-sharding` | native/CBE | 2 | 3 | 3 | Baseline ClickHouse parallelism before any external sharding prototype [6, 8, 21, 32]. |

## Open questions

1. Which benchmark data profiles are currently available for high-cardinality aggregation and dense range-function controls [2, 5]?
2. Do ClickHouse `TimeSeries` table internals expose additional pruning/metadata signals beyond generated table-function behavior and query-log counters [6, 7, 8, 35]?
3. Is the current benchmark report schema the right place for proof signatures, or should they live in a sidecar joined by log comment [6, 10, 11]?
4. What is the minimum acceptable local-executor differential suite for rolling-window reuse across staleness, NaN/Inf, sparse samples, and histograms [17, 20, 21, 22]?
5. Which CBE feature fields are stable enough to expose in long-lived artifacts without creating noisy schema churn [4, 16]?
6. For proof signatures, is the target topology always single-node benchmark ClickHouse, or do we need a distributed query-log collection path [6]?
7. For condition-cache experiments, how will cold/warm pairs isolate condition-cache effects from OS page cache, mark cache, result query cache, and benchmark ordering effects [7, 29, 33]?
8. For hash sharding, what artifact would prove ClickHouse's own parallel execution is insufficient before promshim adds external fan-out [6, 8, 32]?

## Source notes used

- `.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-local.md`

## Sources

External URLs in this Sources section resolved during the citation pass; no dead external links were found. Local paths were inspected from this repository or local external checkouts.

1. Local project — `.pi/plans/layered-optimization-iteration/README.md`
2. Local project — `.pi/plans/layered-optimization-iteration/01-candidate-ranking.md`
3. Local project — `.pi/plans/layered-optimization-iteration/05-hardening-and-repeat.md`
4. Local project — `docs/optimizer-contracts.md`
5. Local project — `.pi/skills/measuring-ch-optimizations/SKILL.md`
6. ClickHouse Docs — `system.query_log` — https://clickhouse.com/docs/operations/system-tables/query_log
7. ClickHouse Docs — `system.events` — https://clickhouse.com/docs/operations/system-tables/events
8. ClickHouse Docs — EXPLAIN Statement — https://clickhouse.com/docs/en/sql-reference/statements/explain
9. Local project — `internal/promshim/storage/settings_profile.go`
10. Local project — `scripts/ch-explain.sh`
11. Local project — `scripts/ch-profile-capture.sh`
12. Local external checkout — `/home/fl/code/external/datafusion/datafusion/optimizer/src/optimizer.rs`
13. Apache Calcite Javadocs — `PushProjector` — https://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/PushProjector.html
14. Local external checkout — `/home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs`
15. Apache DataFusion Docs — Query Optimizer — https://arrow.apache.org/datafusion/library-user-guide/query-optimizer.html
16. Local project — `docs/optimization-rollout.md`
17. Prometheus Docs — Querying basics — https://prometheus.io/docs/prometheus/latest/querying/basics/
18. Local project — `internal/promshim/local/planner_rangefunc.go`
19. Zeying Zhu et al. — “Approximation-First Timeseries Monitoring Query At Scale” — https://arxiv.org/html/2505.10560v1
20. Prometheus Docs — Query functions — https://prometheus.io/docs/prometheus/latest/querying/functions/
21. Prometheus Docs — Operators — https://prometheus.io/docs/prometheus/latest/querying/operators/
22. Prometheus Docs — HTTP API — https://prometheus.io/docs/prometheus/latest/querying/api/
23. Grafana Mimir Docs — Query frontend — https://grafana.com/docs/mimir/latest/references/architecture/components/query-frontend/
24. Thanos Docs — Query Frontend — https://thanos.io/v0.40/components/query-frontend.md/
25. Cortex Docs — Architecture, Query frontend — https://cortexmetrics.io/docs/architecture/#query-frontend
26. VictoriaMetrics source — `rollup_result_cache.go` — https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/app/vmselect/promql/rollup_result_cache.go
27. VictoriaMetrics issue #10098 — “vmselect: instant rollup optimization produces different results” — https://github.com/VictoriaMetrics/VictoriaMetrics/issues/10098
28. VictoriaMetrics issue #9762 — “Instant query with rollup optimization and an offset modifier may return incorrect cached results” — https://github.com/VictoriaMetrics/VictoriaMetrics/issues/9762
29. ClickHouse Docs — Query condition cache — https://clickhouse.com/docs/operations/query-condition-cache
30. VictoriaMetrics MetricsQL source — `optimizer.go` — https://github.com/VictoriaMetrics/metricsql/blob/master/optimizer.go
31. ClickHouse Docs — How does the PREWHERE optimization work? — https://clickhouse.com/docs/en/optimize/prewhere
32. Grafana Mimir Docs — Query sharding — https://grafana.com/docs/mimir/latest/references/architecture/query-sharding/
33. ClickHouse Docs — Query cache — https://clickhouse.com/docs/en/operations/query-cache
34. Froot-NetSys — PromSketch repository — https://github.com/Froot-NetSys/promsketch
35. Local project — `README.md`
36. Prometheus Docs — Recording rules — https://prometheus.io/docs/prometheus/latest/configuration/recording_rules/
