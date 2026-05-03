# JIT recording-rule expansion plan

## Purpose

Some existing dashboards and alerts use Prometheus recording-rule names. In a
ClickHouse-only metrics store, blindly evaluating and remote-writing every
recording rule on a fixed interval recreates the always-on background work this
project is trying to avoid.

The default strategy should be:

> Keep a registry of recording rules, but do not materialize them by default.
> When a query references a recorded metric name, expand the rule expression just
> in time and let promshim's normal tier-1/tier-2/CBE routing execute it.

Materialization remains available later for rules that are repeatedly used,
expensive, alert-critical, or queried as historical range vectors.

## Goals

- Preserve compatibility for dashboards and alerts that refer to recorded metric
  names.
- Avoid continuous recording-rule computation for dashboard-only or rarely used
  rules.
- Keep ClickHouse as the only metrics store by default; do not require a
  Prometheus TSDB or full Thanos stack for recorded series.
- Make virtual recording-rule use visible in explain output, metrics, and
  harness reports.
- Keep tier-2 coverage pressure intact: expanded expressions should still route
  through native SQL when supported.
- Support future selective materialization without changing the query-facing
  contract.

## Non-goals

- Do not implement a full Prometheus rule engine in the first pass.
- Do not remote-write all recording rules by default.
- Do not generate ordinary insert-triggered ClickHouse materialized views for
  arbitrary PromQL rules; Prometheus recording rules are scheduled instant-query
  evaluations, not simple row-on-insert transforms.
- Do not claim exact materialized recording-rule time-series semantics for
  unmaterialized rules. Virtual historical expansion is allowed, but it must be
  identified as virtual expression semantics.
- Do not expand tier 3/4 feature coverage. JIT expansion should feed the existing
  planner and preserve the tier hierarchy.

## Background

A recording rule:

```yaml
- record: job:http_requests:rate5m
  expr: sum by (job) (rate(http_requests_total[5m]))
  labels:
    source: rules
```

means that at each rule evaluation timestamp Prometheus evaluates `expr`, sets
`__name__` to `job:http_requests:rate5m`, applies the rule labels, and writes the
resulting samples back into the TSDB.

A dashboard that queries:

```promql
job:http_requests:rate5m{job="api", source="rules"}
```

is therefore querying a named materialized expression. In promshim, the first
implementation should treat that name as a virtual expression:

```promql
sum by (job) (rate(http_requests_total[5m]))
```

with the recorded metric name and rule labels applied to the result.

## Core model

Introduce a recording-rule registry:

```go
type RecordingRule struct {
    Name        string
    Expr        parser.Expr
    ExprString  string
    Labels      labels.Labels
    GroupName   string
    GroupLabels labels.Labels
    Interval    time.Duration
    QueryOffset time.Duration
    Source      string // file path, CRD namespace/name, generated bundle, ...
}
```

Index by recorded metric name, with enough metadata to resolve conflicts and
explain provenance.

Queries then go through an expansion pass before normal planning:

```text
parse PromQL
  -> expand virtual recording-rule selectors
  -> build logical plan
  -> optimize/analyze
  -> strict or CBE routing
  -> execute
```

The expansion pass should be behavior-preserving for instant-vector use and
explicit about virtual historical expansion versus materialized recording-rule
history.

## Rule sources

### Phase-1 source: rendered rule files

Start by loading Prometheus rule YAML files from disk using upstream
`github.com/prometheus/prometheus/model/rulefmt`.

Why first:

- Prometheus Operator and Thanos Ruler already render `PrometheusRule` objects
  into rule files for their pods.
- Avoids adding Kubernetes client dependencies to promshim immediately.
- Works in the harness and local development without a cluster.
- Uses upstream rule validation structures.

Configuration:

```text
PROM_SHIM_RECORDING_RULE_FILES=/etc/promshim/rules/*.yaml,/etc/promshim/rules/*.yml
PROM_SHIM_RECORDING_RULE_RELOAD_INTERVAL_SECONDS=30
PROM_SHIM_RECORDING_RULE_MODE=off|virtual
```

Default: `off` until the feature is validated.

### Phase-2 source: PrometheusRule CRDs

Add a small sidecar or optional controller integration that watches
`PrometheusRule` objects and writes the same rendered rule file format consumed
by Phase 1.

