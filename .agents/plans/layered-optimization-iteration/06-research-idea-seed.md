# Research idea seed backlog

## Purpose

Normalize the completed research brief into candidate seeds that the iterative
optimization plan can rank and test. This file is a bridge between cited research
and the live execution backlog; it is not a commitment to implement every idea
or an ordering that must be followed.

Primary research outputs:

```text
.pi/feynman/outputs/promshim-optimization-ideas.md
.pi/feynman/outputs/promshim-optimization-ideas.provenance.md
```

Supporting drafts and verification notes:

```text
.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md
.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md
.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md
.pi/feynman/drafts/promshim-optimization-ideas-research-local.md
.pi/feynman/drafts/promshim-optimization-ideas-cited.md
.pi/feynman/drafts/promshim-optimization-ideas-verification.md
```

## Status of research evidence

The research brief is source-backed idea generation, not authoritative product
or implementation guidance. Its ranking language is qualitative first-pass
triage based on breadth, expected evidence value, and observed adjacent-system
patterns. Every idea must still pass this plan's normal ranking, experiment,
measurement, and correctness gates before implementation is accepted.

Important provenance notes:

- Citation verification completed with no dead external links reported.
- Reviewer feedback found no fatal issues, but required softer ranking language
  and clearer caveats around adjacent-system adaptation.
- The optimizer-pattern research task ran in degraded/direct mode after a PDF
  parser failure; the final brief records that limitation.
- ClickHouse telemetry ideas depend on query-log attribution, log flush timing,
  cache state, and benchmark topology; missing or ambiguous query-log joins mean
  `unverified`, not zero work.

## How to consume seed ideas

During candidate ranking:

1. Read the final research brief and this seed file.
2. Compare each seed against current repository artifacts, benchmark profiles,
   CBE calibration, and compliance state.
3. Copy only currently plausible rows into:

   ```text
   harness/artifacts/optimization-backlog.md
   ```

4. Re-score copied rows using current evidence. Do not preserve this file's
   benefit or risk values blindly.
5. Select exactly one candidate for the next experiment.
6. Record accepted experiments in `harness/artifacts/optimization-results.md`.
7. Record rejected, deferred, or split experiments in
   `harness/artifacts/optimization-negative-results.md` only after an actual
   experiment or explicit safety decision.

Seed rows should become live backlog rows only when they have either a usable
baseline artifact or a concrete baseline-capture command. Parked ideas should
remain here until new evidence makes them testable.

## Seed table

`Benefit`, `Risk`, and `Evidence readiness` are initial triage values. Update
all three when moving a row into the live backlog.

