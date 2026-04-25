# CBE candidate routing implementation plan

## Purpose

Build on the gated cost-routing foundation from
[`../cost-based-routing-plan/`](../cost-based-routing-plan/) and turn it into a
true cost-based execution (CBE) router. CBE means tiers 2, 3, and 4 are real
runtime candidates when they are already known-correct for the query. The router
should choose the cheapest safe candidate for the current query shape and data
size instead of preferring native SQL unconditionally.

Desired end state:

- `prefer` keeps the capability hierarchy as the fallback/reference model.
- `strict` remains the default and follows the existing tier priority.
- `cost_shadow` evaluates candidate choices and can run bounded alternates
  without changing served results.
- `cost_prefer` can serve a non-strict tier-3/tier-4 candidate only when
  correctness, estimates, hard caps, and calibrated cost margins pass.
- Explain output, headers, metrics, and sweep reports show the full candidate
  set, rejected candidates, selected candidate, strict/reference candidate, and
  stable reasons.
- Tier 3/4 iteration is allowed when tied to CBE routing quality, safety caps,
  observability, or performance for already-supported semantics.

## Execution order

1. [`01-candidate-contract-and-planning.md`](01-candidate-contract-and-planning.md)
   - Introduce an explicit CBE candidate model and candidate-planning contract
     for tiers 2, 3, and 4 without changing served behavior.
2. [`02-estimates-and-warmup-lifecycle.md`](02-estimates-and-warmup-lifecycle.md)
   - Make estimates reproducible, observable, cache-aware, and warmable via
     sweep workflows.
3. [`03-shadow-and-differential-cbe.md`](03-shadow-and-differential-cbe.md)
   - Run full candidate decisions and bounded alternates under `cost_shadow`,
     recording correctness and prediction evidence before serving changes.
4. [`04-cost-prefer-serving-candidates.md`](04-cost-prefer-serving-candidates.md)
   - Enable `cost_prefer` to serve tier-3/tier-4 candidates for one family at a
     time when evidence and caps pass.
5. [`05-calibration-and-maintenance.md`](05-calibration-and-maintenance.md)
   - Keep calibration, docs, validation, and future candidate expansion current
     as CBE evolves.

## Dependency graph

```text
01 candidate contract
  -> 02 estimates + warmup lifecycle
      -> 03 shadow + differential CBE
          -> 04 cost-prefer serving candidates
              -> 05 calibration + maintenance
```

Each numbered file should leave the repository useful if work pauses after that
slice. Do not skip from candidate scaffolding to served CBE without shadow and
validation evidence.

## Hard constraints

- Tier 1 whole-query delegation remains the preferred ClickHouse-native endpoint
  when it can serve the query correctly.
- Below tier 1, tiers 2, 3, and 4 are valid CBE routing candidates when they are
  known-correct for the query.
- Tier 3/4 work is allowed when it supports CBE routing quality, safety caps,
  observability, or performance for already-supported semantics.
- Do not add unrelated lower-tier semantic coverage opportunistically. New
  semantic coverage must be justified by the CBE plan, a correctness bug, or an
  explicit user request.
- `strict` remains default and behavior-compatible.
- `force_supported` remains native-only visibility mode and must not fall back
  to tier 3/4.
- `off` remains the full-local baseline and should ignore CBE selection.
- Missing estimates, stale estimates, uncertain costs, hard-cap failures, known
  divergences, and absent validation choose the safe/reference route.
- Do not add compliance allowlist entries for CBE routing changes.
- Use stable bounded enum labels for metrics; raw PromQL, matchers, and
  high-cardinality values must not become metric labels.
- Use `./scripts/run-sweep.sh` for named benchmark/compliance validation and
  isolated benchmark data. Long-range/dense data must never use compliance
  ports.

## Candidate terminology

- **Strict/reference candidate**: the candidate chosen by existing strict
  priority for this request.
- **CBE candidate**: a known-correct tier-2, tier-3, or tier-4 execution option
  that can be estimated, capped, and ranked.
- **Selected candidate**: the candidate that CBE would serve in `cost_prefer`,
  or would select in `cost_shadow` if serving were allowed.
- **Served candidate**: the candidate actually used for the response. In
  `cost_shadow`, this remains the strict/reference candidate.
- **Rejected candidate**: a candidate that exists but cannot be ranked or served
  because of support, correctness, estimate, cap, confidence, or policy gates.

## Cross-cutting validation

Fast validation for code slices:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
```

Compliance validation:

```bash
./scripts/run-sweep.sh --name cbe-strict-compliance --skip-bench
```

Shadow/prefer sparse validation for each enabled family:

```bash
./scripts/run-sweep.sh \
  --name cbe-shadow-<family>-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --cost-routing-local-families <family> \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name cbe-prefer-<family>-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families <family> \
  --corpus-set native \
  --memory summary
```

Negative controls:

```bash
./scripts/run-sweep.sh \
  --name cbe-prefer-<family>-long-range-sparse \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families <family> \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name cbe-prefer-<family>-7d-dense-processing \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families <family> \
  --corpus-set processing \
  --memory summary
```

Artifact checks:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run>/manifest.json --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/<run>/memory-summary-*.json
```

Performance claims must follow `.pi/skills/measuring-ch-optimizations/SKILL.md`:
use ProfileEvents and strategy/candidate fields, not p50 alone.

## Cross-cutting risks and rollback

| Risk | Mitigation |
|---|---|
| Local/full-local wins on sparse fixtures but cliffs on dense or long-range inputs | hard caps, dense/long-range negative controls, strict on missing/stale estimates |
| Candidate generation accidentally adds lower-tier semantic coverage | separate support detection from new feature work; require explicit plan task for any semantic expansion |
| Shadow alternates overload ClickHouse or distort query-log evidence | bounded alternates, concurrency/sampling caps, named sweep locks, no ad-hoc stack probes during sweeps |
| Cost model hides native correctness regressions | preserve strict policy, force_supported mode, compliance and differential sweeps |
| Metrics cardinality grows with query labels | stable family/candidate/reason enums only |
| Calibration goes stale after optimizer changes | regenerate from named sweep manifests after tier-2, tier-3, tier-4, corpus, or fixture changes |

Rollback is config-first: set `PROM_SHIM_ROUTING_POLICY=strict`, remove
`routing_policy=cost_prefer`, or remove family gates from
`PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES`. `force_supported` and `off` remain
visibility/baseline modes independent of CBE.

## Final acceptance criteria

This plan is complete when:

- explain output can show all tier-2/3/4 candidates, selected candidate,
  strict/reference candidate, estimates, caps, and rejection reasons;
- `cost_shadow` evaluates and records bounded candidate decisions without
  serving alternate results;
- `cost_prefer` can serve at least one tier-3 or tier-4 candidate for a bounded
  family when evidence proves it is cheaper and correct;
- long-range/native-required and dense/cardinality negative controls do not
  route into unsafe candidates;
- strict compliance remains clean against the existing allowlist;
- calibration can be regenerated from multiple named sweep manifests;
- docs explain CBE candidate semantics, family gates, safety caps, and rollback;
- no unrelated tier-3/tier-4 semantic coverage was added as part of routing work.

## Handoff

This is a long-running split plan. Use `execute-long-plan` when moving from
planning to implementation, and work through the numbered files in order.
