# Native SQL optimization sweep loop

## Objective

Continuously improve promshim runtime outcomes with evidence-driven optimizations.

Primary optimization objective is **overall expected value (EV)** across outcomes that make promshim materially better to run and use in practice, not one benchmark counter in isolation.

Treat raw metrics as evidence for product/operator value: faster answers, cheaper ClickHouse load, safer concurrency, fewer timeouts, more predictable behavior, simpler operations, and preserved PromQL correctness.

EV/usability checklist:
- **Latency:** end-to-end wall time, ClickHouse query time/`RealTime`, p50/p95/p99 when available, cold vs warm behavior when relevant.
- **CPU:** ClickHouse `UserTime`/`SystemTime`, `FunctionExecute`, aggregation/sort/join/hash counters, and promshim/local CPU when the change moves work out of ClickHouse.
- **Memory:** peak ClickHouse memory, promshim/local memory when applicable, hash/join/aggregation state size, spill risk, and concurrency headroom.
- **I/O and scan work:** read/selected rows, read/selected bytes, marks/granules/parts when available, repeated scans, duplicated selector work, and prewhere/index effectiveness.
- **Data movement and cardinality:** rows/bytes returned from ClickHouse to promshim, intermediate row counts, emitted/result rows, label/tag width, and network/serialization cost.
- **Query execution shape:** number of ClickHouse queries/fragments/round trips, SQL complexity, joins, subqueries, ASOF/range-grid shape, materialization points, and opportunities for shared work/CSE.
- **Planner/operational cost:** planning/rendering overhead, explainability, strategy stability, cap behavior, timeout risk, failure modes, and reversibility.
- **Production usability:** dashboard/query responsiveness, tail-latency predictability, capacity/concurrency headroom, reduced ClickHouse cluster pressure, lower cost to serve common workloads, fewer operator surprises, and maintainable/debuggable plans.
- **Correctness/semantic risk:** PromQL equivalence, counter/rate reset handling, staleness, histogram semantics, label preservation, ordering/tie behavior, and compatibility with compliance expectations.

Accept/reject based on material application value. A regression in one metric can be acceptable if net EV is clearly positive for real promshim use and the regression is understood, bounded, and documented. Do not accept a benchmark-only win that makes the app less reliable, less predictable, harder to operate, or semantically riskier without a compelling and explicit tradeoff.

## Current focus

- **Only tier 2/native SQL is in scope for now.**
- The optimization boundary is any tier-2 query or tier-2 **query shape** with plausible EV upside.
- **Query shape** means a repeatable structural pattern that appears across multiple queries, e.g. a PromQL/operator + logical/native plan + SQL physical-shape pattern. Shape-level optimizations are preferred because they can improve many queries and usually have higher EV than one-off query-specific changes.
- Do not work on tier 1 delegation, tier 3 local-pushdown, tier 4 full-local, or CBE routing except as incidental context needed to understand a tier-2/native-SQL change.
- Do not run broad/routine non-tier-2 measurements. Historical tier-3/tier-4 artifacts can be consulted only as context; they are not the current optimization target.

## Current boundary state

The previous mixed-tier boundary has been closed. New iterations should continue from tier-2/native-SQL query or query-shape selection.

Fresh tier-2 processing baseline artifact:
`harness/artifacts/bench/sweeps/20260429-iter5-tier2-baseline-processing-15m/`

Latest accepted attempt:
`.pi/loops/native-sql-optimization-sweep/attempts/20260429-instant-gauge-range-late-tags.md`

Committed: `cbc9da1 perf: late materialize instant gauge tags`.

Result: instant scalar gauge range functions such as `sum by (...) (avg_over_time(gauge[window]))` now compute the per-series scalar from id-only matched rows, then join tags once per output series before outer aggregation. Representative `processing_avg_memory_6h_by_job_type_instant_7d` improved Shim p50 -14.57%, CH p50 -14.16%, Mem p95 -38.78%, UserTime -17.67%, RealTime -16.26%; read bytes were flat and join rows +0.07%, with FunctionExecute +32.19% from the extra tag lookup stage. Follow-up broader validation in `.pi/loops/native-sql-optimization-sweep/attempts/20260429-instant-gauge-range-broader-validation.md` confirmed `sum_over_time` and `max_over_time` representatives also improved ~15% wall time with lower CPU/memory and bounded join overhead, so broad `sum/avg/max` coverage remains justified.

