# Native SQL optimization sweep loop

## Objective

Continuously improve promshim's PromQL → logical IR → native SQL and fallback execution surface by repeatedly finding high-expected-value optimization opportunities, implementing the safest useful candidate, measuring correctness and runtime signals, and keeping or rejecting each attempt based on evidence.

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
- Measurement and tooling improvements when they reduce future optimization risk or cost.

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
- The claimed runtime signal moves in the expected direction:
  - CSE/dedup/row-source reuse: `FunctionExecute`, `ArrayMap`/array counters, join rows, pipeline stages, or duplicated source work drops.
  - Pushdown/pruning: `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes`, or `EXPLAIN PLAN indexes=1` improves with unchanged result rows.
  - CPU reduction: `UserTimeMicroseconds`, `RealTimeMicroseconds`, and relevant thread/settings evidence improve without unacceptable latency regression.
  - Memory reduction: query-log memory or `MemoryTrackerUsage` improves without correctness loss.
  - SQL-builder migration: no runtime claim unless `EXPLAIN`/ProfileEvents change; otherwise accept only for maintainability with stable SQL/goldens.
- Broad optimizations should pass a focused corpus around the affected shape and at least smoke-test adjacent shapes.
- Query-specific optimizations require either high observed cost, common dashboard relevance, or a strong path toward generalization.

## Rejection, deferral, and split rules

Reject or revert when:

- Compliance or focused correctness fails due to the change.
- The optimization changes Prometheus-visible semantics.
- The required ProfileEvents/EXPLAIN/query-log signal does not move and the commit's purpose is runtime improvement.
- The change is only cosmetic SQL churn with identical `EXPLAIN SYNTAX` and ProfileEvents.
- It adds broad complexity for one low-value query without durable generalization.

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

1. **Native row-source reuse for repeated range sources**
   - Plan: `.agents/plans/native-row-source-reuse-optimizer.md`
   - Current target family: non-cancelled repeated range-function arithmetic and comparisons, including bool comparisons where semantics are preserved by existing comparison SQL behavior (e.g., `rate(...) + rate(...)`, `rate(...) * rate(...)`, `rate(...) >= rate(...)`, `rate(...) >= bool rate(...)`) in one native query.
   - Expected signal: duplicated source work drops in ProfileEvents/query log; strategy remains `native_sql`.
   - Next action: add typed eligibility/rejection metadata and keying for less-trivial repeated sources beyond direct one-to-one repeated subtrees.

2. **Subquery physical preference propagation**
   - Target: nested range/subquery shapes where an inner source is eligible for sparse/native-grid strategy but parent context suppresses or fails to propagate the best preference.
   - Expected signal: physical-decision metadata shows intended strategy inside nested shape; benchmark rows keep native SQL and reduce CPU/memory or avoid fallback.
   - First action after row-source reuse: map representative subquery corpus rows and compare physical decisions at root vs child nodes.

3. **Estimate inputs for later CBE**
   - Target: add explicit cardinality/window/step/lookback estimate plumbing without changing routing.
   - Expected signal: explain reports candidate estimates, and no strategy changes occur until a later CBE plan.
   - First action after subquery mapping: identify existing analysis fields and benchmark corpus metadata that can seed estimates safely.

### Recent attempt summaries

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