# Dashboard compatibility mode exploration plan

## Purpose

Promshim's current goal is strict Prometheus query compatibility. That remains
the default and the correctness target for compliance, alerting, recording rules,
and migration validation.

There is likely a useful product mode for dashboards where exact Prometheus
storage quirks and edge-case semantics are less important than latency,
scalability, and visually faithful trends. This plan explores an explicit
compatibility policy for that use case.

Do **not** treat this as permission to hide strict-mode bugs. Dashboard mode must
be opt-in, observable, measured, and documented separately from compliance.

## Proposed user-facing model

Add an explicit compatibility mode:

```text
PROM_SHIM_COMPATIBILITY_MODE=strict|dashboard|fast
```

Per-request override:

```text
compatibility=strict|dashboard|fast
```

Terminology:

| Mode | Goal | Intended use |
|---|---|---|
| `strict` | Match Prometheus as closely as possible | compliance, alerting, recording rules, migration validation |
| `dashboard` | Preserve visually meaningful dashboard behavior with bounded, declared deviations | Grafana panels, exploratory dashboards, overview charts |
| `fast` | Prefer ClickHouse-friendly / approximate / downsampled execution where declared acceptable | large historical exploration, "show me the shape" workflows |

`strict` remains the default.

## Non-goals

- Do not weaken strict mode.
- Do not add dashboard deviations to the compliance allowlist.
- Do not silently choose approximate semantics without request/config opt-in.
- Do not change labels, timestamps, or series cardinality unless a deviation is
  explicitly designed and measured.
- Do not use dashboard mode for alerting or recording rules.
- Do not expand tier 3/4 feature coverage as part of this exploration.

## Design principles

1. **Explicit opt-in.** Every non-strict response must identify its
   compatibility mode.
2. **Declared deviations.** Every relaxed behavior must have a stable name,
   explanation, expected impact, and validation coverage.
3. **Bounded drift.** Dashboard mode should preserve labels, timestamps, series
   cardinality, and broad numerical shape.
4. **Strict remains the reference.** Differential testing compares dashboard/fast
   behavior against strict/Prometheus and reports drift; it does not redefine
   correctness.
5. **Explainability.** Headers, explain output, metrics, and sweep reports must
   show when relaxed behavior was used.
6. **Cost-aware routing.** Dashboard/fast modes can allow CBE to choose cheaper
   execution where the declared semantic trade-off permits it.

## Response visibility

Headers:

```text
X-Promshim-Compatibility-Mode: dashboard
X-Promshim-Deviations: topk_tie_order,counter_edge_approximation
X-Promshim-Strategy: native_sql
```

Explain output:

```json
{
  "compatibilityMode": "dashboard",
  "deviations": [
    {
      "kind": "counter_edge_approximation",
      "reason": "dashboard mode allows faster extrapolation approximation",
      "expectedImpact": "small numeric drift near range boundaries",
      "enabledBy": "compatibility=dashboard"
    }
  ]
}
```

Metrics:

```text
promshim_queries_total{compatibility_mode="dashboard",strategy="native_sql"}
promshim_dashboard_deviations_total{kind="counter_edge_approximation"}
promshim_compatibility_mode_active{mode="dashboard"}
```

## Candidate relaxed behaviors

Each candidate must be explored separately with benchmarks and differential
reports before being enabled by `dashboard` or `fast`.

| Candidate | Strict behavior | Dashboard/fast possibility | Risk |
|---|---|---|---|
| Output tie ordering | Match Prometheus where possible, including storage-order quirks when feasible | deterministic label/value ordering for ties | cosmetic for dashboards, unsafe if clients depend on ordering |
| Tiny numeric drift | Tight Prometheus tolerances | bounded absolute/relative tolerance | usually safe if labels/timestamps match |
| Counter extrapolation edges | Exact Prometheus `rate`/`increase` extrapolation | faster approximation near range boundaries | can mislead near sparse edges/resets |
| Staleness/lookback | Exact Prometheus lookback/staleness behavior | simplified range-panel behavior when visually equivalent | can create/drop points unexpectedly; high risk |
| Range step density | Exact requested step | optional max-points coarsening for dashboard panels | can hide spikes; must be explicit |
| Historical ranges | raw full-resolution scan | downsampled/pre-aggregated source when available | can hide detail; must expose resolution |
| Histogram quantile | exact classic PromQL semantics | faster approximation or pre-aggregated bucket path | can shift percentiles; needs bounded validation |
| Metadata | live metadata | cached metadata with TTL | usually acceptable for dashboards |
| TopK tie-break | Prometheus TSDB-order tie quirks | deterministic tie ordering | already an accepted strict-mode deviance in narrow cases |

High-risk behaviors such as label changes, timestamp shifts, dropped series,
vector-matching cardinality changes, hidden counter resets, or unit changes are
not acceptable for dashboard mode unless explicitly designed and approved.

## Routing interaction

Compatibility mode should be separate from native lowering mode and routing
policy.

Existing/native-lowering mode examples:

```text
native_lowering_mode=prefer|off|explain|shadow|force_supported
```

Proposed routing policy examples:

```text
routing_policy=strict|cost_shadow|cost_prefer
```

Compatibility interaction:

| Compatibility | Routing behavior |
|---|---|
| `strict` | current strict tier hierarchy unless explicit routing policy is selected |
| `dashboard` | may allow cost-aware routing among candidates with declared acceptable deviations |
| `fast` | may prefer ClickHouse-native/downsampled/approximate paths when declared acceptable |

Important invariants:

- `force_supported` still means native SQL root only.
- Dashboard mode must not make native coverage look better than strict coverage.
- Shadow/differential metrics should compare dashboard/fast candidates against
  strict behavior where feasible.

## IR integration

The IR should carry compatibility requirements and allowed deviations explicitly.

