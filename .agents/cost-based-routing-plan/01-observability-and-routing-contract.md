# 01. Observability and routing contract

## Purpose and scope

Add the cost-routing control surface and strict-routing observability without
changing served behavior. This slice defines the public contract for routing
policy, native lowering mode interactions, query classification, response
headers, explain output, and bench report fields.

Cost decisions in this file are metadata-only. `strict` remains the only served
behavior.

## Prerequisites

- Read the master constraints in [`README.md`](README.md).
- Preserve the existing tier hierarchy and native lowering mode semantics.
- Do not add lower-tier feature coverage.

## Affected areas

- `internal/promshim/service.go`
- `internal/promshim/httpapi/response.go`
- `internal/promshim/local/explain.go`
- native/logical analysis packages as needed for query classification
- benchmark report structs if needed
- `cmd/promshim-bench` report metadata
- `scripts/run-bench.sh` / `scripts/run-sweep.sh` manifest metadata
- config/env/request parsing helpers

## Routing policies

Add a separate routing policy instead of overloading
`PROM_SHIM_NATIVE_LOWERING_MODE`:

```text
PROM_SHIM_ROUTING_POLICY=strict|cost_shadow|cost_prefer
```

Per-request override:

```text
routing_policy=strict|cost_shadow|cost_prefer
```

Policy behavior:

| Policy | Served result | Candidate behavior | Intended use |
|---|---|---|---|
| `strict` | Existing tier hierarchy | No cost override | Default and baseline |
| `cost_shadow` | Existing strict winner | Compute and record what cost routing would choose; later files may run bounded alternate candidates asynchronously | Calibration and rollout safety |
| `cost_prefer` | Cost-selected candidate, subject to hard caps | Fall back to strict if estimates are missing, uncertain, or above caps | Opt-in latency optimization |

Interaction with native lowering modes:

- `force_supported`: routing policy is ignored; root must be `native_sql`.
- `off`: routing policy is ignored; serve local baseline.
- `shadow`: keep existing shadow semantics initially; do not mix with cost
  routing until the new routing shadow metrics are stable.
- `prefer` / `explain`: routing policy may eventually select among
  already-supported candidates, but this file only reports strict decisions.

## Query classification contract

Introduce a cheap query classifier shared by planning, explain, bench reports,
and routing metrics.

```go
type QueryCostClass struct {
    Endpoint              string // query or query_range
    Family                string // selector, rate, range_rate, histogram_quantile, vector_match, ...
    RootStrategyStrict    string
    OutputKind            string
    HasAggregation        bool
    HasRangeFunction      bool
    HasVectorJoin         bool
    HasHistogram          bool
    HasSubquery           bool
    HasLabelMutation      bool
    HasSelectionAgg       bool
    DropsAllLabels        bool
    SelectorCount         int
    EstimatedSeries       int64
    EstimatedInputSamples int64
    EstimatedOutputPoints int64
    RangePointsPerSeries  int64
    LookbackMS            int64
    StepMS                int64
    OverlapSlots          float64
    NativeRoundTrips      int
    LocalRoundTrips       int
}
```

Implementation guidance:

- Reuse the fragment-shape idea from
  `.pi/optimizer/04-cost-model-driven-pass-ordering.md`.
- Reuse selector signatures from native analysis/renderer where possible.
- Put family names in terms of domain/query behavior, not plan phase labels.
- Include classifier output in explain responses so reviewers can understand
  decisions.
- Keep family names stable because they become calibration and metrics keys.

## Response headers

Add headers on query endpoints:

```text
X-Promshim-Routing-Policy: strict|cost_shadow|cost_prefer
X-Promshim-Routing-Decision: strict|local_override|shadow_only|strict_missing_estimate|strict_over_cap|strict_low_confidence
X-Promshim-Strict-Strategy: delegated_promql|native_sql|local|chunked_local
X-Promshim-Selected-Strategy: delegated_promql|native_sql|local|chunked_local
X-Promshim-Routing-Reason: <short stable reason>
```

`X-Promshim-Strategy` can keep its existing meaning as selected root strategy.

## Explain output

Add a `routing` object to explain responses. In this file most cost and cap
fields may be empty or marked unavailable, but the structure should be stable so
later files can populate it.

