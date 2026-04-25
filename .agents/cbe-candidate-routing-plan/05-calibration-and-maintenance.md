# 05. Calibration and maintenance

## Purpose and scope

Make CBE durable after the first served candidate family. Calibration must be
regenerated from named artifacts, candidate behavior must remain documented, and
future family/candidate expansion must follow the same shadow-first discipline.

Scope:

- multi-manifest calibration;
- candidate/family maintenance docs;
- regression checks for strategy/candidate flips;
- future expansion rules;
- no broad candidate expansion in this slice unless earlier slices produced the
  required evidence.

## Prerequisites

- Complete [`04-cost-prefer-serving-candidates.md`](04-cost-prefer-serving-candidates.md)
  for at least one bounded family.
- Preserve all validation sweep artifact directories used to justify serving.

## Affected areas

- `cmd/promshim-routing-calibrate`
- `.pi/cost-routing-calibration.json`
- `.pi/cost-routing-calibration.md`
- `README.md`
- `.pi/cbe-candidate-routing-plan/`
- `scripts/bench-matrix.sh`
- `scripts/run-sweep.sh`
- CI or local validation docs if present

## Implementation tasks

- [ ] Extend calibration tooling to accept multiple sweep manifests.
  - Include sparse, dense, long-range, shadow, and prefer artifacts.
  - Preserve source manifest paths and run names in generated calibration.
  - Emit family/profile/density coverage and confidence notes.
- [ ] Add candidate-aware calibration outputs.
  - Track strict/reference candidate and selected candidate.
  - Track candidate-specific p50/p95, CH round trips, and ProfileEvents where
    available.
  - Mark insufficient data explicitly.
- [ ] Add calibration review notes.
  - Document which families are enabled.
  - Document which candidates are disabled and why.
  - Document known cliffs such as dense local range processing if still present.
- [ ] Add strategy/candidate flip checks to matrix review.
  - Candidate flips should be visible even when p50 is green.
  - Unexpected flips should be hard warnings for review.
- [ ] Update README and plan docs.
  - CBE candidate semantics.
  - Family gate list.
  - Safety caps.
  - Rollback commands.
  - Required validation bundle for future families.
- [ ] Keep future expansion concrete.
  - Add future family/candidate tasks only when they name the candidate, family,
    validation sweeps, and done criteria.
  - Avoid vague catch-all future tasks.

## Validation tasks

- [ ] Unit-test multi-manifest calibration merge behavior.
- [ ] Unit-test insufficient-data reporting.
- [ ] Run calibration against preserved artifacts:

```bash
go run ./cmd/promshim-routing-calibrate \
  --sweep harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/manifest.json \
  --sweep harness/artifacts/sweeps/cbe-prefer-rate-7d-sparse/manifest.json \
  --sweep harness/artifacts/sweeps/cbe-prefer-rate-long-range-sparse/manifest.json \
  --sweep harness/artifacts/sweeps/cbe-prefer-rate-7d-dense-processing/manifest.json \
  --out-json .pi/cost-routing-calibration.json \
  --out-md .pi/cost-routing-calibration.md
```

Adjust manifest names to actual preserved runs.

- [ ] Run final focused tests:

```bash
go test ./cmd/promshim-routing-calibrate/... ./internal/promshim/... ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

- [ ] Run final strict compliance if candidate serving changed since the last
  compliance artifact:

```bash
./scripts/run-sweep.sh --name cbe-final-strict-compliance --skip-bench
```

## Compatibility, docs, and cleanup

- [ ] Keep strict default documented.
- [ ] Keep `force_supported` native-only behavior documented.
- [ ] Keep `off` local-baseline behavior documented.
- [ ] Make rollback commands obvious.
- [ ] Remove or clearly mark stale calibration artifacts.
- [ ] Do not delete preserved sweep artifact directories used as evidence.

## Future expansion checklist

Before enabling any new family or candidate:

- [ ] candidate is already semantically supported;
- [ ] candidate has no known divergence for that family;
- [ ] hard caps are defined;
- [ ] estimate source/freshness is available;
- [ ] shadow sweep is clean;
- [ ] cost-prefer differential sweep is clean;
- [ ] long-range negative control is clean;
- [ ] dense/cardinality negative control is clean or explicitly reconciled;
- [ ] strict compliance remains clean;
- [ ] rollback is config-only and verified.

## Exit criteria

- [ ] Calibration can merge multiple named CBE sweep manifests.
- [ ] Generated calibration artifacts identify enabled families and insufficient
  data clearly.
- [ ] Candidate/family docs are current.
- [ ] Future expansion checklist is documented and actionable.
- [ ] Final validation commands pass or gaps are documented with concrete
  blockers.

## Handoff notes

After this file, future CBE work should be planned as new bounded slices, one
family or candidate class at a time. Do not batch unrelated tier-3/tier-4
coverage expansion into calibration or maintenance work.
