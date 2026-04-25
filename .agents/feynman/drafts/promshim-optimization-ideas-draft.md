# Candidate optimization ideas for promshim

## Executive summary

This brief is a source-backed idea generator for promshim's iterative optimization search plan. It is **not exhaustive and not authoritative**. Every idea below should enter `harness/artifacts/optimization-backlog.md` as a hypothesis to test, not as a committed roadmap.

The strongest near-term pattern is to improve the **fundamental optimization loop** before adding many narrow optimizations: attach compact ClickHouse proof signatures to benchmark rows, add optimizer-pass/rewrite trace metadata, and extract richer CBE features from IR and artifacts. These do not directly speed user queries, but they make every future accepted/rejected optimization cheaper to prove.

The strongest direct runtime ideas are: exact rolling-window reuse for local range functions, exact rollup/subtree result caching with a freshness tail, safer IR dependency analysis for projection/pushdown/reuse, and a query-condition-cache experiment for repeated selective ClickHouse filters. These ideas all need careful Prometheus-semantics boundaries: range query timestamps, lookback/staleness, left-open/right-closed windows, NaN/Inf, histograms, vector matching, offsets, and subquery grids.

## How to use these ideas

For each candidate, add a backlog row with:

- candidate ID;
- layer;
- breadth score;
- expected non-p50 signal;
- baseline artifact path or command;
- correctness risks;
- first experiment; and
- decision status.

Rejected/deferred ideas should go into `harness/artifacts/optimization-negative-results.md` with retry conditions so they are not repeated without new evidence.

## Highest-value fundamental candidates

### 1. Add a ClickHouse proof signature to benchmark rows

**Candidate ID:** `bench-clickhouse-proof-signature`
**Layer:** benchmark/artifact foundation
**Breadth:** 3 — strengthens most future SQL/settings/CBE decisions.

ClickHouse exposes the right runtime fields in `system.query_log`: duration, read rows/bytes, memory, `ProfileEvents`, changed settings, `log_comment`, `query_id`, projections, and query-cache usage. The docs also describe `system.events` counters for selected marks/parts/ranges, PREWHERE readers, index analysis, and query/condition-cache hits. The local ClickHouse research note ties these to existing `ch-explain.sh` and profile capture helpers.

**Hypothesis:** If each benchmark result row carries a compact proof signature, ranking and reviewing optimizations becomes less dependent on ad-hoc artifact inspection.

**Expected signal:** No direct query speedup. Evidence-quality improvement: every benchmark row can show normalized SQL hash or bounded query identity, changed settings profile, `read_rows`, `read_bytes`, selected marks/ranges where available, `FunctionExecute`, memory, projection usage, and cache usage.

**First experiment:** Add a post-bench aggregation that joins benchmark rows to `system.query_log` by bounded log comment and emits a filtered proof map per row. Run it on an existing optimization sweep and verify zero missing log comments.

**Risks:** Artifact size and sensitive data leakage. Keep raw PromQL/labels out of unbounded metric labels and prefer bounded IDs/hashes.

### 2. Add optimizer rule budget and rewrite trace instrumentation

**Candidate ID:** `ir-rewrite-trace-budget`
**Layer:** IR optimizer / artifact foundation
**Breadth:** 3 — helps all future IR/native/local rewrites.

DataFusion's optimizer source warns that rule order matters, adding new rules is expensive because they run for all queries, and new rules should use aggressive no-op paths. Calcite and DataFusion both expose transformation concepts through named rule classes. promshim already requires stable rewrite names, preconditions, skipped reasons, and expected physical signals in `docs/optimizer-contracts.md`.

**Hypothesis:** Measuring pass hit rate, no-op reasons, and optimizer time will help prioritize fundamental rewrites and prevent optimizer overhead from exceeding execution savings.

**Expected signal:** Explain/artifact fields show pass name, applied/skipped reason, node count inspected, changed fingerprint, iteration count, and optimizer time. No direct runtime claim until later rewrites use the data.

**First experiment:** Instrument existing IR passes only. Do not add new rewrites. Compare traces over the optimization corpus and identify high-cost/no-op or high-hit pass opportunities.

**Risks:** Trace fields must be bounded and stable. Avoid leaking raw query text, labels, or tenant values.

### 3. Build a semantic dependency classifier for projection, pushdown, and reuse