Prefer a file-producing sidecar over embedding a Kubernetes client in promshim
unless direct API watching becomes clearly necessary. That keeps promshim usable
outside Kubernetes and keeps the query process less privileged.

### Phase-3 source: direct CRD watch, if needed

Only add a native Kubernetes watcher if file-based ingestion is operationally
awkward. If added, keep it optional and behind a separate build/config path.

## Conflict handling

Recording rules can collide. Treat this explicitly.

| Shape | Behavior |
|---|---|
| Same `record`, same expression, same labels | Deduplicate and keep one registry entry. |
| Same `record`, different static labels that make selectors disjoint | Allow as multiple candidates; select by static-label matchers. |
| Same `record`, different expression with overlapping labels | Mark conflict; do not expand; return an explicit query error when selected. |
| Same `record`, no selector can disambiguate | Error with source locations. |
| Invalid rule expression | Reject the rule at load time and expose load error metrics. |

The safe initial implementation can reject all same-name/different-expression
rules, then relax to disjoint-label support later.

## Instant selector expansion semantics

### Input pattern

A vector selector whose metric name matches a recording rule:

```promql
recorded_metric{...}
```

is replaced by a virtual recording-rule expression.

### Output label semantics

The virtual result must model Prometheus recording-rule output labels:

1. evaluate the rule expression;
2. preserve expression result labels;
3. set `__name__` to the recording rule name;
4. apply group labels and rule labels, with rule-level labels taking precedence
   over group labels where Prometheus semantics require it;
5. apply the selector matchers from the original query to the virtual output.

The first version should prefer correctness over pushdown:

- expand and evaluate the rule expression;
- apply record-name/static labels as a post-expression logical wrapper;
- filter the final virtual output by the original selector matchers.

Later optimization can push safe matchers into the child expression using label
lineage.

### Why not generate PromQL strings

It is possible to emit awkward PromQL using `label_replace`, `label_join`, and
extra filters, but that makes semantics hard to audit and may pessimize native
lowering.

Prefer a logical node:

```go
type RecordingRulePlan struct {
    RecordName string
    Labels     labels.Labels
    Matchers   []*labels.Matcher
    Child      logical.Node
    Source     RecordingRuleSource
}
```

Then teach local/native planning to apply the output-label mutation and final
matcher filter.

## Matcher handling

Original selector matchers apply to the virtual recorded series.

Example:

```promql
job:http_requests:rate5m{job="api", source="rules"}
```

Matcher categories:

1. `__name__` matchers select the rule itself.
2. Static rule-label matchers can be resolved before query execution:
   - `source="rules"` matches the rule above;
   - `source="other"` returns an empty vector/matrix without querying.
3. Matchers on labels produced by the child expression can be applied after
   expansion. Pushdown is optional and should wait for lineage-safe support.
4. Matchers on labels that cannot exist after static labels and child lineage are
   known may short-circuit to empty.

Initial rule:

> Apply all non-`__name__` matchers after the virtual labels are constructed.
> Add lineage-based pushdown only as a later tier-2 optimization.

## Supported query positions

### Instant-vector positions

Support recorded metrics where a vector selector is used as an instant vector:

```promql
recorded_metric
sum(recorded_metric)
recorded_metric > 10
recorded_metric / another_recorded_metric
label_replace(recorded_metric, ...)
```

The expansion pass can replace the selector wherever it appears in the AST or
logical tree, then normal promshim planning handles the surrounding expression.
This includes `/api/v1/query_range` for instant-vector expressions: the virtual
rule expression is evaluated at the requested range steps rather than reading a
pre-existing materialized recorded series.

### Historical virtual expansion

It is not necessary to error just because a query asks for historical values of a
virtual rule. For the ClickHouse-only model, the useful default is often to
recompute the recording-rule expression over the requested time range. This can
be more accurate than a precomputed Prometheus recording series when late data or
missed rule evaluations exist, because it evaluates from the underlying samples
at query time.

This is a deliberate semantic distinction:

- **Materialized Prometheus rule history** means "read the samples the rule
  evaluator happened to write at its evaluation interval".
- **Virtual rule history** means "evaluate the rule expression for the requested
  timestamps from the underlying source data".

