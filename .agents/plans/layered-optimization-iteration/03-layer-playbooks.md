# Layer experiment playbooks

## Purpose

Provide layer-specific patterns for designing one optimization experiment. These
are options for the candidate-ranking loop, not a sequence that must all be run.
Within each layer, prefer fundamental reusable improvements over single-query
special cases when the measurement signal and correctness risk are comparable.
When a candidate comes from `06-research-idea-seed.md`, keep the seed caveat in
the iteration note and validate the local promshim applicability before treating
adjacent-system behavior as evidence.

## ClickHouse deployment/reference profile

### When to choose this layer

Choose this layer when evidence suggests performance depends on server/profile,
schema, index, projection, logging, or deployment configuration that promshim
should not set per request.

### Experiment pattern

1. Classify the surface in `docs/clickhouse-tuning-inventory.md` as
   operator-owned, unsafe, distributed-only, version-dependent, or rejected.
2. Add one benchmark-only reference-profile variant if testing requires config.
3. Run paired sweeps where only `clickHouseReferenceProfile` differs.
4. Accept only operator guidance or benchmark profile changes, not hidden runtime
   defaults.

### Required evidence

- Manifest `axes.clickHouseReferenceProfile` differs as intended.
- Query-log/ProfileEvents or EXPLAIN counters move for the claimed family.
- Compliance stack is not modified by the benchmark profile.
- Docs state the tuning is optional operator guidance.

### Rejection signals

- The only movement is p50 noise.
- The profile hides query work through result caching when the claim is not about
  result caching.
- The change requires promshim to assume an external deployment setting for
  correctness.

## Promshim ClickHouse session settings

### When to choose this layer

Choose this layer when a ClickHouse setting can safely be scoped to promshim's
own statement/session and the expected benefit is tied to a query family or
measurement profile.

### Experiment pattern

1. Add or update a named settings profile in
   `internal/promshim/storage/settings_profile.go`.
2. Add version support or skip behavior when the setting is version-dependent.
3. Expose applied/skipped setting reasons in explain and query-log-visible
   metadata if missing.
4. Run paired sweeps where only `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE` differs.
5. Keep performance profiles opt-in until evidence justifies serving defaults.

### Required evidence

- Unit tests cover applied and skipped behavior.
- Manifest `axes.promshimSettingsProfile` differs as intended.
- Query-log settings show the concrete setting value or an explicit skip reason.
- ProfileEvents/EXPLAIN/memory signal matches the hypothesis.

### Rejection signals

- The setting is readonly or unavailable in expected deployments without a safe
  fallback.
- It improves one family while harming another family that would share the same
  profile.
- It changes freshness, isolation, or partial-result behavior without a product
  contract.

## IR metadata and rewrite passes

### When to choose this layer

Choose this layer when the missing optimization depends on semantic knowledge:
required labels, repeated expressions, time bounds, vector matching, value kind,
range widths, or known-correct candidate state.

### Experiment pattern

1. Add the smallest analysis fact or rewrite precondition needed by the selected
   candidate.
2. Keep rewrites pure over IR and re-analyze after changes.
3. Emit stable applied/skipped rewrite names where explain visibility is needed.
4. Add unit tests for preconditions, preserved semantics, and rejected risky
   shapes.
5. Use renderer or executor evidence only after semantic tests pass.

### Required evidence

- Tests cover both applied and skipped paths.
- Explain output or trace metadata identifies the rewrite when applicable.
- Performance claims are backed by executor-visible evidence, not IR shape alone.
- Fundamental rewrites explain the class of shapes they improve and the bounded
  reasons adjacent risky shapes remain excluded.

### Rejection signals

- The rewrite requires algebraic transformations that can alter PromQL float,
  NaN, histogram, staleness, or vector-matching semantics.
- The same SQL/EXPLAIN shape reaches ClickHouse and ProfileEvents do not move.
- The rewrite cannot be explained or disabled when it affects serving behavior.

## Native SQL lowering

### When to choose this layer

Choose this layer when ClickHouse is already the correct execution location but
the generated SQL does duplicate work, carries excess labels/columns, misses a
predicate shape, or uses a more expensive physical pattern than necessary.

### Experiment pattern

1. Capture focused `ch-explain.sh` baseline for the target PromQL.
2. Change one renderer or SQL builder shape.
3. Add renderer/storage SQL tests that pin the intended shape and excluded
   shapes.
4. Capture focused post-change `ch-explain.sh` under the same mode/policy.
5. Run native renderer tests and relevant promshim tests.

When choosing between a one-off SQL improvement and a renderer capability that
removes the same work for a broader safe class, prefer the capability-level
change if it can be tested and measured with the same confidence.

### Required evidence

- `EXPLAIN SYNTAX`, `EXPLAIN PLAN`, or `EXPLAIN PIPELINE` changes in the claimed
  way.
- ProfileEvents or query-log counters move for the claimed signal.
- Compliance remains clean for the affected family.

### Rejection signals

- SQL text is shorter but ClickHouse `EXPLAIN SYNTAX` is byte-identical and
  counters do not move.
- Strategy falls back from native to local/reference.
- A fast path excludes a semantic edge without a rejection reason.

## CBE selection and calibration

### When to choose this layer

Choose this layer when multiple known-correct candidates already exist and the
main opportunity is choosing the cheaper one for a bounded query/data shape.

### Experiment pattern

1. Regenerate calibration from named artifacts when inputs changed.
2. Add or adjust recommendation logic, caps, confidence, or family labels with
   tests.
3. Validate served behavior through the three-artifact pattern:
   - shadow discovery;
   - negative prefer control; and
   - shadow-warmed prefer.
