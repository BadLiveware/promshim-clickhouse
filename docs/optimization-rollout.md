# Optimization rollout, calibration, and maintenance

This playbook turns individual optimization evidence into maintainable CBE
behavior. It complements:

- [`docs/optimizer-contracts.md`](optimizer-contracts.md) — semantic and evidence
  contracts;
- [`docs/clickhouse-tuning-inventory.md`](clickhouse-tuning-inventory.md) —
  shim-owned versus operator-owned settings;
- [`docs/clickhouse-reference-profile.md`](clickhouse-reference-profile.md) —
  reference ClickHouse deployment context; and
- the current CBE configuration in [`README.md`](../README.md#cost-routing-policies).

## Rollout posture

Strict/reference behavior remains the default and the rollback path. A new
optimization may change SQL shape, local execution, subtree pushdown, or settings
profile provenance before it is allowed to change served routing.

| Surface | Default / safe state | Rollout gate | Rollback control |
|---|---|---|---|
| Native lowering mode | `PROM_SHIM_NATIVE_LOWERING_MODE=prefer` with strict priority order, or request `native_lowering_mode=off` for full local reference. | `force_supported` only for native-only validation; `shadow` for background native visibility. | `PROM_SHIM_NATIVE_LOWERING_MODE=off` or request `native_lowering_mode=off`. |
| Routing policy | `PROM_SHIM_ROUTING_POLICY=strict`. | `cost_shadow` before `cost_prefer`; served `cost_prefer` requires family gate, estimates, caps, confidence, and preserved artifacts. | `PROM_SHIM_ROUTING_POLICY=strict` or request `routing_policy=strict`. |
| CBE local family serving | No local override unless family is explicitly listed. | `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=<bounded families>` and calibration evidence. | Remove the family or unset `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES`. |
| Logical IR rewrites | Enabled, but explain-traced and conservative. | New risky passes need pass/family gate before serving. | `PROM_SHIM_DISABLE_OPTIMIZED_IR=true`. |
| Native aggregation label projection | Enabled for safe instant-vector `by(...)` aggregation children. | Excludes `without`, selection aggregations, `count_values`, and range-function/subquery children unless separately proven. | `PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION=true`. |
| Native repeated subexpression reuse | Enabled for safe identical instant vector subexpressions in native SQL. | Limited to same-expression default one-to-one vector addition; excludes set ops, matching modifiers, bool comparisons, range output, and algebraic simplification. | `PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE=true`. |
| Local repeated expression cache | Enabled for identical local range-function subexpressions inside one request. | Request-scoped only; cached values are copied before reuse and do not broaden CBE serving gates. | `PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE=true`. |
| ClickHouse settings profiles | `default_safe`: safety/provenance only. | Performance profile names require measured evidence and version checks before applying aggressive settings. | `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=none` or `default_safe`; unset optional caps. |
| Reference ClickHouse profile | Documentation context only. | A benchmark claim must name whether it used `promshim-ch-timeseries-reference-v1` or another environment. | No server/operator tuning is required for promshim correctness. |

## Optimization chains and parked near-misses

Some optimizations are only valuable after another rewrite changes the query into
the shape they accelerate. Treat a rejected near-miss as reusable evidence when it
has all of these properties:

- it preserved correctness, `native_sql` strategy, and ClickHouse roundtrips;
- it moved the expected lower-variance signal in the right direction but missed
  the standalone gate;
- it had no unrelated-row guardrail regression; and
- a later accepted change shifts the bottleneck or exposes a simpler logical
  shape that the near-miss can now target.

Do not lower the normal commit gate to keep these changes. Instead, keep the
standalone gate strict and retest parked near-misses after the baseline changes.
Use fresh named artifacts and compare against the new accepted baseline.

The rate-range optimization chain is the reference example:

1. Rate materialization sorting made the duplicated `rate` branch a larger share
   of remaining work.
2. Strict logical cancellation rewrote bounded repeated averages such as
   `(rate(x[5m]) + rate(x[5m])) / 2`,
   `(rate(x[5m]) + rate(x[5m]) + rate(x[5m])) / 3`, and exact power-of-two
   reciprocal spellings like `(rate(x[5m]) + ... + rate(x[5m])) * 0.25` to
   `rate(x[5m])` only when the divisor or reciprocal multiplier exactly matched
   the repeated term count, each addition used implicit one-to-one matching,
   every operand was structurally identical, and every operand dropped the
   metric name.
3. The simplified `rate(x[5m])` shape then became eligible for the direct
   selector-window aggregate path.

Those changes are nonlinear: the second change removes duplicate rate execution
and binary join materialization, while the third change applies only after the
query has become a direct `rate(...)` range shape. This is why the combined
stack moved far more than either idea suggested on older baselines.

When reviewing similar work, ask whether the candidate changes the query class or
only prettifies SQL text. Prioritize chains that move from a compound expression
to a simpler semantic shape and then route that shape to a specialized physical
path. Avoid re-running wrapper, alias, or tuple-accessor cleanups unless explain
and ProfileEvents show that they now remove real executor work.

## Shadow and differential validation

Use serving modes deliberately:

1. `strict` is the production/reference behavior.
2. `cost_shadow` computes cost decisions and may run a bounded alternate
   candidate while serving strict/reference results.
3. `force_supported` keeps native SQL coverage and native-only gaps visible.
4. `off` is the local/reference comparison mode.
5. `cost_prefer` is only for bounded families after shadow and calibration
   evidence.

Reports and explain output must preserve these as separate concepts:

- strict/reference candidate;
- selected CBE candidate;
- served candidate;
- routing policy, decision, and reason;
- divergence status and fallback reason;
- cap rejections and family gates; and
- settings profile name and applied/skipped settings.

Shadow work must be bounded. Do not let alternate execution concurrency distort
the benchmark or production workload that it is supposed to measure.

## CBE family gate validation

Before enabling or broadening a `cost_prefer` local family gate, preserve three
named benchmark artifacts for the candidate family:

1. Shadow discovery: run `strict,cost_shadow` with a shadow warmup. This should
   expose selected-vs-strict candidate flips while the served candidate remains
   strict/reference.
2. Negative prefer control: run `strict,cost_prefer` with the family gate but
   without shadow-warmed estimates. This must fail safe to strict/reference,
   typically with `strict_missing_estimate`.
3. Shadow-warmed prefer: run `strict,cost_prefer` with the family gate after a
   `cost_shadow` warmup. Only the intended bounded rows may serve the alternate
   candidate; over-cap, stale, missing-estimate, delegated, and unsupported rows
   must remain strict/reference.

Example command sequence:

```bash
./scripts/run-sweep.sh \
  --name cbe-<family>-shadow-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --warmup-routing-policies cost_shadow \
  --corpus-set optimization --memory summary

./scripts/run-sweep.sh \
  --name cbe-<family>-prefer-negative-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --warmup-routing-policies cost_prefer \
  --cost-routing-local-families <family> \
  --corpus-set optimization --memory summary

./scripts/run-sweep.sh \
  --name cbe-<family>-prefer-warmed-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --warmup-routing-policies cost_shadow \
  --cost-routing-local-families <family> \
  --corpus-set optimization --memory summary
```

For each artifact, render the summary and per-query matrix:

```bash
./scripts/bench-artifact-summary.sh harness/artifacts/sweeps/<run-name>
./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/<run-name>/manifest.json \
  --per-query
```

Reviewers should confirm candidate flips, served candidates, routing reasons,
hard-cap decisions, and zero missing log comments before accepting a served CBE
change. Dense or long-range controls are required when the candidate family can
cliff on cardinality, window width, output points, or range duration.

## Calibration refresh workflow

Calibration is generated from named artifacts. It must not be hand-entered from
an impression or a single p50 delta.

1. Preserve the sweep directories used as inputs.
2. Confirm each input has a manifest, bench report, and memory summary when the
   claim depends on query-log/ProfileEvents evidence.
3. Generate calibration with the existing tool:

   ```bash
   go run ./cmd/promshim-routing-calibrate \
     --sweep harness/artifacts/sweeps/<sweep>/manifest.json \
     --out-json .pi/cost-routing-calibration.json \
     --out-md .pi/cost-routing-calibration.md
   ```

4. Review the generated markdown as code: it is routing evidence, not durable
   truth.
5. Use `cost_shadow` after refresh to warm/check estimates before any
   `cost_prefer` served change.

Calibration inputs must record or be recoverable from artifacts:

| Input | Where to capture |
|---|---|
| Git revision / diff context | PR description or artifact notes. |
| Sweep name and manifest path | `harness/artifacts/sweeps/<name>/manifest.json`. |
| Corpus set and query categories | Bench report `corpusPath`, rows, and categories. |
| Profile, density, transport | Sweep manifest labels and run labels. |
| ClickHouse version and reference deployment profile | README/artifact notes; reference profile name when applicable. |
| Promshim settings profile | Bench report `settingsProfile` and explain metadata. |
| Query-family labels and route decisions | Response headers, explain routing fields, bench report rows. |
| Query-log/ProfileEvents evidence | Memory summaries and focused `system.query_log` captures. |

Calibration is stale after changes to any of:

- IR rewrite logic or pass gates;
- native SQL renderer or SQL-shape optimization;
- local executor or subtree pushdown behavior;
- CBE cost model, thresholds, caps, or family labels;
- benchmark corpus, fixture, profile, density, or seeded data;
- ClickHouse version, transport, or reference deployment profile; or
- ClickHouse settings profile behavior.

Missing or stale calibration must fail safe to strict/reference behavior. A stale
calibration is evidence to rerun sweeps, not a reason to broaden serving.

## Regression detection

Treat these as review-worthy signals:

| Signal | Why it matters | Required follow-up |
|---|---|---|
| Strict/selected/served candidate flip | May be a real win, a stale estimate, or a regression. | Compare explain routing fields, family gates, caps, and before/after artifacts. |
| Fallback from native SQL to local/reference | Could hide unsupported coverage or a optimizer regression. | Check rejection reason, compliance/native gap report, and focused tests. |
| Byte-identical `EXPLAIN SYNTAX` after a SQL rewrite | Suggests ClickHouse sees the same executable shape. | Do not claim executor improvement unless ProfileEvents still move for another reason. |
| Lower p50 without ProfileEvents/EXPLAIN movement | Could be cache/noise. | Re-run with named artifacts and inspect query-log counters. |
| Missing log comments or query-log rows | Breaks attribution. | Fix observability before making an optimization claim. |
| New compliance diff | Correctness blocker unless it is an already-approved allowed deviance. | Do not add expected failures for shim bugs; fix or keep visible. |

Preserve before/after artifact directories. Do not overwrite the only baseline
when updating a calibration file or a benchmark report.

## Optimization PR review criteria

Every optimization PR or review note should answer:

- Query family and candidate type affected.
- Semantic risk: staleness, NaN, histograms, vector matching, label retention,
  output ordering, subquery step grids, or none.
- Expected measurement signal before measurement: rows/bytes, transfer width,
  function executions, memory, round trips, route choice, or local CPU.
- Baseline artifact path.
- Optimized/shadow artifact path.
- Negative controls: long-range, dense/cardinality, risky family, or documented
  reason a gate excludes that shape.
- EXPLAIN evidence: `SYNTAX`, `PLAN`, and `PIPELINE` when claiming SQL-shape
  change.
- ProfileEvents/query-log evidence and log-comment correlation.
- Compliance result and native-only gap visibility.
- Settings profile and reference ClickHouse profile used.
- Rollback gate and how it was tested.
- External-example note when applicable:
  - source repo/path;
  - borrowed idea;
  - rejected parts;
  - PromQL/ClickHouse risks; and
  - evidence that the adaptation is valid for promshim.

For the aggregation projection change, the external-example note is:
DataFusion/Calcite projection-pushdown patterns informed the rule boundary, but
promshim applied it only through PromQL label semantics. Range-function children,
`without`, selection aggregations, and `count_values` were rejected because they
need full label identity or synthesize/exclude labels differently.

## Documentation and troubleshooting guide

| User question | Start here |
|---|---|
| Why was this candidate served? | Explain response routing fields and response headers: `X-Promshim-Routing-*`, `X-Promshim-*-Candidate`, `X-Promshim-Cost-Family`. |
| Which ClickHouse settings were used? | `X-Promshim-Settings-Profile`, explain `clickHouseSettingsProfile`, and `system.query_log.Settings`. |
| How do I disable optimized IR? | Set `PROM_SHIM_DISABLE_OPTIMIZED_IR=true`. |
| How do I disable aggregation label projection? | Set `PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION=true`. |
| How do I disable native repeated subexpression reuse? | Set `PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE=true`. |
| How do I disable local repeated expression caching? | Set `PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE=true`. |
| How do I disable performance settings? | Use `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=none` or `default_safe`; performance profiles remain evidence-gated. |
| How do I compare local/reference behavior? | Request `native_lowering_mode=off` and compare against `prefer`/`force_supported` with tolerance-aware harnesses. |
| How do I prove a SQL-shape claim? | Capture `EXPLAIN SYNTAX`, `PLAN`, `PIPELINE`, and query-log ProfileEvents under a bounded log comment. |
| Is ClickHouse server tuning required? | No. See `docs/clickhouse-reference-profile.md`; it is benchmark/operator guidance, not a hidden correctness dependency. |

## Long-term maintenance workflow

Run this workflow periodically and whenever major optimizer or ClickHouse changes
land:

1. Rerun baseline sweeps against the reference profile and current corpora.
2. Rebuild calibration from preserved manifests using
   `cmd/promshim-routing-calibrate`.
3. Review strategy/candidate flips in `bench-matrix.sh` output.
4. Revisit settings allowlist and version gates when ClickHouse changes.
5. Revisit family gates when new PromQL semantic coverage lands.
6. Remove temporary gates or compatibility shims only when artifacts show they
   are no longer needed and rollback remains possible.
7. Keep README/docs aligned with actual config names and emitted explain/header
   fields.

## Routine validation commands

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
./scripts/run-sweep.sh --dry-run --estimate --name promshim-rollout-dry-run
```

For risky family rollout, add a compliance gate and named sparse/dense/long-range
negative controls before enabling served `cost_prefer` behavior.