Promshim should support virtual history, but explain it clearly and cap it.

### Range selectors over virtual rules

A recorded metric used as a range vector can be expanded to a subquery over the
rule expression:

```promql
recorded_metric[1h]
```

conceptually becomes:

```promql
(<recording_rule_expr>)[1h:<rule_interval>]
```

with output labels set to the recorded metric at each subquery step. Expressions
such as these can therefore be supported under virtual semantics:

```promql
rate(recorded_metric[1h])
avg_over_time(recorded_metric[30m])
```

This must be capped by:

- maximum generated subquery steps;
- maximum lookback duration;
- known rule interval;
- cost model estimate;
- no ambiguous/conflicting rule definitions;
- native-only support if `force_supported` is requested.

If the expansion is over caps or the rule has no safe interval, return a clear
materialization-needed error instead of silently running an unbounded query.

### Materialization candidates

If virtual expansion is too expensive or a rule is queried often as a range
vector, mark it as a materialization candidate instead of expanding
indefinitely.

## Integration with the planner

### Preferred insertion point

Add expansion after PromQL parsing and before `BuildLogicalPlan` /
`logical.ToLogical`.

Reasons:

- one implementation covers native and local execution;
- existing logical/native analysis sees the expanded expression;
- `force_supported` remains meaningful because the expanded expression must
  still lower natively;
- explain output can include both the original query and expansion metadata.

### Alternative insertion point

If AST rewriting becomes too lossy for label semantics, introduce logical nodes
instead:

- parse original expression;
- build logical plan with `RecordingRulePlan` nodes for matching selectors;
- lower those nodes to native/local wrappers.

Plan recommendation:

1. implement a small AST discovery pass first to detect selectors and range-use;
2. implement logical nodes for actual output-label semantics;
3. avoid string-only PromQL expansion except in debug/explain output.

## Native SQL lowering

The expanded rule expression should remain tier-2-first.

Native renderer responsibilities:

- lower the child expression as usual;
- apply output-label mutation:
  - set `__name__` to the record name;
  - merge group/rule labels;
  - overwrite labels according to Prometheus rule semantics;
- apply final matchers against the virtual label set;
- preserve output type and timestamps;
- include explain metadata showing virtual-rule expansion.

Local executor responsibilities mirror the same behavior for fallback/shadow.

Because tier 2 already covers the targeted PromQL surface, the normal case should
still produce a native SQL root after expansion in `force_supported` mode.

## Explain output

Add expansion metadata to explain responses:

```json
{
  "recordingRules": [
    {
      "record": "job:http_requests:rate5m",
      "source": "namespace/name:group",
      "materialized": false,
      "mode": "instant_virtual",
      "expr": "sum by (job) (rate(http_requests_total[5m]))",
      "labels": {"source": "rules"},
      "rangeExpansion": false
    }
  ]
}
```

For over-cap or unsafe virtual-history requests, include the rule and
materialization-needed reason in the Prometheus error message.

## Metrics

Expose process-local metrics:

```text
promshim_recording_rule_registry_loaded_total{source}
promshim_recording_rule_registry_errors_total{source,reason}
promshim_recording_rule_definitions{state}
promshim_recording_rule_expansions_total{record,mode}
promshim_recording_rule_expansion_errors_total{record,reason}
promshim_recording_rule_materialization_candidates_total{record,reason}
promshim_recording_rule_query_duration_seconds{record,mode}
```

Cardinality note: `record` labels are bounded by rule count, but if that becomes
too large, switch to a short hash and expose a separate debug endpoint for the
record-name mapping.

## Query API and metadata behavior

### Query endpoints

`/api/v1/query` and `/api/v1/query_range` should support virtual recording-rule
expansion according to the phase rules above.

### Series/labels metadata endpoints

There are two possible semantics:

1. show only physically stored ClickHouse series;
2. include virtual recorded metrics from the registry.

Initial recommendation:

- keep `/api/v1/series`, `/api/v1/labels`, and `/api/v1/label/{name}/values`
  physical-only by default;
- add `include_virtual_rules=1` later if Grafana/dashboard variable workflows
  need virtual recorded metrics to appear in metadata calls.

Reason: virtual metadata is surprisingly subtle because label values may depend
on evaluating the rule expression, not just static registry data.