4. Keep family gates explicit and narrow.

### Required evidence

- Calibration artifacts preserve profile, density, transport, settings profile,
  and reference ClickHouse profile.
- Missing estimates fail safe to strict/reference.
- Warmed `cost_prefer` serves only intended bounded rows.
- Over-cap and unsupported rows remain strict/reference.

### Rejection signals

- Candidate choice depends on stale or missing estimates.
- Family gate catches broader shapes than intended.
- Served candidate changes without a fresh calibration path.

## Tier 1 delegation

### When to choose this layer

Choose this layer when ClickHouse's native PromQL endpoint can answer an entire
query correctly and faster than promshim lowering or local execution.

### Experiment pattern

1. Add classifier coverage for the exact PromQL shape.
2. Verify delegated result parity against reference Prometheus and existing
   promshim behavior.
3. Ensure rejection reasons remain visible for unsupported adjacent shapes.
4. Run compliance or focused differential tests.

### Required evidence

- Delegated strategy appears only for known-correct shapes.
- Fallback remains available and explainable.
- No native-only diff failures are hidden.

### Rejection signals

- ClickHouse PromQL behavior differs from Prometheus for labels, ordering,
  staleness, histograms, or vector matching.
- Delegation removes visibility needed by CBE or compliance.

## Tier 3 subtree pushdown

### When to choose this layer

Choose this layer when full native SQL is unsafe or unsupported, but a subtree
can be executed in ClickHouse while preserving local PromQL semantics.

### Experiment pattern

1. Identify the subtree boundary and why it is known-correct.
2. Add candidate metadata and rejection reasons if missing.
3. Measure round trips, transfer width, selected/served candidate fields, and
   divergence status.
4. Keep serving gated by CBE policy and family/cap controls.

Prefer improvements to boundary selection, cap modeling, or transfer accounting
that help many subtree candidates over a single hard-coded pushdown shape, unless
the single shape has much stronger measured impact.

### Required evidence

- Subtree result parity with full local/reference behavior.
- Reduced transfer, round trips, or local CPU/memory.
- Hard caps prevent high-cardinality or range-output cliffs.

### Rejection signals

- Pushdown changes label-set production or vector matching behavior.
- Local fallback hides a wrong pushed result.
- Transfer reduction is not visible in artifacts.

## Tier 4 local execution

### When to choose this layer

Choose this layer when local execution is known-correct and either already wins
for a bounded family or can be made cheaper for CBE candidates.

### Experiment pattern

1. Isolate the local executor cost: repeated reads, decoding, sample windows,
   allocations, CPU, or memory.
2. Add request-scoped caching or local fast paths only with mutation/copy safety.
3. Prefer reusable local execution improvements, such as shared sample-window
   reuse or bounded memoization primitives, over query-specific branches when the
   semantic proof is comparable.
4. Add focused local tests and Go benchmarks or pprof when local CPU/memory is
   the claim.
4. Validate with sweeps that include `native_lowering_mode=off` or equivalent
   local candidate metadata.

### Required evidence

- Local tests preserve PromQL semantics.
- Round trips, CH ms, transfer, allocations, or pprof signal moves.
- CBE serving gates do not broaden unless the CBE validation pattern passes.

### Rejection signals

- Caching crosses request boundaries or risks stale data.
- Cached values can be mutated by later evaluation.
- Optimization helps local mode but lacks a route where local is a serving
  candidate or useful reference improvement.

## Research-seed candidate mapping

Use this quick mapping when selecting ideas from `06-research-idea-seed.md`:

| Seed candidate | Primary playbook section | Extra local check before implementation |
|---|---|---|
| `bench-clickhouse-proof-signature` | Native SQL lowering, CBE selection/calibration, or measurement support as applicable. | Prove query-log joins are complete and ambiguous rows become `unverified`, not success. |
| `ir-rewrite-trace-budget` | IR metadata and rewrite passes. | Keep trace fields bounded and verify optimizer overhead is measured separately from query execution. |
| `ir-semantic-dependency-classifier` | IR metadata and rewrite passes. | Fail closed for unknown PromQL facts such as staleness, histograms, vector matching, offsets, and subqueries. |
| `cbe-ir-feature-extraction` | CBE selection and calibration. | Keep feature extraction non-serving until shadow/negative/warmed-prefer controls justify any served route change. |
| `native-prewhere-pruning-audit` | Native SQL lowering. | Audit automatic ClickHouse PREWHERE/primary pruning before adding manual SQL rewrites. |
| `settings-query-condition-cache-profile` | Promshim ClickHouse session settings. | Isolate cache state from OS page cache, mark cache, result cache, and benchmark ordering effects. |
| `local-rolling-range-rollups` | Tier 4 local execution. | Start with exact float-only whitelist and differential tests for range boundary/staleness behavior. |
| `exact-rollup-result-cache` | Tier 3 subtree pushdown or Tier 4 local execution. | Define the complete cache key and freshness tail before benchmarking. |
| `ir-binary-label-filter-pushdown` | IR metadata and rewrite passes, then Native SQL lowering. | Use explain-only annotations until concrete before/after examples prove the added matcher is non-no-op and semantics-preserving. |
| `native-associative-hash-sharding` | Native SQL lowering or CBE selection/calibration. | First prove ClickHouse native parallelism is insufficient and external fan-out will not just increase backend load. |

## Exit criteria

For the selected candidate, this file has identified:

- the owning layer;
- the experiment pattern;
- required evidence;
- rejection signals; and
- the validation commands that must appear in the iteration note.
