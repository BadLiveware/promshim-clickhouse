# IR/query optimizer research notes for promshim optimization ideas

Task status: **degraded/direct**. The delegated researcher crashed while attempting PDF parsing despite the workflow instruction to avoid PDFs. The lead continued from web search results, fetched HTML/doc pages, and local source checkouts.

These are design-inspiration notes, not authoritative recommendations.

## Evidence table

| # | Source | URL/path | Key claim | Type | Confidence |
|---|---|---|---|---|---|
| 1 | Apache DataFusion docs — Query Optimizer | https://arrow.apache.org/datafusion/library-user-guide/query-optimizer.html | DataFusion exposes a logical optimizer pipeline and examples of optimizer passes such as filter elimination, CSE, and projection pushdown in explainable logical plans. | primary docs | high |
| 2 | Apache DataFusion blog — Optimizing SQL/DataFrames Part 2 | https://datafusion.apache.org/blog/2025/06/15/optimizing-sql-dataframes-part-two/ | DataFusion frames projection/filter pushdown as doing less work earlier and highlights that optimizers need careful rule ordering and expression movement. | project blog | medium-high |
| 3 | DataFusion local source — optimizer rule order | /home/fl/code/external/datafusion/datafusion/optimizer/src/optimizer.rs | The default optimizer comments say rule order matters, new rules are applied to all queries and are expensive, and contributors should prefer extending existing rules or aggressive no-op paths. | local primary source | high |
| 4 | DataFusion local source — CommonSubexprEliminate | /home/fl/code/external/datafusion/datafusion/optimizer/src/common_subexpr_eliminate.rs | CSE computes repeated expressions once and rewrites later expressions to use the computed value; current rule scope is common subexpressions within a single logical plan. | local primary source | high |
| 5 | DataFusion local source — PushDownFilter | /home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs | Filter pushdown moves filters earlier to avoid redundant work but must not cross operators such as limits when doing so would change results. | local primary source | high |
| 6 | Calcite Javadoc — PushProjector | https://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/PushProjector.html | Projection pushdown depends on expression input references; expressions can be pushed to an input if they depend only on that input, and preserved expressions are pushed intact or not at all. | primary docs | high |
| 7 | Calcite Javadoc — ProjectFilterTransposeRule.Config | http://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/ProjectFilterTransposeRule.Config.html | Calcite makes pushdown behavior configurable, including whether to push whole filter/project expressions and which expressions must be preserved. | primary docs | high |
| 8 | Calcite local source — ProjectFilterTransposeRule | /home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/ProjectFilterTransposeRule.java | The rule avoids some plan changes when they add redundant projects or trigger optimizer instability/infinite matches. | local primary source | high |
| 9 | promshim docs — optimizer contracts | docs/optimizer-contracts.md | promshim already requires stable rewrite names, preconditions, preserved invariants, expected physical signals, skipped reasons, and rollback controls when serving is affected. | local primary source | high |
| 10 | promshim plan — iterative optimization search | .pi/plans/layered-optimization-iteration/README.md | The local plan now ranks candidates by benefit, breadth, evidence readiness, rollbackability, correctness risk, and implementation cost, preferring fundamental reusable improvements when risk/evidence are comparable. | local primary source | high |

## Candidate ideas

### 1. Optimizer rule budget and no-op precheck instrumentation

- **Source-backed note:** DataFusion's optimizer comments warn that adding a new rule is expensive because it runs on all queries, rule order matters, and new rules should have aggressive no-op paths when possible [3].
- **Promshim layer:** IR optimizer and explain/artifact metadata.
- **Expected proof signal:** A per-query rewrite trace reports which rules were skipped by cheap prechecks, how many nodes were inspected, which rules changed the IR, and optimizer time. Later performance experiments can distinguish rewrite cost from execution savings.
- **Correctness risks:** Low if observational. Avoid unbounded labels or raw PromQL in metrics/artifacts.
- **First experiment shape:** Add bounded optimizer-pass counters and timings to explain output/artifacts for existing passes only. Use this to rank future IR rewrites by pass cost and hit rate before adding new passes.

### 2. Fundamental projection-requirement analysis before SQL rendering

- **Source-backed note:** DataFusion and Calcite both treat projection pushdown as an early-work-reduction pattern; Calcite's PushProjector reasons about which fields/expressions are required by an input and preserves expressions that should not be split [2], [6], [7].
- **Promshim layer:** IR metadata and native SQL lowering.
- **Expected proof signal:** Generated SQL and query-log settings show fewer label columns/materialized arrays carried through native subqueries; ClickHouse `FunctionExecute`, `read_bytes`, transfer width, or row-width-related counters move for aggregation/histogram/vector paths.
- **Correctness risks:** PromQL label production is not relational column projection. `without`, selection aggregations, label mutation, vector matching, histogram buckets, and `__name__` handling can require labels that are not obvious from output grouping [9].
- **First experiment shape:** Extend IR analysis with a required-labels provenance table for one safe family already supported by native SQL. Emit skipped reasons for unsafe families before changing SQL. Then compare a focused aggregation/histogram query before/after with `ch-explain.sh`.

### 3. Expression dependency classifier for safe pushdown/reuse

