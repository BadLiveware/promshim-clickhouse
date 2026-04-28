# Native SQL optimization sweep loop

## Objective

Continuously improve promshim's PromQL → logical IR → native SQL and fallback execution surface by repeatedly finding high-expected-value optimization opportunities, implementing the safest useful candidate, measuring correctness and runtime signals, and keeping or rejecting each attempt based on evidence. The overarching objective is to make promshim look like a better solution by being a better solution. We do this by being fast and efficient, and producing fast and efficient sql, and choosing the appropriate execution tier balancing speed/resource usage with easy of running promshim.

Primary metric directions:

- Lower end-to-end query wall time for representative native-lowered PromQL.
- Lower ClickHouse CPU work, especially `UserTimeMicroseconds`, `RealTimeMicroseconds`, and high-volume function/profile counters.
- Lower ClickHouse memory usage and peak `MemoryTrackerUsage` for heavy shapes.
- Lower scanned/selected rows and bytes when a storage pushdown or pruning optimization is claimed.
- Preserve or improve strategy selection: avoid silent fallback from `native_sql` to lower tiers unless explicitly chosen by a later CBE policy.
- Improve explainability of physical choices, optimizer decisions, and fallback reasons.

The loop is open-ended. It does not finish when the seed backlog is exhausted; each accepted/rejected attempt should update the surface map and replenish the next hypotheses from evidence.

## Scope

In scope:

- Native SQL lowering, native physical strategy selection, renderer/storage SQL shapes, typed SQL-builder evolution where touched by an optimization, local/subtree execution when it competes with native SQL, and future CBE candidate instrumentation.
- Query-shape families across PromQL operators, range functions, aggregations, subqueries, histograms, label manipulation, joins/binops, selector matchers, and common dashboard-style expressions.
- General optimizations first, query-specific optimizations when they have strong expected value or unblock a common dashboard pattern.
- **Primary priority: measurable runtime improvements** (latency/CPU/memory/scan reductions with corroborating evidence).
- Measurement/tooling/diagnostic work is allowed and often necessary, but should primarily serve near-term measurable optimization attempts rather than become the main output repeatedly.

Out of scope unless explicitly approved in a later attempt:

- Broad semantic coverage expansion unrelated to an optimization candidate.
- Production rollout or external deployment changes.
- Cross-request caches or persistent query-result caches.
- Global CBE routing changes before correctness, observability, and candidate evidence are in place.
- Editing compliance expected failures to hide shim bugs.

## Guardrails

- Correctness is mandatory. Prefer reference-safe routes when estimates, costs, or semantics are uncertain.
- Do not broaden PromQL support opportunistically; new coverage must be tied to a measured optimization, correctness bug, or explicit user request.
- Do not move SQL text construction into `internal/promshim/native/physical/` or turn the physical layer into a SQL renderer.
- When adding or changing a native physical SQL shape, represent semantic/physical choices in typed plan or strategy structs first, then render through existing renderer/storage builders or narrowly evolved `sqlb` constructs.
- Do not benchmark long-range/profile data against compliance ports.
- Do not trust small wall-clock deltas alone; require ClickHouse `ProfileEvents`, query log, `EXPLAIN`, strategy histograms, or other low-variance evidence appropriate to the claim.
- Preserve generated/artifact hygiene: named benchmark/explain artifacts under `harness/artifacts/...`; durable loop context under `.pi/loops/native-sql-optimization-sweep/`; Ralph runtime state under `.ralph/` only.
- Avoid destructive operations unless predeclared safe. Benchmark-stack resets are allowed only when the attempt explicitly needs fresh benchmark data; compliance volume resets require explicit approval unless the user pre-approves during loop startup.
- **Infrastructure/diagnostic work budget:** infrastructure work is not disallowed; it is acceptable when it enables further improvements. However, the loop should prioritize attempts that deliver measurable execution-resource improvements (wall-time, absolute CPU, memory, scan/bytes) and avoid long runs of infra-only iterations without a concrete path to those outcomes.

## Evaluation protocol

Use the project playbooks before the matching actions:

- Read `.pi/skills/measuring-ch-optimizations/SKILL.md` before accepting any optimization claim, especially CSE, alias, pushdown, scan reduction, or small wall-clock deltas.
- Read `.pi/skills/running-sweep/SKILL.md` before benchmark setup, sweep runs, profile comparisons, or active-series changes.
- Read `.pi/skills/running-compliance/SKILL.md` before compliance runs or compliance-failure triage.

Default per-attempt evaluation sequence:

1. **Map** the candidate shape:
   - Identify PromQL query family, logical/native analysis shape, physical decisions, rendered SQL pattern, strategy, and current fallback behavior.
   - Use `scripts/ch-explain.sh` first for one-query diagnosis.
2. **Baseline** current evidence:
   - Capture `promshim-explain-summary.tsv`, `promshim-physical-decisions.tsv`, `query-log-summary.tsv`, `qN/query-clean.sql`, `qN/profile-events-top.tsv`, and relevant `EXPLAIN` artifacts.
   - For broader changes, capture a focused benchmark or sweep before editing.
3. **Select** one high-expected-value attempt:
   - Prefer broad shapes that affect multiple corpus rows or dashboards.
   - Prefer changes with clear correctness boundaries and measurable low-variance signals.
   - Split risky or unclear ideas into metadata/keying/instrumentation before SQL-shape changes.
4. **Implement** the smallest reviewable change:
   - Add or update tests before/with behavior changes.
   - Keep refactors separate from runtime behavior when that improves reviewability.
5. **Validate correctness**:
   - Run focused Go tests for changed packages.
   - Run compliance when renderer/native behavior changes materially.
6. **Measure runtime effect**:
   - Use `ch-explain.sh`, `run-bench.sh`, or `run-sweep.sh` according to scope.
   - Compare the signal required by the claim, not only p50 wall time.
7. **Decide**:
   - Keep, reject/revert, defer, or split based on the acceptance rules below.
8. **Record**:
   - Update this loop file with a compact attempt summary and artifact pointers.
   - Put bulky notes in `.pi/loops/native-sql-optimization-sweep/attempts/<attempt-id>.md`.
   - Commit accepted coherent changes when commit permission is active.

Useful commands and artifact conventions:

