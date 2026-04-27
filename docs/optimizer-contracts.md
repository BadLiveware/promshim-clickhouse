# Optimizer evidence and semantic contracts

This document defines the optimization evidence contract for promshim. It is
review guidance, not a serving change: later rewrites, SQL-shape alternatives,
and ClickHouse settings profiles must satisfy this contract before they can be
used for `cost_shadow` or `cost_prefer` decisions.

## External patterns surveyed

The examples below are non-authoritative inputs. Prometheus semantics,
ClickHouse `TimeSeries`, promshim's tiering contract, and local compliance are
more important than matching another project's optimizer architecture.

| Source | Useful pattern | Adopt for promshim | Deliberately not adopted | Validation required |
|---|---|---|---|---|
| `~/code/external/datafusion/datafusion/optimizer/` | Rule modules are separated from analysis; passes such as common-subexpression elimination and projection optimization have explicit preconditions and tests. | Keep logical rewrite passes small, named, ordered, and pure over IR. Re-analyze after each change and make every applied/skipped rewrite explainable. | Do not import a relational optimizer abstraction before PromQL-specific invariants are encoded. | Unit tests per pass; explain rewrite trace; compliance/differential tests for every served family. |
| `~/code/external/calcite/core/src/main/java/org/apache/calcite/rel/rules/` | Rules are named by transformation and guarded by trait/shape checks. Logical and physical concerns are kept separate. | Make rewrite names stable and domain-facing; require a precondition/preserved-invariant block for every rule. | Do not expose generic relational traits as public promshim concepts. PromQL label and staleness semantics are first-class. | Golden explain output for rule names and skipped reasons; focused semantic tests. |
| `~/code/external/ClickHouse/src/TableFunctions/` and `src/Storages/TimeSeries` | `prometheusQuery*`, `timeSeries*`, `EXPLAIN`, and ProfileEvents provide execution-side evidence for generated SQL and storage pruning. | Treat `EXPLAIN SYNTAX/PLAN/PIPELINE/ESTIMATE` and `system.query_log.ProfileEvents` as acceptance evidence for SQL and settings claims. | Do not assume shorter SQL means less work; ClickHouse may rewrite equivalent shapes internally. | Named sweep artifacts plus focused `ch-explain.sh` / profile captures for optimization claims. |
| `~/code/external/prometheus/promql/` | Prometheus owns value kinds, lookback, staleness, histograms, vector matching, and extrapolation semantics. | Encode these as IR semantic invariants and use Prometheus-compatible differential tests as the correctness oracle. | Do not adopt VictoriaMetrics/MetricsQL extensions or ClickHouse primitive differences as defaults. | Compliance remains clean without new allowlist entries; edge-case tests for staleness, NaN, histogram, and vector matching rewrites. |
| `~/code/external/VictoriaMetrics/app/vmselect/promql/` | Alternate engine shows practical execution strategies for binary operations, rollups, and label joins. | Use as a source of ideas for bounded local/vectorized execution and cardinality protection. | Differences from Prometheus are compatibility risks, not shortcuts. | Any borrowed idea gets Prometheus differential coverage before serving. |
| `~/code/external/hyperdx` | ClickHouse-backed observability products separate operational deployment/schema tuning from query construction. | Keep server/storage recommendations in the reference deployment profile unless promshim explicitly owns a statement/session setting. | Do not make HyperDX/ClickStack schema defaults hidden promshim correctness dependencies. | Benchmark reports name the reference profile; promshim works correctly without those optional server optimizations. |

## Query-family taxonomy

Family labels used in reports, metrics, explain output, and gates must be stable,
bounded enums. They may describe a query shape, but they must never include raw
PromQL, matchers, label values, tenant names, metric names, or unbounded
function text.