- **Source-backed note:** Calcite PushProjector pushes an expression to an input when the expression depends on no other inputs, and preserved expressions are pushed intact or not pushed at all [6]. DataFusion filter pushdown similarly checks whether moving a filter across an operator preserves results, and it does not push filters past a limit when that changes results [5].
- **Promshim layer:** IR rewrite, native SQL lowering, subtree pushdown.
- **Expected proof signal:** More candidate subexpressions are classified as safe-to-reuse or safe-to-pushdown, with bounded rejection reasons for vector matching, set ops, histogram, offset, subquery, or label mutation. Execution proof is fewer scans/round trips or lower transfer.
- **Correctness risks:** PromQL dependency is semantic, not just column-based. Time bounds, lookback, staleness, offsets, vector matching, and label-set production are dependency inputs [9].
- **First experiment shape:** Build an IR helper that returns `(dependsOnTime, dependsOnLabels, dependsOnVectorMatching, dependsOnHistograms, dependsOnStepGrid, requiredLabels)` for candidate nodes. Use it first only to explain why existing repeated-subexpression reuse applies or skips.

### 4. CSE candidate generation across logical nodes with strict placement rules

- **Source-backed note:** DataFusion's CSE rule computes repeated expressions once and rewrites later expressions to reference the computed value [4]. DataFusion says its current CSE only eliminates common subexpressions within a single logical plan [4].
- **Promshim layer:** IR/native SQL and local executor.
- **Expected proof signal:** Duplicate native SQL fragments, local range-function evaluations, or ClickHouse round trips decrease for repeated subtree families; `read_rows`, `SelectedMarks`, `FunctionExecute`, and `X-Promshim-CH-Roundtrips` move.
- **Correctness risks:** Avoid algebraic equivalence. Only exact subtree reuse should be considered initially, and keys must include eval time/range/step/lookback/offset/native mode/settings profile when relevant [9].
- **First experiment shape:** Add a reusable CSE eligibility service used by both native SQL and local executor paths, replacing one-off repeated-expression checks. First run it in explain-only mode to compare current decisions with potential broader safe matches.

### 5. Optimizer instability guardrails

- **Source-backed note:** Calcite's ProjectFilterTransposeRule skips some transformations when they would add redundant projects or trigger infinite rule matches; DataFusion optimizer comments recommend no-op paths and careful rule ordering [3], [8].
- **Promshim layer:** IR optimizer pass scheduler.
- **Expected proof signal:** Rewrite fixpoint iteration count is bounded; artifacts show skipped reasons such as `would_reintroduce_projection`, `no_semantic_gain`, or `max_iterations` without changing query results.
- **Correctness risks:** Low if it only prevents rewrites. A too-broad guard could hide valid optimizations, so skipped-rewrite counters should be inspectable.
- **First experiment shape:** Add a generic pass application harness that records before/after fingerprints, pass iteration counts, and no-op/skipped reasons for existing passes. Reject any pass that cycles or changes only cosmetic metadata.

### 6. Cost model feature extraction from IR rather than query-family strings

- **Source-backed note:** Calcite and DataFusion both operate over structured relational/expression plans rather than substring family checks; promshim already documents physical hints such as estimated rows, samples, output points, candidate route eligibility, settings profile, and observed ProfileEvents as non-semantic annotations [9].
- **Promshim layer:** CBE calibration and candidate ranking.
- **Expected proof signal:** Calibration groups explain recommendations with feature fields such as range width, output points, selector count, aggregation grouping count, histogram bucket use, and vector-matching cardinality rather than only family labels. Prediction errors shrink or fallback reasons improve.
- **Correctness risks:** Physical hints must remain non-semantic and stale/missing hints must choose strict/reference [9].
- **First experiment shape:** Add feature extraction to calibration output without changing serving: per-class medians for window seconds, output points, selector count, grouping label count, and observed rows/bytes. Use it to split misleading broad families before changing CBE routing.

## Coverage status

- **Checked:** DataFusion docs/blog, DataFusion local optimizer sources, Calcite Javadocs, Calcite local rule sources, promshim optimizer contracts, and the local iterative optimization plan.
- **Blocked:** The delegated T3 researcher crashed in PDF parsing (`Promise.try is not a function` from `unpdf/pdfjs`). The lead did not fetch PDFs and continued from HTML/docs/local source.
- **Uncertain:** I did not inspect every optimizer rule in DataFusion/Calcite. Findings should be used as design prompts for promshim experiments, not proof that a specific rewrite is safe.

## Sources

1. Apache DataFusion, Query Optimizer docs — https://arrow.apache.org/datafusion/library-user-guide/query-optimizer.html
2. Apache DataFusion blog, Optimizing SQL and DataFrames Part 2 — https://datafusion.apache.org/blog/2025/06/15/optimizing-sql-dataframes-part-two/
3. DataFusion local source, optimizer rule order — /home/fl/code/external/datafusion/datafusion/optimizer/src/optimizer.rs
4. DataFusion local source, CommonSubexprEliminate — /home/fl/code/external/datafusion/datafusion/optimizer/src/common_subexpr_eliminate.rs
5. DataFusion local source, PushDownFilter — /home/fl/code/external/datafusion/datafusion/optimizer/src/push_down_filter.rs
6. Apache Calcite Javadocs, PushProjector — https://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/PushProjector.html
7. Apache Calcite Javadocs, ProjectFilterTransposeRule.Config — http://calcite.apache.org/javadocAggregate/org/apache/calcite/rel/rules/ProjectFilterTransposeRule.Config.html
8. Calcite local source, ProjectFilterTransposeRule — /home/fl/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/ProjectFilterTransposeRule.java
9. promshim optimizer contracts — docs/optimizer-contracts.md
10. promshim iterative optimization search plan — .pi/plans/layered-optimization-iteration/README.md