## Materialization policy

JIT expansion should collect evidence for selective materialization.

A rule becomes a materialization candidate when any of these are true:

- used as a range vector and virtual range expansion is disabled or over caps;
- queried frequently;
- expensive under native SQL or local fallback;
- output is much lower cardinality than input;
- alert-critical and latency-sensitive;
- shared by many dashboards/alerts;
- repeated expansion dominates ClickHouse profile counters.

Candidate handling:

1. emit a metric;
2. include it in a report/plan artifact;
3. optionally generate a suggested scheduled materialization definition.

## Future materialization implementation

Prefer scheduled materialization over ordinary insert-triggered materialized
views.

### Scheduled `INSERT SELECT`

For selected rules:

```text
every rule interval:
  render tier-2 instant SQL for expr at eval_time
  wrap output labels with record name and rule labels
  write samples to ClickHouse via remote-write or direct TimeSeries-compatible insert
```

Remote-write is preferred first because it reuses the ClickHouse `TimeSeries`
identity/tag path and avoids depending on inner-table internals.

### Refreshable materialized views

ClickHouse refreshable materialized views may be useful for stable, independent
rules, but use them only after validating:

- exact evaluation timestamp alignment;
- late/out-of-order sample behavior;
- staleness semantics;
- rule dependencies;
- replica behavior;
- refresh failure visibility.

### Ordinary insert-triggered materialized views

Do not use ordinary MVs for arbitrary PromQL recording rules. They may be useful
for simple ingestion-time rollups, but they do not naturally model Prometheus's
scheduled evaluation semantics, lookback windows, staleness, offsets, and
subqueries.

## Interaction with Thanos Ruler and alerting

Thanos Ruler remains useful for alerting because it can select `PrometheusRule`
objects and send alerts to Alertmanager while querying promshim.

With virtual recording rules:

- Thanos Ruler alert expressions that reference recorded metric names can query
  promshim and get JIT expansion.
- Thanos Ruler does not have to remote-write recording rule outputs by default.
- Recording rules can remain compatibility metadata until measurements justify
  materialization.

Caveat: if an alert uses a recorded metric as a range vector, promshim will use
virtual range expansion when it is under caps. If expansion is over caps or the
rule is ambiguous, the rule should be materialized or rewritten before rollout.

## Implementation phases

### Phase 1. Rule registry and file loading

Goal: load recording rules without affecting query behavior.

Tasks:

1. Add `internal/promshim/rules/` with registry types.
2. Parse rule YAML files with upstream `rulefmt`.
3. Store recording rules only; ignore alerting rules for expansion.
4. Validate expressions with the upstream PromQL parser.
5. Detect duplicate/conflicting records.
6. Add env/config options for files, reload interval, and mode.
7. Add registry metrics and a debug/explain-safe summary.

Validation:

- unit tests for file parsing, reload, invalid rules, duplicates, and conflicts;
- `go test ./internal/promshim/rules`;
- no behavior changes when `PROM_SHIM_RECORDING_RULE_MODE=off`.

### Phase 2. Detection and expansion planning

Goal: detect queries that reference virtual rules and decide whether they can be
expanded immediately, need bounded virtual-history expansion, or require
materialization.

Tasks:

1. Walk parsed PromQL AST and find vector selectors whose metric name matches a
   registry entry.
2. Detect whether the selector is used as an instant vector, a `/query_range`
   instant expression, or a matrix/range selector.
3. For matrix/range use, compute the virtual subquery shape and cap estimate;
   return a materialization-needed error only when expansion is unsafe or over
   caps.
4. Add explain metadata showing matched virtual rules, planned expansion mode,
   and cap/materialization reasons without changing results yet.

Validation:

- unit tests for selector detection in roots, aggregations, binary expressions,
  functions, range selectors, and subqueries;
- tests for virtual range expansion planning and over-cap rejection;
- HTTP tests for materialization-needed error shape.

### Phase 3. JIT expansion for instant and range-query contexts

Goal: support recorded metric names in instant-vector contexts, including
`/api/v1/query_range` evaluations of instant expressions.

Tasks:

