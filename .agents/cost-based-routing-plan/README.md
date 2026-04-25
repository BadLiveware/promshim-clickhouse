# Cost-based routing implementation plan

## Purpose

Promshim currently routes by a strict capability hierarchy:

1. whole-query delegation to ClickHouse PromQL
2. native SQL lowering
3. local Go executor with subtree pushdown
4. full local execution

That hierarchy remains the coverage model. This split plan answers a narrower
runtime question: once a higher tier and a lower tier are both known-correct for
a query, can `prefer` mode choose the lower tier because measured cost predicts
it is faster for the current query shape and dataset size?

Desired end state: cost-aware routing exists only behind an explicit policy,
starts in shadow mode, is limited to bounded query families with hard caps, is
fully explainable in headers/explain/metrics, preserves `force_supported`, and
never becomes a reason to stop tier-2 coverage or optimization work.

## Execution order

1. [`01-observability-and-routing-contract.md`](01-observability-and-routing-contract.md)
   - Add policy parsing, strict routing metadata, query classification, headers,
     explain output, and bench report fields without changing served behavior.
2. [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md)
   - Generate calibration artifacts from `run-sweep` manifests, v2 bench
     reports, and memory summaries, then add cheap/cached selector estimate
     plumbing without serving alternate routes.
3. [`03-cost-shadow.md`](03-cost-shadow.md)
   - Implement transparent cost decisions and bounded alternate execution in
     `cost_shadow` while strict remains the served result.
4. [`04-cost-prefer-rollout.md`](04-cost-prefer-rollout.md)
   - Enable `cost_prefer` only for the safest classes behind explicit gates and
     validate correctness/performance before broadening.
5. [`05-future-candidates-and-maintenance.md`](05-future-candidates-and-maintenance.md)
   - Consider tier-3 candidates, native SQL shape alternatives, recalibration,
     and long-term maintenance after the root native-vs-local router is proven.

## Dependency graph

```text
01 observability contract
  -> 02 calibration + estimates
      -> 03 cost shadow
          -> 04 cost prefer rollout
              -> 05 future candidates + maintenance
```

The repository should remain useful after each numbered file. Do not skip ahead
from `cost_shadow` to serving alternate routes without the earlier metadata,
calibration, estimate, and divergence signals.

## Hard constraints

- New feature/coverage work still belongs in tiers 1 and 2.
- Tiers 3 and 4 are fallbacks. This plan may evaluate them as runtime
  candidates, but it must not expand their feature surface without explicit
  approval.
- The compliance allowlist remains for accepted deviations only. Routing changes
  must not add allowlist entries.
- `force_supported` remains the native-only visibility mode and must continue to
  fail when tier 2 cannot produce a native root.
- Default behavior must remain strict until a cost policy has shadow evidence.
- Strict routing remains the reference behavior and the default production
  behavior until explicitly changed.
- `./scripts/run-sweep.sh` is the primary validation workflow for combined
  compliance, benchmark, profile-density comparison, and memory/ProfileEvents
  evidence. Do not replace it with ad-hoc long-range bench loops unless
  debugging a lower-level script.
- Long-range and dense benchmark data must use the isolated benchmark stack
  (`promshim` `:29191`, Prometheus `:29190`, ClickHouse `:28124`), never the
  compliance ports (`:29091`, `:29090`, `:28123`).
- No unconditional metadata probe may double ClickHouse round trips for simple
  served queries.
- Missing, uncertain, over-cap, or uncalibrated estimates choose strict.
- Query family and reason labels must be stable bounded enums; raw query strings
  must never be metrics labels.

## Requirements carried through all files

1. Keep strict routing as the default and as the reference behavior.
2. Add an opt-in cost-routing policy that can initially shadow decisions without
   changing served results.
3. Make every routing decision explainable in headers, explain plans, and
   metrics.
4. Use cheap, bounded estimates. Do not add an unconditional metadata probe that
   doubles ClickHouse round trips for simple queries.
5. Prefer tier 2 unless the lower-tier candidate has a clear predicted win and
   the query is under hard safety caps.
6. Preserve `force_supported` semantics: no fallback is allowed in that mode.
7. Preserve compliance: results must remain Prometheus-equivalent under both
   strict and cost policies.
8. Treat sweep benchmark and memory/ProfileEvents artifacts as calibration
   inputs, not eternal truths.
9. Calibrate by query family and dataset size, not by one global threshold.
10. Keep all behavior reversible through config and per-request override.

## Non-goals

- Do not replace the tier hierarchy as the conceptual model.
- Do not stop tier-2 feature work because local is faster for some small cases.
- Do not add ML/black-box prediction; start with transparent rules and measured
  coefficients.
- Do not route large uncertain queries to tier 4 just because one short fixture
  looked faster.
- Do not expand tier 3/4 feature support as part of this plan.
- Do not make production default behavior cost-based until shadow mode has real
  evidence.

## Source evidence to reuse

Existing `.pi` guidance:

- `.pi/skills/running-sweep/SKILL.md` owns benchmark/compliance sweep workflow
  selection, isolated benchmark stack usage, artifact interpretation, and
  validation commands for sweep-related changes.

- `.pi/optimizer/04-direct-window-join-cost-model.md` sketches a local cost
  model for one native-SQL shape: direct window join vs materialize/window
  range-function lowering.