```bash
# Single-query diagnosis; choose ports/profile for the current attempt.
scripts/ch-explain.sh '<promql>' \
  --mode range \
  --range-seconds <seconds> \
  --step <seconds> \
  --eval-time 2026-03-14T21:45:42Z \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 --ch-user default --ch-pass otel \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/explain/<attempt-id>-before

# Benchmark readiness / setup should go through run-sweep.
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --dry-run --estimate --name <attempt-id>

# Focused benchmark after code changes; rebuild benchmark promshim first.
(cd harness/bench && docker compose up -d --build promshim)
./scripts/run-bench.sh \
  --corpus <focused-corpus.json> \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/<attempt-id> \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict \
  --include-prom false \
  --repeats 1 \
  --warmup 0 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

## Acceptance rules

Keep an attempt only when all applicable conditions hold:

- Correctness validation passes, or any remaining gap is explicitly non-user-facing instrumentation with no runtime behavior change.
- Strategy selection is preserved or intentionally improved with evidence; no silent fallback regression.
- For behavior changes, the claimed runtime signal moves in the expected direction:
  - CSE/dedup/row-source reuse: `FunctionExecute`, `ArrayMap`/array counters, join rows, pipeline stages, or duplicated source work drops.
  - Pushdown/pruning: `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes`, or `EXPLAIN PLAN indexes=1` improves with unchanged result rows.
  - CPU reduction: `UserTimeMicroseconds`, `RealTimeMicroseconds`, and relevant thread/settings evidence improve without unacceptable latency regression.
  - Memory reduction: query-log memory or `MemoryTrackerUsage` improves without correctness loss.
  - SQL-builder migration: no runtime claim unless `EXPLAIN`/ProfileEvents change; otherwise accept only for maintainability with stable SQL/goldens.
- Broad optimizations should pass a focused corpus around the affected shape and at least smoke-test adjacent shapes.
- Query-specific optimizations require either high observed cost, common dashboard relevance, or a strong path toward generalization.
- Instrumentation/diagnostic-only attempts are acceptable when they clearly unblock or de-risk measurable optimization work; they should be treated as enabling steps, not the primary long-term output.

## Rejection, deferral, and split rules

Reject or revert when:

- Compliance or focused correctness fails due to the change.
- The optimization changes Prometheus-visible semantics.
- The required ProfileEvents/EXPLAIN/query-log signal does not move and the commit's purpose is runtime improvement.
- The change is only cosmetic SQL churn with identical `EXPLAIN SYNTAX` and ProfileEvents.
- It adds broad complexity for one low-value query without durable generalization.
- An iteration repeatedly drifts into infra/tooling work without a credible near-term path to measurable execution-resource improvements.

Defer when:

- Evidence suggests a win but benchmark data/profile setup is missing or too noisy.
- Correctness boundaries require more fixtures or upstream Prometheus investigation.
- The right implementation depends on a prior typed SQL-builder or physical-decision refactor.

Split when:

- Instrumentation/keying/explain metadata is useful and safe but SQL-shape changes need more proof.
- A general optimization and a query-specific special case are entangled.
- A local execution improvement and native SQL improvement should be validated independently.

## Current state snapshot

Current branch context at loop design time:

- `2bb356b refactor: centralize native physical strategies`
  - Added `internal/promshim/native/physical/` and explainable physical decisions.
  - Compliance passed: prefer/native 537 passed + 1 accepted tolerance, 0 failures.
  - Focused profile-50k benchmark without Prom reference had `regressionCount: 0`, all 10 shim rows `native_sql`, memory/profile summaries with 10 rows and no errors.
- `55305e5 chore: show physical decisions in ch-explain`
  - `scripts/ch-explain.sh` now emits compact and detailed physical-decision TSV artifacts.
- New bounded plan exists at `.agents/plans/native-row-source-reuse-optimizer.md` and is the recommended first attempt family.

Latest trusted benchmark/data assumptions must be rechecked at loop start with:

```bash
./scripts/run-sweep.sh --bench-status
```

## Active context window

Keep only the next 1-3 active hypotheses and the last 3-5 attempt summaries here. Archive older detail according to the compaction policy.

### Active hypotheses

1. **Controlled CBE behavior experiments (consume diagnostics)**
   - Target: use the now-available subquery estimate diagnostics in controlled, behavior-safe experiments (e.g., shadow/advisory interpretation first) before any routing flips.
   - Expected signal: clearer candidate rationale and measurable decision-quality signals without correctness regressions.
   - Next action: design one bounded advisory/shadow slice that references current complexity diagnostics and keeps served strategy unchanged.

2. **Estimate inputs for later CBE (maintenance only)**
   - Status: instrumentation tranche complete for current subquery scope (`subqueryRangeMs`, `subqueryStepMs`, `subqueryPointsPerEval`, `subqueryOverlapSlots`, `subqueryWorkUnits`, `subqueryComplexityBand`).
   - Re-entry condition: missing estimate dimension identified by a concrete behavior experiment.

3. **Subquery physical preference propagation (follow-up only after new evidence)**
   - Target: nested range/subquery shapes where an inner source is eligible for sparse/native-grid strategy but parent context suppresses or fails to propagate the best preference.
   - Status: current `rate(sum(...)[5m:])` hotspot tranche is paused after repeated low-signal runtime trials.
   - Re-entry condition: a new design-backed branch with clearer expected runtime headroom and corroborating metrics beyond noise.

3. **Native row-source reuse for repeated range sources (maintenance only)**
   - Plan: `.agents/plans/native-row-source-reuse-optimizer.md`
   - Status: major wins already landed; treat as maintenance unless a new high-impact repeated-source shape appears.

### Recent attempt summaries

- `20260428-subquery-serving-measurement-gate` — **keep (measurement gate)**. Ran a focused measurable comparison for subquery shape `rate(up[5m])[30m:1m]` across native vs local modes. Native (`prefer/force_supported`) is ~10x faster than local (`off`) and uses far fewer CH round-trips (1 vs 30), so serving expansion toward local for this family is currently unjustified. Artifacts: `harness/artifacts/bench/standalone/20260428-iter74-subquery-serving-candidate/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-serving-measurement-gate.md`.
- `20260428-loop-guardrail-clarification-balance` — **keep (policy clarification, no code change)**. Clarified canonical loop language: infrastructure/diagnostic work is allowed as enabling work, but measurable execution-resource improvements remain the primary priority, and repeated infra-only drift without near-term measurable path is discouraged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-loop-guardrail-clarification-balance.md`.
- `20260428-cbe-shadow-scorecard` — **keep (measurement/evidence)**. Produced bounded pre/post warm-up decision-quality scorecard across representative families/policies. Missing-estimate rate dropped to zero and shadow-local candidate rate increased after warm-up, confirming coherent state-dependent behavior for the first controlled branch. Artifacts: `harness/artifacts/explain/20260428-iter72-shadow-scorecard/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-shadow-scorecard.md`.
- `20260428-reflection71-shadow-branch-status` — **keep (reflection, no code change)**. Reflection checkpoint confirms first controlled shadow branch is stable and transparent, and shifts next priority to a bounded decision-quality scorecard across a fixed mini-corpus before any further branch expansion. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection71-shadow-branch-status.md`.
- `20260428-cbe-subquery-shadow-runtime-branch-evidence` — **keep**. Captured rebuilt-runtime before/after warm-up evidence for subquery `cost_shadow` behavior showing transition from `strict_missing_estimate` (with `missing_estimates` advisory) to `shadow_only` + `wouldSelect=local` (with `shadow_subquery_cap_bypass=subquery` advisory). Confirms controlled branch behavior under runtime state changes while preserving served strategy neutrality. Artifacts: `harness/artifacts/explain/20260428-iter70-subquery-shadow-blocked.json`, `harness/artifacts/explain/20260428-iter70-subquery-shadow-bypass.json`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-shadow-runtime-branch-evidence.md`.
- `20260428-cbe-subquery-bypass-blocked-advisory` — **keep**. Extended controlled subquery shadow branch explainability with explicit blocked-path advisory (`shadow_subquery_cap_bypass_blocked=<reason>`) when cap bypass guardrails fail. Added focused regression coverage for both activation and blocked branches; strategy behavior unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-bypass-blocked-advisory.md`.
- `20260428-cbe-subquery-shadow-bypass-advisory` — **keep**. Added explicit advisory `shadow_subquery_cap_bypass=subquery` when the bounded `cost_shadow` subquery cap-bypass branch activates, and validated via routing-policy tests plus rebuilt-runtime explain evidence. Strategy neutrality remains preserved. Artifacts: `harness/artifacts/explain/20260428-iter68-subquery-shadow-advisory-warm.json`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-shadow-bypass-advisory.md`.
- `20260428-cbe-controlled-subquery-shadow-evidence` — **keep**. Captured rebuilt-runtime post-change matrix evidence showing the new bounded behavior branch is active for `subquery + cost_shadow` (`decision=shadow_only`, `wouldSelect=local`) while strict/selected served strategies remain unchanged. Artifacts: `harness/artifacts/explain/20260428-iter67-behavior-eval-rebuilt/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-controlled-subquery-shadow-evidence.md`.
- `20260428-reflection66-first-behavior-experiment-status` — **keep (reflection, no code change)**. Reflection checkpoint confirms successful entry into the first controlled behavior experiment and sets next priority to evidence capture on decision-quality impact before any further behavior expansion. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection66-first-behavior-experiment-status.md`.
- `20260428-cbe-controlled-subquery-shadow-candidate` — **keep**. Entered the first bounded behavior experiment by allowing a shadow-only local candidate interpretation for light/fresh subquery shapes in `cost_shadow` (served strategy unchanged). Added focused regression coverage for this branch and cap-bypass guard conditions. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-controlled-subquery-shadow-candidate.md`.
- `20260428-cbe-first-behavior-boundary-scope` — **keep (scope decision, no code change)**. Defined the first controlled behavior-experiment boundary to include only `cost_prefer` + confirmed-fresh estimate combinations, while explicitly excluding `cost_shadow` subquery rows until freshness parity is resolved. This enables a clean next-step experiment without confounded missing-estimate noise. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-first-behavior-boundary-scope.md`.
- `20260428-cbe-estimate-freshness-prep-check` — **split/defer (no code change)**. Ran estimate-freshness prep (query warm-up + rebuilt-runtime advisory matrix rerun). Fresh cache state improved for cost_prefer rows, but subquery cost_shadow still reports `strict_missing_estimate`/`selector_stats` missing. Behavior-experiment entry remains deferred until scoped boundary or targeted fix is chosen. Artifacts: `harness/artifacts/explain/20260428-iter63-advisory-matrix-warmed/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-freshness-prep-check.md`.
- `20260428-cbe-readiness-gate-checklist` — **split/defer (no code change)**. Applied explicit readiness checklist for entering the first controlled behavior experiment. Advisory consistency/neutrality gates are met, but estimate freshness/availability for target subquery cases is not yet sufficient (`strict_missing_estimate` still common), so behavior entry is deferred pending a bounded freshness-prep slice. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-readiness-gate-checklist.md`.
- `20260428-reflection61-advisory-readiness-gate` — **keep (reflection, no code change)**. Reflection checkpoint concludes advisory tranche is near saturation and sets a readiness-gate pivot: next step is either enter one tightly bounded behavior experiment if readiness is met, or fix only the missing gate item. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection61-advisory-readiness-gate.md`.
- `20260428-cbe-advisory-low-confidence-coverage` — **keep**. Expanded advisory low-confidence coverage with an additional reason path (`candidate_serving_disabled`) to verify reason-specific advisory consistency across multiple strict-low-confidence outcomes. No routing behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-advisory-low-confidence-coverage.md`.
- `20260428-cbe-advisory-low-confidence-reason` — **keep**. Extended advisory/shadow diagnostics so `strict_low_confidence` decisions carry explicit `low_confidence_reason=<reason>` hints, improving decision transparency with no selected/served strategy change. Added regression assertion on family-gate low-confidence path. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-advisory-low-confidence-reason.md`.
- `20260428-cbe-advisory-matrix-eval-rebuilt` — **keep**. Re-ran the advisory behavior matrix after rebuilding benchmark promshim runtime; advisory hints now surface consistently where intended (subquery/rate shadow cases) while strategy neutrality remains intact. This resolves prior capture mismatch and completes the bounded advisory decision-quality evaluation tranche. Artifacts: `harness/artifacts/explain/20260428-iter58-advisory-matrix-rebuilt/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-advisory-matrix-eval-rebuilt.md`.
- `20260428-cbe-advisory-matrix-eval` — **split/defer (no code change)**. Ran a bounded advisory behavior matrix across representative query families/policies; strategy neutrality held, but live `query_explain` rows showed empty advisory fields despite passing repository advisory-generation tests. Indicates runtime capture mismatch (likely stale service build). Re-run matrix against rebuilt service before judging advisory decision-quality completeness. Artifacts: `harness/artifacts/explain/20260428-iter57-advisory-matrix/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-advisory-matrix-eval.md`.
- `20260428-reflection56-advisory-consumption-status` — **keep (reflection, no code change)**. Reflection checkpoint confirms instrumentation-to-advisory transition is complete enough to shift next work from field expansion to a bounded decision-quality evaluation of advisory usefulness/consistency across representative query families. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection56-advisory-consumption-status.md`.
- `20260428-cbe-advisory-missing-estimate-hint` — **keep**. Extended advisory/shadow diagnostics so strict-missing-estimate decisions include an explicit advisory hint (`missing_estimates=<fields>`), improving explainability without changing selected/served strategy behavior. Added regression assertion in routing policy tests. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-advisory-missing-estimate-hint.md`.
- `20260428-cbe-subquery-advisory-strategy-neutral-guard` — **keep**. Added API-level regression guard that advisory subquery complexity hints remain strategy-neutral by asserting strict/selected strategies stay `native_sql` while `routing.advisory` is present. No routing behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-advisory-strategy-neutral-guard.md`.
- `20260428-cbe-subquery-advisory-api-guard` — **keep**. Added API-level explain regression guard asserting `routing.advisory` contains `subquery_complexity=light` for subquery explain shape, reinforcing advisory-surface stability without changing routing decisions. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-advisory-api-guard.md`.
- `20260428-cbe-subquery-advisory-shadow` — **keep**. Executed a bounded advisory/shadow consumption experiment by surfacing `routing.advisory[]` and populating `subquery_complexity=<band>` for cost-policy routing contexts, without changing selected/served strategy. Added focused routing-policy tests to prove advisory presence and unchanged strict decision behavior. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-subquery-advisory-shadow.md`.
- `20260428-reflection51-cbe-experiment-readiness` — **keep (reflection, no code change)**. Reflection checkpoint confirms readiness to transition from instrumentation to consumption: next iteration should run one bounded advisory/shadow experiment that uses subquery complexity diagnostics while keeping served strategy unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection51-cbe-experiment-readiness.md`.
- `20260428-cbe-estimate-tranche-closure` — **keep (scope decision, no code change)**. Closed the current estimate-input instrumentation tranche after landing and validating the full subquery diagnostic set. Next execution focus shifts to controlled CBE behavior experiments that consume existing diagnostics rather than adding more fields. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-tranche-closure.md`.
- `20260428-cbe-estimate-input-subquery-complexity-band` — **keep**. Added qualitative derived diagnostic `subqueryComplexityBand` (`light|moderate|elevated|heavy`) from existing subquery complexity inputs, with classifier and API explain regression coverage. No routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-complexity-band.md`.
- `20260428-cbe-estimate-input-subquery-temporal-fanout` — **keep**. Added derived subquery complexity indicator `subqueryTemporalFanout` to routing cost class (`subqueryPointsPerEval * max(rangePointsPerSeries,1)`), with classifier and API explain regression coverage. No routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-temporal-fanout.md`.
- `20260428-cbe-estimate-input-subquery-work-units` — **keep**. Added derived subquery complexity indicator `subqueryWorkUnits` to routing cost class (`subqueryPointsPerEval * max(selectorCount,1)`), with classifier and API explain regression coverage. No routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-work-units.md`.
- `20260428-reflection46-cbe-instrumentation-balance` — **keep (reflection pivot, no code change)**. Reflection checkpoint notes estimate-input surfacing progress and shifts next priority from adding more raw fields to deriving/packaging useful subquery complexity diagnostics from existing fields before any routing behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection46-cbe-instrumentation-balance.md`.
- `20260428-cbe-estimate-input-subquery-overlap` — **keep**. Added derived estimate metadata `subqueryOverlapSlots` to routing cost class (`subqueryRangeMs/subqueryStepMs`) and validated classifier/API explain surfacing with subquery regression tests. No routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-overlap.md`.
- `20260428-cbe-estimate-input-subquery-points` — **keep**. Added derived estimate metadata `subqueryPointsPerEval` to routing cost class (`subqueryRangeMs/subqueryStepMs + 1`) and validated classifier/API explain surfacing with subquery regression tests. No routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-points.md`.
- `20260428-cbe-estimate-input-subquery-fields` — **keep**. Added instrumentation-first estimate metadata for later CBE by surfacing `subqueryRangeMs` and `subqueryStepMs` in routing cost class, populated from parsed subquery nodes. Added classifier and API explain regression tests; no routing/strategy behavior changes. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-cbe-estimate-input-subquery-fields.md`.
- `20260428-subquery-hotspot-tranche-closure` — **keep (scope decision, no code change)**. Applied the iteration-41 decision rule and explicitly closed the current `rate(sum(...)[5m:])` hotspot tranche after repeated low-signal runtime trials. Pivoted next execution focus to estimate-input plumbing for later CBE (instrumentation-first, no routing change). Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-hotspot-tranche-closure.md`.
- `20260428-reflection41-pivot-scope` — **keep (reflection pivot, no code change)**. Reflection checkpoint concludes recent hotspot work is over-indexed on low-signal micro-variants; next execution should tighten to one explicit high-EV branch with predeclared accept/reject thresholds, or declare this hotspot tranche exhausted and pivot families. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection41-pivot-scope.md`.
- `20260428-subquery-avg-over-time-rows-path-rejected` — **reject/defer (reverted)**. Tried routing subquery-child `avg_over_time` through range rows fast path to reduce `draft_cand_0416...` hotspot cost. Correctness tests passed, but focused benchmark/profile comparison against iteration-33 baseline showed no meaningful memory reduction (still ~81.3MiB p95) and no consistent latency win, so prototype was reverted. Artifacts: `harness/artifacts/bench/standalone/20260428-iter40-subquery-hotspots-after-avgrows/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-avg-over-time-rows-path-rejected.md`.
- `20260428-rate-sum-windowed-rows-collapsed-tags-rejected` — **reject/defer (reverted)**. Tested a collapsed-tag-set specialization on the actual windowed-rows path used by `rate(sum(...)[5m:])`. Prototype validated but focused bench/profile signals did not improve (slight shim/CH regressions within noise, no corroborating memory gain), so it was reverted. Artifacts: `harness/artifacts/bench/standalone/20260428-iter39-cand0242-after-shape/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-rate-sum-windowed-rows-collapsed-tags-rejected.md`.
- `20260428-rate-sum-collapsed-tags-guard-rejected` — **reject/defer (reverted)**. Tried a collapsed-tag-set specialization prototype, but SQL/explain triage confirmed the target `rate(sum(...)[5m:])` hotspot does not run through the touched rows-fast-path function. Reverted as low-EV/no-op for target path. Artifacts: `harness/artifacts/explain/20260428-iter38-cand0242-after/`, `harness/artifacts/bench/standalone/20260428-iter38-cand0242-after/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-rate-sum-collapsed-tags-guard-rejected.md`.
- `20260428-subquery-rate-sum-design-slice` — **keep (design artifact, no code change)**. Produced a bounded implementation design for the `rate(sum(...)[5m:])` hotspot at `.agents/plans/subquery-rate-sum-hotspot-design.md`, including candidate alternatives, chosen narrow first slice (constant-tag specialization), guard conditions, validation matrix, and rollback criteria. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-rate-sum-design-slice.md`.
- `20260428-reflection36-design-slice-priority` — **keep (reflection reset, no code change)**. Reflection checkpoint confirms progress on behavior guards/measurement reliability and resets next-step execution shape to a bounded design-first slice for the `rate(sum(...)[5m:])` hotspot before further runtime edits. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection36-design-slice-priority.md`.
- `20260428-subquery-hotspot-sql-shape-triage` — **split/defer (no code change)**. Triaged the current higher-latency subquery hotspot SQL shape (`rate(sum(...)[5m:])`) and confirmed it is dominated by grid+ASOF+windowed rate evaluation with constant empty-tag aggregation. Candidate optimizations appear non-trivial and contract-sensitive; deferred direct runtime edits pending a bounded design slice with explicit correctness/perf signals. Artifacts: `harness/artifacts/explain/20260428-iter35-cand0242-baseline/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-hotspot-sql-shape-triage.md`.
- `20260428-avg-over-time-rows-fastpath-rejected` — **reject/defer (reverted)**. Tested enabling range-mode rows fast path for `avg_over_time` to target the subquery memory hotspot, but focused validation failed (`TestLowerHighOverlapAvgOverTimeRangeUsesDirectAggregate`, explain/physical-decision guards). Reverted in-iteration; no code retained. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-avg-over-time-rows-fastpath-rejected.md`.
- `20260428-subquery-hotspot-candidate-mapping` — **keep (measurement-only)**. Mapped a broader 3-query subquery hotspot corpus from dashboard subset and measured on isolated bench stack. All rows remained `native_sql`; prefer-vs-force deltas stayed small, but absolute-cost/memory outliers emerged (`draft_cand_0242...` CPU/latency and `draft_cand_0416...` memory), giving higher-EV runtime targeting for next behavior attempt. Artifacts: `harness/artifacts/bench/standalone/20260428-iter33-subquery-hotspots/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-hotspot-candidate-mapping.md`.
- `20260428-binary-subquery-runtime-candidate-rejected` — **reject/defer (no code change)**. Reviewed focused post-unblock runtime/profile artifacts for binary-subquery thread-policy shapes; prefer vs force_supported deltas were sub-1% and corroborating signals (queryDuration, memory, functionExecute) were effectively flat. Rejected additional behavior tweak as noise-driven/low-EV for this narrow family. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-binary-subquery-runtime-candidate-rejected.md`.
- `20260428-reflection31-runtime-priority-reset` — **keep (reflection reset, no code change)**. Reflection checkpoint confirms harness/measurement reliability improvements are in place and resets loop priority back to runtime-impact candidates, requiring non-wall-clock corroborating signals for future optimization claims. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection31-runtime-priority-reset.md`.
- `20260428-bench-corpus-validation-test-hardening` — **keep**. Added loader regression tests for non-positive `query_range` step and unsupported endpoint errors, complementing the prior invalid-offset fail-fast coverage. No runtime behavior change; harness validation guard-only improvement. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-bench-corpus-validation-test-hardening.md`.
- `20260428-binary-thread-policy-post-unblock-measurement` — **keep (measurement-only)**. Ran focused binary-subquery smoke corpus with repeats/warmup after harness unblocking. All rows served `native_sql` with stable CH/runtime metrics and only small prefer-vs-force_supported deltas (within noise-scale for this setup). Artifacts: `harness/artifacts/bench/standalone/20260428-iter29-binary-thread-policy-measure/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-binary-thread-policy-post-unblock-measurement.md`.
- `20260428-bench-corpus-range-validation` — **keep**. Added runtime query-corpus validation in `promharness` so invalid `query_range` windows (`endOffsetSeconds < startOffsetSeconds`) and non-positive step sizes fail fast at corpus load with actionable errors. Added loader unit test and verified `run-bench` now exits early with explicit validation failure instead of row-level HTTP 400s. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-bench-corpus-range-validation.md`.
- `20260428-binary-subquery-bench-harness-unblock` — **keep**. Fixed focused bench corpus window shape (`startOffsetSeconds <= endOffsetSeconds`) and reran isolated benchmark smoke for mixed-root + nested-binary subquery queries. HTTP 400 failures cleared; all rows executed as `native_sql` with usable CH/runtime metrics. Artifacts: `harness/artifacts/bench/standalone/20260428-iter27-binary-thread-policy-smoke-fixed/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-binary-subquery-bench-harness-unblock.md`.
- `20260428-reflection26-harness-debug-priority` — **keep (reflection pivot, no code change)**. Reflection checkpoint confirms behavior/guardrail progress is solid, but runtime validation for the binary-subquery family is blocked by `run-bench` HTTP 400 failures. Next priority is a bounded harness-debug slice to unblock reliable measurement before additional behavior tuning. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection26-harness-debug-priority.md`.
- `20260428-subquery-binary-bench-harness-400` — **defer/split**. Attempted focused benchmark smoke for mixed/nested binary subquery thread-policy shapes using `run-bench` on isolated benchmark endpoints, but all rows returned HTTP 400 in both `prefer` and `force_supported`, yielding no runtime comparison signal. Captured artifacts and a repro corpus for follow-up harness debugging. Artifacts: `harness/artifacts/bench/standalone/20260428-iter25-binary-thread-policy-smoke/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-binary-bench-harness-400.md`.
- `20260428-binary-root-thread-policy-service-guard` — **keep**. Added API-level regression coverage for root-vs-branch thread-policy surfacing after binary-root scoping: pure subquery-rate explain keeps root `query_settings`, while mixed binary root omits root `query_settings` and preserves subquery-node `query_settings=no_thread_cap` with canonical reason. Runtime behavior unchanged; guard-only follow-up. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-binary-root-thread-policy-service-guard.md`.
- `20260428-binary-root-thread-policy-scoping` — **keep**. Scoped global no-thread-cap suppression away from binary roots in `suppressThreadCapForPlan`, preserving non-binary-root behavior while relying on branch-local subquery suppression for binary shapes. After rebuilding bench promshim, mixed-root and nested-binary explain artifacts show `query_settings=no_thread_cap` attached to subquery branch nodes instead of root. Validation stayed green on local/native packages. Artifacts: `harness/artifacts/explain/20260428-mixed-root-thread-policy-after-rebuild/`, `harness/artifacts/explain/20260428-nested-binary-thread-policy-after-rebuild/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-binary-root-thread-policy-scoping.md`.
- `20260428-mixed-root-thread-policy-baseline-mapping` — **split/defer (no code change)**. Captured mixed-root baseline for `sum(avg_over_time(up[1h])) + sum(rate((sum by (job) (up))[5m:1m]))`; explain still resolves to root `query_settings=no_thread_cap` (`subquery_rate_over_aggregate_regresses_with_thread_cap`) with no branch-specific thread-policy split. This established pre-change evidence for the bounded behavior slice. Artifacts: `harness/artifacts/explain/20260428-mixed-root-subquery-thread-policy-baseline/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-mixed-root-thread-policy-baseline-mapping.md`.
- `20260428-reflection21-subquery-propagation-pivot` — **keep (planning pivot, no code change)**. Reflection checkpoint concluded that subquery decision observability prerequisites are now largely complete (node surfacing, canonicalization, service guard), so expected value has shifted to a bounded behavior-oriented subquery preference propagation slice with runtime evidence rather than more metadata-only hardening. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-reflection21-subquery-propagation-pivot.md`.
- `20260428-subquery-node-decision-service-guard` — **keep**. Added service-level explain regression coverage to lock nested subquery `query_settings=no_thread_cap` visibility in API responses (`query_range_explain`), including canonical reason-code assertion. This guards against root-only decision regressions and stack/build drift confusion. Runtime strategy/SQL unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-node-decision-service-guard.md`.
- `20260428-subquery-node-decision-canonicalization` — **keep**. Switched subquery-node explain annotation to reuse canonical `ThreadPreferenceDecision(no_cap)` and only prepend the subquery-specific guard (`needs_subquery_step_grid`). Added regression assertions for canonical rejected alternative text/shape (`set_max_threads`, `suppressed by no-thread-cap preference`). Runtime strategy/SQL unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-node-decision-canonicalization.md`.
- `20260428-subquery-thread-preference-reason-alignment` — **keep**. Unified thread-preference reason codes across renderer policy and explain-only subquery-node annotation by introducing shared constants in `physical` and switching all call sites/tests. This removes root/child reason drift in diagnostics while keeping runtime strategy/SQL behavior unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-thread-preference-reason-alignment.md`.
- `20260428-subquery-node-preference-decision-surfacing` — **keep**. Added explain-only nested decision annotation so subquery nodes with `lowering.needsSubqueryStepGrid=true` emit `query_settings=no_thread_cap` metadata (reason `subquery_rate_over_aggregate_regresses_with_thread_cap`, explicit guards/rejected alternative). Added planner regression test `TestExplainPlanIncludesSubqueryNodeNoThreadCapDecision`. No runtime SQL strategy change. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-node-preference-decision-surfacing.md`.
- `20260428-subquery-preference-propagation-mapping` — **split/defer (no code change)**. Captured a subquery baseline for `rate((sum by (job) (up))[30m:1m])` showing native SQL selection with root-level `query_settings=no_thread_cap` metadata, but no child/node-level preference decision rows in explain output. This narrowed the next step to decision-surfacing instrumentation before any propagation/routing behavior change. Artifacts: `harness/artifacts/explain/20260428-subquery-pref-propagation-baseline/`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-preference-propagation-mapping.md`.
- `20260428-direct-agg-threadcap-decision-guard` — **keep**. Added planner/explain regression coverage for direct range aggregation (`sum by(job)(up)`) to lock `query_settings=set_max_threads` with guardrail reason/guard metadata. No runtime behavior change; this protects the newly surfaced query-settings decision contract. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-direct-agg-threadcap-decision-guard.md`.
- `20260428-fused-rate-threadcap-decision-surfacing` — **keep**. Surfaced `query_settings=set_max_threads` in explain metadata for fused range-rate aggregation where thread-cap guardrails were already being applied (`max_threads=4`). This is observability parity (no runtime strategy change) and aligns physical decision reporting with applied query settings. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-fused-rate-threadcap-decision-surfacing.md`.
- `20260428-subquery-no-cap-rejected-alternative` — **keep**. Updated no-thread-cap physical decision metadata to include an explicit rejected alternative (`set_max_threads`) so explain output shows why thread-cap settings were suppressed. Runtime unchanged for the validated query shape; this is preference-precedence observability for upcoming subquery propagation work. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-no-cap-rejected-alternative.md`.
- `20260428-subquery-no-cap-mixed-root-guard` — **keep**. Added a planner/explain regression guard for mixed-root queries that include both thread-cap-eligible shapes and `rate(subquery-over-aggregation)`; confirms final preference remains `query_settings=no_thread_cap`. No runtime behavior change; this locks no-cap precedence ahead of subquery propagation work. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-no-cap-mixed-root-guard.md`.
- `20260428-subquery-no-thread-cap-nested-guard` — **keep**. Added a planner/explain regression guard for nested `rate(subquery-over-aggregation)` under a binary root so `query_settings=no_thread_cap` remains visible in `physicalDecisions`. No runtime behavior change; this protects subquery preference behavior ahead of deeper subquery propagation work. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-subquery-no-thread-cap-nested-guard.md`.
- `20260428-row-source-reuse-mismatch-range-test` — **keep**. Added a range-mode renderer regression test that locks `row_source_reuse=not_reused` mismatch metadata for repeated-candidate unequal operands (`rate[1h] + rate[6h]`). No runtime behavior change; this prevents instant/range explainability drift. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-row-source-reuse-mismatch-range-test.md`.
- `20260428-row-source-reuse-mismatch-decision` — **keep**. Added instant-mode `row_source_reuse=not_reused` metadata for repeated-candidate operand mismatches (e.g., `rate[1h] + rate[6h]`) with explicit guard/rejected-alternative diagnostics. Explain now reports mismatch reason; runtime shape remains effectively unchanged. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-row-source-reuse-mismatch-decision.md`.
- `20260428-instant-reuse-followup-noop` — **defer/no-op**. Re-ran instant self-reuse exploration and confirmed the targeted instant decision coverage had already landed; no additional high-value code delta was justified in that narrow area. Validation remained clean and instant strategy selection stayed stable. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-instant-reuse-followup-noop.md`.
- `20260428-row-source-reuse-instant-decision` — **keep**. Unified self-reuse decision logic across render modes and added instant-mode decision metadata. Repeated instant shape now reports `row_source_reuse=instant_self_join`; repeated shape with non-default matching reports `row_source_reuse=not_reused` with guard reason. For `rate(...) + rate(...)` instant: `query_duration_ms` `136 → 133`, `memory_usage` `251614040 → 231505327`, `function_execute` `9976 → 7131`, `real_time_us` `3334915 → 3091185`. Compliance stayed clean; focused instant benchmark remained all `native_sql` with regressionCount 0. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-row-source-reuse-instant-decision.md`.
- `20260428-row-source-reuse-decision-observability` — **keep**. Added typed `row_source_reuse` decision metadata for repeated range binary shapes even when reuse is rejected. Example `rate(...) + on(job) rate(...)` now reports `row_source_reuse=not_reused` with reason `range self-reuse currently requires default one-to-one matching labels` and rejected alternative `range_self_join`. Validation: package tests passed, compliance remained clean (prefer/native 537 passed + 1 accepted tolerance, 0 failures), focused benchmark stayed `native_sql` with regressionCount 0. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-row-source-reuse-decision-observability.md`.
- `20260428-range-self-join-bool-comparison` — **keep**. Extended range self-reuse to repeated bool comparisons under conservative gates (supported comparison op, default one-to-one matching, identical operand expression and repeated subtree key). For `rate(...) >= bool rate(...)`: `query_duration_ms` `8707 → 7206`, `memory_usage` `4045586437 → 3044895581`, `real_time_us` `293214755 → 242960764`, `join_build_rows` `3347760 → 11544`. Compliance passed; focused benchmark kept `native_sql` for all rows. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-range-self-join-bool-comparison.md`.
- `20260428-range-self-join-comparison` — **keep**. Extended range self-reuse from repeated arithmetic to repeated non-bool comparisons under the same conservative gates. For `rate(...) >= rate(...)`: `join_build_rows` `3347760 → 11544`, `memory_usage` `4034302245 → 3023742564`, `real_time_us` `292427191 → 239284230`. Compliance passed; focused benchmark kept `native_sql` in prefer/force_supported. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-range-self-join-comparison.md`.
- `20260428-range-self-join-arithmetic` — **keep**. Generalized range self-reuse from `A + A` to identical one-to-one arithmetic repeated range-function operands (`+ - * / % ^`) with a repeated-subtree gate (`cseSubtreeKey`) so leaf arithmetic (`up * up`) is not rewritten. For `rate(...) * rate(...)`: `join_build_rows` `3347760 → 11544`, `memory_usage` `4055860420 → 3027569371`, `real_time_us` `293298166 → 233424519`. Compliance passed; focused benchmark stayed `native_sql`. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-range-self-join-arithmetic.md`.
- `20260428-range-self-join` — **keep**. Added range-mode binary self-join rendering for identical default one-to-one `A + A` operands. Baseline showed `(A + A) / 2` targets are already cancelled by logical optimization, so the runtime target shifted to `rate(...) + rate(...)`. For `rate(...) + rate(...)`: `join_build_rows` `3347760 → 11544`, `memory_usage` `4056171689 → 3299962607`, `real_time_us` `290023641 → 248337765`. Compliance passed. Attempt notes: `.pi/loops/native-sql-optimization-sweep/attempts/20260428-range-self-join.md`.

### Reflection checkpoint (iteration 6)

1. **What has been accomplished so far?**
   - Built a conservative range self-reuse ladder: repeated arithmetic, non-bool comparison, and bool comparison range-function shapes now reuse one flattened source under strict guards.
   - Added reproducible measurement artifacts and self-contained commit evidence for each accepted attempt.
   - Kept compliance clean after each behavior change.

2. **What is working well?**
   - The attempt loop (baseline → implementation → compliance/bench → commit) is producing consistent, low-variance wins.
   - `join_build_rows`, memory, and CPU signals are strongly correlated with successful reuse changes.
   - Commit policy updates improved reviewability by forcing in-commit metrics and validation outcomes.

3. **What is not working / blockers?**
   - Explain metadata currently emphasizes applied strategies; eligibility/rejection visibility is still partial across all binary families.
   - Some non-repeated binary shapes do not surface row-source-reuse decisions in service-level explain output, which limits diagnosability for "why not reused" questions.

4. **Should the approach be adjusted?**
   - Yes: prioritize observability and decision transparency next, not broader operator expansion.
   - Keep optimization scope conservative and only expand runtime behavior when explain metadata can justify decisions clearly.

5. **Next priorities**
   - Add typed eligibility/rejection row-source-reuse decision metadata that is consistently visible in explain output for repeated candidate shapes (applied and not-applied cases where relevant).
   - Then move to subquery preference propagation / estimate plumbing once reuse decision observability is solid.

### Reflection checkpoint (iteration 71)

1. **What has been accomplished so far?**
   - Completed advisory + diagnostics foundation and entered a bounded shadow behavior branch.
   - Verified activation/blocked-path transparency and runtime cold/warm branch transitions.

2. **What's working well?**
   - Guarded behavior evolution with strong strategy-neutrality constraints.
   - Evidence collection now clearly links routing outcomes to advisory state.

3. **What's not working or blocking progress?**
   - Decision-quality improvement is not yet summarized with a compact comparative scorecard.

4. **Should the approach be adjusted?**
   - Yes: focus next on a bounded scoring pass, not more branch logic.

5. **What are the next priorities?**
   - Build mini-corpus scorecard (advisory presence, missing-estimate rate, shadow-local candidate rate) pre/post warm-up.
   - Use scorecard to decide keep/tighten/expand branch.

### Reflection checkpoint (iteration 66)

1. **What has been accomplished so far?**
   - Completed diagnostic/advisory groundwork and landed the first bounded behavior branch.
   - Preserved served-strategy neutrality while evolving shadow candidate interpretation.

2. **What's working well?**
   - Guarded, incremental behavior changes with targeted tests are containing risk effectively.

3. **What's not working or blocking progress?**
   - Decision-quality impact evidence for the new behavior branch is not yet fully documented.

4. **Should the approach be adjusted?**
   - Yes: pause further branching and gather focused before/after behavior evidence first.

5. **What are the next priorities?**
   - Produce a compact routing-matrix evidence pass for the new branch.
   - Verify no collateral effects on non-target families.
   - Decide expansion vs. tighten/revert based on that evidence.

### Reflection checkpoint (iteration 61)

1. **What has been accomplished so far?**
   - Completed estimate-input and advisory consumption tranches with solid coverage.
   - Confirmed advisory consistency and strategy neutrality on rebuilt runtime captures.

2. **What's working well?**
   - Tranche execution remains controlled and test-backed.
   - Explain/routing transparency is materially improved.

3. **What's not working or blocking progress?**
   - Additional advisory-only work is approaching diminishing returns.
   - No controlled behavior-change experiment has started yet.

4. **Should the approach be adjusted?**
   - Yes: define a readiness gate and transition to a minimal behavior experiment once satisfied.

5. **What are the next priorities?**
   - Record readiness checklist and immediately either enter behavior experiment or close the remaining gap.

### Reflection checkpoint (iteration 56)

1. **What has been accomplished so far?**
   - Completed subquery estimate instrumentation and derived diagnostics.
   - Landed advisory/shadow consumption with explicit strategy-neutral guards.

2. **What's working well?**
   - Strong test-backed metadata contracts across classifier/routing/API layers.
   - Tranche sequencing is controlling risk and keeping progress coherent.

3. **What's not working or blocking progress?**
   - Advisory diagnostics utility is not yet empirically evaluated across query families.

4. **Should the approach be adjusted?**
   - Yes: run a bounded decision-quality advisory evaluation before further metadata expansion.

5. **What are the next priorities?**
   - Build/verify an advisory behavior matrix over representative queries and cost policies.
   - Use that evidence to define the first safe controlled behavior-experiment boundary.

### Reflection checkpoint (iteration 51)

1. **What has been accomplished so far?**
   - Completed and validated the subquery estimate-diagnostics tranche end-to-end.
   - Preserved routing behavior neutrality while improving explain/routing metadata depth.
   - Closed low-EV runtime branches explicitly instead of carrying speculative partial work.

2. **What's working well?**
   - Tranche-based scope control and reflection checkpoints are improving loop quality.
   - Metadata additions remain coherent and test-backed.

3. **What's not working or blocking progress?**
   - Diagnostics value remains mostly theoretical until consumed by decision logic in a controlled experiment.

4. **Should the approach be adjusted?**
   - Yes: execute one bounded consumption experiment now (advisory/shadow only, no served-strategy changes).

5. **What are the next priorities?**
   - Add one narrow rationale path that uses subquery complexity diagnostics.
   - Assert unchanged selected/served strategy in tests.
   - Capture before/after explain/routing artifacts proving improved decision transparency.

### Reflection checkpoint (iteration 46)

1. **What has been accomplished so far?**
   - Closed the low-signal subquery hotspot runtime tranche responsibly.
   - Landed multiple behavior-neutral CBE estimate-input metadata fields with stable explain/API surfacing.

2. **What's working well?**
   - Low-risk, additive instrumentation is moving predictably with solid regression coverage.
   - Scope control is preventing return to noisy micro-optimization churn.

3. **What's not working or blocking progress?**
   - Additional raw fields now have diminishing standalone value without synthesis/consumption.
   - Runtime value remains deferred until these inputs are used to improve candidate interpretation.

4. **Should the approach be adjusted?**
   - Yes: shift from field expansion to derived diagnostics using existing fields, still behavior-neutral.

5. **What are the next priorities?**
   - Add one bounded derived subquery complexity indicator in explain/cost diagnostics.
   - Keep routing unchanged; validate classifier/API contracts.
   - Reassess readiness for controlled CBE behavior experimentation afterward.

### Reflection checkpoint (iteration 41)

1. **What has been accomplished so far?**
   - Shipped binary-root thread-policy scoping with explain/service guard coverage.
   - Stabilized focused benchmarking and captured reproducible hotspot evidence.
   - Executed and rejected several bounded runtime prototypes using corroborated measurements.

2. **What's working well?**
   - The loop reliably prevents low-confidence changes from landing.
   - Measurement and explain artifacts are now decision-grade and repeatable.

3. **What's not working or blocking progress?**
   - Incremental SQL micro-variants in the current hotspot family are not yielding strong signal.
   - Runtime-win throughput has dropped as experiment rejection rate increased.

4. **Should the approach be adjusted?**
   - Yes: narrow to one stronger expected-value branch with explicit thresholds, and avoid parallel micro-variant probing.

5. **What are the next priorities?**
   - Choose one branch with predeclared accept/reject criteria.
   - If no viable branch remains, explicitly close this hotspot tranche and pivot to another family with clearer headroom.

### Reflection checkpoint (iteration 36)

1. **What has been accomplished so far?**
   - Completed binary-root thread-policy behavior scoping with explain/service guard coverage.
   - Stabilized focused benchmark workflows and removed key harness blockers.
   - Identified concrete subquery hotspot candidates and captured detailed SQL-shape evidence for the top-latency path.

2. **What's working well?**
   - Evidence-first decisions are containing risk and reducing speculative churn.
   - Measurement and explain artifacts now provide reliable decision inputs.

3. **What's not working or blocking progress?**
   - Top runtime candidate is contract-sensitive; quick tweaks are failing or low-signal.
   - Runtime improvement throughput has slowed versus harness/triage throughput.

4. **Should the approach be adjusted?**
   - Yes: switch to a bounded design-first slice for the top hotspot before further code edits.

5. **What are the next priorities?**
   - Produce a compact design note with candidate shape alternatives, correctness/decision-contract impacts, expected perf signals, and a tight validation matrix.
   - Implement only the smallest design-backed variant with explicit rollback criteria.

### Reflection checkpoint (iteration 31)

1. **What has been accomplished so far?**
   - Landed binary-root subquery thread-policy scoping plus planner/service explain guards.
   - Unblocked focused binary-subquery benchmarking by fixing corpus-window shape and adding fail-fast corpus validation.
   - Captured stable focused measurement artifacts for mixed/nested binary shapes.

2. **What's working well?**
   - Measurement workflow is now significantly more reliable (rebuild discipline + corpus validation + focused artifacts).
   - Explain/API observability makes policy-placement effects easier to verify.

3. **What's not working or blocking progress?**
   - Recent output has leaned heavily toward harness/test hardening; direct runtime gains have slowed.
   - Current focused deltas remain small, making optimization claims sensitive to noise.

4. **Should the approach be adjusted?**
   - Yes: pivot back to runtime-impact candidates now that harness reliability is restored.
   - Require at least one corroborating non-wall-clock signal for any runtime claim.

5. **What are the next priorities?**
   - Select one bounded runtime candidate in this subquery family with explicit expected signal.
   - Run before/after focused benchmark with profile/memory summaries.
   - Keep harness-only work as reactive support, not primary output.

### Reflection checkpoint (iteration 26)

1. **What has been accomplished so far?**
   - Delivered the first bounded behavior change in this subquery phase: binary-root thread-policy scoping.
   - Added planner + service guards that lock root-vs-branch query-settings explain behavior.
   - Preserved package-level correctness signals after the behavior shift.

2. **What's working well?**
   - Iteration structure remains disciplined: baseline evidence, minimal change, guard tests, commit.
   - Explain visibility is now robust enough to diagnose policy placement precisely.

3. **What's not working or blocking progress?**
   - Focused benchmark rows for this shape family currently fail with HTTP 400 in `run-bench`, preventing runtime comparisons.
   - This creates a temporary measurement bottleneck for judging further behavior tweaks.

4. **Should the approach be adjusted?**
   - Yes: prioritize a bounded harness-debug attempt next to unblock measurement for this query family.
   - Avoid additional behavior tuning until measurement reliability is restored.

5. **What are the next priorities?**
   - Capture exact failing request/response details for one bench row.
   - Fix corpus/encoding or bench request construction so the row executes.
   - Re-run focused bench smoke and only then continue subquery behavior optimization.

### Reflection checkpoint (iteration 21)

1. **What has been accomplished so far?**
   - Landed conservative repeated-source reuse wins with strong runtime evidence and clean compliance history.
   - Completed a focused subquery explainability hardening arc: nested node surfacing, reason-code and rejected-alternative canonicalization, and API-level regression coverage.

2. **What is working well?**
   - The loop’s evidence-first structure keeps risk low and avoids speculative edits.
   - Decision metadata is now materially easier to audit across planner and service explain surfaces.

3. **What is not working / blockers?**
   - Recent accepted iterations have mostly been observability/testing improvements, not runtime wins.
   - External `ch-explain` artifact visibility can lag local commits when benchmark stack binaries are stale, creating interpretation noise.

4. **Should the approach be adjusted?**
   - Yes. Shift from metadata-only safeguards to a bounded behavior slice for subquery preference propagation, with explicit runtime measurement requirements.
   - Keep scope tight: one query family, one preference path, one decision-quality before/after comparison.

5. **What are the next priorities?**
   - Implement one minimal subquery propagation behavior candidate (not just metadata), preserving correctness and native strategy selection.
   - Capture before/after explain + query-log/ProfileEvents evidence from a rebuilt benchmark stack.
   - Retain the newly added explain/service guards to prevent observability regression while behavior changes land.

### Reflection checkpoint (iteration 16)

1. **What has been accomplished so far?**
   - Established a broad set of conservative reuse and thread-cap/no-cap explainability guards with clean validation history.
   - Confirmed through fresh subquery baseline artifacts that current explain output for nested subquery families is still mostly root-level for preference decisions.

2. **What is working well?**
   - Iteration discipline continues to prevent speculative behavior edits: baseline evidence first, then scoped implementation.
   - Explain artifacts are good enough to detect strategy and top-level preference outcomes quickly.

3. **What is not working / blockers?**
   - Missing child/node-level preference decision surfacing limits our ability to safely change or validate subquery propagation logic.
   - Without nested decision visibility, runtime propagation edits would be hard to audit and prone to overfitting.

4. **Should the approach be adjusted?**
   - Yes: perform an instrumentation-first slice for nested decision surfacing (no routing change), then revisit propagation behavior with clear before/after evidence.

5. **Next priorities**
   - Add typed child/node-level preference decision reporting for subquery-relevant nodes.
   - Add regression tests locking that metadata shape.
   - After visibility is in place, evaluate a minimal propagation behavior change with targeted measurement.

### Reflection checkpoint (iteration 11)

1. **What has been accomplished so far?**
   - Landed conservative row-source reuse improvements (range arithmetic/comparison/bool, instant reuse) with consistent explain metadata for applied and rejected paths.
   - Added mismatch diagnostics (`not_reused` reasons/guards/rejected alternatives) and mode-alignment regression tests.
   - Added subquery no-thread-cap nested regression coverage to preserve known-safe execution preference behavior.

2. **What is working well?**
   - The loop discipline (baseline explain artifact → small change → package tests → commit) keeps risk low and review quality high.
   - Explain artifacts and `physicalDecisions` make decisions auditable and reduce blind optimization edits.

3. **What is not working / blockers?**
   - Recent wins are mostly observability/regression guards, not new runtime speedups.
   - We are nearing diminishing returns in the current repeated-binary family without widening scope.

4. **Should the approach be adjusted?**
   - Yes: pivot from reuse-observability polish to a bounded first subquery-preference propagation slice with measurable runtime signal.
   - Keep semantics conservative; prefer additive decision plumbing and guardrails before any routing behavior shifts.

5. **Next priorities**
   - Implement a narrow subquery preference propagation candidate (single shape, single preference path) with before/after explain and query-log evidence.
   - Follow with estimate-plumbing scaffolding that reports candidate estimates without changing strategy selection.

## Attempt notes policy

Each Ralph iteration should be one complete evaluated attempt. Use attempt IDs like:

```text
YYYYMMDD-<iteration number>-short-shape-name
```

For each attempt, create:

```text
.pi/loops/native-sql-optimization-sweep/attempts/<attempt-id>.md
```

The attempt file should contain:

- hypothesis and expected value;
- exact query/corpus/profile;
- baseline artifacts;
- implementation summary;
- validation commands and outputs;
- before/after signal table;
- decision: keep, reject, defer, or split;
- commit hash if kept and committed.

The canonical loop file should keep only a 5-10 line summary plus artifact pointers.

## Artifact policy

Durable artifacts:

- Named benchmark/sweep artifacts under `harness/artifacts/bench/...` when used as evidence for accepted or rejected runtime claims.
- Named explain artifacts under `harness/artifacts/explain/...` for single-query before/after structural claims.
- Attempt summaries under `.pi/loops/native-sql-optimization-sweep/attempts/`.
- Commits for accepted code changes when commit permission is active.

Ephemeral artifacts:

- `/tmp` corpora and scratch scripts are allowed during attempts but must be either promoted to durable paths or described in the attempt file before relying on them.
- Raw command output should not be pasted into the canonical loop file unless it is short and decision-critical.

## Runner policy

Iteration unit: one meaningful, complete evaluated attempt. A Ralph iteration should be large enough to produce a decision-quality result, not just a single micro-step such as reading one file, running one command, or adding one small helper. The default guideline is: one iteration = one attempt.

Each attempt should usually include:

1. choose candidate from active hypotheses/evidence;
2. baseline;
3. implement or split/defer;
4. validate;
5. measure;
6. accept/reject/defer/split;
7. record and commit;
8. reset active checklist for the next attempt;
9. call `ralph_done`.

Only split an attempt across multiple Ralph iterations when the work is too large to complete safely in one iteration or a predeclared blocker/approval boundary is hit. In that case, the iteration must end with a durable partial decision and updated attempt notes, not a generic progress update.

Create `.ralph/native-sql-optimization-sweep.md` at start time as a short runtime pointer to this canonical file rather than duplicating the full charter.

## Ralph runtime task template

Use this content for `.ralph/native-sql-optimization-sweep.md` when starting Ralph:

```markdown
# Native SQL optimization sweep

