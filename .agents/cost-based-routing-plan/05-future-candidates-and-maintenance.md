# 05. Future candidates and maintenance

## Purpose and scope

Extend and maintain cost routing only after the root native-vs-full-local router
is proven. This slice covers later candidate types, recalibration, native SQL
shape cost models, and ongoing validation discipline.

This file is intentionally deferred. Do not use it to justify broadening tier 3
or tier 4 feature coverage.

## Current execution decision

The initial rollout stops at root native-vs-full-local cost routing for the
explicit `selector_instant,rate_instant` local-family gates. Validation in
[`04-cost-prefer-rollout.md`](04-cost-prefer-rollout.md) showed one small-query
local override with strict rollback, clean strict compliance, and no new
long-range/native-required local override. It also showed that dense processing
cardinality cliffs already exist in strict local fallback shapes, so this slice
does **not** broaden tier-3/tier-4 candidates or enable histogram/range local
routing.

Future candidate expansion should start from new shadow-only evidence and named
sweep manifests, not from the current rollout artifacts alone.

## Prerequisites

- Complete [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md).
- Complete [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md).
- Complete [`03-cost-shadow.md`](03-cost-shadow.md).
- Complete [`04-cost-prefer-rollout.md`](04-cost-prefer-rollout.md).
- Root native-vs-full-local cost routing has clean shadow evidence and a clean
  cost-prefer differential corpus for enabled families.

## Affected areas

- service planner orchestration
- tier-3 candidate planning where already supported
- native SQL renderer/plan shape selection
- plan-cache/subtree-hash cost accounting
- calibration generator and artifacts
- named sweep manifests, benchmark reports, and memory summaries

## Later candidate set

Only after root-level cost routing is stable, consider adding:

1. **Local with subtree pushdown candidate** — current tier-3 plan where it
   already exists.
2. **Alternative native SQL shape candidate** — e.g. the range-function strategy
   from `.pi/optimizer/04-direct-window-join-cost-model.md`.
3. **Whole-query delegation vs native SQL** — compare tier 1 and tier 2 where
   both are supported.

Each added candidate needs its own shadow metrics, hard caps, and differential
validation. Adding a candidate does not authorize new lower-tier feature work.

## Future implementation tasks

- [ ] Add tier-3 candidate evaluation only for existing subtree-pushdown shapes.
  - Do not add new tier-3 feature coverage.
  - Compare tier 3 vs tier 4 vs tier 2 for bounded corpora.
  - Require separate shadow metrics and caps for tier-3 candidates.
- [ ] Integrate native SQL shape choices from
  `.pi/optimizer/04-direct-window-join-cost-model.md`.
  - Treat this as a tier-2 internal cost model.
  - Prefer native SQL shape optimization over local fallback when it solves the
    same small-query overhead.
  - This work may graduate earlier than tier-3 candidate evaluation if it is
    purely tier 2.
- [ ] Integrate plan-cache/subtree-hash evidence from
  `.pi/optimizer/02-fragment-subtree-hashing.md`.
  - If plan cache lands, recalibrate small-query native costs before broadening
    local routing.
  - Include planning overhead in calibration where it materially affects latency.
- [ ] Revisit value-only/scalar-output native optimization from
  `.pi/optimizer/04-time-series-value-only-mode.md`.
  - If native small-query costs drop, local overrides may no longer be needed
    for some classes.
- [ ] Add whole-query delegation vs native SQL comparison only after there is an
  explicit tier1-vs-tier2 model.
  - Until then, whole-query delegation remains strict-preferred.
- [ ] Regenerate `.pi/cost-routing-calibration.*` after every major tier-2 SQL
  optimization, plan-cache change, or candidate-set expansion.
- [ ] Keep documentation current for family gates, caps, and rollback behavior.

## Validation tasks

- [ ] For each new candidate, add shadow-only validation before serving it.
- [ ] For each new candidate, add a bounded differential corpus with positive
  and negative controls.
- [ ] Re-run strict compliance after candidate-set changes.
- [ ] Re-run cost-prefer differential validation for all enabled families.
- [ ] Re-run named sparse, dense, and long-range sweeps and inspect
  memory/ProfileEvents counters, not just p50.
- [ ] Verify `strategy_used`, routing policy, and selected strategy fields catch
  strategy flips.
- [ ] Preserve complete before/after sweep artifact directories for review.

Commands remain the cross-cutting validation commands from
[`README.md`](README.md), with workflow details governed by
`.pi/skills/running-sweep/SKILL.md` and performance claims evaluated under
`.pi/skills/measuring-ch-optimizations/SKILL.md`.

## Maintenance rules

- Calibration data is not permanent truth. Recalibrate from named sweep
  manifests when SQL plans, plan caches, ClickHouse behavior, fixture density,
  corpus sets, routing policies, or candidate sets change.
- Strategy flips are hard warnings, even when matrix benches look green.
- Small wall-clock deltas are noise unless sweep memory/ProfileEvents counters
  support them.
- Strict policy and `force_supported` remain visibility modes for native
  coverage.
- Do not expand expected failures to make routing changes look clean.
- Do not put raw queries, matchers, or high-cardinality values in metrics.
- Prefer tier-2 internal cost models over local fallback when both address the
  same overhead.

## Exit criteria

- [ ] Cost routing considers tier 3 only where tier 3 already exists and proves
  useful under shadow and differential validation.
- [ ] Tier-2 SQL-shape cost models are preferred over local fallback when they
  solve the same small-query overhead.
- [ ] Whole-query delegation comparisons have an explicit tier1-vs-tier2 model
  before changing strict preference.
- [ ] Calibration is regenerated from sweep manifests and reviewed after each
  material optimizer or candidate-set change.
- [ ] Enabled cost-prefer families continue to pass correctness and performance
  validation after each expansion.

## Handoff notes

This is a future-work file. If execution reaches this point, first reassess the
state of strict compliance, shadow divergence, cost-prefer sweep results, and
current tier-2 optimizer work. If native optimizations have erased the original
small-query gap, narrow or remove local overrides rather than broadening them.
