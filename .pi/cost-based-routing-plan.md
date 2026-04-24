# Cost-based routing plan for promshim

## Purpose

Promshim currently routes by a strict capability hierarchy:

1. whole-query delegation to ClickHouse PromQL
2. native SQL lowering
3. local Go executor with subtree pushdown
4. full local execution

That hierarchy is the right **coverage** model. It keeps native gaps visible,
keeps development pressure on tiers 1/2, and preserves the long-term path where
ClickHouse owns more of PromQL over time.

The question for this plan is narrower: once a higher tier and a lower tier are
both known-correct for a query, should `prefer` mode ever choose the lower tier
because measured cost says it is faster for the current dataset size?

The answer should be: **only behind an explicit cost-routing policy, only for
bounded query families, only after shadow evidence, and never as an excuse to
stop tier-2 coverage work.**

## Source evidence and constraints

### Project constraints

- New feature/coverage work still belongs in tiers 1 and 2.
- Tiers 3 and 4 are fallbacks. This plan may evaluate them as runtime
  candidates, but it must not expand their feature surface without explicit
  approval.
- The compliance allowlist remains for accepted deviations only. Routing changes
  must not add allowlist entries.
- `force_supported` remains the native-only visibility mode and must continue to
  fail when tier 2 cannot produce a native root.
- Default behavior must remain strict until a cost policy has shadow evidence.

### Existing `.pi` guidance to reuse

- `.pi/optimizer/04-direct-window-join-cost-model.md` already sketches a local
  cost model for one native-SQL shape: direct window join vs materialize/window
  range-function lowering.
- `.pi/optimizer/04-time-series-value-only-mode.md` identifies cases where
  native SQL does unnecessary work when output labels collapse to one series.
- `.pi/optimizer/02-fragment-subtree-hashing.md` describes plan/cache costs that
  matter for small queries where planning overhead is a large share of latency.
- `.pi/optimizer/04-cost-model-driven-pass-ordering.md` motivates cheap shape
  fingerprints and pass skipping; the same fingerprinting idea should feed the
  router.
- `.pi/path2-compliance-matrix.md` and
  `.pi/path2-native-sql-compliance-refresh-2026-04-22.md` establish that broad
  tier-2 support is a compliance goal, not something to abandon for local
  speedups.
- `.pi/skills/measuring-ch-optimizations/SKILL.md` defines how to read bench
  and ClickHouse profile evidence: wall-clock is noisy; `strategy_used`,
  `SelectedRows`, `ReadCompressedBytes`, `FunctionExecute`,
  `MemoryTrackerUsage`, and explain SQL shape are required signals.

### Current bench evidence to calibrate against

The current artifacts show the central tension:

- On the short fixture, local fallback is often faster for small or overhead-heavy
  shapes:
  - `plain_selector_range`: native `24.79 ms`, fallback `7.19 ms`, F/N `0.29`.
  - `topk_range`: native `39.26 ms`, fallback `9.59 ms`, F/N `0.24`.
  - `vector_match_range`: native `74.89 ms`, fallback `16.43 ms`, F/N `0.22`.
  - `histogram_quantile_instant`: native `147.31 ms`, fallback `60.02 ms`, F/N
    `0.41`.
- On long-range data, native remains necessary for large range reductions:
  - `rate_5m_range_1d` at 7d: native `577 ms`, fallback `3379 ms`, F/N `5.85`.
  - `rate_1h_range_30d`: native `5435 ms`, fallback `9661 ms`, F/N `1.78`.
  - `avg_over_time_1d_range_1y`: native `6332 ms`, fallback `7245 ms`, F/N
    `1.14`.
  - `sum_rate_by_job_range_30d`: native `1407 ms`, fallback `2533 ms`, F/N
    `1.80`.