**Candidate ID:** `ir-semantic-dependency-classifier`
**Layer:** IR metadata shared by native SQL, subtree pushdown, and local execution
**Breadth:** 3 — one fundamental primitive can support many later optimizations.

Calcite's PushProjector pushes expressions only when they depend on a single input and preserves expressions that should not be split. DataFusion filter pushdown moves filters earlier only when results are preserved; its docs/source give the limit-crossing example as unsafe. For promshim, dependency must include PromQL-specific facts: time bounds, lookback, offsets, label-set production, vector matching, histograms, staleness, and subquery grids.

**Hypothesis:** A shared semantic dependency classifier will reduce one-off eligibility checks and enable safer broader classes of projection, CSE, pushdown, and local memoization.

**Expected signal:** Initially explain-only: candidate nodes have bounded dependency facts and rejection reasons. Later runtime signals include fewer scans, fewer round trips, less transfer, or lower `FunctionExecute` for optimizations that use the classifier.

**First experiment:** Add an explain-only classifier for existing repeated-subexpression and aggregation-projection cases. Confirm it reproduces current allow/reject decisions before using it to broaden any optimization.

**Risks:** Under-modeling PromQL dependencies can produce wrong results. Treat missing/unknown dependency facts as reference-required or no-rewrite.

### 4. Add CBE feature extraction beyond family strings

**Candidate ID:** `cbe-ir-feature-extraction`
**Layer:** CBE calibration and candidate ranking
**Breadth:** 3 — improves future route choice and candidate ranking.

Local docs already distinguish semantic IR facts from physical hints, and calibration now preserves settings/reference profile dimensions. Optimizer systems operate on structured plan features rather than substring family names. CBE would likely improve if calibration classes recorded range width, output points, selector count, grouping labels, histogram buckets, vector matching, and observed rows/bytes.

**Hypothesis:** More structured feature extraction will split misleading broad families and make CBE recommendations safer and more explainable.

**Expected signal:** Calibration artifacts include feature medians and split classes; prediction error or fallback reasons improve in later shadow/warmed prefer artifacts.

**First experiment:** Add feature extraction to calibration output only, without serving changes. Regenerate calibration from named sweeps and inspect which families should split.

**Risks:** Physical hints must remain non-semantic. Missing/stale features must fail safe.

## Direct runtime candidates worth testing

### 5. Exact rolling-window reuse for local range functions

**Candidate ID:** `local-rolling-range-rollups`
**Layer:** tier 4 local execution, possibly CBE candidate quality
**Breadth:** 2–3 depending on function coverage.

Prometheus defines range queries as repeated instant evaluations at equally spaced steps, and range-vector windows are left-open/right-closed. The PromQL engine research notes found that promshim local range-function execution currently evaluates each step and assembles a matrix, while PromSketch identifies repeated scans/computation over overlapping windows as a bottleneck. The PromSketch result is approximate, but the reusable idea for promshim is exact rolling-window reuse.

**Hypothesis:** For exact float-only rollups over plain selectors, maintaining an incremental sample window can reduce repeated local work over dense/long range queries.

**Expected signal:** lower local CPU/allocations, fewer decoded sample operations, lower CH ms or round trips if repeated reads are avoided, and unchanged output versus the current evaluator and Prometheus.

**First experiment:** Start with `count_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` over plain selectors. Exclude `rate`, `increase`, `delta`, `deriv`, `quantile_over_time`, `changes`, histograms, offsets, `@`, and subqueries until exact semantics are separately proven.

**Risks:** Boundary samples at `t-window`, staleness, NaN/Inf, sparse samples, and mixed histogram/float behavior.

### 6. Exact rollup/subtree result cache with freshness tail

**Candidate ID:** `exact-rollup-subtree-cache`
**Layer:** local/subtree execution and possibly HTTP/query service
**Breadth:** 2–3 if cache key and freshness machinery are reusable.

Mimir, Thanos, and Cortex split/cache range queries in query frontends. VictoriaMetrics has a rollup-result cache keyed by expression/window/step/tag filters and skips too-recent data, but public issues show that rollup rewrites involving `rate`/`increase` and `offset` can produce wrong answers. For promshim, exact evaluated-subtree caching is safer than algebraic rewrite caching.

**Hypothesis:** Caching exact evaluated rollup subtrees for historical chunks can speed repeated dashboard queries and different queries sharing heavy subtrees.

