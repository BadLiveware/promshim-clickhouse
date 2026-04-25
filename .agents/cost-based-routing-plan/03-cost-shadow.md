# 03. Cost shadow

## Purpose and scope

Implement cost decisions and bounded alternate execution in `cost_shadow` without
changing served results. This slice computes what cost routing would choose,
records why, optionally runs safe alternate local candidates, compares results,
and reports prediction error.

`cost_shadow` must serve the strict candidate. Any alternate execution is
observability and calibration only.

## Prerequisites

- Complete [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md).
- Complete [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md).
- Classification, calibration, and estimates are visible in explain output.
- Missing estimates and over-cap cases choose strict.

## Affected areas

- service planner orchestration
- cost model package or routing decision package
- shadow evaluator or new routing-shadow package
- response/explain/metrics
- harness corpora
- `scripts/run-sweep.sh`, `scripts/run-bench.sh`, and `cmd/promshim-bench`
  routing-policy sweep axis

## Candidate set

### Initial candidates

Start with only two root-level candidates:

1. **Strict candidate** — whatever the current hierarchy would serve in
   `prefer` mode.
2. **Full local candidate** — the equivalent plan produced with native lowering
   disabled.

Reason: this directly answers tier 2 vs tier 4 without increasing tier 3 scope.
It also lets the model compare against current benchmark fields
(`nativeP50Ms` and `fallbackP50Ms`).

### Explicitly out of scope for this slice

Do not add these yet:

- local with subtree pushdown candidate
- alternative native SQL shape candidate
- whole-query delegation vs native SQL comparison

Those candidates are deferred to
[`05-future-candidates-and-maintenance.md`](05-future-candidates-and-maintenance.md).

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

### Local-candidate allowlist for first shadow rollout

Only these families may be local candidates in initial shadow decisions:

1. **Instant plain/regex selectors**
   - Evidence: long profiles show fallback faster for plain selectors
     (`F/N 0.65` to `0.78`) and the short fixture shows a stronger local win.
   - Caps: small estimated output series and points; no range output.
2. **Instant rate-like single-vector functions**
   - Evidence: `rate_1d_instant_1y` fallback `21.91 ms` vs native `28.52 ms`.
   - Caps: one selector, one output vector, bounded matched series, bounded
     lookback samples.
3. **Instant histogram quantile helper over bounded bucket sets**
   - Evidence: current long artifacts show large local win (`F/N ~0.19-0.20`).
   - Extra caution: require bucket/cardinality caps and shadow result comparison
     before serving local in any later file.
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
- any query where local requires raw sample transfer much larger than final
  output

Current artifacts show native beating fallback on these despite native still
being slower than reference Prometheus. These are tier-2 optimization targets,
not local-routing targets.

## Cost decision contract

Implement:

```go
CostModel.Decide(class, estimates, calibration) RoutingDecision
```

A decision should include at least:

- policy
- strict strategy
- selected strategy for the active policy
- would-select strategy for `cost_shadow`
- stable decision enum
- stable reason enum/string
- estimated native/local costs and unit
- cap values and cap hits
- missing estimate fields
- confidence/uncertainty marker when applicable

In `cost_shadow`, selected strategy remains strict even if `wouldSelect` is
local.

## Sweep integration

Extend the benchmark workflow so cost policies can be evaluated from named
sweeps instead of ad-hoc curl loops. Add a routing-policy axis to
`run-sweep`/`run-bench`/`promshim-bench` if it does not already exist:

```text
--routing-policies strict,cost_shadow,cost_prefer
```

Requirements for that axis:

- append or otherwise send per-request `routing_policy=` without changing the
  native lowering mode axis
- keep native lowering mode (`--shim-modes`) and routing policy as separate
  report dimensions
- include routing policy in v2 rows, sweep manifests, summaries, and log comments
- ensure memory summaries can distinguish `mode=prefer policy=cost_shadow` from
  `mode=prefer policy=strict`