| Family label | Shape | Initial serving posture | Primary evidence signals |
|---|---|---|---|
| `selector` | Instant vector selector or selector-root instant expression. | Early CBE candidate only when estimates are fresh and caps pass. | `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes`, round trips, output series. |
| `range_selector` | Query-range selector without rollup work. | Early only for tiny range outputs; otherwise strict/shadow. | Output points, local round trips, `SelectedRows`, response points. |
| `rate` | Instant `rate`/`irate` over one selector. | Early for short-window single-selector families with clean evidence. | Function CPU, input samples, local/native p50, divergence count. |
| `increase` | Instant `increase` over one selector. | Same posture as `rate`; remains separate for calibration. | Function CPU, input samples, local/native p50, divergence count. |
| `range_rate` | Query-range `rate`/`irate`. | Shadow-only until long-range/dense controls prove no cliff. | Range points per series, overlap slots, memory, input samples. |
| `range_increase` | Query-range `increase`. | Shadow-only until long-range/dense controls prove no cliff. | Range points per series, overlap slots, memory, input samples. |
| `range_function` | `*_over_time`, `delta`, `deriv`, `changes`, `resets`, `predict_linear`, and similar rollups. | Shadow-only by default; specific functions may be enabled later. | FunctionExecute, ArrayMap/ArrayFilter counters, memory, semantic edge tests. |
| `range_range_function` | Query-range rollup function. | Shadow-only. | Same as `range_function`, plus range-output caps. |
| `aggregation` | Aggregation with `by`/`without` or global aggregation, excluding selection aggregation. | Strict or shadow until pushdown preconditions are documented. | Required labels, transfer bytes, output cardinality, aggregation ProfileEvents. |
| `selection_aggregation` | `topk`, `bottomk`, `limitk`, `limit_ratio`. | Shadow-only; tie-breaking and ordering need focused evidence. | Output ordering, cardinality, memory, Prometheus parity. |
| `binary` | Scalar/vector arithmetic or comparison without vector-vector matching. | Shadow-only until type/value-kind rules are encoded. | Function CPU, output cardinality, NaN/bool behavior tests. |
| `vector_match` | Vector/vector binary op or set operator with explicit/default matching. | Shadow-only; high-risk family. | Matching cardinality, label-set production, many-to-one/many-to-many tests. |
| `histogram_quantile` | Classic histogram quantile helpers. | Strict/shadow unless histogram-specific evidence is current. | Bucket grouping, interpolation semantics, FunctionExecute, memory. |
| `histogram_function` | Other histogram helpers/projections. | Shadow-only unless function-specific evidence exists. | Prometheus histogram parity and bucket/native-histogram guards. |
| `label_mutation` | `label_replace`, `label_join`, `info`. | Shadow-only unless label lineage and regex behavior are fully tested. | Label lineage, output labels, regex behavior, row counts. |
| `subquery` | Subquery-root or subquery-fed expression. | Shadow-only. | Step-grid semantics, time bounds, repeated evaluation work, memory. |
| `repeated_subexpr` | Same selector/subtree repeated in one query. | Evidence-only until EXPLAIN/ProfileEvents prove less work; do not serve optimized reuse prematurely. | `EXPLAIN SYNTAX` difference, FunctionExecute, SelectedRows, duplicated query-log entries. |
| `scalar` | Scalar literal or scalar-root expression. | Strict/reference; not a performance priority. | Correct type and timestamp semantics. |
| `string` | String literal root. | Strict/reference. | Correct rejection/result semantics. |
| `reference_required` | Shapes with known divergence, missing semantics, or unsupported high-risk combinations. | Strict/reference only. | Rejection reason and compliance visibility. |
| `unknown` | Parser or classifier did not recognize the shape. | Strict/reference only. | Classifier test coverage before reclassification. |

Early CBE serving currently remains narrower than this taxonomy. A family label
being listed here does not make it eligible to serve a non-strict candidate.
Eligibility still requires known-correct candidates, estimates, caps, confidence,
family gates, and fresh validation.

## Corpus coverage contract

Benchmark and differential rows should preserve these axes where practical:

- small fixture rows for fast correctness and metadata checks;
- `7d`, `30d`, and `1y` sparse rows for scan-work and partition-pruning signals;
- `7d` dense processing rows for memory and output-cardinality cliffs;
- high-cardinality grouping or vector-matching rows for cap behavior;
- repeated-selector/subtree rows for reuse claims; and
- reference-required rows that keep unsafe families visible without enabling
  optimized serving.

Corpus `category` values are report labels; they should remain bounded and must
not include raw labels or tenant data. Cost-family labels in explain output are
separate from corpus categories and are used by CBE gates.

## IR semantic invariants

The logical IR represents PromQL semantics independent of where the query runs.
Every node analysis or rewrite must preserve the relevant facts below:

- **Value kind:** scalar, instant vector, range vector, string, or histogram
  flavor when applicable.
- **Time requirements:** evaluation timestamp, range, offset, fixed `@` anchors,
  `start()`/`end()`, subquery step grid, and lookback delta.
- **Selector requirements:** matcher semantics, metric-name behavior, required
  data/tags tables, and exact input time bounds.
- **Label-set production:** output labels, dropped labels, `__name__` handling,
  grouping keys, `by`/`without`, label mutation, and absent-derived labels.
- **Vector matching:** `on`/`ignoring`, group modifiers, matching labels,
  many-to-one/one-to-many/many-to-many cardinality, bool modifiers, and set-op
  behavior.