Possible additions:

```go
type CompatibilityMode string

const (
    CompatibilityStrict    CompatibilityMode = "strict"
    CompatibilityDashboard CompatibilityMode = "dashboard"
    CompatibilityFast      CompatibilityMode = "fast"
)

type SemanticDeviation struct {
    Kind           string
    ExpectedImpact string
    EnabledBy      CompatibilityMode
}
```

Planner/lowering responsibilities:

- annotate candidate plans with required/used deviations;
- reject non-strict plans in strict mode;
- expose deviations in explain output;
- feed deviation metadata into cost routing;
- ensure deviations are stable names, not free-form messages only.

## Validation strategy

Dashboard mode needs a differential harness, not a compliance allowlist.

Add a dashboard-diff suite that reports:

- exact match count;
- structural match count;
- label mismatch count;
- timestamp mismatch count;
- series cardinality mismatch count;
- max absolute error;
- max relative error;
- per-series/range aggregate drift;
- visual-band classification:
  - `identical`,
  - `visually_equivalent`,
  - `noticeable`,
  - `unsafe`.

Example command target:

```bash
./scripts/run-sweep.sh \
  --suite bench,dashboard-diff \
  --compatibility strict,dashboard \
  --profile 7d,30d \
  --density dense
```

Dashboard mode should not go green by hiding diffs. It should quantify them.

## Benchmark strategy

Evaluate candidate deviations using the sweep machinery:

- sparse and dense datasets;
- 7d/30d/1y profiles;
- transport modes;
- execution modes (`prefer`, `force_supported`, `off`);
- ProfileEvents;
- memory trade-off reporting.

Key questions:

1. Which candidate deviations unlock real latency or memory wins?
2. Do wins happen on dashboard-like queries, not only synthetic cases?
3. Are labels, timestamps, and series cardinality preserved?
4. Is numerical drift visually acceptable?
5. Does CBE choose dashboard candidates predictably?
6. Can users see what semantic shortcuts were used?

## Implementation phases

### Phase 1. Compatibility mode plumbing

Goal: make compatibility mode explicit without changing behavior.

Tasks:

1. Add config/env parsing for `PROM_SHIM_COMPATIBILITY_MODE`.
2. Add per-request `compatibility=` override.
3. Add mode to request context.
4. Add response header `X-Promshim-Compatibility-Mode`.
5. Add explain field `compatibilityMode`.
6. Add metrics label `compatibility_mode`.
7. Default to `strict`.

Validation:

```bash
go test ./...
./scripts/run-compliance.sh
```

### Phase 2. Deviation model and explain visibility

Goal: plans can declare semantic deviations, but strict mode still rejects them.

Tasks:

1. Add stable deviation registry/names.
2. Add IR/plan metadata for used deviations.
3. Add explain output for deviations.
4. Add `X-Promshim-Deviations` header when non-empty.
5. Ensure strict mode rejects any non-strict-only candidate.

Validation:

- Unit tests for strict rejection.
- Explain snapshot tests for deviation metadata.

### Phase 3. First low-risk dashboard deviation

Goal: implement one safe, measurable deviation to exercise the model.

Candidate: deterministic tie ordering for topk/bottomk or tiny numeric tolerance
classification in dashboard-diff. Prefer something with low semantic risk and
clear visibility.

Tasks:

1. Implement deviation behind `compatibility=dashboard`.
2. Add differential tests.
3. Add explain/header/metric visibility.
4. Benchmark strict vs dashboard.

Validation:

```bash
./scripts/run-harness.sh --corpus common-dashboard-subset.json --subjects shim
./scripts/run-sweep.sh --suite bench,dashboard-diff --compatibility strict,dashboard --profile 7d --density sparse
```

### Phase 4. Dashboard-diff harness

Goal: quantify drift instead of relying on hand inspection.

Tasks:

1. Add result comparison modes for dashboard equivalence.
2. Add drift metrics and visual-band classification.
3. Emit dashboard-diff JSON/Markdown artifacts.
4. Add sweep integration.

Validation:

- Known exact matches classify as `identical`.
- Known tiny drift classifies as `visually_equivalent`.
- Label/timestamp/cardinality mismatches classify as `unsafe` by default.

### Phase 5. CBE integration exploration

Goal: allow cost routing to use dashboard-compatible candidates.

Tasks:

1. Feed compatibility/deviation metadata into routing candidate selection.
2. Add `cost_shadow` reporting for dashboard candidates.
3. Compare strict vs dashboard route choices in sweep matrices.
4. Keep `force_supported` semantics unchanged.

Validation:

```bash
./scripts/run-sweep.sh \
  --suite bench,dashboard-diff \
  --compatibility strict,dashboard \
  --routing-policy cost_shadow \
  --profile 7d,30d \
  --density dense
```

## Exit criteria for exploration

This exploration is successful when:

1. Compatibility mode is explicit and defaults to strict.
2. Non-strict deviations are visible in headers, explain output, metrics, and
   sweep artifacts.
3. Strict compliance remains unchanged.
4. At least one low-risk dashboard deviation is implemented and measured.
5. Dashboard-diff quantifies drift with labels/timestamps/cardinality guarded.
6. Sweep output shows whether dashboard mode unlocks meaningful latency/memory
   wins.
7. Docs clearly say dashboard/fast modes are not for alerting or compliance.

## Open questions

- Should `dashboard` allow step coarsening by default, or require an explicit
  `max_points`/resolution parameter?
- Should `fast` exist initially, or should we launch only `strict|dashboard`?
- Which deviations are safe enough for first release?
- How should Grafana users opt in: datasource URL parameter, header, or per-panel
  query parameter?
- Should dashboard mode be global config, per request, or both?
- What drift thresholds qualify as visually equivalent for common panels?
