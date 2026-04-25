# 05. CBE tier selection and calibration

## Purpose and scope

Tune cost-based execution so promshim chooses tier 2 native SQL, tier 3 local
with subtree pushdown, or tier 4 full local execution based on measured family
costs. The objective is not to prefer one tier globally; it is to route each
known-correct family to the cheapest safe candidate for the current query shape
and data size.

## Prerequisites

- Benchmark foundation exists with target corpus and baseline artifacts.
- ClickHouse reference profile and promshim settings profile are fixed for the
  calibration run.
- Native SQL/IR optimization evidence from file 04 is available.
- Existing CBE routing headers and calibration tooling are available.

## Affected areas

- `internal/promshim/routing_policy.go`
- `internal/promshim/query_cost_class.go`
- `internal/promshim/cbe_candidates.go`
- `internal/promshim/service.go`
- `cmd/promshim-routing-calibrate/`
- `.pi/cost-routing-calibration.json`
- `.pi/cost-routing-calibration.md`
- `README.md`
- `docs/optimization-rollout.md`
- `scripts/bench-matrix.sh`

## Calibration dimensions

Calibration must separate at least these dimensions when enough samples exist:

- query family;
- endpoint (`query` vs `query_range`);
- profile and density;
- output points/series bands;
- input samples/series estimates;
- ClickHouse transport;
- settings profile;
- reference ClickHouse profile;
- strict candidate and alternate candidate;
- p50/p95 plus ProfileEvents/memory signals for heavy families.

Do not mix sparse, dense, and long-range results into one unqualified threshold.

## Implementation tasks

1. Audit current CBE decision points.
   - Map where strict, selected, and served candidates are chosen.
   - Identify current family gates, hard caps, confidence checks, and missing
     estimate behavior.
   - Acceptance: short note or code comments identify the decision path for each
     candidate type.

2. Extend calibration inputs if needed.
   - Ensure `cmd/promshim-routing-calibrate` reads v2 bench fields for settings
     profile, routing policy, candidate names, profile/density/transport, and
     errors/timeouts.
   - Include memory/query-log summary references when available.
   - Acceptance: unit tests cover multi-manifest inputs with sparse and dense
     examples and preserve source manifest paths.

3. Generate current calibration.
   - Use named artifacts from files 01-04.
   - Generate `.pi/cost-routing-calibration.json` and `.pi/cost-routing-calibration.md`.
   - Acceptance: generated markdown explains which families are enabled,
     shadow-only, insufficient, or rejected.

4. Implement stale/missing calibration checks if absent.
   - Routing must fail safe to strict/reference when calibration is missing,
     stale, low-confidence, or does not match the current family/profile/density.
   - Include invalidation signals for IR/SQL renderer/settings profile changes in
     docs and, where feasible, calibration metadata.
   - Acceptance: tests cover missing/stale calibration choosing strict.

5. Tune family gates.
   - Start with families where artifacts show clear tier differences:
     - short instant `rate`/`increase` where local may beat native;
     - simple selectors where native overhead may dominate tiny queries;
     - aggregation/range rows where native dominates local round trips;
     - heavy dense controls that must remain strict or capped.
   - Acceptance: gates are bounded enums and never include raw PromQL/labels.

6. Add shadow-before-prefer workflow.
   - For any new served `cost_prefer` family, first run `cost_shadow` or a
     warmed equivalent to collect prediction and divergence evidence.
   - Acceptance: docs and tests show `cost_prefer` only serves when confidence,
     caps, estimates, and family gates pass.

7. Update reporting.
   - Ensure `bench-matrix.sh` makes candidate flips, errors, and confidence bands
     visible.
   - Acceptance: matrix output flags route flips and timeouts in a way reviewers
     can act on.

## Validation tasks

Calibration tool checks:

```bash
go test ./cmd/promshim-routing-calibrate/...
go test ./internal/promshim/...
```

Generate calibration from named sweeps:

```bash
go run ./cmd/promshim-routing-calibrate \
  --sweep harness/artifacts/sweeps/<baseline-or-family-sweep>/manifest.json \
  --out-json .pi/cost-routing-calibration.json \
  --out-md .pi/cost-routing-calibration.md
```

Shadow/prefer validation pattern:

```bash
./scripts/run-sweep.sh \
  --name cbe-<family>-shadow-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --warmup-routing-policies cost_shadow \
  --corpus-set native --memory summary

PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=<family> ./scripts/run-sweep.sh \
  --name cbe-<family>-prefer-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --warmup-routing-policies cost_shadow \
  --corpus-set native --memory summary
```

Negative controls:

```bash
PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=<family> ./scripts/run-sweep.sh \
  --name cbe-<family>-dense-control \
  --profile 7d --density dense --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --corpus-set processing --memory summary
```

Compliance gate before broadening served families:

```bash
./scripts/run-sweep.sh --name cbe-<family>-compliance --skip-bench
```

## Exit criteria

- Calibration artifacts are regenerated from preserved sweep manifests.
- Missing/stale/low-confidence calibration chooses strict/reference behavior.
- At least one family gate is adjusted from evidence, or the current gates are
  explicitly preserved with artifact-backed reasoning.
- Strategy/candidate flips are visible in reports.
- Long-range and dense controls either pass or keep the family gated off.
- README/docs explain how to regenerate calibration and rollback served CBE.

## Handoff to next file

Use CBE results to identify tier 3/4 candidates that are already cheaper but need
implementation cleanup, or families where tier 3/4 could become cheaper with
local/subtree optimization.
