# 03. Shadow and differential CBE

## Purpose and scope

Use `cost_shadow` to evaluate the full tier-2/3/4 candidate set and collect
correctness, cost, and prediction evidence before any new served CBE behavior.
This slice proves candidate ranking and rejection logic under real sweep
artifacts while strict/reference remains served.

Scope:

- candidate ranking in shadow;
- bounded alternate execution for selected non-served candidates;
- candidate-level divergence and prediction metrics;
- shadow artifact inspection;
- no new `cost_prefer` serving expansion yet.

## Prerequisites

- Complete [`01-candidate-contract-and-planning.md`](01-candidate-contract-and-planning.md).
- Complete [`02-estimates-and-warmup-lifecycle.md`](02-estimates-and-warmup-lifecycle.md).
- Warmup workflow can populate estimates reproducibly.

## Affected areas

- `internal/promshim/cost_shadow.go`
- `internal/promshim/routing_policy.go`
- `internal/promshim/service.go`
- `internal/promshim/routingmetrics/metrics.go`
- `internal/promshim/shadow/metrics.go`
- `internal/promharness/bench.go`
- `cmd/promshim-bench`
- `scripts/run-sweep.sh`
- `scripts/bench-matrix.sh`

## Implementation tasks

- [ ] Rank candidate sets in `cost_shadow`.
  - Candidates rejected by correctness/support/caps should not enter cost
    ranking.
  - Ranked-but-not-served candidates should record predicted winner and reasons.
- [ ] Add bounded alternate execution by candidate ID.
  - Keep concurrency, sample-rate, and hard-cap controls.
  - Do not run alternates for candidates with missing/stale estimates or known
    divergences.
- [ ] Record candidate-level shadow outcomes.
  - strict/reference candidate;
  - predicted selected candidate;
  - served candidate;
  - alternate candidate execution status;
  - divergence status;
  - prediction error when both candidates were measured.
- [ ] Extend metrics with bounded candidate labels.
  - candidate considered/rejected/selected;
  - rejection reason;
  - shadow alternate executed/skipped/failed;
  - divergence by candidate/family.
- [ ] Extend bench report and matrix rendering.
  - Include selected candidate ID and strict/reference candidate ID.
  - Preserve existing strategy fields for compatibility.
  - Surface candidate flips as review-visible warnings.
- [ ] Add a known-divergence registry if needed.
  - Keep it code/data driven with stable family/candidate reasons.
  - Do not use compliance expected failures for routing bugs.

## Validation tasks

- [ ] Unit-test candidate ranking and rejection order.
- [ ] Unit-test that shadow serves strict/reference regardless of predicted
  winner.
- [ ] Unit-test alternate execution skips unsupported/over-cap/missing-estimate
  candidates.
- [ ] Run focused Go tests:

```bash
go test ./internal/promshim/... ./internal/promharness ./cmd/promshim-bench
```

- [ ] Run shadow sparse sweep for first target family:

```bash
./scripts/run-sweep.sh \
  --name cbe-shadow-rate-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --cost-routing-local-families rate_instant \
  --corpus-set native \
  --memory summary
```

- [ ] Inspect memory summaries and matrix:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/cbe-shadow-rate-7d-sparse/memory-summary-*.json
```

- [ ] Run long-range and dense shadow controls before serving expansion.

## Compatibility, docs, and cleanup

- [ ] Document shadow candidate semantics.
- [ ] Preserve old routing policy fields for dashboards.
- [ ] Keep shadow alternate execution optional/bounded so it is safe in local/dev
  and not accidentally expensive.

## Exit criteria

- [ ] `cost_shadow` evaluates tier-2/3/4 candidate sets and serves strict.
- [ ] Candidate rejection, selected candidate, and prediction evidence are
  visible in metrics, explain, and bench artifacts.
- [ ] Shadow sweeps show no unexpected divergences for the target family.
- [ ] Long-range and dense controls do not produce unsafe candidate selections.

## Handoff to next file

After shadow evidence is clean for a bounded family, move to
[`04-cost-prefer-serving-candidates.md`](04-cost-prefer-serving-candidates.md) to
serve selected tier-3/tier-4 candidates behind explicit gates.