1. Add a logical `RecordingRulePlan` or equivalent wrapper.
2. Evaluate/lower the child rule expression through the existing planner.
3. Apply record name and static labels to output labels.
4. Apply original selector matchers to the virtual output.
5. Support multiple recorded metrics in one query.
6. Add explain metadata with original and expanded expressions.

Validation:

- local executor tests for label semantics and matcher filtering;
- native renderer tests for output-label mutation and final matcher filters;
- integration tests:
  - `recorded_metric`
  - `sum(recorded_metric)`
  - `recorded_metric > 0`
  - `recorded_metric / another_recorded_metric`
  - static label match and mismatch;
- `force_supported` test proves expanded expressions still produce native roots.

### Phase 4. Harness support and dashboard compatibility corpus

Goal: make virtual recording rules measurable and regression-tested.

Tasks:

1. Add a small rule file fixture under `harness/`.
2. Extend the compare harness to mount/pass rule files to promshim.
3. Add corpus rows for common dashboard recorded-metric usage.
4. Add corpus rows for query-range virtual history and negative rows only for
   unsafe/over-cap range-vector expansion.
5. Add report fields for `recordingRuleExpanded=true` and expansion mode.

Validation:

```bash
go test ./internal/promshim/...
./scripts/run-harness.sh --corpus recording-rules-virtual.json --subjects shim
./scripts/run-compliance.sh --ready-timeout 120
```

### Phase 5. Usage telemetry and materialization candidate report

Goal: learn which rules should be materialized.

Tasks:

1. Count rule expansion frequency by record.
2. Capture duration and selected strategy for expanded queries.
3. Mark over-cap or repeatedly expensive virtual-history expansions as
   materialization candidates.
4. Add a command/report:

```bash
go run ./cmd/promshim-recording-rules-report \
  --rules <rules.yaml> \
  --usage harness/artifacts/recording-rule-usage.json \
  --out .pi/recording-rule-materialization-candidates.md
```

5. Include candidate reason, estimated cost, output cardinality, and consumers
   when known.

Validation:

- unit tests for candidate classification;
- report generation from synthetic usage artifacts.

### Phase 6. Bounded virtual range-selector expansion

Goal: support recorded metrics inside PromQL matrix/range selectors without
materialization when the generated virtual history is safely bounded.

Tasks:

1. Rewrite `recorded_metric[range]` to a subquery over the rule expression using
   the rule group interval.
2. Apply caps for generated steps, range, and cost.
3. Preserve output labels at every generated step.
4. Reject ambiguous/conflicting rules.
5. Expose `rangeExpansion=true` in explain output.

Validation:

- differential tests against a materialized fixture where possible;
- native-only tests for expanded subquery roots;
- performance tests to confirm caps avoid pathological expansions.

### Phase 7. Selective scheduled materialization

Goal: materialize only rules proven worthwhile.

Tasks:

1. Generate native SQL for selected recording rules in `force_supported` mode.
2. Wrap results into recorded metric samples.
3. Write through ClickHouse remote-write first.
4. Add idempotency/duplicate-sample handling tests.
5. Validate against Thanos Ruler or Prometheus output on the same fixture.
6. Optionally evaluate ClickHouse refreshable materialized views for stable
   rules after scheduled `INSERT SELECT` works.

Validation:

- generated SQL explain/profile artifacts;
- output parity against a known Prometheus recording-rule evaluation;
- ingestion delay and duplicate write tests;
- dashboard query latency before/after materialization.

## Validation ladder

Fast unit loop:

```bash
go test ./internal/promshim/rules ./internal/promshim/logical ./internal/promshim/native/renderer
```

Promshim package loop:

```bash
go test ./internal/promshim/...
```

Virtual-rule harness:

```bash
./scripts/run-harness.sh --corpus recording-rules-virtual.json --subjects shim
```

Compliance guard:

```bash
./scripts/run-compliance.sh --ready-timeout 120
```

Native-only guard for expanded expressions:

```bash
./scripts/run-harness.sh \
  --corpus recording-rules-virtual.json \
  --subjects shim \
  --native-only
```

Performance/materialization evaluation:

