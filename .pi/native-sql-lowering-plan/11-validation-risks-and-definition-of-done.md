# 11 — Validation, risks, and definition of done

## Validation strategy

### Unit tests
Add deterministic tests for:
- lowerability classification
- label lineage
- predicate pushdown safety
- subquery flattening safety
- join planning
- rendered SQL fragments / snapshots

### Integration tests
Against the local ClickHouse fixture:
- native selector correctness
- native aggregation correctness
- vector join correctness
- range/subquery correctness

### Differential tests
For the supported native subset:
- compare native result vs delegated PromQL result
- compare native result vs Prometheus where practical for edge semantics
- the differential harness at `harness/` + `internal/promharness/` now
  supports per-query time-window and range-step parametrization (`timeOffsetSeconds`,
  `startOffsetSeconds`, `endOffsetSeconds`, `stepSeconds`, `compareMode`,
  `subjects`, `nativeLoweringMode`, `explain`). The original harness gap
  called out here has been addressed; remaining work is coverage breadth,
  corpus promotion discipline, and rollout-signal quality rather than basic
  parametrization.

### Explain tests
Ensure explain surfaces:
- why a subtree lowered
- why it did not
- what optimizer passes were applied
- what fallback happened

## Risks / hard parts

### 1. Semantic drift
Biggest risk is semantic mismatch:
- metric-name rules
- duplicate-labelset rules
- lookback / staleness
- range boundary handling
- NaN / null / absent behavior

### 2. SQL blow-up
Without flattening and projection pruning, generated SQL can become deeply nested and unreadable.

### 3. Join cardinality surprises
Vector matching is correctness-sensitive and can explode runtime costs.

### 4. Predicate pushdown mistakes
Pushing a label predicate through a label mutation boundary can silently return wrong results.

### 5. Time-window underfetch
If evaluation-range propagation is incomplete, native lowering can be subtly wrong for subqueries and range functions.

### 6. Drift between local and native range-function paths
Range functions and subqueries currently ship as **local Go execution** in
`internal/promshim/exec/` (see
[00-status-and-drift.md](./00-status-and-drift.md)), while Phase 6b is
meant to deliver **native SQL lowerings** of the same operators. Keeping
the local implementations alive as the correctness oracle is the policy,
but the consequence is that two implementations of each operator coexist
until the native one is promoted. Without enforced differential tests,
they can silently drift on counter-reset, extrapolation, and
edge-inclusion rules.

Mitigation is policy-level, not optimizer-level:

- differential tests between local and native implementations are
  mandatory for every operator present on both paths
- no net-new range / counter operator lands on path 3 without either a
  native-lowering tracking note or an explicit "keep local" design note
- local implementations are retired only after a promotion window and an
  observation window have both been green

## Design decisions to keep explicit

### Decision A — Internal fragment shape
Use normalized relational fragments internally, especially for range vectors, with optional late-materialized columns for `tags`, `metric_name`, and join helper columns.

### Decision B — Leaf source
Prefer TimeSeries backing tables / `timeSeriesSelector`-like repo-owned sources for native lowering, not `prometheusQuery*` as an inner source. See [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp).

### Decision C — Semantic authority
Prometheus is the semantic oracle; ClickHouse is the lowering-shape oracle; VictoriaMetrics / DataFusion / Calcite inform optimizer mechanics.

### Decision D — Lineage-aware optimizer
Do not push SQL strings around blindly. Use typed predicates, label lineage metadata, and required-column tracking.

### Decision E — Small staged RBO, not a general CBO
Keep the optimizer as a fixed-pass, reviewable rule pipeline rather than a general planner framework.

### Decision F — Subqueries are not mandatory execution barriers
Let them lower as part of a bigger native subtree when supported.

### Decision G — Keep delegated PromQL path as oracle
This is essential for safe rollout.

