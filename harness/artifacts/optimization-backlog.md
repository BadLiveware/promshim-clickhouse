# Optimization backlog

Updated: 2026-04-25
Git revision: `e45b759ff7b40d61cb6f1ed7ca26230925be26a4`

## Evidence freshness

- Benchmark stack status was inspected with `./scripts/run-sweep.sh --bench-status`.
- Available benchmark seed markers: 7d sparse/dense, 30d sparse, and 1y sparse are present for both Prometheus and ClickHouse; 30d dense and 1y dense are missing.
- Recent usable context artifacts include:
  - `harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/manifest.json`
  - `harness/artifacts/sweeps/tier34-local-repeated-rate-prechange/manifest.json`
  - `harness/artifacts/sweeps/tier34-local-repeated-rate-postchange-rebuilt/manifest.json`
  - `harness/artifacts/sweeps/cbe-rate-shadow-7d-sparse/manifest.json`
  - `harness/artifacts/sweeps/cbe-rate-prefer-gated-7d-sparse/manifest.json`
  - `harness/artifacts/sweeps/cbe-rate-prefer-shadow-warmed-7d-sparse/manifest.json`
- `promshim-optimization-foundation-7d-sparse` has 15 query rows, 45 result rows, 0 errors, 0 missing log comments, and optimization corpus coverage across `prefer`, `force_supported`, and `off`.
- Several older manifests predate the latest planning/research commits and are context artifacts, not acceptance evidence for new code. Use fresh artifacts for post-change decisions.
- Pre-existing untracked file left outside this backlog update: `.agents/plans/cost-routing-calibration.md` duplicate.

## Ranking formula

```text
score = benefit + breadth + evidence_readiness + rollbackability - correctness_risk - implementation_cost
```

The score is a tie-breaker. Prefer candidates with a clearer proof path or broader reusable value when scores are close.

## Ranked candidates