| Candidate ID | Layer | Query family | Breadth | Benefit | Risk | Evidence readiness | First action | Expected signal | First experiment | Research source | Caveat |
|---|---|---|---:|---:|---:|---:|---|---|---|---|---|
| `bench-clickhouse-proof-signature` | benchmark/artifact foundation | all benchmarked SQL/settings/CBE families | 3 | 3 | 1 | 2 | `experiment` | Benchmark rows or sidecars show bounded query identity, query-log/ProfileEvents proof, settings/profile context, and `unverified` for missing joins. | Add post-bench aggregation joined by bounded log comment for one existing optimization sweep. | `promshim-optimization-ideas.md#1-add-a-clickhouse-proof-signature-to-benchmark-rows` | Query log is node-local/topology-sensitive; cache state, log flush timing, and one-to-many query mapping must be explicit. |
| `ir-rewrite-trace-budget` | IR metadata and rewrites | all optimized IR families | 3 | 2 | 1 | 2 | `experiment` | Explain/artifact fields show pass name, applied/skipped reason, node count inspected, changed fingerprint, iteration count, and optimizer time. | Instrument existing passes only; compare traces over optimization corpus before adding new rewrites. | `promshim-optimization-ideas.md#2-add-optimizer-rule-budget-and-rewrite-trace-instrumentation` | Trace fields must remain bounded and stable; no raw labels or tenant data. |
| `ir-semantic-dependency-classifier` | IR metadata shared by native SQL, subtree pushdown, and local execution | projection, pushdown, reuse, vector matching sensitive shapes | 3 | 3 | 2 | 1 | `explain-only` | Candidate nodes expose dependency facts and rejection reasons; existing allow/reject decisions are reproduced before broadening behavior. | Add explain-only classifier for existing repeated-subexpression and aggregation-projection cases. | `promshim-optimization-ideas.md#3-build-a-semantic-dependency-classifier-for-projection-pushdown-and-reuse` | Missing/unknown PromQL facts must fail closed; staleness, histograms, offsets, subqueries, and vector matching are high-risk. |
| `cbe-ir-feature-extraction` | CBE selection/calibration | families where coarse strings hide distinct costs | 3 | 2 | 1 | 2 | `experiment` | Calibration artifacts include non-serving feature medians/splits such as range width, output points, selector count, grouping labels, and observed rows/bytes. | Add feature extraction to calibration output only; regenerate from named sweeps and inspect splits. | `promshim-optimization-ideas.md#4-add-cbe-feature-extraction-beyond-family-strings` | Features must be non-semantic hints; missing/stale features must fail safe to strict/reference behavior. |
| `native-prewhere-pruning-audit` | native SQL lowering / measurement | high-read native selector/aggregation queries | 2 | 2 | 1 | 2 | `experiment` | EXPLAIN indexes/PREWHERE evidence and query-log counters show whether automatic PREWHERE and primary pruning happen for high-read SQL shapes. | Add a focused audit workflow comparing default vs PREWHERE-disabled or EXPLAIN-indexed captures for top high-read native queries. | `promshim-optimization-ideas.md#9-prewhereprimary-pruning-audit-before-manual-sql-rewrites` | Manual PREWHERE may be cosmetic or harmful because ClickHouse already performs automatic movement. |
| `settings-query-condition-cache-profile` | promshim ClickHouse session settings | repeated selective historical filters | 2 | 2 | 2 | 1 | `refresh-baseline` | Warm runs show `QueryConditionCacheHits` and lower read/mark/index-analysis counters or duration without correctness drift. | Add an explicit experimental profile or benchmark axis for `use_query_condition_cache`, then run cold/warm repeated-selective controls. | `promshim-optimization-ideas.md#7-query-condition-cache-profile-for-repeated-selective-filters` | Stateful cache can contaminate measurements; analyzer support, restart volatility, mark cache, OS page cache, and result cache must be isolated. |
| `local-rolling-range-rollups` | tier 4 local execution | exact float-only range-over-time functions | 2 | 3 | 3 | 1 | `split` | Fewer decoded sample operations, lower local CPU/allocations, lower CH ms or round trips if reads are avoided, and exact result parity. | Prototype only `count_over_time`, `sum_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` over plain selectors. | `promshim-optimization-ideas.md#5-exact-rolling-window-reuse-for-local-range-functions` | Exclude counter/extrapolation, quantile, histograms, offset, `@`, subqueries, sparse/stale edge cases until separately proven. |
| `exact-rollup-result-cache` | local/subtree execution and query service | repeated historical dashboard windows | 2 | 3 | 3 | 1 | `split` | Cache hit/miss/skip metrics, lower repeated-query latency or fewer reads/local CPU, unchanged strict/reference results. | Start with exact full-result cache for a tiny historical whitelist with a freshness tail; do not share arbitrary subtrees initially. | `promshim-optimization-ideas.md#6-exact-rollup-result-or-range-chunk-cache-with-freshness-tail` | Cache-key completeness is hard; include query/AST, matchers, time grid, lookback, mode, profile, versions, flags, and future tenant/auth context. |
| `ir-binary-label-filter-pushdown` | IR/native SQL | simple one-to-one arithmetic binary expressions | 2 | 2 | 3 | 1 | `explain-only` | Lower selected rows/bytes or read rows only after explain annotations prove the additional matcher is non-no-op and semantics-preserving. | Add explain-only before/after annotations with concrete examples and skipped reasons before enabling any whitelist. | `promshim-optimization-ideas.md#8-safe-label-filter-pushdown-through-simple-binary-expressions` | Vector matching, set ops, `or`/`unless`, bool comparisons, metric-name dropping, label mutation, and histograms are high-risk. |
| `native-associative-hash-sharding` | native SQL / subtree execution / CBE | high-cardinality associative aggregations | 2 | 3 | 3 | 1 | `refresh-baseline` | Baseline proves ClickHouse native parallelism is insufficient before external fan-out; prototype must lower wall-clock without unacceptable backend work. | Capture `EXPLAIN PIPELINE`, current `max_threads`, query-log/ProfileEvents, backend load, and total query count before any sharding prototype. | `promshim-optimization-ideas.md#10-selector-hash-sharding-for-associative-aggregations` | Mimir sharding is only an analogy; external fan-out may increase load on a single ClickHouse backend. |

## Parked or do-not-default ideas

These are not negative results unless an experiment has been run or an explicit
safety decision is made. Keep them out of default paths during ranking.

| Idea | Status | Reason | Retry condition |
|---|---|---|---|
| Default ClickHouse result query cache | parked / do not default | Can mask real query work and has TTL/transactional-consistency semantics that conflict with ordinary optimization proof. | Only test as a separate dashboard-replay freshness experiment with explicit cache proof and response contract. |
| Approximate intermediate caches | parked / do not default | promshim is a PromQL compatibility layer; approximate answers do not fit default `/api/v1/query` or `/api/v1/query_range` behavior. | Only revisit for an explicitly approximate mode with response metadata and measured error. |
| Transparent precomputed rollups/materialized views | parked | Requires freshness, backfill, label identity, and exact range semantics before transparent rewrites can preserve PromQL behavior. | Start with explicit manual/operator guidance or recording-rule-like experiments before any transparent rewrite. |

## Initial ranking guidance

When current artifacts do not strongly favor another candidate, prefer the
foundation candidates first because they improve the quality of all later
optimization decisions:

1. `bench-clickhouse-proof-signature`
2. `ir-rewrite-trace-budget`
3. `cbe-ir-feature-extraction`
4. `ir-semantic-dependency-classifier`
5. `native-prewhere-pruning-audit`

This order is not mandatory. A runtime candidate can outrank a foundation
candidate when it has fresher baseline evidence, a cleaner proof path, or a
larger expected non-p50 signal.

## When copying a row into the live backlog

Add these fields if the seed row does not already provide them:

- selected benchmark profile, density, transport, settings profile, reference
  profile, and corpus rows;
- exact baseline artifact path or baseline command;
- whether the idea is `scope: fundamental` or `scope: specific` for commit and
  results-ledger purposes;
- validation commands from the appropriate layer playbook;
- rollback path or reason existing broader controls/normal revert are enough;
- decision status; and
- link back to this seed row and the final research brief.

Do not copy citations into the live backlog unless they are needed to explain a
specific caveat. The live backlog should stay short and execution-focused.