Prior accepted attempt:
`.pi/loops/native-sql-optimization-sweep/attempts/20260429-instant-range-aggregation-label-projection.md` (`59de0fa perf: narrow instant range aggregation tags`).

Earlier accepted attempt:
`.pi/loops/native-sql-optimization-sweep/attempts/20260429-cumulative-avg-id-only-boundaries.md` (`79e4154 perf: reduce cumulative avg_over_time scans`).

Latest rejected attempt:
`harness/artifacts/bench/standalone/20260429-iter10-histogram-native-grid-child/` and `.pi/loops/native-sql-optimization-sweep/attempts/20260429-histogram-range-native-grid-child-aggregation.md`.

Prior rejected attempt:
`harness/artifacts/bench/standalone/20260429-iter8-native-grid-sum-rate-late-tags/` and `.pi/loops/native-sql-optimization-sweep/attempts/20260429-native-grid-sum-rate-late-tags.md`.

Historical retained work exists in external artifacts and prior attempt notes; do not expand it unless it directly supports a tier-2/native-SQL optimization.

Boundary validation (latest fresh run):
- `bash -n scripts/run-bench.sh scripts/run-sweep.sh`
- `go test -count=1 ./internal/promshim/storage ./internal/promshim/native/renderer ./internal/promshim/native ./internal/promshim/local ./internal/promshim ./internal/promharness ./cmd/promshim-bench`
- `git diff --check`
- `./scripts/run-compliance.sh --skip-native`

Latest compliance signal: prefer-mode clean (`537 passed + 1 accepted tolerance, 0 failures`).

## Gate decisions in force