| Status | Candidate ID | Query family | Layer | Evidence source | Hypothesis | Expected signal | Benefit | Breadth | Evidence readiness | Correctness risk | Implementation cost | Rollbackability | Score | Next action | Research source | Caveat |
|---|---|---|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|
| accepted | `bench-clickhouse-proof-signature` | all benchmarked SQL/settings/CBE families | benchmark/artifact foundation | `bench-clickhouse-proof-signature-smoke` plus research seed | Adding bounded ClickHouse proof signatures to benchmark artifacts will make later optimization decisions attributable without ad-hoc query-log spelunking. | `artifact-summary.json` includes 15 proof signatures with `proofMatched=15`, `proofMissing=0`, `proofAmbiguous=0`; synthetic controls produced expected missing and ambiguous statuses. | 3 | 3 | 3 | 1 | 2 | 3 | 9 | accepted | `.pi/feynman/outputs/promshim-optimization-ideas.md#1-add-a-clickhouse-proof-signature-to-benchmark-rows` | Query-log joins are topology- and timing-sensitive; ambiguous rows are represented explicitly instead of treated as success. |
| accepted | `ir-rewrite-trace-budget` | all optimized IR families | IR metadata and rewrites | `ir-rewrite-trace-budget-smoke` + `ir-rewrite-trace-budget-smoke-posttiming` and explain captures | Instrumenting rewrite pass hit/no-op/skip costs will expose broad optimization opportunities and avoid high-overhead passes. | Runtime explain now includes bounded pass metadata with non-null optimizer timing (`optimizerTimeMicros`), inspected node counts, and before/after fingerprints. | 2 | 3 | 3 | 1 | 2 | 3 | 8 | accepted | `.pi/feynman/outputs/promshim-optimization-ideas.md#2-add-optimizer-rule-budget-and-rewrite-trace-instrumentation` | Trace fields remain bounded and avoid raw query/label leakage; optimizer trace timing is clamped to avoid omitted zero-micro rows. |
| accepted | `cbe-ir-feature-extraction` | families where coarse strings hide distinct costs | CBE selection/calibration | regenerated calibration outputs from optimization and settings-profile sweeps | Structured feature extraction can split misleading broad families without changing serving behavior. | Calibration outputs now include bounded non-serving feature medians (range window, selector count, grouping labels, query length) while recommendations remain unchanged. | 2 | 3 | 3 | 1 | 2 | 3 | 8 | accepted | `.pi/feynman/outputs/promshim-optimization-ideas.md#4-add-cbe-feature-extraction-beyond-family-strings` | Features remain non-semantic hints; missing/stale values still fail safe and are not used for served routing. |
| deferred | `native-prewhere-pruning-audit` | high-read native selector/aggregation queries | native SQL lowering / measurement | fresh `ch-explain` selector + aggregation captures | Auditing generated SQL against ClickHouse automatic PREWHERE/primary pruning may reveal SQL-shape candidates before risky manual rewrites. | Sampled native SQL shapes already show active primary-key/prewhere pruning, so manual PREWHERE rewrite is not justified yet. | 2 | 2 | 3 | 1 | 1 | 3 | 8 | deferred | `.pi/feynman/outputs/promshim-optimization-ideas.md#9-prewhereprimary-pruning-audit-before-manual-sql-rewrites` | Retry only with concrete high-read shape gaps where pruning is weak/absent or explicit PREWHERE shows safer lower read/mark counters. |
| selected | `ir-semantic-dependency-classifier` | projection, pushdown, reuse, vector matching sensitive shapes | IR metadata shared by native SQL, subtree pushdown, and local execution | research seed plus existing repeated-expression/projection work | A shared dependency classifier can reduce one-off eligibility checks and safely broaden projection, reuse, and pushdown preconditions. | Explain-only facts and rejection reasons reproduce current allow/reject decisions before broadening behavior. | 3 | 3 | 1 | 2 | 3 | 2 | 4 | explain-only | `.pi/feynman/outputs/promshim-optimization-ideas.md#3-build-a-semantic-dependency-classifier-for-projection-pushdown-and-reuse` | Missing PromQL facts around staleness, histograms, offsets, subqueries, and vector matching must fail closed. |
| candidate | `settings-query-condition-cache-profile` | repeated selective historical filters | promshim ClickHouse session settings | research seed; no cold/warm local artifacts yet | A scoped experimental settings profile could show whether ClickHouse query condition cache helps repeated selective historical filters. | Warm runs show `QueryConditionCacheHits` and lower read/mark/index-analysis counters or duration without correctness drift. | 2 | 2 | 1 | 2 | 2 | 3 | 4 | refresh-baseline | `.pi/feynman/outputs/promshim-optimization-ideas.md#7-query-condition-cache-profile-for-repeated-selective-filters` | Stateful cache effects must be isolated from OS page cache, mark cache, result cache, and benchmark ordering effects. |
| candidate | `local-rolling-range-rollups` | exact float-only range-over-time functions | tier 4 local execution | research seed; existing local memoization evidence is adjacent only | Exact rolling-window reuse may reduce repeated local sample work for a narrow range-function whitelist. | Fewer decoded sample operations, lower local CPU/allocations, lower CH ms or round trips if reads are avoided, and exact result parity. | 3 | 2 | 1 | 3 | 3 | 2 | 2 | split | `.pi/feynman/outputs/promshim-optimization-ideas.md#5-exact-rolling-window-reuse-for-local-range-functions` | Must exclude or separately prove counter/extrapolation, quantile, histograms, offset, `@`, subqueries, sparse/stale edges. |
| candidate | `exact-rollup-result-cache` | repeated historical dashboard windows | local/subtree execution and query service | research seed only | Exact historical result caching may speed repeated dashboards if cache key and freshness boundaries are complete. | Cache hit/miss/skip metrics, lower repeated-query latency or fewer reads/local CPU, unchanged strict/reference results. | 3 | 2 | 1 | 3 | 3 | 2 | 2 | split | `.pi/feynman/outputs/promshim-optimization-ideas.md#6-exact-rollup-result-or-range-chunk-cache-with-freshness-tail` | Cache-key completeness and freshness tail must be designed before benchmarking. |
| candidate | `ir-binary-label-filter-pushdown` | simple one-to-one arithmetic binary expressions | IR/native SQL | research seed only | Some inferred label filters might reduce scan work for binary expressions once vector matching proves the extra matcher cannot remove valid output. | Lower selected rows/bytes or read rows only after explain annotations prove the added matcher is non-no-op and semantics-preserving. | 2 | 2 | 1 | 3 | 2 | 2 | 2 | explain-only | `.pi/feynman/outputs/promshim-optimization-ideas.md#8-safe-label-filter-pushdown-through-simple-binary-expressions` | Vector matching, set ops, bool comparisons, metric-name dropping, label mutation, and histograms are high-risk. |
| candidate | `native-associative-hash-sharding` | high-cardinality associative aggregations | native SQL / subtree execution / CBE | research seed only | External hash sharding could help only if ClickHouse native parallelism is insufficient for high-cardinality associative aggregation. | Baseline proves ClickHouse native parallelism is insufficient before any fan-out; prototype lowers wall-clock without unacceptable backend work. | 3 | 2 | 1 | 3 | 3 | 2 | 2 | refresh-baseline | `.pi/feynman/outputs/promshim-optimization-ideas.md#10-selector-hash-sharding-for-associative-aggregations` | Mimir sharding is only an analogy; fan-out may just increase single-backend load. |

## Parked ideas

| Idea | Status | Reason | Retry condition |
|---|---|---|---|
| Default ClickHouse result query cache | parked / do not default | It can mask real query work and has TTL/transactional-consistency semantics that make ordinary optimization proof unreliable. | Only test as a separate dashboard-replay freshness experiment with explicit cache proof and response contract. |
| Approximate intermediate caches | parked / do not default | promshim is a PromQL compatibility layer; approximate answers do not fit default API semantics. | Only revisit for an explicitly approximate mode with response metadata and measured error. |
| Transparent precomputed rollups/materialized views | parked | Requires freshness, backfill, label identity, and exact range semantics before transparent rewrites can preserve PromQL behavior. | Start with explicit manual/operator guidance or recording-rule-like experiments before any transparent rewrite. |

## Selected candidate

`bench-clickhouse-proof-signature`, `ir-rewrite-trace-budget`, and `cbe-ir-feature-extraction` are accepted instrumentation improvements. Attempt 3 (`native-prewhere-pruning-audit`) is deferred on negative evidence (active auto-pruning in sampled shapes). Attempt 4 is now `ir-semantic-dependency-classifier` as the next explain-only candidate.
