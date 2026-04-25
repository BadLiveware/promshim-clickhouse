## Summary

This is a verification pass of `.pi/feynman/drafts/promshim-optimization-ideas-cited.md`, not a venue-style peer review. The draft is generally framed as an idea generator and frequently labels candidates as hypotheses/experiments, which is appropriate for speculative optimization work. Several local citations do support the process claims: the layered optimization plan requires one experiment at a time and named expected signals; `docs/optimizer-contracts.md` requires stable rewrite metadata, semantic invariants, and bounded explain/artifact fields; and the local scripts do capture ClickHouse query-log/ProfileEvents artifacts.

The main verification problems are not dead links but evidentiary precision. The executive summary and some candidate hypotheses use stronger ranking/benefit language than the cited evidence supports. Several critical recommendations borrow from adjacent systems (Mimir/Thanos/Cortex/VictoriaMetrics/DataFusion/Calcite) without making the adaptation boundary explicit enough for promshim-over-ClickHouse. There are also ClickHouse instrumentation caveats that should be added before treating proof signatures or cache experiments as reliable evidence.

## Strengths

- [S1] The draft repeatedly marks optimization ideas as hypotheses and first experiments rather than committed roadmap items; this is supported by the local optimization plan’s requirement to record hypothesis, expected signal, baseline/post-change artifact, and decision per iteration.
- [S2] The PromQL semantic risk lists are well-supported by Prometheus docs: range queries are repeated instant evaluations; range-vector selectors are left-open/right-closed; offset/`@`, lookback/staleness, histogram behavior, vector matching, warnings/infos, and special-float JSON encoding are real documented constraints.
- [S3] The proposal to prefer bounded IDs/hashes and bounded artifact fields is supported by `docs/optimizer-contracts.md` and by `scripts/ch-explain.sh`, which already enforces bounded `X-Promshim-Log-Comment` values and joins lowered SQL to `system.query_log`.
- [S4] The draft is appropriately cautious about approximate answers: PromSketch is explicitly approximate, while promshim’s README frames the project as a PromQL compatibility layer and Prometheus API docs define exact response structures and warning/info semantics.

## Weaknesses

- [W1] **MAJOR:** The executive summary’s “strongest” rankings are not verified by the cited evidence. The draft later says benefit/risk/breadth scores are qualitative and not benchmark measurements, but the summary presents a ranked conclusion without showing a scoring computation, artifact comparison, or tie-break analysis.
- [W2] **MAJOR:** The ClickHouse “proof signature” proposal omits important instrumentation caveats. `system.query_log.Settings` only contains changed settings when ClickHouse logging is configured for query settings; `system.events` is a server-wide counter table, while per-query event evidence must come from `system.query_log.ProfileEvents`; and `query_log` can be node-local in ClickHouse Cloud/distributed settings. The current wording risks treating missing or mixed telemetry as proof.
- [W3] **MAJOR:** The exact rollup/subtree cache recommendation is under-supported for “different queries sharing heavy subtrees.” Mimir/Thanos/Cortex sources support query-frontend range result caching/splitting; VictoriaMetrics source supports a rollup result cache keyed by expression/window/step/tag filters; none directly verify promshim’s proposed arbitrary evaluated-subtree cache semantics or cache-key completeness.
- [W4] **MAJOR:** The query-condition-cache recommendation overstates safety by saying it avoids freshness risks. ClickHouse’s condition cache is less risky than result caching because it caches per-filter/per-granule skip information rather than final results, but the docs still require analyzer support, repeated same filters, mostly immutable data, stateful warm/cold handling, restart volatility, and hit/miss isolation. The wording should say “lower result-staleness risk,” not “without freshness risks.”
- [W5] **MAJOR:** The binary label-filter pushdown candidate has an unresolved logical gap. If both sides already have “identical common equality matchers,” pushing those same matchers into both selectors does not reduce scan work. The cited VictoriaMetrics optimizer pushes label filters through a broader MetricsQL expression tree with many modifier-specific rules; the promshim proposal needs to specify exactly where the additional filters originate, when one side lacks them, and how Prometheus metric-name/vector-matching semantics remain unchanged.
- [W6] **MAJOR:** The selector-hash sharding candidate relies too heavily on Mimir’s architecture without a ClickHouse-specific baseline. Mimir query sharding distributes work across queriers and storage shards; promshim issuing N parallel ClickHouse queries against the same backend may only add query overhead and backend load if ClickHouse already parallelizes the scan/aggregation internally. The draft notes backend-load risk, but the “can reduce wall-clock latency” hypothesis needs an explicit comparison against ClickHouse `max_threads`, pipeline, and current parallelism.
- [W7] **MINOR:** Several expected-signal statements mix “candidate hypothesis” with implied outcomes. This is less severe because the draft warns that scores are subjective, but some passages should be softened from “can/will” to “would be considered supported if measured by …”.
- [W8] **MINOR:** GitHub issues are used correctly as cautionary examples, but they should not be treated as general proof that all VictoriaMetrics rollup rewrites/cache designs are unsafe. Keep them as “reported failure modes,” not as broad evidence.