- Tier-2/native-SQL work is the only active optimization lane.
- `processing_sum_rate_1h_by_job_range_7d` remains eligible only if there is a **new structural tier-2 hypothesis** beyond the previously rejected tag-projection/thread-tuning and native-grid late-tag-materialization variants.
- Do not pursue local-pushdown/CBE expansion for `processing_sum_rate_5m_by_job_range_24h_7d` in this focus period; it is outside the current tier-2 boundary.
- Do not retry native-grid `sum(rate(...))` late tag materialization unless there is a materially different structural reason; focused evidence showed no scan/join reduction and a +2.61% CH p50 / +2.48% UserTime regression on the most expensive row.
- Do not retry histogram range native-grid child aggregation as implemented in iteration 10 unless a follow-up can convert the filter-row reduction into wall-time or memory improvement; evidence showed filter rows -99.31% and UserTime -2.51%, but shim p50 +0.23%, CH p50 +0.28%, memory +0.26%, and read/join rows unchanged.
- Do not repeat instant aggregation-over-range-function label projection unless there is a materially different structural hypothesis; the accepted implementation already narrows labels while preserving per-series rate semantics by grouping on series id.
- Do not retry instant rate late tag materialization as implemented in iteration 12. Evidence was mixed: 6h/job,mode improved modestly (Shim p50 -2.88%, CH p50 -3.44%, Mem p95 -37.97%, UserTime -13.66%), but 1h/job regressed (Shim p50 +9.54%, CH p50 +8.45%, Mem p95 +384.73%) and `FunctionExecute` rose about 19% on both rows due the extra tag lookup stage.
- Do not repeat instant scalar gauge range-function late tag materialization unless there is a broader shape validation or materially different structural hypothesis; the accepted implementation already covers `sum_over_time`, `avg_over_time`, and `max_over_time` leaf matrix-selector instant fast paths.
- Do not retry direct range-window aggregate id-only matched-series split for `max_over_time` as implemented in iteration 15 without a materially different structural hypothesis. Baseline and post both failed strict native warmup with `HTTP 502` for `processing_max_memory_1h_by_job_type_range_24h_7d`, yielding no served-outcome improvement.
- Do not retry the overlapping-window `max_over_time` direct-aggregate preference override as implemented in iteration 17 without materially different structural evidence. Post-change strict native warmup still failed with the same `HTTP 502`, so served-outcome reliability did not improve.
- Do not retry the overlapping-window `max_over_time` window-join preference override as implemented in iteration 18 without materially different structural evidence. Post-change strict native warmup still failed with the same `HTTP 502`, so served-outcome reliability did not improve.
- Do not retry the native-grid rows late-series-join rewrite (`d.id IN (matchedSeries)` + late tag join) as implemented in iteration 19 without a concrete correctness fix and targeted semantics evidence. It improved row-level benchmark counters but caused broad compliance regressions (`32 unexpected 502 failures`).
- A scoped late-join rewrite in the native-grid **sum aggregation** path (`BuildRangeNativeGridSelectorSumAggregationQuerySQLWithFinalTags`) is now accepted with clean compliance and should be treated as current baseline behavior.
- Iteration 23 validation (no new code) confirms that optimization carries over to `processing_sum_rate_1h_by_job_range_7d` with large gains (p50 ~-32.2%, read rows ~-53.8%, read bytes ~-50.8%).
- Iteration 25 validation (no new code) shows that this optimization does not positively carry over to `processing_histogram_quantile_1h_range_24h_7d` (p50 regressed ~+13.8%); do not assume cross-shape benefit from sum-rate wins.
- Do not retry the native-grid sum-aggregation id/tag matched-series split from iteration 27 as-is; effect was noise-level (~-0.13% p50) with no meaningful resource change.
- Do not retry the explicit join-filter replacement for the sum-rate 1h native-grid path from iteration 28 as-is. It caused severe regression (p50 ~+46.4%, join probe ballooned to ~465.5M).
- Iteration 29: deferred code changes after candidate scan; no safe materially new structural lever identified from current evidence. Next attempt should start from fresh explain/profile-derived operator-elimination hypothesis, not another micro-variant of already rejected shapes.
- Iteration 30: repeated explain/profile scan confirmed high residual CH cost on `processing_sum_rate_1h_by_job_range_7d` but still no safe materially new lever from current evidence; defer until a narrower operator-specific hypothesis is identified.
- Iteration 32: operator-focused shortlist across remaining heavy rows still found no candidate meeting safety + novelty + p50-upside criteria; defer and require finer operator decomposition before next code edit.
- Iteration 33: deferred again pending benchmark-visible physical-decision telemetry/explain capture for heavy rows; next code attempt must be grounded in strategy-level evidence (not inferred from aggregate counters alone).
- Iteration 34: verified bench harness currently lacks per-row physical-decision telemetry despite rich counter capture; defer code optimizations until this evidence channel exists to avoid further speculative/noisy variants.
- Iteration 35: added `X-Promshim-Physical-Decisions` header emission + bench artifact capture (`physicalDecisions` in v2 shim result rows). Telemetry prerequisite is now satisfied for strategy-level hypothesis selection.
- Iteration 37: telemetry-directed no-thread-cap variant for cumulative avg path reached intended decision pattern (`query_settings=no_thread_cap`) but delivered no p50 gain (~+0.33% vs baseline); do not retry as-is.
- Iteration 38: telemetry snapshot for `processing_sum_rate_5m_by_job_range_24h_7d` confirms current good decision pattern `fused_range_aggregation=native_grid_sum_aggregation,query_settings=set_max_threads`; use this as a concrete reference when evaluating future strategy-level changes.
- Iteration 39: telemetry-directed cumulative-avg ASOF thread-guardrail variant (`query_settings=set_max_threads`) severely regressed p50 (~+51.5%); do not retry as-is.
- Iteration 40 accepted: fused sum-rate 5m path now prefers `query_settings=no_thread_cap` (telemetry-verified) with large p50 win and clean compliance. Treat as current baseline for this shape.
- Iteration 42 validation: same no-thread-cap fused-rate decision transfers to 1h sum-rate sibling row with large p50 improvement (~-71.8% vs iter23 reference).
- Iteration 43 telemetry refresh: full processing corpus rerun confirms sum-rate improvements hold; primary remaining bottleneck is `processing_avg_memory_1h_by_job_type_range_24h_7d` (~6771ms p50). Next edits should target avg-memory with materially new structural hypothesis only.
- Iteration 44 defer: shortlisted avg-memory structural candidate is selector-id source dedup/hoist (currently repeated identical `SELECT DISTINCT id` tags scans). No safe micro-change applied yet; implement as dedicated bounded renderer/planner slice next.
- Iteration 45 rejected/reverted: cumulative-avg matched-series CTE hoist attempt triggered served failure (`force_supported/strict: warmup 1: HTTP 502`) on target row; no retry of this exact CTE shape.
- Iteration 47 rejected/reverted: diagnostic final-series-only CTE hoist also reproduces `HTTP 502`; treat CTE-hoist family for this avg-memory cumulative path as unsafe until deeper CH compatibility evidence exists.
- Iteration 48 rejected/reverted: histogram-range rate no-thread-cap probe produced only noise-level shift (~-1.3%) and no thread-setting telemetry evidence for that row; no retry as-is.
- Iteration 49 defer/pivot: no safe high-EV code change selected; avg-memory path remains unsafe (recent `HTTP 502` family), and histogram-range tuning is telemetry-blind. Next step is histogram-path decision telemetry prerequisite before more perf probes.
- Iteration 50 accepted: histogram-range row now emits path telemetry (`histogram_child_path=fused_range_aggregation_child_le_only`), enabling decision-grounded tuning for this shape.
- Do not retry the cumulative-avg states-stream `d.id IN (matchedSeries)` rewrite from iteration 22 as-is: despite large scan/join reductions it regressed p50 latency (+3.8%) and memory (+0.7%) on the representative row.
- Do not retry the cumulative-avg states-subquery `ORDER BY` removal from iteration 24 as-is: p50 still regressed (+2.4%) with no material scan/join win.

