# PromQL Harness Coverage Improvement Plan

## Goal

Improve confidence in promshim PromQL compatibility by moving from a curated passing subset to an explicit, reviewable coverage model that answers:

1. what query families are implemented,
2. which of those are protected by the differential harness,
3. which are only covered by unit/integration tests,
4. which are intentionally unsupported, and
5. how much of the real `monitoring-v2` and harvested external dashboard query surface is actually exercised.

The differential harness should remain the acceptance gate for supported query-behavior changes, but it should cover more of the implemented surface and be tied to real dashboard/alert/query usage instead of only synthetic hand-picked examples.

## Constraints and non-goals

### Preserve
- Differential Prometheus-vs-promshim comparison as the primary parity oracle for supported success cases.
- Explicit unsupported boundaries where parity is not yet trustworthy.
- Current HTTP API contract for `/api/v1/query` and `/api/v1/query_range`.
- Deterministic seeding and reproducible harness runs.

### Non-goals
- Do **not** chase full PromQL completeness in one pass.
- Do **not** silently approximate unsupported semantics just to widen coverage numbers.
- Do **not** fold native histogram sample-type implementation into this plan; classic-bucket coverage remains the current scope.
- Do **not** treat unit/integration coverage as a replacement for differential harness parity for query behavior.

## Current snapshot

### Harness status today
- Stable corpus: `82` passing queries.
- Endpoints covered:
  - `49` instant `/api/v1/query`
  - `33` range `/api/v1/query_range`
- Differential oracle:
  - Prometheus direct query result
  - promshim result over ClickHouse `TimeSeries`
- Normalization compares:
  - `resultType`
  - labels
  - timestamps
  - values (including `NaN` handling)

### Stable families currently covered
- selectors and basic matchers
- `offset`
- selected `@ start()` / `@ end()` handling
- scalar math
- aggregation subset (`sum`, `count`, `avg`, `topk`, `bottomk`, `count_values`)
- vector-scalar and selected vector-vector expressions
- selected vector matching (`on`, `group_left`, `group_right`)
- set operators (`and`, `or`, `unless`)
- `label_join`, `label_replace`
- classic histogram quantile path
- local matrix/subquery path for:
  - `last_over_time`
  - `sum_over_time`
  - `avg_over_time`
  - `max_over_time`
  - `min_over_time`
  - `count_over_time`
- `absent`
- `absent_over_time`

### Explicitly excluded / unstable today
Documented exclusions remain outside the stable corpus because they are known divergence classes and should keep failing explicitly until parity exists:

- `rate(...[range:step])`
- `irate(...[range:step])`
- `increase(...[range:step])`
- `delta(...[range:step])`
- `idelta(...[range:step])`
- `deriv(...[range:step])`
- `changes(...[range:step])`

References:
- `harness/README.md`
- `.pi/phase9-delegated-divergence-catalog.md`
- `.pi/implementation-order-plan.md`

## Coverage status taxonomy

Use these statuses consistently in docs and future reviews:

- **Stable differential**
  - covered by `harness/corpus/queries.json` and required for parity gating.
- **Targeted probe / non-stable differential**
  - differential checks exist but are not yet promoted into the stable corpus.
- **Unit/integration only**
  - behavior has local tests but no Prometheus differential coverage.
- **Explicit unsupported**
  - analyzer/planner intentionally rejects the shape with a stable public error boundary.
- **Open / unknown**
  - not implemented, not validated enough, or not yet inventoried against real usage.

## Query-surface coverage matrix