- Some instant long-window shapes still look local-favorable in current bench
  artifacts:
  - `histogram_quantile_6h_instant_30d`: native `163 ms`, fallback `33 ms`, F/N
    `0.20`.
  - `histogram_quantile_1d_instant_1y`: native `157 ms`, fallback `29 ms`, F/N
    `0.19`.
  - `plain_selector_instant_1y`: native `17.65 ms`, fallback `11.44 ms`, F/N
    `0.65`.
  - `rate_1d_instant_1y`: native `28.52 ms`, fallback `21.91 ms`, F/N `0.77`.

Interpretation:

- Native SQL should remain the default for high-cardinality/range-reduction
  paths.
- The cost model must distinguish **instant/short/small-output** queries from
  **range/high-reduction/large-input** queries.
- A naive "native if supported" policy leaves small-query latency on the table,
  but a naive "local if currently faster" policy risks severe cliffs as input
  size grows.

## Requirements

1. Keep strict routing as the default and as the reference behavior.
2. Add an opt-in cost-routing policy that can initially shadow decisions without
   changing served results.
3. Make every routing decision explainable in headers, explain plans, and
   metrics.
4. Use cheap, bounded estimates. Do not add an unconditional metadata probe that
   doubles ClickHouse round trips for simple queries.
5. Prefer tier 2 unless the lower-tier candidate has a clear predicted win and
   the query is under hard safety caps.
6. Preserve `force_supported` semantics: no fallback is allowed in that mode.
7. Preserve compliance: results must remain Prometheus-equivalent under both
   strict and cost policies.
8. Treat bench/profile artifacts as calibration inputs, not eternal truths.
9. Calibrate by query family and dataset size, not by one global threshold.
10. Keep all behavior reversible through config and per-request override.

## Non-goals

- Do not replace the tier hierarchy as the conceptual model.
- Do not stop tier-2 feature work because local is faster for some small cases.
- Do not add ML/black-box prediction. Start with transparent rules and measured
  coefficients.
- Do not route large uncertain queries to tier 4 just because one short fixture
  looked faster.
- Do not expand tier 3/4 feature support as part of this plan. They are runtime
  candidates only where they already work.
- Do not make production default behavior cost-based until shadow mode has real
  evidence.

## Routing policies

Add a separate routing policy instead of overloading
`PROM_SHIM_NATIVE_LOWERING_MODE`:

```text
PROM_SHIM_ROUTING_POLICY=strict|cost_shadow|cost_prefer
```

Per-request override:

```text
routing_policy=strict|cost_shadow|cost_prefer
```

Policy behavior:

| Policy | Served result | Candidate behavior | Intended use |
|---|---|---|---|
| `strict` | Existing tier hierarchy | No cost override | Default and baseline |
| `cost_shadow` | Existing strict winner | Compute and record what cost routing would choose; optionally run the alternate candidate asynchronously for bounded families | Calibration and rollout safety |
| `cost_prefer` | Cost-selected candidate, subject to hard caps | Fall back to strict if estimates are missing, uncertain, or above caps | Opt-in latency optimization |

Interaction with native lowering modes:

- `force_supported`: routing policy is ignored; root must be `native_sql`.
- `off`: routing policy is ignored; serve local baseline.
- `shadow`: keep existing shadow semantics initially; do not mix with cost
  routing until the new routing shadow metrics are stable.
- `prefer` / `explain`: routing policy may select among already-supported
  candidates.

## Candidate set

### Initial candidates

Start with only two root-level candidates:

1. **Strict candidate** — whatever the current hierarchy would serve in
   `prefer` mode.
2. **Full local candidate** — the equivalent plan produced with native lowering
   disabled.

Reason: this directly answers tier 2 vs tier 4 without increasing tier 3 scope.
It also lets us compare against current benchmark fields (`nativeP50Ms` and
`fallbackP50Ms`).

### Later candidates

Only after root-level cost routing is stable, add:

3. **Local with subtree pushdown candidate** — current tier-3 plan where it
   already exists.
4. **Alternative native SQL shape candidate** — e.g. the range-function strategy
   from `.pi/optimizer/04-direct-window-join-cost-model.md`.
5. **Whole-query delegation vs native SQL** — compare tier 1 and tier 2 where
   both are supported.

Each added candidate needs its own shadow metrics and hard caps.

