# 04. Cost-prefer rollout

## Purpose and scope

Enable `cost_prefer` for the safest query families only after shadow evidence
shows correctness and meaningful predicted wins. This slice changes served
behavior only behind explicit policy and family gates. Strict routing remains the
default and the rollback path.

## Prerequisites

- Complete [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md).
- Complete [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md).
- Complete [`03-cost-shadow.md`](03-cost-shadow.md).
- Shadow comparisons for the target family show zero unexpected divergences.
- Calibration and estimate data exist for the target family.

## Affected areas

- service routing selection
- config/env/request handling
- explain/headers/metrics
- harness corpora
- benchmark/profile validation artifacts from named sweeps
- `scripts/run-sweep.sh` cost-routing family gate support if needed

## Cost-prefer serving contract

`cost_prefer` may serve the cost-selected candidate only when all of these are
true:

- the request is not in `force_supported`, `off`, or the existing native
  lowering `shadow` mode;
- the family is explicitly enabled by config;
- estimates required by the family are present or a safe no-metadata rule is
  documented;
- hard caps are satisfied;
- the predicted local win exceeds both relative and absolute margins;
- no known divergence exists for the family;
- the class is not one of the native-required classes.

Otherwise choose strict and report a stable strict reason.

## Family gates

Add a config gate for each local-candidate family or class group. Example:

```text
PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=selector_instant,rate_instant
```

The isolated benchmark stack must make these gates explicit and reproducible.
If the stack cannot already vary them per sweep, extend `run-sweep` with a
recorded option such as:

```text
--cost-routing-local-families selector_instant,rate_instant
```

The option should configure the benchmark promshim container, restart/recreate it
when the gate set changes, and record the enabled families in `manifest.json`.

Initial enablement order:

1. instant selector
2. instant rate-like single-vector functions
3. instant histogram helper, only after histogram-specific shadow evidence
4. tiny range selector-only rows, only after explicit evidence and very small
   output caps

Keep range-query local routing disabled except tiny range selector-only rows
until explicit evidence says otherwise.

## Hard caps

Enforce at least:

- max estimated input samples
- max estimated output series
- max estimated output points
- max local ClickHouse roundtrips
- max local observed p95 from recent shadow data if available
- family-specific caps, e.g. bucket/cardinality caps for histogram helpers

Missing cap data chooses strict unless the family has a documented safe
no-metadata rule.

## Rollout strategy

1. Land strict observability only.
   Covered by [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md).
2. Generate calibration from current artifacts.
   Covered by [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md).
3. Run `cost_shadow` locally and in the harness until decisions stabilize.
   Covered by [`03-cost-shadow.md`](03-cost-shadow.md).
4. Enable `cost_prefer` only in local/dev with a narrow family allowlist.
5. Add named `run-sweep` coverage for cost-prefer mode using the routing-policy
   axis and isolated benchmark stack.
6. Recalibrate from the resulting sweep manifests after each major tier-2 SQL
   optimization.
7. Only consider broader default use if:
   - strict compliance is clean
   - cost-prefer differential is clean
   - shadow divergence is zero for the target families
   - small-query p50/p95 improves materially
   - long-range native-required families never route local

## Implementation tasks

- [ ] Implement `cost_prefer` selected-strategy behavior.
  - Reuse `CostModel.Decide` from shadow mode.
  - Select local only when the decision passes family gate, caps, estimate
    availability, and margin checks.
  - Fall back to strict with stable reasons for all blocked cases.
- [ ] Add per-family config gates.
  - Start with `selector_instant` and `rate_instant` disabled by default.
  - Make gates visible in explain output.
  - Ensure `run-sweep` can set and record the benchmark-stack gate value for
    reproducible cost-prefer sweeps.
- [ ] Enable `cost_prefer` for instant selector and instant rate classes first.
  - Require bounded matched series, bounded output points, and bounded lookback
    samples.
- [ ] Keep instant histogram helper disabled until shadow evidence confirms the
  current bench win without divergence.
  - When enabled later, require bucket/cardinality caps and histogram-focused
    differential coverage.
- [ ] Keep range-query local routing disabled except tiny range selector-only
  rows with explicit evidence.
- [ ] Add cost-prefer harness corpus coverage.
  - Include enabled classes and native-required negative controls.
- [ ] Ensure headers, explain output, and metrics distinguish:
  - selected local override
  - strict due to missing estimate
  - strict due to over cap
  - strict due to disabled family gate
  - strict due to low confidence/insufficient margin
- [ ] Add targeted comments for non-obvious safety behavior, especially strict
  fallback on missing data and why broad range routing remains disabled.

## Validation tasks

### Correctness

- [ ] Focused differential sweep under `routing_policy=cost_prefer` for instant
  selector and instant rate classes.
- [ ] Negative-control rows for long-range/native-required classes verify they
  remain strict-native.
- [ ] Histogram-focused sweep before enabling histogram helper.
- [ ] Prefer-mode compliance stays clean under strict routing.
- [ ] Cost-prefer differential rows stay clean against Prometheus in sweep
  reports where Prometheus is included.

Commands:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-prefer-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families selector_instant,rate_instant \
  --corpus-set native \
  --memory summary

./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/cost-routing-prefer-7d-sparse/manifest.json \
  --per-query
```

### Performance

- [ ] Sparse 7d sweep shows meaningful improvement for selected small-query rows.
- [ ] Dense processing sweep does not reveal cardinality cliffs for enabled
  families.
- [ ] Long-range/native-required rows do not regress or route local.
- [ ] Sweep memory summaries support the claim; do not rely only on p50
  wall-clock.
- [ ] Inspect `strategy_used`, selected strategy, routing policy, `SelectedRows`,
  `ReadCompressedBytes`, `FunctionExecute`, and `MemoryTrackerUsage`.

Commands:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-prefer-7d-dense-processing \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families selector_instant,rate_instant \
  --corpus-set processing \
  --memory summary

./scripts/run-sweep.sh \
  --name cost-routing-prefer-long-range-sparse \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families selector_instant,rate_instant \
  --corpus-set native \
  --memory summary

./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/cost-routing-prefer-long-range-sparse/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/cost-routing-prefer-long-range-sparse/memory-summary-*.json
```

Use `.pi/skills/running-sweep/SKILL.md` for the workflow and
`.pi/skills/measuring-ch-optimizations/SKILL.md` when evaluating the performance
claim.

## Compatibility and rollback notes

- `PROM_SHIM_ROUTING_POLICY=strict` restores prior behavior.
- Removing a family from `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES` disables local
  overrides for that family.
- `force_supported` continues to ignore routing policy and require native root.
- `off` continues to serve local baseline and ignores routing policy.
- Existing `shadow` mode should not mix with cost routing until explicitly
  revisited.
- `run-sweep --bench-reset --yes` deletes only isolated benchmark volumes; it is
  not a compliance cleanup command.

## Exit criteria

- [ ] `cost_prefer` improves chosen small-query benchmark rows by a meaningful,
  sweep ProfileEvents-supported margin.
- [ ] Long-range/native-required rows continue to route strict-native.
- [ ] Prefer-mode compliance remains clean under strict routing.
- [ ] Cost-prefer differential corpus remains clean against Prometheus.
- [ ] Histogram helper and tiny range selectors remain disabled unless they have
  their own clean shadow and differential evidence.
- [ ] Rollback to strict is config-only and verified.

## Handoff to next file

After this file, [`05-future-candidates-and-maintenance.md`](05-future-candidates-and-maintenance.md)
can evaluate broader candidate sets and native-shape cost models. Do not start
that work until root native-vs-local cost routing is stable and validated.