| Query family | Current shim status | Current harness status | Other validation | Main gaps | Priority |
| --- | --- | --- | --- | --- | --- |
| Selectors, equality/regex matchers, `offset`, selected `@ start()` / `@ end()` rewrites | Implemented for supported subset | **Stable differential** for basic `=`, `=~`, `offset`, selected `@ start()`/`@ end()` | planner + service tests | no stable corpus rows for `!=` / `!~`; lookback/staleness boundary behavior still incomplete | High |
| Scalar expressions | Implemented | **Stable differential** | unit/service tests | low risk; keep as baseline | Low |
| Local aggregations (`sum`, `count`, `avg`, `min`, `max`, `topk`, `bottomk`, `count_values`) | Implemented subset | **Stable differential** for `sum`, `count`, `avg`, `topk`, `bottomk`, `count_values`; `min`/`max` are thin or absent in stable corpus | `exec/aggregate_test.go`, planner tests | missing stable rows for `min`, `max`, `without(...)`, and broader grouping shapes | High |
| Binary arithmetic/comparison (scalar-scalar, vector-scalar, vector-vector) | Implemented subset | **Stable differential** for selected `+`, `*`, unary `-`, `== bool` | local expression tests | thin/no stable coverage for `/`, `%`, `^`, `!=`, `>`, `<`, `>=`, `<=`; limited range equivalents | High |
| Vector matching (`on`, `ignoring`, `group_left`, `group_right`, fill modifiers) | Implemented subset | **Stable differential** for `on`, `group_left`, `group_right`; no stable fill-modifier rows; `ignoring(...)` not represented | vector matching tests, hard integration tests | add `ignoring(...)`; resolve fill-modifier oracle/doc mismatch; add error-path parity for cardinality failures | High |
| Set operators (`and`, `or`, `unless`) | Implemented | **Stable differential** | vector matching tests | add more label/matching variants and negative/error cases | Medium |
| Label mutation (`label_replace`, `label_join`) | Implemented | **Stable differential** | label mutation tests | duplicate-labelset collision parity not fully differentialized | Medium |
| Classic histogram functions (`histogram_quantile`, `histogram_count`, `histogram_sum`, `histogram_avg`) | Implemented for classic buckets | **Stable differential** only for `histogram_quantile` | planner + histogram executor tests | add stable rows for count/sum/avg; native histogram sample-type remains out of scope | High |
| Subqueries and matrix-consuming functions | Implemented for current local subset | **Stable differential** for current promoted subset | planner/range-function tests | full Prometheus-compatible subquery semantics not complete; rate-family-over-subquery remains intentionally unsupported | High |
| Absence and staleness-sensitive behavior (`absent`, `absent_over_time`, sparse/disappearing series) | `absent` family implemented; staleness edge behavior still open | **Stable differential** for core absent-family cases; extra probes exist in `harness/corpus/phase10-staleness-probes.json` | absent tests, phase 10 docs | promote more staleness/lookback boundary cases after parity verification | High |
| Error semantics (`unsupported`, `bad_data`, duplicate-labelset, invalid expression type`) | explicit error model exists | **No stable differential corpus for expected errors** | service/planner/unit tests | add expected-error harness mode so unsupported and bad-data behavior are regression-checked | High |
| Metadata/query-UX endpoints (`/labels`, `/label/{name}/values`, `/series`) | endpoints exist | **No differential harness coverage** | some service code/tests | add endpoint-specific differential checks because dashboard compatibility depends on them too | Medium |
| Real dashboard/alert/query inventory from `monitoring-v2` | Unknown | **No coverage accounting** | none in repo yet | biggest blind spot: current corpus is synthetic, not usage-derived | Highest |
| External/public dashboard query inventory (GitHub repos, Grafana.com dashboards) | Not harvested yet | **No coverage accounting** | none in repo yet | useful source of common PromQL shapes, but requires dedupe, datasource filtering, and semantic rewrite before stable use | High |

## Fixture/data coverage matrix

The current harness dataset is deterministic and useful, but it is still too narrow to expose many PromQL edge cases.

| Fixture pattern | Current status | Why it matters | Gap / next step |
| --- | --- | --- | --- |
| Regular cadence gauge/counter series | Present | baseline selector, aggregation, binary, range behavior | keep |
| Classic histogram bucket family | Present | latency dashboards and `histogram_quantile` | expand stable coverage to count/sum/avg |
| Sparse series | Present | empty-window and irregular-emission behavior | promote more stable cases |
| Disappearing series | Present | `absent_over_time` and boundary checks | promote late-window parity cases |
| Counter reset patterns | Missing | essential for rate-family correctness and reset-sensitive functions | add reset fixtures before any broader rate-family push |
| Irregular scrape intervals / jitter | Missing | exposes lookback and subquery boundary drift | add jittered timestamps fixture |
| NaN / `+Inf` / `-Inf` samples | Missing | PromQL semantics often diverge here | add focused fixture family |
| Duplicate-labelset collision scenarios | Missing in harness dataset | needed for Prometheus-style collision/error parity | add targeted collision fixture + expected-error harness cases |
| High-cardinality / wider match fan-out | Missing | needed for vector matching/cardinality/error behavior | add wider label fan-out family |
| Empty/missing label variations | Thin | selector and label mutation parity often depends on label presence | add cases with missing optional labels |
| Mixed histogram/non-histogram inputs | Missing in harness | needed for histogram projection edge behavior | add targeted mixed-input fixture |

## Corpus source model

Use three input buckets and three output layers.

### Input buckets
1. **Internal corpus**
   - extracted from `monitoring-v2` dashboards, variables, alerts, and recording rules.
   - highest priority because it reflects actual migration risk.
2. **GitHub dashboard harvest**
   - start intentionally small with a curated, diverse batch of roughly `20` repositories rather than a star-only top list.
   - prefer diversity across Kubernetes, node/container, app/latency, and common infra dashboards.
3. **Grafana.com dashboard harvest**
   - start with roughly `10` dashboards as a second public source of common PromQL idioms.

### Output layers
1. **Raw harvested corpus**
   - preserve original query text and source metadata.
2. **Unique normalized shape corpus**
   - dedupe by exact text and structural PromQL shape/fingerprint.
3. **Rewritten seeded candidate corpus**
   - keep only queries that can be faithfully retargeted to the harness dataset or to newly added fixtures.
   - this is the pool from which stable differential rows are promoted.

Do **not** treat all harvested queries as stable harness material. The stable corpus should remain much smaller than the harvested input and should only contain parity-verified, semantically preserved cases.

## Ordered plan

### Slice 0 — Normalize the coverage contract

#### Scope
- Add this plan file as the explicit source for harness coverage goals.
- Standardize the coverage taxonomy (`stable differential`, `targeted probe`, `unit/integration only`, `explicit unsupported`, `open/unknown`).
- Resolve the fill-modifier oracle ambiguity:
  - `harness/docker-compose.yml` enables `--enable-feature=promql-binop-fill-modifiers`
  - `.pi/implementation-order-plan.md` still says the harness Prometheus image does not cover fill modifiers
- Decide whether fill modifiers can become true differential cases in the stable corpus or must remain non-differential.

#### Validation
- Documentation review only.
- If fill-modifier status is clarified by code/config, update docs before new corpus promotion.

#### Risk
- If the oracle contract is ambiguous, future coverage claims will be misleading.

---

### Slice 1 — Build a real query inventory from `monitoring-v2`

#### Scope
Inventory the PromQL surface that actually matters in current usage.

Primary inputs:
- `~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/monitoring-v2/`
- `~/code/tradera/tradera-iac/setup/dev-dynamic/apps/monitoring-v2/`
- `~/code/tradera/helm-charts/charts/monitoring/`

Extract and classify queries from:
- Grafana dashboards/panels
- Grafana templating/variable queries
- alert rules
- recording rules
- any Prometheus datasource query snippets in chart values or manifests

For each discovered query, classify into:
- already stable differential covered
- implemented but not harnessed
- explicitly unsupported
- open/unknown

#### Deliverable
A concrete inventory summary, ideally grouped by family and frequency, so harness work can be prioritized by real usage instead of abstract language completeness.

#### Validation
- Spot-check sampled queries against source files.
- Ensure each inventory entry is mapped to a coverage status.

#### Risk
- Without this slice, the harness can only claim synthetic subset coverage, not practical dashboard/alert compatibility.

---

### Slice 1A — Harvest public dashboard/query sources and normalize them into candidate corpus

#### Scope
Add a public-query ingestion path that complements the internal `monitoring-v2` inventory without turning the stable harness into an uncurated dump.

Start with a deliberately small, reviewable batch:
- internal extracted corpus from `monitoring-v2`
- roughly `20` curated GitHub repositories with Grafana dashboards
- roughly `10` Grafana.com dashboards from `grafana.com/grafana/dashboards`

Selection guidance:
- prefer diversity over raw stars/popularity
- avoid over-sampling only Kubernetes/node-exporter packs
- include a mix of:
  - Kubernetes / kube-state / cluster dashboards
  - node / host / container dashboards
  - service / latency / RED-style dashboards
  - common infra dashboards such as Postgres, Redis, ingress, Kafka, JVM/Go services

For each harvested query, record at least:
- source kind (`internal`, `github`, `grafana.com`)
- source location (repo/file/path or dashboard URL/id)
- original query text
- datasource/type confidence
- normalized query fingerprint / structural shape
- query family classification
- rewrite status (`mechanically_retargetable`, `needs_fixture`, `explicit_unsupported`, `discarded`)

Extraction targets:
- panel target expressions
- Grafana templating/variable queries
- alert/recording rule expressions when present in the same source sets

Normalization and reduction rules:
1. dedupe by exact text,
2. dedupe by normalized PromQL shape,
3. filter out obvious non-Prometheus datasource queries,
4. keep provenance so later promoted rows can be traced back to the original source.

Rewrite policy:
- only retarget a harvested query to seeded harness data if the rewrite preserves the semantic class of the original query.
- acceptable examples:
  - `rate(http_requests_total[5m])` -> `rate(harness_requests_total[5m])`
  - `histogram_quantile(...)` over a public latency bucket metric -> same shape over `harness_request_duration_seconds_bucket`
- non-goal:
  - blindly renaming unrelated domain metrics in ways that destroy the purpose of the original query.

#### Deliverables
- a raw harvested query archive,
- a deduped normalized-shape corpus,
- a rewritten seeded candidate corpus,
- a summary of top candidate PromQL families that recur across internal and public sources.

#### Validation
- spot-check sampled queries against original sources,
- parse retained PromQL queries successfully where applicable,
- manually verify that rewritten candidates preserve the original semantic family,
- report counts for raw, deduped, rewritten, discarded, and needs-fixture buckets.

#### Risk
- popularity-only selection will over-sample a few common dashboard packs,
- raw public dashboards contain lots of duplicates, Grafana template syntax, and non-Prometheus datasource queries,
- aggressive rewrites can create false confidence by preserving syntax but not semantics.

---

### Slice 2 — Expand stable coverage for already-implemented semantics

#### Scope
Promote low-risk, high-value additions where implementation already exists and the main missing piece is differential locking.

Priority additions:
1. **Matchers and selector variants**
   - `!=`
   - `!~`
   - additional `@ start()` / `@ end()` combinations where already implemented
2. **Aggregation variants**
   - `min by (...)`
   - `max by (...)`
   - `sum without (...)`
   - `count without (...)`
3. **Binary/comparison variants**
   - `/`, `%`, `^`
   - `>`, `<`, `>=`, `<=`, `!=`
   - more `bool` comparison shapes
4. **Vector matching**
   - `ignoring(...)`
   - fill-modifier cases if Slice 0 confirms the Prometheus oracle is reliable
5. **Classic histogram projections**
   - `histogram_count(...)`
   - `histogram_sum(...)`
   - `histogram_avg(...)`
6. **Range analogues**
   - add range equivalents where only instant coverage exists today

#### Promotion rule
Only promote a row into the stable corpus after:
1. unit/integration support already exists or is added,
2. differential parity is verified in harness artifacts, and
3. adjacent unsupported/error behavior remains explicit.

#### Validation
- targeted `go test ./internal/promshim/...`
- `go test ./integration/promshim`
- `go test ./...`
- `./scripts/run-harness.sh --no-build`

#### Risk
- Adding too many rows at once will make it harder to isolate genuine semantic drift.
- Promote in small batches by family.

---

### Slice 3 — Add an expected-error harness mode

#### Scope
The current harness only compares successful result payloads. Extend it to cover cases where compatibility is about failing the same way, not returning data.

Add a second corpus type or per-row expectation model such as:
- expected success
- expected `unsupported`
- expected `bad_data`
- expected `execution`

Initial target families:
- query-range invalid expression type
- explicit unsupported rate-family-over-subquery forms
- duplicate-labelset failures
- vector matching cardinality failures
- label/regex validation failures where surfaced publicly

#### Deliverable
A differential error harness that compares at least:
- HTTP success/error outcome
- `errorType`
- stable error-class expectation
- optionally a normalized message fragment where exact wording matters

#### Validation
- targeted service tests for HTTP envelopes
- targeted planner/executor tests for internal error mapping
- harness run including expected-error corpus

#### Risk
- exact message parity can be brittle; compare stable classifications first, then message fragments only where valuable.

---

### Slice 4 — Expand the deterministic fixture dataset

#### Scope
Add new seeded fixture families that expose important PromQL edge classes without destabilizing existing stable corpus behavior.

Priority fixture additions:
1. counter reset series
2. irregular cadence / jittered timestamps
3. `NaN` / `+Inf` / `-Inf` samples
4. duplicate-labelset collision scenarios
5. wider label fan-out for vector matching/cardinality behavior
6. mixed histogram/non-histogram inputs for histogram projection edges
7. missing/optional label combinations

#### Validation
- dataset regression tests
- keep existing stable corpus green before promoting new rows
- promote new corpus entries separately from fixture-introduction changes when practical

#### Risk
- dataset changes can accidentally invalidate unrelated stable rows if existing series behavior changes.
- preserve old series and add new ones rather than mutating current fixtures casually.

---

### Slice 5 — Promote staleness and lookback-boundary coverage

#### Scope
Use the sparse/disappearing fixture family and any new jittered fixtures to differential-test:
- instant selectors near the lookback boundary
- range selectors over sparse/disappearing series
- `absent_over_time` windows that transition from non-empty to empty
- `offset` and `@` interactions at boundary timestamps

Existing seed material to build from:
- `harness/corpus/phase10-staleness-probes.json`
- `.pi/phase10-plan.md`

#### Validation
- start with targeted probe corpus
- promote only the stable cases into `harness/corpus/queries.json`
- run full harness before promotion

#### Risk
- staleness behavior is one of the easiest places to claim false compatibility if the fixture model is too simple.

---

### Slice 6 — Add metadata API differential coverage

#### Scope
Extend parity checks beyond `/api/v1/query*` to the metadata endpoints relied on by dashboards and variables:
- `/api/v1/labels`
- `/api/v1/label/{name}/values`
- `/api/v1/series`

Add endpoint-specific normalization and comparison rules that account for sorting and shape differences while still checking semantic equivalence.

#### Validation
- targeted service tests
- metadata differential checks against Prometheus for the seeded dataset

#### Risk
- Dashboard compatibility can still fail even if query semantics look good when metadata endpoints diverge.

---

### Slice 7 — Add coverage reporting and a standing acceptance bar

#### Scope
Define a lightweight, repeatable coverage report that summarizes:
- number of stable differential rows by family
- number of targeted probes by family
- number of explicit unsupported classes
- number of real inventory queries mapped to each status

Add a review checklist for future PromQL work:
1. Which query family changed?
2. Is it already in the real inventory?
3. Does the stable corpus cover it?
4. If not, why not?
5. Is the boundary intentionally unsupported?

#### Acceptance bar for a family to count as “covered”
A family should only be described as covered when it has:
- at least one representative instant case,
- at least one representative range case where relevant,
- negative/error coverage if the semantics are error-sensitive,
- fixture support for the edge behavior that normally breaks parity.

#### Validation
- review-only for the report format
- automated counts if that becomes cheap to maintain

#### Risk
- Coverage language drifts unless there is a standing reporting convention.

## Recommended execution order

1. Slice 0 — normalize coverage contract and resolve fill-modifier oracle ambiguity
2. Slice 1 — build real `monitoring-v2` query inventory
3. Slice 1A — harvest external/public dashboard queries and normalize them into candidate corpus
4. Slice 2 — widen stable coverage for already-implemented semantics
5. Slice 3 — add expected-error differential harness support
6. Slice 4 — expand dataset fixtures
7. Slice 5 — promote staleness/lookback cases
8. Slice 6 — cover metadata endpoints
9. Slice 7 — add coverage reporting/acceptance bar

## Validation ladder

### Inner loop
- targeted `go test ./internal/promshim/...`
- targeted `go test ./integration/promshim`
- inspect new/updated harness corpus rows against `harness/artifacts/compare-report.json`

### Checkpoints
- `go test ./...`
- `./scripts/run-harness.sh --no-build`

### Manual review points
- verify newly promoted stable cases against real Prometheus output, not just prior expectations
- verify that unsupported boundaries still fail explicitly after widening fixture coverage
- verify that documentation stays aligned with actual harness config and promoted coverage

## Risks and failure modes

- **Coverage inflation risk**: counting unit tests as harness parity will overstate real compatibility.
- **Synthetic-only blind spot**: without a real query inventory, stable corpus growth may still miss important dashboard/alert usage.
- **Public corpus noise risk**: harvested GitHub/Grafana dashboards will contain duplicates, variable syntax, stale dashboards, and non-Prometheus queries unless filtered aggressively.
- **Semantic rewrite drift risk**: retargeting public queries onto seeded metrics can preserve syntax while losing the original behavioral intent.
- **Fixture instability risk**: mutating current seeded series can break unrelated parity cases and make regressions hard to localize.
- **Oracle ambiguity risk**: fill modifiers and other feature-flagged parser/runtime behavior must be clearly documented before being counted.
- **Message brittleness risk**: exact error-text parity can create churn; prefer stable error-class comparisons first.

## Definition of done for this plan

This improvement plan is successful when:

1. the repo has an explicit coverage matrix for PromQL harness support,
2. real `monitoring-v2` queries are inventoried and mapped to coverage status,
3. a small but representative external/public dashboard harvest is deduped, classified, and reduced into reviewable candidate PromQL shapes,
4. already-implemented but unharnessed semantics are promoted into stable differential coverage where parity holds,
5. explicit unsupported/error behavior is checked by an expected-error differential path,
6. fixture coverage includes the major edge classes that commonly break PromQL parity, and
7. future compatibility claims can be stated as measurable coverage rather than informal confidence.

## References
- `harness/README.md`
- `harness/corpus/queries.json`
- `harness/corpus/phase10-staleness-probes.json`
- `internal/promharness/`
- `internal/promshim/plan/promql.go`
- `.pi/implementation-order-plan.md`
- `.pi/phase9-delegated-divergence-catalog.md`
- `.pi/phase10-plan.md`
- `~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/monitoring-v2/`
- `~/code/tradera/tradera-iac/setup/dev-dynamic/apps/monitoring-v2/`
- `~/code/tradera/helm-charts/charts/monitoring/`
- `grafana.com/grafana/dashboards`
- selected GitHub repositories containing Grafana dashboard JSON / Jsonnet / Grafonnet sources (to be chosen during Slice 1A)