- **Staleness and NaN sensitivity:** Prometheus staleness markers, stale lookback,
  NaN propagation, comparison filtering, histogram NaN behavior, and extrapolation
  details.
- **Cardinality expectations:** selector count, output series/points estimates,
  local round trips, and hard caps.
- **Support/correctness status:** whether each candidate is supported,
  known-correct, shadow-only, rejected, or reference-required.

## Physical hints

The following annotations are not semantic facts. Rewrites may use them for
planning, but they must not change query meaning and must be invalidated when
stale or unavailable:

- estimated rows, samples, bytes, output points, and output series;
- estimate source, freshness, TTL, and missing/stale selector counts;
- candidate route eligibility and calibrated costs;
- preferred execution location;
- ClickHouse settings profile;
- observed ProfileEvents and memory summaries; and
- benchmark p50/p95 signals.

Missing, stale, or untrusted physical hints choose strict/reference routing.

## Rewrite pass responsibilities

Each rewrite pass must document and test:

1. stable name used in explain output;
2. exact preconditions;
3. semantic invariants it preserves;
4. physical signal it expects to move, if it is an optimization;
5. skipped-rewrite reasons for failed preconditions;
6. known non-goals and high-risk excluded shapes;
7. interaction with candidate generation and CBE gates; and
8. rollback control if it can affect serving.

Rewrites must be pure over IR: do not mutate input nodes in place, re-analyze
after a change, and cap fixpoint iteration. A cosmetic SQL or alias rewrite is
not an accepted optimization unless ClickHouse `EXPLAIN` or ProfileEvents show
that executor work changed.

## Explain and artifact contract

Explain responses and sweep artifacts must be able to connect a request to the
following bounded metadata:

- original PromQL parse shape and original IR summary;
- optimized IR summary when rewrite metadata is emitted;
- applied rewrite names and skipped rewrite reasons;
- query family and support/correctness posture;
- candidate list with tier, strategy, known-correct state, estimates, caps,
  selected/reference/served markers, and rejection reasons;
- strict/reference candidate and selected CBE candidate;
- ClickHouse SQL, statement/session settings profile, and statement settings
  when applicable;
- ClickHouse query ID and log comment used for `system.query_log` correlation;
- routing policy, decision, and reason; and
- validation artifact names for named sweeps or focused explain/profile captures.

Fields may be omitted when not applicable, but field names and enum values must
remain stable once emitted in artifacts.

### Rejection reason enums

Use bounded reason enums. Prefer the closest existing reason instead of creating
near-duplicates.

| Category | Reasons |
|---|---|
| Support | `unsupported_shape`, `unsupported_function`, `unsupported_value_kind`, `unsupported_histogram`, `unsupported_vector_matching`, `unsupported_subquery` |
| Correctness | `known_divergence`, `reference_required`, `staleness_sensitive`, `nan_sensitive`, `histogram_semantics_unproven`, `ordering_or_tie_break_unproven` |
| Estimate | `missing_estimate`, `stale_estimate`, `missing_cost`, `low_confidence`, `predicted_win_below_margin` |
| Cap | `over_cap`, `maxLocalInputSamples`, `maxLocalOutputPoints`, `maxLocalOutputSeries`, `maxLocalRoundTrips`, `rangePointsPerSeries`, `highCardinalityVectorJoin`, `subquery` |
| Setting availability | `setting_unavailable`, `setting_version_unsupported`, `setting_profile_disabled`, `setting_conflict` |
| Policy | `strict_policy`, `policy_ignored`, `native_lowering_mode_ignores_cost_routing`, `family_gate_disabled`, `candidate_serving_disabled`, `strict_reference_already_local` |

## Evidence standards for optimization claims

Every optimization claim must name its expected signal before measurement.
Examples:

- storage pruning: `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes`,
  `EXPLAIN PLAN indexes=1`;
- fewer function executions: `FunctionExecute`, `ArrayMap`/`ArrayFilter` family
  counters, CPU microseconds;
- lower memory: `MemoryTrackerUsage`, memory summary artifacts;
- fewer round trips: `X-Promshim-CH-Roundtrips` and candidate execution metadata;
- less transfer: response bytes, output points/series, ClickHouse send bytes;
- better route choice: strict/selected/served candidate, prediction error, zero
  unexpected divergences; and
- local CPU/memory reduction: Go benchmarks or pprof only when local execution is
  the candidate being optimized.

A small p50 movement alone is not evidence. Strategy/candidate flips are review
signals and must be explained.