## Questions for Authors

- [Q1] For `bench-clickhouse-proof-signature`, is `log_query_settings=1` enabled in the benchmark ClickHouse profile, and do preserved artifacts prove it? If not, `Settings` will be incomplete.
- [Q2] Are proof signatures intended to read per-query `ProfileEvents` from `system.query_log`, or server-wide deltas from `system.events`? The draft currently cites both in a way that could be confused.
- [Q3] Is the benchmark ClickHouse topology always single-node? If not, how will query-log/proof joins handle node-local `system.query_log` rows?
- [Q4] For exact subtree caching, what is the full semantic cache key, including promshim code/IR version, parser/Prometheus dependency version, per-request `lookback_delta`, response limit behavior, native/local mode, settings profile, table/database, ClickHouse version, feature flags, and any auth/tenant context if added later?
- [Q5] For condition-cache experiments, how will cold/warm pairs isolate condition-cache effects from OS page cache, ClickHouse mark cache, query cache, projection/pruning changes, and benchmark ordering effects?
- [Q6] For hash sharding, what raw artifact will show that ClickHouse’s own parallel execution was saturated or insufficient before promshim adds external query fan-out?

## Verdict

Verification status: **revise before relying on this document as an optimization backlog seed**. The document is useful as a hypothesis catalog, but several recommendations need stronger caveats and more precise citation-to-claim alignment before they should drive implementation order. Confidence: **0.78**. The largest risks are overstated ranking language, telemetry caveats in proof signatures, and system-analogy claims that are not yet validated for promshim’s ClickHouse-backed architecture.

## Revision Plan

1. Replace “strongest” executive-summary language with “highest-priority by subjective first-pass triage” or add an explicit scoring table explaining how those candidates outrank alternatives.
2. Add a ClickHouse proof-signature caveat block: `log_query_settings=1`, per-query `ProfileEvents` vs server-wide `system.events`, log flushing/window isolation, distributed/node-local query logs, `log_comment` uniqueness, and missing-row failure handling.
3. Narrow the exact subtree cache claim to what sources support: range result caching and VictoriaMetrics-style rollup result caching. Mark “different queries sharing heavy subtrees” as a promshim-specific hypothesis requiring an explain-only/cache-key design proof.
4. Change condition-cache wording from “without freshness risks” to “without final-result-cache staleness semantics, but still stateful and measurement-sensitive.” Add cold/warm isolation and analyzer/version checks as acceptance criteria.
5. Rewrite the binary filter pushdown candidate with concrete before/after examples and a proof obligation for metric-name retention/drop, `on`/`ignoring`, group modifiers, set ops, bool comparisons, absent/label mutation, and histograms.
6. Add a ClickHouse-specific precondition to hash sharding: compare against current single-query ClickHouse parallelism (`EXPLAIN PIPELINE`, `max_threads`, query-log/ProfileEvents, backend CPU/load) before external sharding is considered a win.
7. Audit all GitHub issue citations and label them “reported failure modes” rather than generalizable evidence.

## Inline Annotations

> “The strongest near-term pattern is to improve the **fundamental optimization loop** before adding many narrow optimizations: attach compact ClickHouse proof signatures to benchmark rows, add optimizer-pass/rewrite trace metadata, and extract richer CBE features from IR and artifacts [4, 5, 6, 7, 8, 11, 16].”

**[W1] MAJOR:** The sources support these as plausible foundations, but they do not verify “strongest.” The draft later says scores are qualitative and not measured. Either show the ranking calculation/artifacts or soften to “a high-priority first-pass pattern.”

> “The strongest direct runtime ideas are: exact rolling-window reuse for local range functions, exact rollup/subtree result caching with a freshness tail, safer IR dependency analysis for projection/pushdown/reuse, and a query-condition-cache experiment for repeated selective ClickHouse filters [13, 14, 17, 18, 19, 23, 24, 25, 26, 29].”

**[W1] MAJOR:** Same ranking problem. The citations establish analogs and semantic constraints, not comparative strength across promshim candidates. Treat this as a hypothesis ranking unless backed by current benchmark artifacts or a scored backlog.

> “Benefit, risk, and breadth scores below are qualitative triage scores, not measured benchmark results.”

**[S1]/[W1]:** This caveat is good and should be pulled closer to the executive summary. It conflicts with the unqualified “strongest” phrasing above.