## Query classification

Introduce a cheap query classifier shared by planning, explain, bench reports,
and routing metrics.

Fields:

```go
type QueryCostClass struct {
    Endpoint              string // query or query_range
    Family                string // selector, rate, range_rate, histogram_quantile, vector_match, ...
    RootStrategyStrict    string
    OutputKind            string
    HasAggregation        bool
    HasRangeFunction      bool
    HasVectorJoin         bool
    HasHistogram          bool
    HasSubquery           bool
    HasLabelMutation      bool
    HasSelectionAgg       bool
    DropsAllLabels        bool
    SelectorCount         int
    EstimatedSeries       int64
    EstimatedInputSamples int64
    EstimatedOutputPoints int64
    RangePointsPerSeries  int64
    LookbackMS            int64
    StepMS                int64
    OverlapSlots          float64
    NativeRoundTrips      int
    LocalRoundTrips       int
}
```

Implementation guidance:

- Reuse the fragment-shape idea from `.pi/optimizer/04-cost-model-driven-pass-ordering.md`.
- Reuse selector signatures from native analysis/renderer where possible.
- Put family names in terms of domain/query behavior, not plan phase labels.
- Include the classifier output in explain responses so reviewers can understand
  decisions.

## Cost model inputs

### Always-available inputs

These require no extra ClickHouse query:

- endpoint (`query` vs `query_range`)
- expression family and fragment shape
- range points per series
- lookback and step
- overlap factor
- number of selectors
- grouping labels / whether labels collapse
- strict strategy and ClickHouse roundtrip count from the plan
- response limits

### Cached metadata inputs

These can come from a TTL cache populated opportunistically:

- matched series count from `timeSeriesTags(...)` with the same matcher/time
  overlap clauses used by the real query
- approximate samples per series from observed profile/fixture data or optional
  metric-specific scrape interval configuration
- recent observed native/local latency by query cost class
- recent observed result size by query cost class

Rules:

- In `cost_prefer`, if a needed estimate is missing and no conservative rule
  applies, choose strict.
- In `cost_shadow`, missing estimates should be recorded as
  `reason="missing_estimate"` and can schedule async/background estimation.
- Metadata probes must have a small timeout/budget and must be cached. The
  default served query must not pay an uncached probe cost.

## Initial routing rules

Start with transparent rules, not fitted regression.

### Always strict-native rules

Choose the strict candidate when any of these are true:

- native lowering mode is `force_supported`
- strict winner is whole-query delegation and no explicit tier1-vs-tier2 model
  exists yet
- estimates are missing for a family that needs them
- estimated input samples or output points exceed safety caps
- range query has high reduction ratio or large range points per series
- query has subquery range expansion unless explicitly allowlisted
- query has vector matching over estimated high cardinality
- local candidate would require more than the configured local roundtrip cap
- predicted local win is smaller than both:
  - relative margin, initially `local_cost <= 0.70 * native_cost`
  - absolute margin, initially `native_cost - local_cost >= 3ms`

### Local-candidate allowlist for first rollout

Only these families may choose local in the first `cost_prefer` rollout:

1. **Instant plain/regex selectors**
   - Evidence: long profiles show fallback faster for plain selectors
     (`F/N 0.65` to `0.78`) and short fixture shows stronger local win.
   - Caps: small estimated output series and points; no range output.
2. **Instant rate-like single-vector functions**
   - Evidence: `rate_1d_instant_1y` fallback `21.91 ms` vs native `28.52 ms`.
   - Caps: one selector, one output vector, bounded matched series, bounded
     lookback samples.
3. **Instant histogram quantile helper over bounded bucket sets**
   - Evidence: current long artifacts show large local win (`F/N ~0.19-0.20`).
   - Extra caution: require bucket/cardinality caps and shadow result comparison
     before serving local.
4. **Short range selector-only outputs**
   - Evidence: short fixture `plain_selector_range` fallback is much faster.
   - Extra caution: disable if estimated output points are not tiny.

Everything else stays strict until there is class-specific shadow evidence.