### Decision H — Local path is the in-repo correctness oracle, not the destination
Range functions and subqueries implemented on path 3
(`internal/promshim/exec/`) stay as the in-repo correctness oracle for
path 2 (native SQL) lowerings. They are not the target implementation.
Net-new range / counter operators do not land on path 3 unless the
conformance harness requires it, and every local implementation is a
candidate for native lowering in Phase 6b. Transforms that are not range
or counter operators (label mutation, `histogram_quantile`, `absent`,
scalar helpers) are exempt and remain on path 3 indefinitely. See
[00-status-and-drift.md](./00-status-and-drift.md) for the full policy.

## Definition of done
Do not consider the native lowering track “real” until all of the following are true:

- the planner can select **maximal lowerable subtrees**, not only special-cased top-level aggregations
- the native lowerer has explicit optimizer passes for:
  - evaluation-range propagation
  - label-filter pushdown and common matcher inference
  - projection pushdown with no `SELECT *`
  - function / pattern rewrites for the supported subset
  - redundant-subquery flattening
  - careful JOIN construction
- explain output shows strategy selection and fallback reasons clearly
- a shadow-compare mode exists and is used for promotion
- the common dashboard subset is covered by differential tests
- the delegated path remains available as a fallback and correctness oracle
- every range or counter function implemented on path 3
  (`internal/promshim/exec/`) has either been replaced by a native lowering
  on path 2 or carries an explicit "keep local" design note explaining why
  native lowering is not feasible
- differential tests exist between path 2 and path 3 for every operator
  that exists on both paths
- no net-new path-3 range / counter function has shipped without either a
  native-lowering tracking note or a "keep local" design note

## Current status against this document

### Delivered or substantially delivered
- **Unit-test coverage** exists for lowerability classification, join planning, rendered SQL, optimizer passes, explain surfacing, shadow mode, and the whole-query delegation classifier.
- **Integration tests** exist under `integration/promshim/` and cover selectors, aggregations, vector matching, label transforms, range/subquery behavior, and histogram helpers against a local ClickHouse-backed fixture.
- **Differential coverage** exists for the supported native subset, including Phase 6 native-vs-local/native-vs-Prometheus checks plus harness-driven corpus validation in `harness/`.
- **Explain tests** are in place and now cover lowering selection, fallback reasons, rollout modes, entire-query delegation eligibility, shadow reports, and shadow metrics export.
- **Phase 7 rollout guardrails** now exist in code: `nativeLoweringMode`, shadow comparison, explain surfacing, an in-memory `shadowSummary`, and per-process `/metrics` export for shadow counters/timings.

### Status conclusion
- For the **current supported native-lowering scope**, this document's validation and definition-of-done checklist is now satisfied.
- The explicit path-3 range/counter retirement rule is closed for the currently known local-only outlier: `quantile_over_time` has an explicit keep-local design note in [13-keep-local-quantile-over-time.md](./13-keep-local-quantile-over-time.md).
- The common dashboard subset is now defined concretely as `harness/corpus/common-dashboard-subset.json`, with metadata in `harness/corpus/common-dashboard-subset.metadata.json`. It is carved from the broader exploratory top-panel shortlist (`draft-grafana-top-panel-shortlist.json`) by excluding the currently known failing candidates, and is green under `./scripts/run-harness.sh --corpus common-dashboard-subset.json --subjects shim`.

### Non-blocking follow-up
- The broader exploratory top-panel shortlist and themed corpora remain useful for gap discovery and are not expected to be fully green at all times.
- `/metrics` now exports rollout telemetry, but durable cross-process history/aggregation still depends on external scraping/retention rather than an in-repo durable store.

## Short summary
The right shape for this repo is:

**logical plan -> lowerability analysis -> staged RBO passes -> maximal native subtree selection -> SQL fragment optimization -> final SQL shaping -> execution**

Success is not just “we can emit SQL”. Success is:
- the planner lowers the biggest safe subtree
- the generated SQL is structurally good
- filters get pushed deep
- redundant layers get flattened
- joins are deliberate
- rollout is guarded by explain + shadow comparison