## Operating rules

- Correctness is mandatory.
- Do not relax caps or change served routing without explicit evidence and intent.
- Keep attempts small, measurable, and reversible.
- Prefer structural optimizations over parameter nudges.
- Prefer putting reusable optimization decisions in the existing optimizer pass structures rather than inline in renderers/storage SQL builders:
  - Use `internal/promshim/logical/opt/` for PromQL/logical-IR canonicalization, algebraic rewrites, and semantics-preserving rewrites that should apply before native analysis.
  - Use `internal/promshim/native/optimizer.go` and its pass pipeline for native-lowering/physical-shape optimization, predicate/projection pushdown, duplicate-source detection, late materialization, and optimizer-report/explain-visible decisions.
  - Use renderer/storage code for implementing the chosen SQL primitive or narrow rendering support, not as the first place to hide optimization policy.
  - Consolidating existing optimization policy out of specific rendering/storage arms into these general optimizer shapes is encouraged when it preserves behavior and simplifies the specific paths.
  - If an optimization truly belongs in renderer/storage because it is a local SQL emission detail, document why it cannot live in a pass and keep the policy visible in physical decisions or optimizer reports.
- Commit accepted changes while current user authorization remains active. If commit scope or permission becomes ambiguous, stop and ask.

## Per-iteration protocol

Each Ralph iteration should complete one meaningful tier-2/native-SQL optimization attempt for one query or query shape.

1. **Measure or validate baseline**
   - Measure the current behavior unless a previous artifact is still valid for the exact code, data profile, query shape, mode, routing policy, and benchmark setup.
   - If reusing prior measurement, cite the artifact and explain why it is still valid.
   - Baseline evidence should cover the EV/usability checklist above where available: latency, CPU, memory, scan I/O, data movement/cardinality, execution shape, planner/operational cost, production usability, and correctness risk. At minimum capture wall time, memory, read/selected rows+bytes, ClickHouse CPU (`UserTime`, `RealTime`, `FunctionExecute` or relevant counters), result/emitted rows, strategy, and SQL/explain shape.