> “ClickHouse exposes runtime fields in `system.query_log`, including duration, read rows/bytes, memory, `ProfileEvents`, changed settings, `log_comment`, `query_id`, projections, query-cache usage, and `normalized_query_hash` [6].”

**[W2] MAJOR:** Mostly supported, but incomplete. ClickHouse docs state `Settings` records settings changed by the client **to enable logging changes to settings, set `log_query_settings` to 1**. Add that prerequisite. Also note that ClickHouse Cloud/distributed deployments can require `clusterAllReplicas` for a complete query-log view.

> “ClickHouse also documents `system.events` counters for selected marks/parts/ranges, PREWHERE readers, index analysis, and query/condition-cache hits [7].”

**[W2] MAJOR:** `system.events` is server-wide since server start. For per-benchmark-row proof, the relevant source is `system.query_log.ProfileEvents` using event names documented by `system.events`. State this explicitly to avoid deriving per-row claims from global counters.

> “Each benchmark row should be able to show normalized SQL hash or bounded query identity, changed settings profile, `read_rows`, `read_bytes`, selected marks/ranges where available, `FunctionExecute`, memory, projection usage, and cache usage [6, 7, 11].”

**[W2]/[Q1]/[Q2] MAJOR:** This is a good target, but it is only reliable if query-log rows are complete, settings logging is enabled, ProfileEvents are per-query, cache state is controlled, and every benchmark row has a unique/bounded log-comment join. Add missing-row behavior: no proof row should mean “unverified,” not “zero.”

> “Run it on an existing optimization sweep and check whether every benchmark row has a corresponding log-comment-backed proof artifact [6, 10, 11].”

**[Q3]:** If any benchmark query fans out to multiple ClickHouse statements, retries, or distributed nodes, what is the expected cardinality of proof artifacts per benchmark row? Define one-to-many joins before using “every row” as a pass/fail check.

> “Caching exact evaluated rollup subtrees for historical chunks can speed repeated dashboard queries and different queries sharing heavy subtrees [23, 24, 25, 26, 36].”

**[W3] MAJOR:** Mimir/Thanos/Cortex support range result caching/splitting; VictoriaMetrics supports rollup result caching keyed by expression/window/step/tag filters; recording rules precompute named expressions. These sources do not directly verify promshim caching arbitrary evaluated subtrees across different query ASTs. Mark “different queries sharing heavy subtrees” as promshim-specific and require a cache-key/equivalence proof.

> “For promshim, exact evaluated-subtree caching is safer than algebraic rewrite caching because promshim is documented as a PromQL compatibility layer over ClickHouse-backed metrics [35].”

**[W3] MAJOR:** Directionally reasonable, but “safer” still requires a precise cache-key and equivalence contract. Exact cached values can be wrong if keyed under an incomplete semantic context. Tie this claim to the cache-key proof, not only to the README compatibility goal.

> “Cache key completeness is the primary correctness risk; relevant inputs include query/AST, matchers, table/database, start/end/step, lookback, offset, native mode, settings profile, ClickHouse version, and feature flags [17, 22, 26, 28].”

**[Q4]:** Good list, but likely not complete. Consider adding promshim code/IR version, Prometheus parser/semantic version, request `lookback_delta`, response `limit` behavior if truncation can interact with cached subtrees, tenant/auth context if applicable, and whether the cache stores warnings/infos/histogram annotations as part of exactness.

> “Repeated selective historical PromQL queries can benefit from condition-cache hits without the freshness risks of result caching [29, 33].”

**[W4] MAJOR:** “Without the freshness risks” is too absolute. ClickHouse condition cache avoids final result reuse and is therefore less exposed to result staleness, but the docs still constrain it to analyzer-enabled, repeated-filter, mostly immutable workloads, and the cache is stateful/non-retained across restarts. Say “with lower final-result staleness risk than query-result caching.”

> “Cold/control runs show misses or no benefit [7, 29].”

**[Q5]:** A cold/control design also needs to isolate OS/page cache, mark cache, ClickHouse query cache, and benchmark ordering. A miss in `QueryConditionCacheHits` does not prove no other cache warmed the workload.

> “For one-to-one arithmetic binary expressions with identical common equality matchers and no modifiers, pushing filters into both selector sides can reduce scan work while preserving output [21, 30].”

**[W5] MAJOR:** If the matchers are already identical on both selector sides, pushing them again does not reduce scan work. If the intended optimization is to copy a matcher from one side to the other, or from an enclosing common condition into both sides, state the exact before/after. The VictoriaMetrics source handles many more cases and modifiers; the Prometheus operator docs show why preserving output labels and metric-name behavior is nontrivial.

> “VictoriaMetrics MetricsQL optimizer pushes common label filters into metric selectors across binary expressions under safety checks [30].”

