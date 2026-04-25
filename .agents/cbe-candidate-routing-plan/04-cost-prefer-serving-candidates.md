# 04. Cost-prefer serving candidates

## Purpose and scope

Allow `cost_prefer` to serve a tier-3 or tier-4 candidate when shadow evidence,
correctness gates, estimates, hard caps, family gates, and calibrated margins all
pass. This is the first slice where CBE may change served behavior beyond the
current narrow local override.

Scope is one family at a time. Start with short `rate_instant` because previous
validation already showed a sparse small-query win while long-range and dense
controls stayed safe.

## Prerequisites

- Complete [`03-shadow-and-differential-cbe.md`](03-shadow-and-differential-cbe.md).
- Shadow evidence for the target family has zero unexpected divergences.
- Warmup/estimate lifecycle is reproducible.
- Candidate explain and metrics are in place.
- Strict prefer-mode compliance is clean against the existing allowlist.

## Affected areas

- `internal/promshim/routing_policy.go`
- `internal/promshim/service.go`
- candidate/cost model files from earlier slices
- `internal/promshim/routingmetrics/metrics.go`
- `cmd/promshim-bench`
- `scripts/run-sweep.sh`
- `scripts/bench-matrix.sh`
- docs and calibration artifacts

## Implementation tasks

- [ ] Replace ad-hoc local override selection with candidate-based selection.
  - The selected candidate must pass correctness, support, family, estimate,
    cap, confidence, and margin gates.
  - Preserve existing strict fallback reasons.
- [ ] Start with `rate_instant` short-window serving.
  - Require bounded matched series.
  - Require bounded lookback samples.
  - Require bounded output series/points.
  - Require no known divergence for the selected candidate.
- [ ] Keep broad ranges disabled.
  - Dense processing already showed strict/local range cliffs; do not serve range
    candidates unless a later plan adds tiny range-specific evidence and caps.
- [ ] Keep histogram helpers disabled.
  - Histogram candidates require histogram-specific shadow and differential
    evidence before serving.
- [ ] Keep aggregation-range local fallbacks from becoming CBE wins by accident.
  - Existing strict local fallback is not the same as CBE choosing local over
    native.
- [ ] Add candidate-specific rollback behavior.
  - Removing the family gate disables serving changes.
  - `PROM_SHIM_ROUTING_POLICY=strict` restores strict/reference behavior.
- [ ] Add targeted comments around safety-critical fallback behavior.
  - Missing estimates, stale estimates, over caps, and known divergence must
    choose strict/reference.

## Validation tasks

- [ ] Unit-test serving selection for:
  - eligible local/full-local candidate wins;
  - missing estimate;
  - stale estimate;
  - over cap;
  - family gate disabled;
  - known divergence;
  - `force_supported`, `off`, and native-lowering `shadow` policy ignored.
- [ ] Run focused Go tests:

```bash
go test ./internal/promshim/... ./internal/promharness ./cmd/promshim-bench
```

- [ ] Run warmed cost-prefer sparse sweep:

```bash
./scripts/run-sweep.sh \
  --name cbe-prefer-rate-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families rate_instant \
  --corpus-set native \
  --memory summary
```

- [ ] Run long-range negative control:

```bash
./scripts/run-sweep.sh \
  --name cbe-prefer-rate-long-range-sparse \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families rate_instant \
  --corpus-set native \
  --memory summary
```

- [ ] Run dense processing negative control:

```bash
./scripts/run-sweep.sh \
  --name cbe-prefer-rate-7d-dense-processing \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families rate_instant \
  --corpus-set processing \
  --memory summary
```

- [ ] Run strict compliance:

```bash
./scripts/run-sweep.sh --name cbe-strict-compliance --skip-bench
```

- [ ] Verify rollback:

```bash
./scripts/run-sweep.sh \
  --name cbe-strict-rollback-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict \
  --cost-routing-local-families rate_instant \
  --corpus-set native \
  --memory summary
```

## Performance evidence requirements

- [ ] Selected CBE rows improve by a meaningful margin beyond noise.
- [ ] ProfileEvents/memory summaries support the claim.
- [ ] Strategy/candidate changes are expected and documented.
- [ ] Long-range and dense rows do not route to unsafe candidates.
- [ ] Any timeout or missing query-log comments are reconciled before declaring
  the family complete.

## Compatibility, docs, and cleanup

- [ ] Update README with served CBE semantics for the enabled family.
- [ ] Update calibration docs/artifacts after validation.
- [ ] Preserve old fields for dashboards while adding candidate-specific fields.

## Exit criteria

- [ ] `cost_prefer` can serve a tier-3/tier-4 candidate for the target family.
- [ ] Correctness and differential validation are clean.
- [ ] Long-range and dense negative controls are clean or explicitly reconciled
  as unrelated strict/local cliffs.
- [ ] Strict rollback is verified.
- [ ] Histogram/range/broad aggregation candidates remain disabled unless they
  have their own evidence.

## Handoff to next file

After one family is served safely, move to
[`05-calibration-and-maintenance.md`](05-calibration-and-maintenance.md) to make
calibration and future expansions durable.
