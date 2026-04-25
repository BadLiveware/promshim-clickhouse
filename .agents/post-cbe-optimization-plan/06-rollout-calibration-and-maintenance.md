# 06. Rollout, calibration, and maintenance

## Purpose and scope

Turn individual optimization wins into maintainable CBE behavior. This stage
covers family gates, shadow/prefer rollout, calibration refresh, documentation,
regression detection, and rollback.

The goal is to keep optimization work iterative without letting stale
calibration, hidden ClickHouse settings, or noisy benchmark deltas become
production behavior.

## Prerequisites

- Stage 01 evidence contracts exist.
- Stage 02 IR rewrites are explainable and disableable.
- Stage 03 settings profiles are allowlisted, version-aware, and visible in
  explain output.
- Stage 04 has at least one query-family optimization with named artifacts.
- Stage 05 reference deployment profile is documented or explicitly deferred.

## Affected areas

- CBE family gates and routing policies.
- Calibration artifacts.
- Sweep report interpretation.
- Metrics, headers, and explain output.
- Documentation and troubleshooting.
- Rollback and operational playbooks.

## Requirements

- Optimizations must roll out family-by-family.
- Shadow evidence must precede served `cost_prefer` behavior for risky routes.
- Calibration must be derived from named sweep artifacts, not hand-entered
  impressions.
- Stale calibration must fail safe.
- Every performance claim must remain reproducible from preserved artifacts.
- Rollback must not require code removal for normal failures.
- If an optimization borrows a pattern from `~/code/external/`, the rollout
  notes must identify the source, the promshim-specific adaptation, and the
  evidence that the adaptation is valid here.

## Implementation tasks

### 1. Define rollout gates

- [ ] Keep strict/reference behavior available and default until explicitly
  changed.
- [ ] Gate optimized IR rewrites by family or pass where practical.
- [ ] Gate CBE serving by query family, estimated input size, output size,
  confidence, and settings-profile availability.
- [ ] Gate ClickHouse performance profiles separately from safety profiles.
- [ ] Document per-request and environment/config rollback controls.

### 2. Shadow and differential validation

- [ ] Run optimized candidates in shadow where serving risk is non-trivial.
- [ ] Record strict/reference candidate, selected CBE candidate, and served
  candidate separately.
- [ ] Record divergence status, prediction error, and cap rejections.
- [ ] Limit alternate execution concurrency and sampling so shadow work does not
  distort benchmark or production load.
- [ ] Keep `force_supported` native-only visibility intact.

### 3. Calibration refresh workflow

- [ ] Generate calibration from named sweep manifests and memory summaries.
- [ ] Track calibration inputs:
  - git revision;
  - corpus set;
  - profile/density;
  - ClickHouse version;
  - reference deployment profile;
  - settings profile;
  - query-family labels.
- [ ] Mark calibration stale after IR, SQL renderer, CBE cost model, corpus,
  fixture, ClickHouse version, or settings-profile changes.
- [ ] Fail safe to strict/reference behavior when calibration is stale or
  missing.

### 4. Regression detection

- [ ] Treat strategy/candidate flips as review-worthy events, not background
  noise.
- [ ] Alert or fail validation when a family unexpectedly falls back from native
  SQL to local/reference or from optimized candidate to strict.
- [ ] Compare expected and actual ProfileEvents for known optimization claims.
- [ ] Preserve before/after artifact directories and avoid overwriting the only
  baseline capture.
- [ ] Add focused tests for any bug found through sweep artifacts.

### 5. Documentation and review checklist

- [ ] Add an external-example note to optimization reviews when applicable:
  source repo/path, borrowed idea, rejected parts, PromQL/ClickHouse risks, and
  validation artifacts.
- [ ] Add an optimization PR checklist covering:
  - query family;
  - semantic risk;
  - expected measurement signal;
  - baseline artifact;
  - after artifact;
  - ProfileEvents/EXPLAIN evidence;
  - compliance result;
  - negative controls;
  - rollback gate.
- [ ] Document how to disable a rewrite, candidate family, or settings profile.
- [ ] Document how to interpret explain output for optimized IR and CBE routing.
- [ ] Document known unsupported or shadow-only families.

### 6. Long-term maintenance

- [ ] Periodically rerun baseline sweeps against the reference profile.
- [ ] Revisit settings allowlist when ClickHouse versions change.
- [ ] Revisit query-family gates after new PromQL semantic coverage lands.
- [ ] Remove obsolete compatibility shims or temporary gates only when artifacts
  show they are no longer needed.
- [ ] Keep plan docs aligned with current CBE terminology and actual config
  names.

## Validation tasks

Routine fast checks:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

Calibration/rollout sweep pattern:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-rollout-strict-baseline \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-rollout-shadow-<family> \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-rollout-prefer-<family> \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

Negative controls before broadening a gate:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-rollout-<family>-long-range \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-rollout-<family>-dense \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set processing \
  --memory summary
```

Compliance gate:

```bash
./scripts/run-sweep.sh --name post-cbe-rollout-compliance --skip-bench
```

## Exit criteria

- [ ] Family gates and rollback controls are documented and tested.
- [ ] Shadow evidence exists for risky optimized candidates before serving.
- [ ] Calibration can be regenerated from named sweep artifacts.
- [ ] Stale/missing calibration fails safe.
- [ ] Review checklist prevents p50-only optimization claims.
- [ ] Strategy/candidate flips are visible in reports and treated as review
  signals.
- [ ] Documentation explains optimized IR, CBE routing, settings profiles, and
  reference ClickHouse profile interactions.

## Final handoff

When this file is complete, optimization work can continue as a repeating loop:

1. choose a query family;
2. define the semantic risk and expected signal;
3. collect a baseline;
4. apply an IR, SQL, local/hybrid, or settings-profile change;
5. validate with compliance and named sweeps;
6. calibrate CBE only from preserved artifacts;
7. roll out behind family/profile gates; and
8. keep rollback config ready.