```json
{
  "policy": "strict",
  "strictStrategy": "native_sql",
  "selectedStrategy": "native_sql",
  "wouldSelect": "native_sql",
  "decision": "strict",
  "reason": "strict_policy",
  "class": { "family": "selector", "estimatedInputSamples": 0 },
  "cost": { "native": null, "local": null, "unit": "ms_p50_estimate" },
  "caps": {}
}
```

Do not break the existing Prometheus response envelope.

## Run-sweep contract

`run-sweep` is the primary workflow for validating routing changes. This slice
should make the benchmark/report schema ready for routing policy data even
before `cost_shadow` executes alternate candidates. The report/manifest contract
should include:

- native lowering mode (`prefer`, `force_supported`, `off`)
- routing policy (`strict`, `cost_shadow`, `cost_prefer`) when present
- strict strategy and selected strategy
- query cost class
- profile, density, transport, corpus, and run name from the sweep
- memory artifact paths when `--memory summary|detailed` is used

If `run-sweep` does not yet have a routing-policy axis, reserve additive fields
in the v2 bench report and manifest so later files can add
`--routing-policies` without another schema rethink.

## Implementation tasks

- [ ] Add `RoutingPolicy` parsing with default `strict`.
  - Support env config and per-request override.
  - Reject or normalize invalid values using project-standard error behavior.
  - Preserve native lowering mode behavior exactly.
- [ ] Add the `QueryCostClass` data model and extraction path.
  - Cover selector, rate, range rate, histogram, aggregation, vector match, and
    subquery shapes.
  - Prefer existing analysis data over new PromQL tree walks when possible.
- [ ] Surface strict routing metadata in headers.
  - `strict` policy should report strict decision and matching selected strategy.
  - `force_supported` and `off` should make policy ignored/strict in a visible,
    stable way.
- [ ] Surface strict routing metadata in explain output.
  - Include the query cost class.
  - Include empty/unavailable estimates without implying routing was cost-based.
- [ ] Extend bench output to preserve cost class, strict strategy, selected
  strategy, routing policy, and native lowering mode.
  - This supports later calibration and strategy-flip detection.
  - Keep schema changes additive for existing v2 report consumers.
- [ ] Extend sweep manifests/summaries to expose routing-policy metadata when
  benchmark reports contain it.
  - Preserve profile/density/transport/corpus labels.
  - Preserve memory summary and detail manifest links.
- [ ] Add targeted comments only for non-obvious interactions, especially why
  `force_supported` ignores cost routing and why `shadow` is not mixed with cost
  routing yet.

## Validation tasks

- [ ] Unit-test env/request override parsing.
- [ ] Table-test classification for selector, rate, range rate, histogram,
  aggregation, vector match, and subquery.
- [ ] HTTP-test the new headers for `strict`, `force_supported`, `off`, and
  invalid/unsupported routing policy inputs.
- [ ] Explain JSON tests verify the `routing` object appears without changing
  the Prometheus response envelope.
- [ ] Run:

  ```bash
  go test ./internal/promshim/...
  go test ./internal/promshim/httpapi ./internal/promshim
  ```

- [ ] Run a dry sweep and, when isolated benchmark data is available, a small
  bench/report generation path. Then verify cost class, routing policy, strict
  strategy, and selected strategy fields are present:

  ```bash
  ./scripts/run-sweep.sh --dry-run --estimate --name cost-routing-report-contract
  ./scripts/run-sweep.sh \
    --name cost-routing-report-contract-live \
    --profile 7d \
    --density sparse \
    --seed reuse \
    --skip-compliance \
    --shim-modes prefer \
    --corpus-set native \
    --memory summary
  ```

## Compatibility and migration notes

- Default behavior must be byte/result compatible with current strict routing.
- Existing clients may ignore the new headers and explain fields.
- Header names and explain field names become public debugging contracts; keep
  names stable once introduced.
- Bench report and sweep manifest schema changes should be additive.

## Exit criteria

- [ ] `strict` behavior is byte/result compatible with current behavior.
- [ ] Every query can explain which strict strategy it selected.
- [ ] Every query can report a stable query cost class.
- [ ] Bench output and sweep manifests capture enough strategy/class/routing
  policy data for calibration.
- [ ] No lower-tier coverage or fallback feature surface was expanded.

## Handoff to next file

After this file, [`02-calibration-and-estimates.md`](02-calibration-and-estimates.md)
can rely on stable routing policy parsing, cost class labels, strict/selected
strategy reporting, sweep manifest metadata, and explain/header surfaces for
estimates.