2. **Pick one query/query shape**
   - Choose the highest-EV tier-2/native-SQL candidate available.
   - State the hypothesis before editing: what structural change should improve actual promshim use, which metrics demonstrate that improvement, and what regressions would be acceptable or unacceptable.
   - Prefer repeatable query shapes that span multiple queries. Query-specific work is acceptable only when EV is high enough to justify narrower impact, or when it is a stepping stone toward a broader shape optimization.

3. **Try to optimize**
   - Make the smallest safe, reviewable change.
   - First decide where the optimization belongs:
     - logical rewrite in `internal/promshim/logical/opt/`,
     - native optimizer pass in `internal/promshim/native/optimizer.go`,
     - consolidation of existing renderer/storage-arm optimization policy into one of those general optimizer shapes,
     - or only a renderer/storage SQL primitive when a pass has already made the decision.
   - Keep correctness boundaries explicit, especially around PromQL semantics, counter/rate reset handling, histogram semantics, label preservation, and fallback/strategy behavior.
   - Add or update focused tests with the change.

4. **Validate and measure after**
   - Run focused correctness tests for touched packages.
   - Run compliance when native SQL semantics, renderer behavior, or served strategy can affect PromQL results.
   - Re-measure with the same benchmark/query setup as the baseline unless intentionally testing a broader corpus.

5. **Accept/reject/defer with an artifact**
   - Always record an attempt artifact under `.pi/loops/native-sql-optimization-sweep/attempts/<attempt-id>.md`.
   - For accepted changes, record baseline vs post metrics and why net EV is positive for real promshim usage, not only for the benchmark row.
   - For rejected changes, revert the code unless it is useful standalone instrumentation, and record why evidence did not justify keeping it.
   - For deferred/split work, record the missing evidence or prerequisite.

6. **Commit accepted coherent changes**
   - Commit accepted changes in semantic groups, not dump commits.
   - The commit message must stand alone without chat or external artifacts.
   - For runtime optimizations, include measured pre/post resources and percent change in the commit body for the relevant EV metrics: wall time, memory, read rows/bytes, CPU/counters, result/intermediate rows, data movement, round trips/fragments, and any meaningful regressions.
   - Include validation evidence in the commit body.
   - Do not include rejected prototypes, unrelated cleanup, or unstable scratch artifacts.

7. **Reset and continue**
   - Update this loop file with only compact current state and next focus; keep bulky history in attempt artifacts.
   - Reset `.ralph/native-sql-optimization-sweep.md` checklist/pointer for the next iteration.
   - Call `ralph_done` only after the entire per-iteration checklist is complete: baseline captured, candidate selected, optimization attempted or explicitly rejected/deferred, validation/measurement done, artifact written, accepted changes committed if any, and checklist reset.
   - Do **not** call `ralph_done` merely because a long-running measurement was started or is still running. While waiting, either do independent checklist work that does not compromise measurement isolation, or simply wait for the process to complete without advancing the loop.

## Next action required

Start the next Ralph iteration by selecting a new tier-2/native-SQL query or query shape with measurable EV upside. Do not repeat cumulative `avg_over_time` boundary fusion/id-only materialization, instant aggregation-over-range-function label projection, instant rate late tag materialization, instant scalar gauge range-function late tags, direct range-window aggregate id-only split for `max_over_time`, overlapping-window `max_over_time` direct-aggregate preference override, overlapping-window `max_over_time` window-join preference override, or generic native-grid rows late-series-join rewrite unless a materially different structural hypothesis with explicit correctness safeguards appears.

Current accepted optimization includes the sum-aggregation native-grid late-join scope; future work should build from this baseline rather than re-proving it.

Safety rule: if an in-progress hypothesis is unsafe to continue (missing correctness discriminator, high blast radius, or failed guardrail evidence), defer/split it immediately and pivot in the same loop workflow to a different candidate. Do not block on waiting for additional user choice prompts.

---

## Historical record policy

Historical per-iteration narrative is intentionally reset in this canonical file.
External benchmark/explain/compliance artifacts are the source of historical detail.