### Native-preferred classes from current evidence

These should remain strict-native in the initial model:

- long range `rate(...)`, `avg_over_time(...)`, and `sum by (...) (rate(...))`
- subquery-backed range forms
- high-cardinality aggregations
- high-cardinality vector matching
- any query where local requires raw sample transfer much larger than final output

Current artifacts show native beating fallback on these despite native still
being slower than reference Prometheus. Those are tier-2 optimization targets,
not local-routing targets.

## Cost formula v0

Use a hybrid of rules and calibrated baselines:

```text
native_cost = native_family_base[class]
            + native_scan_weight[class] * estimated_input_samples
            + native_output_weight[class] * estimated_output_points
            + native_roundtrip_weight * native_roundtrips
            + native_shape_penalty[class]

local_cost  = local_family_base[class]
            + local_fetch_weight[class] * estimated_input_samples
            + local_eval_weight[class] * expression_complexity * estimated_output_points
            + local_roundtrip_weight * local_roundtrips
            + local_heap_penalty[class]
```

For v0, most coefficients can be simple constants generated from bench reports:

- `native_family_base` = median native p50 for small fixture rows of that family.
- `local_family_base` = median fallback p50 for small fixture rows of that family.
- `scan_weight` adjusted by long-range profile deltas (`7d`, `30d`, `1y`).
- `shape_penalty` from known expensive native shapes in profiling docs, e.g.
  window array materialization, histogram helper stages, vector joins.

If the model lacks coefficients for a class, choose strict.

## Calibration artifacts

Add a generated calibration artifact rather than hard-coding all thresholds:

```text
.pi/cost-routing-calibration.json
.pi/cost-routing-calibration.md
```

Generator command:

```bash
go run ./cmd/promshim-routing-calibrate \
  --bench harness/artifacts/bench-report.json \
  --bench harness/artifacts/bench-report-7d.json \
  --bench harness/artifacts/bench-report-30d.json \
  --bench harness/artifacts/bench-report-1y.json \
  --profile harness/artifacts/ch-profile.json \
  --out-json .pi/cost-routing-calibration.json \
  --out-md .pi/cost-routing-calibration.md
```

The generator should:

- group by stable query family/category
- compute median native, fallback, and Prometheus p50
- compute fallback/native ratio by profile
- mark classes as `local_candidate`, `native_required`, or `insufficient_data`
- include ClickHouse profile counters when available
- call out strategy flips as hard warnings
- include the artifact paths and generation timestamp

The calibration file should be reviewed like code. It is input to routing but
not a substitute for runtime safety caps.

## Observability and explainability

### Response headers

Add headers on query endpoints:

```text
X-Promshim-Routing-Policy: strict|cost_shadow|cost_prefer
X-Promshim-Routing-Decision: strict|local_override|shadow_only|strict_missing_estimate|strict_over_cap|strict_low_confidence
X-Promshim-Strict-Strategy: delegated_promql|native_sql|local|chunked_local
X-Promshim-Selected-Strategy: delegated_promql|native_sql|local|chunked_local
X-Promshim-Routing-Reason: <short stable reason>
```

`X-Promshim-Strategy` can keep its existing meaning as selected root strategy.

### Explain output

Add a `routing` object to explain responses:

```json
{
  "policy": "cost_shadow",
  "strictStrategy": "native_sql",
  "selectedStrategy": "native_sql",
  "wouldSelect": "local",
  "decision": "shadow_only",
  "reason": "instant_histogram_quantile_local_candidate_under_caps",
  "class": { "family": "instant_histogram_quantile", "estimatedInputSamples": 12345 },
  "cost": { "native": 160.0, "local": 32.0, "unit": "ms_p50_estimate" },
  "caps": { "maxLocalInputSamples": 50000, "maxLocalOutputPoints": 10000 }
}
```

### Metrics

Expose process-local metrics:

```text
promshim_routing_decisions_total{policy,decision,strict_strategy,selected_strategy,family,reason}
promshim_routing_shadow_runs_total{family,strict_strategy,alternate_strategy,status}
promshim_routing_shadow_duration_seconds{family,candidate}
promshim_routing_shadow_divergences_total{family,category}
promshim_routing_estimate_missing_total{family,field}
promshim_routing_over_cap_total{family,cap}
promshim_routing_prediction_error_ratio{family}
```

Shadow metrics should be bounded in cardinality. Use stable family/reason labels,
not raw query strings.

## Implementation phases

### Phase 1. Make strict decisions observable

Goal / scope: add routing metadata without changing behavior.

Code areas:

- `internal/promshim/service.go`
- `internal/promshim/httpapi/response.go`
- `internal/promshim/local/explain.go`
- native/logical analysis packages as needed for query classification
- benchmark report structs if needed

Tasks:

1. Add `RoutingPolicy` parsing with default `strict`.
   - Validation: unit tests for env/request override parsing.
   - Risk: avoid changing native lowering mode semantics.
2. Add `QueryCostClass` extraction from existing logical/native analysis.
   - Validation: table tests for selector, rate, range rate, histogram,
     aggregation, vector match, subquery.
   - Risk: family names must be stable for metrics and calibration.
3. Surface strict routing metadata in headers and explain output.
   - Validation: HTTP API tests for headers/explain JSON.
   - Risk: do not break existing Prometheus response envelope.
4. Extend bench output to preserve cost class and selected strategy.
   - Validation: run a small bench and verify report fields.

Exit criteria:

- `strict` behavior is byte/result compatible with current behavior.
- Every query can explain what strict strategy it selected and what cost class it
  belongs to.

### Phase 2. Build offline calibration from existing artifacts

Goal / scope: turn current bench/profile artifacts into a reviewable seed model.

Code areas:

- new `cmd/promshim-routing-calibrate/`
- possibly `internal/promharness` report parsing helpers
- `.pi/cost-routing-calibration.{json,md}` generated outputs

Tasks:

1. Parse current bench report schema and group rows by category/family.
   - Validation: unit tests with minimal synthetic bench reports.
2. Join optional ClickHouse profile artifacts by log comment or normalized query.
   - Validation: fixture test for profile join; missing profiles are allowed.
3. Emit family summaries and initial recommendations:
   - `local_candidate`
   - `native_required`
   - `insufficient_data`
   - `do_not_route_due_to_strategy_flip`
4. Seed recommendations from current evidence:
   - local candidates: bounded instant selector/rate/histogram helper, tiny range
     selector
   - native required: long range rate/avg/sum-rate/subquery forms
5. Document calibration uncertainty and artifact paths in the generated markdown.

Exit criteria:

- Calibration can be regenerated from the current artifacts.
- The generated markdown explains why each initial family is or is not a local
  candidate.

### Phase 3. Add estimate plumbing without serving alternate routes

Goal / scope: estimate input/output size cheaply and cache metadata.

Code areas:

- `internal/promshim/storage/`
- `internal/promshim/native/selector.go` or related selector signature helpers
- service-level cache plumbing
- metrics package

Tasks:

1. Define selector signatures stable across equivalent matcher order.
   - Validation: equality tests for reordered matchers; inequality tests for
     different offset/lookback/time bounds.
2. Add a TTL cache for selector stats:
   - matched series count
   - time-overlap bounded estimate
   - optional scrape interval estimate
3. Implement budgeted stats probes over `timeSeriesTags(...)`.
   - Validation: storage SQL tests; integration smoke against harness stack if
     available.
   - Risk: probes must use time-overlap predicates and must be timeout-bound.
4. Add cache-only estimate mode for served queries.
   - In `cost_shadow`, missing cache can schedule async fill.
   - In `cost_prefer`, missing cache means strict unless the class has a safe
     no-metadata rule.
5. Expose missing-estimate and probe-failure metrics.

Exit criteria:

- Estimates appear in explain output.
- No default request pays an uncached synchronous probe cost.

### Phase 4. Implement cost shadowing

Goal / scope: compute cost decisions and optionally execute alternate local
candidates without changing served results.

Code areas:

- service planner orchestration
- shadow evaluator or new routing-shadow package
- response/explain/metrics

Tasks:

1. Implement `CostModel.Decide(class, estimates, calibration)`.
   - Validation: table tests covering current evidence families and cap reasons.
2. Add `cost_shadow` policy that serves strict and records `wouldSelect`.
   - Validation: HTTP tests show strict result and shadow decision metadata.
3. Add bounded alternate execution for initial local-candidate families.
   - Validation: compare strict vs alternate result normalization for exact match
     or existing tolerances.
   - Risk: do not double-execute high-cost queries; enforce caps before running
     alternate.
4. Record prediction error: strict duration vs alternate duration where both ran.
   - Validation: metrics tests; manual smoke with `/metrics`.
5. Add harness corpus variant that runs representative rows with
   `routing_policy=cost_shadow`.

Exit criteria:

- `cost_shadow` produces no served-result changes.
- Shadow comparisons report zero unexpected divergences on the initial corpus.
- Metrics show enough data to tune thresholds.

### Phase 5. Enable cost-prefer for the safest families

Goal / scope: serve local for a small allowlist of proven low-risk classes.

Code areas:

- service routing selection
- config/env handling
- explain/headers/metrics
- harness corpora

Tasks:

1. Add a config gate for each local-candidate family or class group.
   - Example: `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=selector_instant,rate_instant`.
2. Enforce hard caps:
   - max estimated input samples
   - max estimated output series
   - max estimated output points
   - max local ClickHouse roundtrips
   - max local observed p95 from recent shadow data if available
3. Enable `cost_prefer` for instant selector and instant rate classes first.
   - Validation: focused differential corpus under `routing_policy=cost_prefer`.
4. Add instant histogram helper only after shadow evidence confirms current bench
   win without divergence.
   - Validation: histogram-focused corpus; long-profile bench row comparison.
5. Keep range-query local routing disabled except tiny range selector-only rows
   until explicit evidence says otherwise.

Exit criteria:

- `cost_prefer` improves the chosen small-query benchmark rows by a meaningful
  margin without regressing long-range/native-required rows.
- Prefer-mode compliance stays clean under strict routing.
- Cost-prefer differential corpus stays clean against Prometheus.

### Phase 6. Consider tier-3 and native-shape candidates

Goal / scope: extend the model beyond root native vs full local only after the
basic router is proven.

Tasks:

1. Add tier-3 candidate evaluation only for existing subtree-pushdown shapes.
   - No new tier-3 feature coverage in this phase.
   - Validation: compare tier 3 vs tier 4 vs tier 2 for bounded corpora.
2. Integrate native SQL shape choices from
   `.pi/optimizer/04-direct-window-join-cost-model.md`.
   - This is a tier-2 internal cost model and can graduate earlier if desired.
3. Integrate plan-cache/subtree-hash evidence from
   `.pi/optimizer/02-fragment-subtree-hashing.md`.
   - If plan cache lands, recalibrate small-query native costs before broadening
     local routing.
4. Revisit value-only/scalar-output native optimization from
   `.pi/optimizer/04-time-series-value-only-mode.md`.
   - If native small-query costs drop, local overrides may no longer be needed
     for some classes.

Exit criteria:

- Cost routing considers tier 3 only where it already exists and proves useful.
- Tier-2 SQL-shape cost models are preferred over local fallback when they solve
  the same small-query overhead.

## Validation ladder

### Fast unit validation

```bash
go test ./internal/promshim/...
go test ./cmd/promshim-routing-calibrate/...
```

### HTTP/explain validation

```bash
go test ./internal/promshim/httpapi ./internal/promshim
```

Manual smoke:

```bash
./scripts/run-compliance.sh --keep-up --skip-native
curl 'http://localhost:29091/api/v1/query?query=up&routing_policy=cost_shadow&explain=1'
curl -s 'http://localhost:29091/metrics' | rg 'promshim_routing'
```

### Correctness validation

Strict behavior:

```bash
./scripts/run-compliance.sh --ready-timeout 120
```

