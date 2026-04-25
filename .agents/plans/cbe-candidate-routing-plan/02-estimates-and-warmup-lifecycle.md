# 02. Estimates and warmup lifecycle

## Purpose and scope

Make estimates reliable enough for candidate ranking. The current served path is
correctly cache-only, but validation showed that `cost_prefer` can only exercise
local overrides after `cost_shadow` warms estimates in the same running service.
This slice turns that behavior into an explicit, reproducible lifecycle.

Scope:

- estimate source/freshness model;
- cache hit/miss/stale metrics;
- explain metadata for estimate state;
- sweep warmup workflow that runs shadow before prefer in the same benchmark
  stack/container;
- no new served CBE behavior.

## Prerequisites

- Complete [`01-candidate-contract-and-planning.md`](01-candidate-contract-and-planning.md).
- Candidate list and rejection reasons are visible in explain output.

## Affected areas

- `internal/promshim/selector_estimates.go`
- `internal/promshim/selector_probe.go`
- `internal/promshim/selector_probe_scheduler.go`
- `internal/promshim/routingmetrics/metrics.go`
- `internal/promshim/service.go`
- `scripts/run-sweep.sh`
- `scripts/run-bench.sh` if warmup needs report-level support
- `cmd/promshim-bench` if it needs a warmup-only mode
- sweep manifest generation

## Implementation tasks

- [ ] Add estimate source and freshness fields.
  - Suggested sources: `none`, `cache`, `shadow_probe`, `calibration`,
    `query_log_recent` if implemented later.
  - Track generated-at time, TTL/staleness, and selector signature.
- [ ] Add estimate-state rejection reasons.
  - `missing_estimate` remains distinct from `stale_estimate` and
    `estimate_signature_mismatch`.
  - Served CBE must choose strict/reference on missing or stale estimates unless
    a family has a documented safe no-metadata rule.
- [ ] Add metrics for estimate lifecycle.
  - cache hits/misses/stale by bounded family and estimate source;
  - async probe scheduled/completed/failed;
  - candidate rejected due estimate state.
- [ ] Add explain fields for estimate state.
  - Candidate-level estimate source, freshness, and high-level cardinality/sample
    estimates.
  - Do not expose raw matcher values.
- [ ] Add a run-sweep warmup mode.
  - It should run a shadow warmup pass and then the measurement pass against the
    same benchmark stack and promshim container.
  - It should record warmup policy, warmup routing policies, and whether the
    benchmark shim was recreated between phases in `manifest.json`.
  - It should be usable for `cost_shadow -> cost_prefer` validation without
    hand-running two commands.
- [ ] Preserve cache-only served behavior.
  - No synchronous uncached selector stats probe should be added to the served
    request path.

## Validation tasks

- [ ] Unit-test cache hit/miss/stale transitions.
- [ ] Unit-test explain output for estimate source and stale/missing reasons.
- [ ] Unit-test metrics registration and bounded labels.
- [ ] Shell-validate scripts:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

- [ ] Dry-run the warmup workflow:

```bash
./scripts/run-sweep.sh \
  --dry-run \
  --estimate \
  --name cbe-warmup-dry-run \
  --profile 7d \
  --density sparse \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families rate_instant
```

- [ ] Live smoke with warmup when benchmark data is available:

```bash
./scripts/run-sweep.sh \
  --name cbe-warmup-rate-7d-sparse \
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

Adapt command flags to the final warmup CLI.

## Compatibility, docs, and cleanup

- [ ] Document estimate lifecycle and warmup behavior in `README.md` or harness
  docs.
- [ ] Make clear that cold starts choose strict/reference until estimates exist.
- [ ] Record warmup in sweep manifests so reviewers can tell whether a prefer
  run had prior shadow evidence.

## Exit criteria

- [ ] Estimate source/freshness is represented in code and explain output.
- [ ] Cache miss/stale behavior is observable and chooses strict/reference.
- [ ] Sweep warmup is reproducible and recorded in artifacts.
- [ ] Served requests still do not perform synchronous uncached metadata probes.

## Handoff to next file

After estimate lifecycle is reproducible, move to
[`03-shadow-and-differential-cbe.md`](03-shadow-and-differential-cbe.md) to run
candidate decisions and bounded alternates in shadow.