**Expected signal:** cache hit/miss/skip metrics, lower p50/p95 on repeated historical queries, fewer CH reads or local CPU, unchanged strict/reference results.

**First experiment:** Cache only exact full results of a narrow whitelist such as `sum_over_time`, `count_over_time`, `min_over_time`, `max_over_time`, `avg_over_time` over plain selectors, no offset, no `@`, no subquery, no histograms, and only chunks ending before a freshness tail.

**Risks:** Cache key completeness: query/AST, matchers, table/database, start/end/step, lookback, offset, native mode, settings profile, ClickHouse version, and feature flags. Incorrect keys can return stale or wrong results.

### 7. Query-condition-cache profile for repeated selective filters

**Candidate ID:** `settings-query-condition-cache-profile`
**Layer:** promshim ClickHouse session settings
**Breadth:** 2 — family/profile-level setting experiment.

ClickHouse query condition cache stores per-filter/per-granule skip bits for repeated selective filters on mostly immutable data. It requires analyzer support, is controlled by `use_query_condition_cache`, is not retained across restarts, and exposes hit/miss counters. promshim already names `repeated_selective` as a profile but skips enabling this setting pending evidence.

**Hypothesis:** Repeated selective historical PromQL queries can benefit from condition-cache hits without the freshness risks of result caching.

**Expected signal:** warm runs show `QueryConditionCacheHits`, fewer selected/read rows or lower duration, and no correctness drift. Cold/control runs show misses or no benefit.

**First experiment:** Add an explicit experimental profile or benchmark axis enabling `use_query_condition_cache` only when version/analyzer support is detected. Run cold/warm pairs for repeated selector/matcher-heavy queries.

**Risks:** Statefulness can contaminate measurement; version/analyzer dependency; may hide pruning changes in unrelated optimization tests.

### 8. Safe label-filter pushdown through simple binary expressions

**Candidate ID:** `ir-binary-label-filter-pushdown`
**Layer:** IR/native SQL
**Breadth:** 2 — family-level if limited to safe binary forms.

VictoriaMetrics MetricsQL optimizer pushes common label filters into metric selectors across binary expressions under safety checks. Prometheus binary/vector matching semantics constrain this heavily: `on`, `ignoring`, group modifiers, set operators, bool comparisons, metric-name dropping, and histogram behavior can all change legality.

**Hypothesis:** For one-to-one arithmetic binary expressions with identical common equality matchers and no modifiers, pushing filters into both selector sides reduces scan work.

**Expected signal:** lower `SelectedRows`/`SelectedBytes`/`read_rows`, unchanged output labels/values, and explicit skipped reasons for unsafe binary shapes.

**First experiment:** Implement explain-only candidate annotation for safe shapes, then enable a tiny whitelist after differential tests.

**Risks:** Vector matching, set ops, `or`/`unless`, absent functions, label mutation, histograms, and bool comparisons.

### 9. PREWHERE/primary-pruning audit before manual SQL rewrites

**Candidate ID:** `native-prewhere-pruning-audit`
**Layer:** native SQL lowering / measurement
**Breadth:** 2 — can identify multiple SQL-shape opportunities.

ClickHouse automatically moves eligible filters to PREWHERE by default, and EXPLAIN/logs can show PREWHERE behavior. Primary key and skip-index pruning can be inspected with `EXPLAIN PLAN indexes=1`. Before forcing manual PREWHERE, promshim can audit whether generated SQL lets ClickHouse perform the expected I/O reduction.

**Hypothesis:** Some native lowered SQL shapes may block automatic PREWHERE or effective primary pruning; detecting those cases will reveal better SQL-shape candidates.

**Expected signal:** control runs with `optimize_move_to_prewhere=false` or EXPLAIN comparisons show whether PREWHERE is active; `RowsReadByPrewhereReaders`, `RowsReadByMainReader`, `SelectedMarks`, `read_bytes`, and `EXPLAIN indexes=1` change or explain lack of movement.

**First experiment:** Add a `ch-explain` mode or notes workflow for top high-read native queries comparing default vs PREWHERE-disabled settings. If auto-PREWHERE is absent for a shape, inspect SQL before adding manual PREWHERE.

**Risks:** Manual PREWHERE can be cosmetic or harmful; `FINAL`/engine-specific interactions; version differences.

