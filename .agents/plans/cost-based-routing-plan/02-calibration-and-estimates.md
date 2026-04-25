# 02. Calibration and estimates

## Purpose and scope

Build the offline calibration pipeline and the cheap estimate plumbing required
for cost decisions. This slice still does not serve alternate routes. It turns
`run-sweep` manifests, v2 bench reports, and memory-summary ProfileEvents into
reviewable seed data, then adds cached, budgeted metadata estimates that later
cost policies can consume.

## Prerequisites

- Complete [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md).
- Query families and selected/strict strategies are present in bench and explain
  output.
- Strict remains the served behavior.

## Affected areas

- new `cmd/promshim-routing-calibrate/`
- `internal/promharness` report parsing helpers if useful
- `.pi/cost-routing-calibration.json`
- `.pi/cost-routing-calibration.md`
- `scripts/run-sweep.sh` artifact contract if manifest fields are missing
- `internal/promshim/storage/`
- `internal/promshim/native/selector.go` or related selector signature helpers
- service-level cache plumbing
- metrics package

## Bench evidence to calibrate against

The current artifacts show the central tension.

On the short fixture, local fallback is often faster for small or
overhead-heavy shapes:

- `plain_selector_range`: native `24.79 ms`, fallback `7.19 ms`, F/N `0.29`.
- `topk_range`: native `39.26 ms`, fallback `9.59 ms`, F/N `0.24`.
- `vector_match_range`: native `74.89 ms`, fallback `16.43 ms`, F/N `0.22`.
- `histogram_quantile_instant`: native `147.31 ms`, fallback `60.02 ms`, F/N
  `0.41`.

On long-range data, native remains necessary for large range reductions:

- `rate_5m_range_1d` at 7d: native `577 ms`, fallback `3379 ms`, F/N `5.85`.
- `rate_1h_range_30d`: native `5435 ms`, fallback `9661 ms`, F/N `1.78`.
- `avg_over_time_1d_range_1y`: native `6332 ms`, fallback `7245 ms`, F/N
  `1.14`.
- `sum_rate_by_job_range_30d`: native `1407 ms`, fallback `2533 ms`, F/N
  `1.80`.

Some instant long-window shapes still look local-favorable in current artifacts:

- `histogram_quantile_6h_instant_30d`: native `163 ms`, fallback `33 ms`, F/N
  `0.20`.
- `histogram_quantile_1d_instant_1y`: native `157 ms`, fallback `29 ms`, F/N
  `0.19`.
- `plain_selector_instant_1y`: native `17.65 ms`, fallback `11.44 ms`, F/N
  `0.65`.
- `rate_1d_instant_1y`: native `28.52 ms`, fallback `21.91 ms`, F/N `0.77`.

Interpretation:

- Native SQL remains preferred for high-cardinality/range-reduction paths.
- The model must distinguish instant/short/small-output queries from
  range/high-reduction/large-input queries.
- Current data is calibration input, not an eternal truth.

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
- Metadata probes must have a small timeout/budget and must be cached.
- The default served query must not pay an uncached probe cost.

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
- `local_family_base` = median fallback p50 for small fixture rows of that
  family.
- `scan_weight` adjusted by long-range sweep deltas (`7d`, `30d`, `1y`) and memory/ProfileEvents counters.
- `shape_penalty` from known expensive native shapes in profiling docs, e.g.
  window array materialization, histogram helper stages, vector joins.

If the model lacks coefficients for a class, choose strict.

## Calibration artifacts

Add generated calibration artifacts rather than hard-coding all thresholds:

```text
.pi/cost-routing-calibration.json
.pi/cost-routing-calibration.md
```

Primary generator input should be one or more named sweep manifests:

```bash
go run ./cmd/promshim-routing-calibrate \
  --sweep harness/artifacts/sweeps/cost-routing-strict-baseline/manifest.json \
  --out-json .pi/cost-routing-calibration.json \
  --out-md .pi/cost-routing-calibration.md
```

The generator may keep legacy `--bench` inputs for ad-hoc debugging, but review
and rollout calibration should use sweep manifests so profile/density/transport,
bench reports, compliance status, and memory summaries stay connected.

A typical source sweep is:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-strict-baseline \
  --profile all \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary
```

For processing-heavy calibration, run a separate dense sweep and keep it as a
separate calibration input rather than mixing densities blindly:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-processing-7d-dense \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer,force_supported,off \
  --corpus-set processing \
  --memory summary
```

The generator should:

- read `manifest.json` and all referenced `bench-report-*.json` files
- join `memory-summary-*.json` ClickHouse query_log/ProfileEvents by log comment
- group by stable query family/category plus profile, density, transport,
  corpus, native lowering mode, and routing policy
- compute median native, fallback/local, and Prometheus p50 where available
- compute fallback/native or selected/strict ratios by profile and density
- mark classes as `local_candidate`, `native_required`, or
  `insufficient_data`
- include ClickHouse ProfileEvents counters when available
- call out missing log comments and strategy flips as hard warnings
- include sweep names, artifact paths, endpoints, seed policy, and generation
  timestamp

The calibration file should be reviewed like code. It is input to routing but
not a substitute for runtime safety caps.

## Implementation tasks

### Offline calibration

- [ ] Create `cmd/promshim-routing-calibrate/`.
- [ ] Parse sweep manifests and referenced v2 bench reports, then group rows by
  category/family.
  - Include profile, density, transport, corpus, native lowering mode, routing
    policy, selected strategy, and strict strategy.
  - Treat strategy flips as hard warnings.
- [ ] Join optional `memory-summary-*.json` ClickHouse ProfileEvents by log
  comment.
  - Missing memory summaries or missing log comments are allowed for incomplete
    local artifacts but must be visible in markdown.
- [ ] Emit family summaries and initial recommendations:
  - `local_candidate`
  - `native_required`
  - `insufficient_data`
  - `do_not_route_due_to_strategy_flip`
- [ ] Seed recommendations from current evidence:
  - local candidates: bounded instant selector/rate/histogram helper, tiny range
    selector
  - native required: long range rate/avg/sum-rate/subquery forms
- [ ] Document calibration uncertainty, sweep names, manifest paths, benchmark
  endpoints, and artifact paths in generated markdown.

### Estimate plumbing

- [ ] Define selector signatures stable across equivalent matcher order.
  - Include offset/lookback/time bounds where they affect matching or sample
    volume.
- [ ] Add a TTL cache for selector stats:
  - matched series count
  - time-overlap bounded estimate
  - optional scrape interval estimate
- [ ] Implement budgeted stats probes over `timeSeriesTags(...)`.
  - Probes must use time-overlap predicates.
  - Probes must be timeout-bound.
  - Probes must not run synchronously on the default served path.
- [ ] Add cache-only estimate mode for served queries.
  - In `cost_shadow`, missing cache can schedule async fill.
  - In `cost_prefer`, missing cache means strict unless the class has a safe
    no-metadata rule.
- [ ] Expose missing-estimate and probe-failure metrics.
- [ ] Add targeted comments for cache/probe invariants: no uncached probe on the
  served path, timeout-bound metadata access, and strict fallback on unknowns.

## Validation tasks

- [ ] Unit-test calibration parsing with minimal synthetic sweep manifests and
  v2 bench reports.
- [ ] Fixture-test memory-summary joins, including missing memory summaries and
  missing log comments.
- [ ] Golden-test generated markdown for recommendation categories and warnings.
- [ ] Unit-test selector signature equality for reordered matchers.
- [ ] Unit-test selector signature inequality for different offset/lookback/time
  bounds.
- [ ] Storage SQL tests for `timeSeriesTags(...)` probes.
- [ ] Integration smoke against the harness stack if available.
- [ ] Verify explain output includes estimates when cache is populated and marks
  missing estimates when not.
- [ ] Run:

  ```bash
  go test ./cmd/promshim-routing-calibrate/...
  go test ./internal/promshim/...
  ```

## Compatibility and migration notes

- Calibration artifacts are generated inputs and should be regenerated after
  major tier-2 optimizer changes.
- The served request path must not become slower due to synchronous metadata
  probes.
- Estimate cache misses are not errors; they make the router choose strict.
- Sweep bench and memory data are calibration evidence, not correctness
  evidence.

## Exit criteria

- [ ] Calibration can be regenerated from named sweep manifests.
- [ ] Generated markdown explains why each initial family is or is not a local
  candidate.
- [ ] Estimates appear in explain output when available.
- [ ] Missing estimates are observable in explain/metrics.
- [ ] No default request pays an uncached synchronous probe cost.

## Handoff to next file

After this file, [`03-cost-shadow.md`](03-cost-shadow.md) can consume stable
classification, sweep-derived calibration recommendations, memory/ProfileEvents
evidence, and cache-only estimates to compute cost decisions while strict remains
the served result.