Cost-shadow differential:

```bash
./scripts/run-harness.sh --corpus phase7-rollout.json --subjects shim
# plus a new routing-cost-shadow corpus once added
```

Cost-prefer differential:

```bash
./scripts/run-harness.sh \
  --corpus routing-cost-prefer.json \
  --subjects shim
```

### Performance validation

Short fixture:

```bash
./scripts/ch-profile-capture.sh --matrix --baseline /tmp/no-bench-baseline.json
```

Long range:

```bash
./scripts/ch-profile-capture.sh \
  --baseline /tmp/no-bench-baseline.json \
  --long-range all \
  --repeats 3 \
  --warmup 1
```

Compare:

```bash
./scripts/bench-matrix.sh --per-query
./scripts/ch-profile-diff.sh <before-profile.json> <after-profile.json>
```

Accept/reject using `.pi/skills/measuring-ch-optimizations/SKILL.md`:

- investigate any `strategy_used` flip
- require profile counters for claims, not just wall-clock p50
- treat small p50 deltas as noise unless counters support them
- preserve before/after artifacts

## Rollout strategy

1. Land strict observability only.
2. Generate calibration from current artifacts.
3. Run `cost_shadow` locally and in the harness until decisions stabilize.
4. Enable `cost_prefer` only in local/dev with a narrow family allowlist.
5. Add dashboard corpus coverage for cost-prefer mode.
6. Recalibrate after each major tier-2 SQL optimization.
7. Only consider broader default use if:
   - strict compliance is clean
   - cost-prefer differential is clean
   - shadow divergence is zero for the target families
   - small-query p50/p95 improves materially
   - long-range native-required families never route local

## Risk register

| Risk | Mitigation |
|---|---|
| Local route is faster in fixture but cliffs in production cardinality | hard input/output caps; missing estimates choose strict; shadow before serving |
| Metadata probes add more overhead than they save | cache-only in served path; async probe in shadow; strict on cache miss |
| Cost model hides native coverage regressions | `force_supported` unchanged; strict policy remains default; compliance still runs native-only |
| Strategy changes make bench results look green for the wrong reason | bench reports and metrics must include strict/selected strategy; strategy flips are hard warnings |
| Alternate shadow execution overloads ClickHouse | only run alternates under caps; sample rate; global concurrency limit |
| Family labels explode metric cardinality | stable enum labels only; raw query strings never in metrics |
| Tier 3/4 start accumulating new feature work | plan explicitly forbids new lower-tier coverage; review changes against this constraint |
| Plan cache or tier-2 optimizations invalidate calibration | regenerate `.pi/cost-routing-calibration.*` after major native optimizer changes |

## Initial task breakdown

1. Add routing policy config and strict decision reporting.
2. Add query cost classification and explain/header surfacing.
3. Build `promshim-routing-calibrate` and generate `.pi/cost-routing-calibration.*`.
4. Add selector-stat signature/cache/probe plumbing with no behavior change.
5. Implement cost model table tests and `cost_shadow` metadata-only decisions.
6. Add bounded alternate execution in `cost_shadow` for initial local-candidate
   families.
7. Add routing metrics and a routing shadow harness corpus.
8. Enable `cost_prefer` for instant selector/rate under hard caps.
9. Re-run compliance, cost-prefer differential, short bench/profile, and
   long-range bench/profile.
10. Recalibrate and decide whether histogram instant and tiny range selectors are
    safe to add.

## Definition of done

This plan is complete when:

- strict routing remains the default and remains behavior-compatible;
- every query can explain its strict strategy, cost class, estimates, and routing
  decision;
- calibration artifacts exist under `.pi/` and can be regenerated;
- `cost_shadow` runs bounded alternate candidates and reports divergence and
  prediction error;
- `cost_prefer` is available only behind explicit config and only for classes
  with hard caps;
- validation demonstrates correctness against Prometheus and measured latency
  improvement for selected small-query classes;
- long-range/high-reduction classes continue to route to tier 2 unless a future
  measured model explicitly proves otherwise.