```bash
./scripts/ch-profile-capture.sh --matrix --baseline /tmp/no-bench-baseline.json
./scripts/ch-profile-capture.sh --baseline /tmp/no-bench-baseline.json --long-range all --repeats 3 --warmup 1
```

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| JIT expansion differs from Prometheus recording-rule label semantics | model output labels in one shared wrapper; compare against Prometheus recording-rule fixtures |
| Virtual history is mistaken for materialized Prometheus rule history | expose expansion mode in explain/metrics; document virtual semantics; materialize rules that require exact written-sample history |
| Duplicate rule names create ambiguous queries | conflict detection; explicit errors with source locations |
| Expansion hides tier-2 gaps | run virtual-rule corpus in `--native-only`; keep `force_supported` strict |
| Query performance regresses for expensive rules | metrics and candidate report; materialize only measured hot rules |
| Metadata endpoints mislead Grafana variables | keep physical-only initially; add `include_virtual_rules=1` later if needed |
| Rule reload changes query semantics mid-request | atomic registry snapshots; include registry generation in explain output |
| Materialization reintroduces always-on work | make it opt-in per measured candidate, not default |

## Definition of done

The JIT recording-rule plan is complete when:

- promshim can load and validate recording rules from rendered rule files;
- recorded metric names can be used in instant-vector query contexts without
  precomputing or storing their outputs;
- output labels and matchers match Prometheus recording-rule semantics;
- historical use is handled by virtual range/query expansion when safely bounded,
  or rejected with a materialization-needed error when not;
- explain output and metrics make expansions visible;
- a harness corpus validates virtual recording-rule behavior;
- materialization candidates are reported from actual usage and cost evidence;
- no blanket recording-rule remote-write loop is required for dashboard
  compatibility.

## Current status (this branch)

**Completed:** Phases 1-3 are largely implemented; PR A has now completed the branch-close work for phases 4 and 6.

**Remaining by phase:**
- **Phase 4:** complete
- **Phase 5:** not started
- **Phase 6:** complete for telemetry, bounded-range semantics, and harness/error plumbing
- **Phase 7:** not started

## Stacked PR completion plan

### PR A — Finish Phases 4 & 6 (close loop, compatibility-safe)
**Scope:** finalize virtual recording-rule harness coverage and bounded-range behavior.
- [x] Add harness fixtures for virtual recording-rule inputs (rules file + corpus rows).
- [x] Mount/pass recording-rule files in harness and add `recording-rules-virtual.json` corpus.
- [x] Add corpus entries for:
  - [x] instant selectors (match/mismatch)
  - [x] `query_range` over instant expressions
  - [x] range-selector expansion within bounds
  - [x] intentional over-cap / unsafe rejection cases
- [x] Extend harness artifact schema to include:
  - [x] `recordingRuleExpanded`
  - [x] `recordingRuleMode`
  - [x] `recordingRuleRangeExpansion`
  - [x] `recordingRuleRejectionReason`
- [x] Add explain/response assertions in service-level tests for rejection reasons.
- [x] Update docs for metadata semantics and current virtual-history behavior.

### PR B — Finish Phase 5 (telemetry + candidate evidence)
**Scope:** production-safe observability before materialization.
- [ ] Add metrics:
  - [ ] `promshim_recording_rule_expansions_total{record,mode}`
  - [ ] `promshim_recording_rule_expansion_errors_total{record,reason}`
  - [ ] `promshim_recording_rule_query_duration_seconds{record,mode}`
- [ ] Track candidate signals (over-cap, frequency threshold, runtime duration).
- [ ] Define and implement candidate selection policy + output report command.
- [ ] Add unit tests for candidate classification and empty/empty-state handling.
- [ ] Add report golden output checks in CI-local tests.

### PR C — Implement Phase 7 (selective scheduled materialization)
**Scope:** opt-in materialization path only.
- [ ] Add scheduler/runner that consumes the candidate report and evaluates selected rules.
- [ ] Generate and execute materialization SQL per rule at configured eval intervals.
- [ ] Add write path (remote-write first; direct insert optional).
- [ ] Add idempotency and retry semantics (dedupe/ordering/error visibility).
- [ ] Add opt-in controls:
  - [ ] allowlist/denylist
  - [ ] per-rule intervals
  - [ ] failure handling policy
- [ ] Add parity/validation tests for materialized vs virtual output where practical.
- [ ] Add operational docs (recovery, backfill, monitoring, disable switch).