- `.pi/optimizer/04-time-series-value-only-mode.md` identifies cases where
  native SQL does unnecessary work when output labels collapse to one series.
- `.pi/optimizer/02-fragment-subtree-hashing.md` describes plan/cache costs that
  matter for small queries where planning overhead is a large share of latency.
- `.pi/optimizer/04-cost-model-driven-pass-ordering.md` motivates cheap shape
  fingerprints and pass skipping; the same fingerprinting idea should feed the
  router.
- `.pi/path2-compliance-matrix.md` and
  `.pi/path2-native-sql-compliance-refresh-2026-04-22.md` establish that broad
  tier-2 support is a compliance goal, not something to abandon for local
  speedups.
- `.pi/skills/measuring-ch-optimizations/SKILL.md` defines how to read bench and
  ClickHouse ProfileEvents evidence: wall-clock is noisy; `strategy_used`,
  `SelectedRows`, `ReadCompressedBytes`, `FunctionExecute`,
  `MemoryTrackerUsage`, and explain SQL shape are required signals.

Current bench interpretation:

- Native SQL should remain the default for high-cardinality/range-reduction
  paths.
- The model must distinguish instant/short/small-output queries from
  range/high-reduction/large-input queries.
- A naive "native if supported" policy leaves small-query latency on the table,
  but a naive "local if currently faster" policy risks severe cliffs as input
  size grows.

## Cross-cutting validation strategy

Fast unit validation:

```bash
go test ./internal/promshim/...
go test ./cmd/promshim-routing-calibrate/...
```

Sweep workflow validation:

```bash
./scripts/run-sweep.sh --dry-run --estimate --name cost-routing-dry-run
./scripts/run-sweep.sh --bench-status
```

HTTP/explain validation remains useful for focused metadata checks. Use the Go
tests by default; only run the curl smoke when a local shim stack is already up:

```bash
go test ./internal/promshim/httpapi ./internal/promshim
curl 'http://localhost:29091/api/v1/query?query=up&routing_policy=cost_shadow&explain=1'
curl -s 'http://localhost:29091/metrics' | rg 'promshim_routing'
```

Correctness and benchmark validation should use named sweeps so compliance logs,
bench reports, strategy histograms, and memory/ProfileEvents evidence live under
one manifest:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-strict-baseline \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary

./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/cost-routing-strict-baseline/manifest.json \
  --per-query
```

If benchmark data is missing, run the setup command printed by `run-sweep` rather
than seeding the compliance stack. Typical one-time setup commands are:

```bash
./scripts/run-sweep.sh --setup --profile all --density sparse --target both
./scripts/run-sweep.sh --setup --profile 7d --density dense --target both
```

Cost-policy sweeps require the routing-policy axis added in the numbered plan
files. Once available, use named sweeps such as:

```bash
./scripts/run-sweep.sh \
  --name cost-routing-shadow-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name cost-routing-prefer-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --cost-routing-local-families selector_instant,rate_instant \
  --corpus-set native \
  --memory summary
```

Inspect sweep artifacts with:

```bash
jq '.bench' harness/artifacts/sweeps/<run-name>/manifest.json
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' \
  harness/artifacts/sweeps/<run-name>/memory-summary-*.json
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json --per-query
```

Accept/reject performance claims using
`.pi/skills/measuring-ch-optimizations/SKILL.md`: investigate strategy flips,
require `run-sweep` memory/ProfileEvents counters for claims, treat small p50
deltas as noise unless counters support them, and preserve the whole sweep
artifact directory for review.

## Cross-cutting risks and rollback notes

| Risk | Mitigation |
|---|---|
| Local route is faster in fixture but cliffs in production cardinality | hard input/output caps; missing estimates choose strict; shadow before serving |
| Metadata probes add more overhead than they save | cache-only in served path; async probe in shadow; strict on cache miss; verify with sweep memory summaries |
| Cost model hides native coverage regressions | `force_supported` unchanged; strict policy remains default; compliance still runs native-only |
| Strategy changes make bench results look green for the wrong reason | sweep reports and metrics include strict/selected strategy and routing policy; strategy flips are hard warnings |
| Alternate shadow execution overloads ClickHouse | only run alternates under caps; sample rate; global concurrency limit |
| Family labels explode metric cardinality | stable enum labels only; raw query strings never in metrics |
| Tier 3/4 start accumulating new feature work | plan forbids new lower-tier coverage; review changes against this constraint |
| Native optimizer changes invalidate calibration | rerun named sweeps and regenerate `.pi/cost-routing-calibration.*` from their manifests after major native optimizer changes |

Rollback is config-first: set `PROM_SHIM_ROUTING_POLICY=strict`, remove any
per-request `routing_policy=cost_prefer`, or disable local family gates. The
strict path must remain behavior-compatible throughout the plan.

## Final acceptance criteria

This split plan is complete when:

- strict routing remains the default and remains behavior-compatible;
- every query can explain its strict strategy, cost class, estimates, and routing
  decision;
- calibration artifacts exist under `.pi/` and can be regenerated;
- `cost_shadow` runs bounded alternate candidates and reports divergence and
  prediction error;
- `cost_prefer` is available only behind explicit config and only for classes
  with hard caps;
- validation demonstrates correctness against Prometheus and measured latency
  improvement for selected small-query classes;
- long-range/high-reduction classes continue to route to tier 2 unless a future
  measured model explicitly proves otherwise.