**[W5] MAJOR:** Source [30] supports that VictoriaMetrics does this, but it is not a proof that the narrower promshim rule is correct under Prometheus semantics. Add a promshim-specific proof obligation and differential tests, especially for metric-name dropping, `on`, `ignoring`, group modifiers, set operators, bool comparisons, label mutations, absent functions, and histograms.

> “For high-cardinality associative aggregation queries, hash-sharded execution can reduce wall-clock latency by parallelizing work, even if total rows read do not fall [21, 32].”

**[W6] MAJOR:** Mimir’s source supports query sharding in a distributed querier architecture, not necessarily external fan-out to a single ClickHouse backend. Before using this as a promshim candidate, require a baseline showing ClickHouse’s own parallelism is insufficient or saturated and that external fan-out does not merely increase backend load.

> “Opt in to `N`-way sharding for `sum by (...) (selector)` and `count by (...) (selector)` on a high-cardinality fixture [21, 32]. Sweep `N={2,4,8}` and measure total ClickHouse work, latency, and correctness [5, 6, 7, 21, 32].”

**[W6]/[Q6] MAJOR:** Add controls for ClickHouse `max_threads`, `EXPLAIN PIPELINE`, backend CPU/load, and total query count. Otherwise a wall-clock win could be a resource-overcommit artifact rather than an efficient plan.

> “VictoriaMetrics has a rollup-result cache keyed by expression/window/step/tag filters and skips too-recent data, but public VictoriaMetrics issues report wrong answers from rollup rewrites involving `rate`/`increase` and `offset` [26, 27, 28].”

**[W8] MINOR:** This is acceptable as cautionary evidence, but GitHub issues are bug reports, not broad comparative evidence. Phrase as “reported failure modes show why promshim should avoid algebraic rollup rewrites until proven,” not as a general indictment of rollup caches.

> “Expected signal: cache hit/miss/skip metrics, lower p50/p95 on repeated historical queries, fewer ClickHouse reads or less local CPU, and unchanged strict/reference results [22, 23, 24, 25, 26].”

**[W7] MINOR:** Better: “support would require cache hit/miss/skip metrics…” This avoids implying those signals are expected before an experiment exists.

> “Expected signal: warm runs show `QueryConditionCacheHits`, fewer selected/read rows or lower duration, and no correctness drift [7, 29].”

**[W7] MINOR:** “Expected signal” is fine in the local plan’s terminology, but for verification it should be clear these are acceptance criteria, not claims already shown. Also, selected/read row reductions may not always appear if condition-cache effects are reflected in different granule/mark counters; specify the exact ClickHouse counters to inspect.

## Sources

Additional sources inspected during this verification:

- Local: `.pi/plans/layered-optimization-iteration/README.md`
- Local: `.pi/plans/layered-optimization-iteration/01-candidate-ranking.md`
- Local: `docs/optimizer-contracts.md`
- Local: `.pi/skills/measuring-ch-optimizations/SKILL.md`
- Local: `docs/optimization-rollout.md`
- Local: `internal/promshim/storage/settings_profile.go`
- Local: `scripts/ch-explain.sh`
- Local: `scripts/ch-profile-capture.sh`
- Local: `internal/promshim/local/planner_rangefunc.go`
- Local: `/home/fl/code/external/datafusion/datafusion/optimizer/src/optimizer.rs`
- Local: `/home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs`
- https://clickhouse.com/docs/operations/system-tables/query_log
- https://clickhouse.com/docs/operations/system-tables/events
- https://clickhouse.com/docs/operations/query-condition-cache
- https://clickhouse.com/docs/en/operations/query-cache
- https://clickhouse.com/docs/en/optimize/prewhere
- https://prometheus.io/docs/prometheus/latest/querying/basics/
- https://prometheus.io/docs/prometheus/latest/querying/functions/
- https://prometheus.io/docs/prometheus/latest/querying/operators/
- https://prometheus.io/docs/prometheus/latest/querying/api/
- https://prometheus.io/docs/prometheus/latest/configuration/recording_rules/
- https://grafana.com/docs/mimir/latest/references/architecture/components/query-frontend/
- https://grafana.com/docs/mimir/latest/references/architecture/query-sharding/
- https://thanos.io/v0.40/components/query-frontend.md/
- https://cortexmetrics.io/docs/architecture/#query-frontend
- https://arxiv.org/html/2505.10560v1
- https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/master/app/vmselect/promql/rollup_result_cache.go
- https://github.com/VictoriaMetrics/VictoriaMetrics/issues/10098
- https://github.com/VictoriaMetrics/VictoriaMetrics/issues/9762
- https://raw.githubusercontent.com/VictoriaMetrics/metricsql/master/optimizer.go
- https://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/PushProjector.html
- https://arrow.apache.org/datafusion/library-user-guide/query-optimizer.html