- preserve isolated benchmark endpoints and artifacts under
  `harness/artifacts/sweeps/<run-name>/`

Shadow validation should use a named sweep such as:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-shadow-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --corpus-set native \
  --memory summary
```

Then inspect:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/cost-routing-shadow-7d-sparse/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/cost-routing-shadow-7d-sparse/memory-summary-*.json
```

## Metrics

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

Shadow metrics must be bounded in cardinality. Use stable family/reason labels,
not raw query strings.

## Explain output example

Populate the routing object with shadow decisions:

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

## Implementation tasks

- [ ] Implement `CostModel.Decide(class, estimates, calibration)`.
  - Cover strict, missing estimate, over-cap, low-confidence, and local-candidate
    decisions.
  - Make reason strings stable and bounded.
- [ ] Add `cost_shadow` policy behavior.
  - Serve the strict candidate.
  - Record `wouldSelect` and cost/cap/missing-estimate details.
  - Emit headers, explain output, and decision metrics.
- [ ] Add bounded alternate execution for initial local-candidate families.
  - Enforce caps before running any alternate.
  - Add sample rate and global concurrency limit to avoid overload.
  - Do not run alternates for high-cost or uncertain classes.
- [ ] Compare strict vs alternate results.
  - Use existing normalization and tolerances where applicable.
  - Report divergences by stable categories.
  - Do not add compliance allowlist entries for divergences.
- [ ] Record prediction error where both candidates ran.
  - Track strict duration vs alternate duration.
  - Ensure metrics are useful for recalibration.
- [ ] Add a routing-policy sweep axis and/or harness corpus variant for
  representative rows with `routing_policy=cost_shadow`.
  - Prefer the sweep axis so strict and cost-shadow share profile, density,
    transport, corpus, and memory artifact context.
- [ ] Add targeted comments for the shadow safety invariants: strict served
  result, bounded alternate execution, and no lower-tier feature expansion.

## Validation tasks

- [ ] Table-test cost decisions for current evidence families and cap reasons.
- [ ] HTTP tests verify `cost_shadow` returns strict results while reporting
  shadow decision metadata.
- [ ] Metrics tests cover decision, missing estimate, over-cap, shadow run,
  divergence, and prediction error counters/histograms.
- [ ] Manual smoke with `/metrics` confirms `promshim_routing_*` metrics appear.
- [ ] Differential tests compare strict vs alternate local results for bounded
  families.
- [ ] Run a representative `routing_policy=cost_shadow` named sweep using the
  isolated benchmark stack.
- [ ] Run:

  ```bash
  go test ./internal/promshim/...
  go test ./internal/promshim/httpapi ./internal/promshim
  ```

## Compatibility and migration notes

- Served responses in `cost_shadow` must match strict responses.
- Alternate execution must be opt-in through `cost_shadow` and guarded by caps,
  sample rate, and concurrency limits.
- Shadow benchmark and memory/ProfileEvents evidence should come from
  `run-sweep` artifacts, not from long-range data on compliance ports.
- Shadow divergence is a bug or calibration blocker, not an expected-failure
  allowlist candidate.
- The model may say `wouldSelect=local`, but `selectedStrategy` remains strict in
  this slice.

## Exit criteria

- [ ] `cost_shadow` produces no served-result changes.
- [ ] Shadow decisions explain would-select strategy, reason, cost estimates,
  caps, and missing estimates.
- [ ] Bounded alternate runs are enforced by caps and concurrency/sample limits.
- [ ] Shadow comparisons report zero unexpected divergences on the initial
  sweep corpus.
- [ ] Metrics and sweep memory summaries show enough data to tune thresholds.

## Handoff to next file

After this file, [`04-cost-prefer-rollout.md`](04-cost-prefer-rollout.md) can
serve alternate routes only for classes that have clean shadow evidence, hard
caps, explicit config gates, and differential validation.