Open-ended optimization loop. Canonical charter and live context:
`.pi/loops/native-sql-optimization-sweep/loop.md`

## Current iteration checklist
One Ralph iteration should complete one meaningful optimization attempt, not one small task.

- [ ] Read canonical loop file and latest active attempt notes.
- [ ] Select exactly one high-expected-value candidate.
- [ ] Create/update `.pi/loops/native-sql-optimization-sweep/attempts/<attempt-id>.md`.
- [ ] Capture baseline evidence.
- [ ] Implement the smallest safe change, or record defer/split/reject without code changes.
- [ ] Run correctness validation required by the change.
- [ ] Run measurement required by the claim.
- [ ] Decide keep/reject/defer/split and update canonical loop file.
- [ ] Commit accepted coherent changes if commit permission is active.
- [ ] Reset this checklist for the next iteration before calling `ralph_done`.
```

## Compaction policy

Trigger compaction when either condition holds:

- five completed attempts have accumulated since the last compaction;
- `.pi/loops/native-sql-optimization-sweep/loop.md` exceeds roughly 500 lines.

Compaction procedure:

1. Move older attempt summaries beyond the last 3-5 into `.pi/loops/native-sql-optimization-sweep/attempt-archive.ndjson` as compact JSON lines with attempt ID, decision, key signal, artifact pointers, and commit hash if any.
2. Keep current state, guardrails, evaluation protocol, active hypotheses, and the last 3-5 summaries in this file.
3. Add a one-line compaction event under recent summaries with timestamp and archive pointer.

## Commit policy

Commit permission is active for this loop unless the user explicitly revokes it.

At every accepted attempt boundary:

- Commit accepted coherent changes before calling `ralph_done`.
- Include the loop/attempt notes, plan updates, tests, corpus changes, implementation, and validation-facing tooling changes that belong to the accepted attempt.
- Keep instrumentation-only, keying-only, and behavior-changing SQL-shape work in separate commits when that improves review and rollback.
- Commit messages must be self-contained: reviewers should be able to evaluate the optimization from the commit alone.
- Do not reference ephemeral artifacts (`/tmp`, transient local paths, ad-hoc scratch files, harness artifacts) in commit messages.
- Include the key metrics needed to judge the optimization independently (before/after signals and what moved), plus the validation commands and their outcomes in non-trivial commit bodies. Include % change
- Do not push unless explicitly asked.
- Make sure `\n` are evaluated as newlines and not litral characters in the commit message

## Runtime decision policy

Preapproved during loop execution once the user starts it:

- Run project Go tests and non-destructive script validations.
- Use benchmark stack commands that do not delete volumes.
- Rebuild/recreate benchmark promshim containers before measuring changed code.
- Create named artifacts under `harness/artifacts/...`.
- Create and edit loop attempt files under `.pi/loops/native-sql-optimization-sweep/`.

Requires explicit user approval during the loop:

- Compliance volume deletion/reset.
- Benchmark volume reset with `--bench-reset --yes` unless the current attempt explicitly states it and the user preapproves at start.
- Pushing, force-pushing, tagging, opening PRs, publishing artifacts, or changing external infrastructure.
- Running unusually expensive broad sweeps, profile-500k setup, 1y all-corpus sweeps, or repeated long-running experiments beyond the attempt's expected value.

Fallback behavior:

- If benchmark data is missing, prefer `run-sweep.sh --setup ... --seed missing` over destructive reset.
- If measurements are noisy, rerun one focused measurement from a quiet stack; if still noisy, defer rather than overfitting.
- If an optimization needs unsupported semantics, split out correctness/coverage planning instead of adding it inside this loop attempt.