### 10. Selector-hash sharding for associative aggregations

**Candidate ID:** `native-associative-hash-sharding`
**Layer:** native SQL / subtree execution / CBE
**Breadth:** 2 if limited to associative aggregation families.

Mimir shards suitable query subtrees and documents associative aggregations such as `sum`, `min`, `max`, `count`, and `avg` as shardable while functions such as `absent`, `histogram_quantile`, and sorting are not. It uses cardinality estimates to reduce shard counts rather than raise them beyond bounds.

**Hypothesis:** For high-cardinality associative aggregation queries, hash-sharded execution can reduce wall-clock latency by parallelizing work, even if total rows read do not fall.

**Expected signal:** lower wall-clock for high-cardinality aggregations, bounded extra query count, unchanged results, and no regression for low-cardinality queries because estimates/caps avoid sharding.

**First experiment:** Opt-in `N`-way sharding for `sum by (...) (selector)` and `count by (...) (selector)` on a high-cardinality fixture. Sweep `N={2,4,8}` and measure total CH work, latency, and correctness.

**Risks:** Increased backend load, merge correctness, `avg` sum/count semantics, deterministic hashing, output ordering, and cap behavior.

## Ideas to reject or keep out of default paths for now

### Default result query cache

ClickHouse result query cache can serve repeated `SELECT` results but is documented as transactionally inconsistent and TTL-based. It can mask real query work. Keep it out of default optimization measurement and default PromQL serving. If tested, treat it as a separate dashboard-replay freshness experiment.

### Approximate intermediate caches

PromSketch suggests approximate intermediate caches for time-series queries, but promshim is a Prometheus compatibility layer. Approximate answers should not enter default `/api/v1/query` or `/api/v1/query_range`. If ever explored, it should be an explicit approximate mode with response metadata and error measurement.

### Transparent precomputed rollups/materialized views

Recording-rule-like precomputation can help known dashboards, but transparent rewrites require freshness, backfill, label identity, and exact range semantics. Start with explicit manual experiments or optional operator guidance rather than transparent default rewrites.

## Suggested first backlog rows

| Candidate ID | Layer | Breadth | Benefit | Risk | First action |
|---|---|---:|---:|---:|---|
| `bench-clickhouse-proof-signature` | artifact foundation | 3 | 3 | 1 | Experiment: add proof signature to existing benchmark reports. |
| `ir-rewrite-trace-budget` | IR/artifacts | 3 | 2 | 1 | Experiment: instrument existing passes only. |
| `ir-semantic-dependency-classifier` | IR/shared | 3 | 3 | 2 | Explain-only experiment reproducing current decisions. |
| `cbe-ir-feature-extraction` | CBE/calibration | 3 | 2 | 1 | Add non-serving calibration fields. |
| `local-rolling-range-rollups` | tier 4 local | 2–3 | 3 | 3 | Prototype exact whitelist on dense/long-range controls. |
| `exact-rollup-subtree-cache` | local/query service | 2–3 | 3 | 3 | Prototype historical exact whitelist with freshness tail. |
| `settings-query-condition-cache-profile` | session settings | 2 | 2 | 2 | Cold/warm repeated-selective profile experiment. |
| `ir-binary-label-filter-pushdown` | IR/native | 2 | 2 | 3 | Explain-only safe-shape annotation first. |
| `native-prewhere-pruning-audit` | native/measurement | 2 | 2 | 1 | Audit high-read native queries before manual rewrites. |
| `native-associative-hash-sharding` | native/CBE | 2 | 3 | 3 | Opt-in high-cardinality associative aggregation prototype. |

## Open questions

1. Which benchmark data profiles are currently available for high-cardinality aggregation and dense range-function controls?
2. Do ClickHouse `TimeSeries` table internals expose additional pruning/metadata signals beyond the generated `timeSeriesData`/`timeSeriesTags` table functions and query-log counters?
3. Is the current benchmark report schema the right place for proof signatures, or should they live in a sidecar joined by log comment?
4. What is the minimum acceptable local-executor differential suite for rolling-window reuse across staleness, NaN/Inf, sparse samples, and histograms?
5. Which CBE feature fields are stable enough to expose in long-lived artifacts without creating noisy schema churn?

## Source notes used

- `.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md`
- `.pi/feynman/drafts/promshim-optimization-ideas-research-local.md`
