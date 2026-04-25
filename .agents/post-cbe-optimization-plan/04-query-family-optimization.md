# 04. Query-family optimization

## Purpose and scope

Optimize specific PromQL query families using the IR optimizer, CBE candidate
selection, SQL/local/hybrid plan choices, and shim-owned ClickHouse execution
profiles. This stage is where performance work resumes, but each optimization is
bounded by query family, correctness evidence, and a measurable expected signal.

The goal is to improve the cheapest safe plan for each family, not to make any
single tier win unconditionally.

## Prerequisites

- Stage 01 evidence contracts and family taxonomy exist.
- Stage 02 low-risk IR rewrite metadata is available or in progress.
- Stage 03 ClickHouse settings profiles are visible in explain output and can be
  disabled.
- Baseline sweep artifacts exist for the targeted family, or missing baseline is
  explicitly documented.

## Affected areas

- IR rewrite exploitation.
- Native SQL rendering and SQL shape.
- Subtree pushdown candidate generation.
- Local executor data flow and materialization.
- CBE cost model, caps, and family gates.
- Bench corpus and sweep comparison artifacts.

## Requirements

- Prefer family work that is expected to reduce rows/bytes read or transfer size
  before family work whose only obvious effect is SQL-text simplification.
- Every family optimization must declare:
  - the targeted query family;
  - the candidate types affected;
  - expected correctness risks;
  - expected ProfileEvents/explain/runtime signal;
  - benchmark profiles/densities required;
  - rollback gate.
- Strategy/candidate changes are not automatically wins. A route flip must be
  explained and validated.
- Optimizations must preserve Prometheus semantics and must not add compliance
  allowlist entries for shim bugs.
- Before implementing a non-trivial family optimization, check relevant examples
  under `~/code/external/` for patterns and pitfalls, then document why the
  adopted approach fits promshim.

## Research-backed checklist patch (family optimization contract)

- [ ] Require each family change to include a mini evidence bundle:
  strict baseline, optimized/shadow run, and at least one negative control
  profile where route cliffs are likely.
- [ ] Treat `EXPLAIN SYNTAX` + `EXPLAIN PLAN` + `EXPLAIN PIPELINE` as mandatory
  for SQL-shape claims; do not accept p50-only wins.
- [ ] For reuse/CSE work, require executor-visible reduction (`FunctionExecute`,
  duplicate scan work, round trips), not shorter SQL text.
- [ ] Keep vector-matching and histogram-sensitive families shadow-first until
  differential correctness evidence is stable.
- [ ] When route flips occur, require explicit strict/selected/served candidate
  explanation and rollback gate in the family report.

## Ranked implementation queue

Use this queue to decide what to build next. It is intentionally biased toward
high-signal, lower-risk work that should improve routing quality and/or reduce
work done before tackling fragile semantic surfaces.

### Build next

1. **Exact selector time-bound derivation for instant/range/rollup families**
   - Why first: strongest expected `SelectedRows` / `SelectedBytes` signal.
   - Dependencies: stage-01 invariants and stage-02 time-bound metadata.
   - Proof required: EXPLAIN + ProfileEvents evidence of narrower reads,
     unchanged results.

2. **Projection/label pruning for selectors, rollups, and simple aggregations**
   - Why next: reusable across native SQL, subtree pushdown, and local paths.
   - Dependencies: explicit required-label/output metadata.
   - Proof required: lower transfer/intermediate width, unchanged output labels.

3. **Repeated-selector fingerprinting and exact-reuse exploitation**
   - Why next: improves both candidate quality and query-family optimization.
   - Dependencies: selector equivalence metadata and blocked-reuse reasons.
   - Proof required: executor-visible change (`FunctionExecute`, duplicate scan
     work, round trips), not shorter SQL.

4. **Safe aggregation pushdown for simple `by` / `without` families**
   - Why next: likely win once pruning and time bounds exist.
   - Dependencies: grouping/output-label invariants and caps.
   - Proof required: lower transfer/memory, identical result labels/values.

### Build later

5. **Measured settings-profile specialization by family**
   - Example: a repeated-selective profile that tests query condition cache.
   - Wait until family metadata and query/log correlation are solid.

6. **Cross-family SQL shape cleanup that has a concrete runtime mechanism**
   - Example: avoid unnecessary sort/materialization/array work.
   - Only take items with a named expected signal.

7. **More ambitious decorrelation/subquery-to-join rewrites**
   - Useful after the pass framework and explain traces are mature.

### Avoid for now

8. **Vector-matching-heavy SQL rewrites as an early optimization target**
   - Too much semantic risk for the current evidence level.

9. **Rollout based on SQL readability or p50-only improvements**
   - Cosmetic SQL churn and noisy latency deltas are not enough.

10. **Hidden dependence on projections/materialized views/server tuning**
   - Keep these explicit in deployment guidance unless intentionally adopted.

## Implementation tasks by family

### 1. Instant vector selectors

These are good early targets for time-bound tightening, selector fingerprinting,
and careful testing of whether local execution still beats native SQL for tiny
inputs.

Typical examples:

```promql
up
http_requests_total{job="api"}
```

Work:

- [ ] Use selector fingerprints and time bounds from IR metadata.
- [ ] Compare native SQL, subtree pushdown, and local candidates for tiny and
  moderate cardinality cases.
- [ ] Evaluate whether a `tiny_instant` ClickHouse profile reduces overhead or
  whether local execution remains cheaper.
- [ ] Add caps so sparse-fixture wins do not route high-cardinality selectors to
  the wrong candidate.

Expected signals:

- lower wall time for tiny selectors without higher memory or extra round trips;
- unchanged output labels and values;
- clear CBE route reason based on cardinality/sample estimates.

### 2. Range selectors and rollups

This family is the main proving ground for exact time-bound derivation and
storage-side pruning claims.

Typical examples:

```promql
rate(http_requests_total[5m])
avg_over_time(cpu_usage[10m])
increase(errors_total[1h])
```

Work:

- [ ] Compare Prometheus and VictoriaMetrics handling for rollup semantics only
  to identify pitfalls; Prometheus remains canonical.
- [ ] Check ClickHouse `TimeSeries` and PromQL endpoint behavior for any native
  lowering or SQL-shape assumptions.
- [ ] Exploit derived selector time bounds.
- [ ] Tighten SQL predicates without violating Prometheus lookback/extrapolation
  semantics.
- [ ] Compare native rollup SQL, subtree pushdown, and local evaluation across
  short, 7d, 30d, and 1y profiles.
- [ ] Use long-range negative controls for any scan-reduction claim.
- [ ] Keep conservative fallbacks for staleness, sparse samples, and unsupported
  rollup edge cases.

Expected signals:

- reduced `SelectedRows`, `SelectedBytes`, or `ReadCompressedBytes` for pruning;
- lower transfer volume for pushdown;
- stable results versus Prometheus/reference;
- no silent fallback from native SQL when force-supported coverage is expected.

### 3. Aggregations

Start with the simplest grouping/label-retention cases and use them to prove out
projection pruning plus safe aggregation pushdown before touching more fragile
histogram/NaN-sensitive families.

Typical examples:

```promql
sum by (job) (rate(http_requests_total[5m]))
max without(instance) (memory_usage_bytes)
```

Work:

- [ ] Compare DataFusion/Calcite projection and aggregation pushdown patterns as
  examples for rule boundaries and explainability.
- [ ] Use projection-pruning metadata to keep only labels required for grouping,
  matching, and output.
- [ ] Push safe aggregations toward ClickHouse when grouping semantics are known
  correct.
- [ ] Compare aggregation-heavy ClickHouse profile against default-safe profile.
- [ ] Model grouping-cardinality estimates in CBE.
- [ ] Keep local/reference route for uncertain NaN, histogram, or label-retention
  semantics.

Expected signals:

- lower transferred bytes and local memory;
- reduced intermediate row width;
- lower `MemoryTrackerUsage` or local heap for heavy aggregations;
- unchanged output label sets.

### 4. Repeated subexpressions and selector reuse

The bar for success here is executor-visible change, not prettier SQL. If
ClickHouse normalizes both shapes to the same `EXPLAIN SYNTAX`/PLAN, treat the
rewrite as cosmetic.

Typical examples:

```promql
rate(x_total[5m]) / sum(rate(x_total[5m]))
(rate(a[5m]) + rate(a[5m])) / 2
```

Work:

- [ ] Compare DataFusion/Calcite common-subexpression and expression reuse
  patterns, then verify whether ClickHouse's own analyzer already performs the
  same rewrite.
- [ ] Use IR selector/subtree fingerprints to identify exact reusable inputs.
- [ ] Generate native SQL that actually avoids duplicate work, not just shorter
  SQL text.
- [ ] Compare CTE/alias/subquery shapes with `EXPLAIN SYNTAX`, `EXPLAIN PLAN`,
  and ProfileEvents.
- [ ] Reuse pushed-down subtree results or local buffers where native CSE is not
  beneficial.

Expected signals:

- lower `FunctionExecute`, duplicate scan counters, or round trips;
- `EXPLAIN SYNTAX`/PLAN confirms the executor sees a different shape;
- no result changes from reuse across offsets, ranges, or label requirements.

### 5. Binary operations and vector matching

Typical examples:

```promql
errors_total / requests_total
rate(a[5m]) / ignoring(instance) rate(b[5m])
foo * on(job) group_left(region) bar
```

Work:

- [ ] Use Prometheus as the canonical source for vector matching semantics and
  VictoriaMetrics only as a source of alternate implementation ideas.
- [ ] Treat vector matching as high-risk and shadow-first.
- [ ] Make IR matching semantics explicit before any aggressive rewrite:
  `on`, `ignoring`, `group_left`, `group_right`, one-to-one, many-to-one,
  output-label rules, and duplicate-labelset behavior.
- [ ] Compare SQL join, hybrid join, and local join candidates only for families
  with proven matching correctness.
- [ ] Add strict cardinality and memory caps for SQL join candidates.

Expected signals:

- correct output labels and values under differential tests;
- bounded memory and row counts;
- no served native/hybrid candidate without shadow evidence.

### 6. Cross-family SQL shape improvements

These improvements should usually be driven by one of four concrete mechanisms:
read less data, move less data, execute fewer functions, or avoid unnecessary
materialization/sorting. "Shorter SQL" is not a mechanism.

A cross-family SQL-shape item should only enter the `Build next` queue if it
already has:
- a targeted family or families;
- a named expected signal;
- an explanation of why ClickHouse's analyzer/planner will see a materially
  different shape; and
- a fallback plan if EXPLAIN/ProfileEvents show no real win.

Work:

- [ ] Avoid scanning unused labels/columns where projection metadata permits.
- [ ] Avoid unnecessary ordering, materialization, or array operations.
- [ ] Prefer SQL shapes that ClickHouse's analyzer/planner actually optimizes,
  as confirmed by EXPLAIN and ProfileEvents.
- [ ] Keep SQL generated from domain/IR semantics rather than one-off string
  rewrites.

Expected signals depend on claim type and must be selected from
`.pi/skills/measuring-ch-optimizations/SKILL.md`.

## Validation tasks

For each family, capture at least:

1. a strict/reference baseline;
2. a shadow or optimized candidate run;
3. a negative-control run when cardinality/range could change the winner; and
4. focused EXPLAIN/ProfileEvents evidence for the specific claim.

Example sparse family comparison:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-family-<family>-baseline-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-family-<family>-optimized-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

Long-range and dense controls:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-family-<family>-long-range-sparse \
  --profile all \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-family-<family>-dense-processing \
  --profile 7d \
  --density dense \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set processing \
  --memory summary
```

Compliance gate:

```bash
./scripts/run-sweep.sh --name post-cbe-family-<family>-compliance --skip-bench
```

## Exit criteria

For each accepted family optimization:

- [ ] Query family and route gates are explicit.
- [ ] Correctness tests and/or differential evidence cover the semantic risk.
- [ ] Sweep artifacts are named and preserved.
- [ ] ProfileEvents/EXPLAIN signals match the optimization claim.
- [ ] Dense/long-range negative controls pass or the family gate excludes those
  cases.
- [ ] Explain output shows why the optimized candidate was selected or rejected.
- [ ] Rollback can disable the family, rewrite, or settings profile.

## Handoff to next file

Stage 05 can document the server/operator profile separately from the
shim-owned settings used here. Stage 06 handles broader rollout, calibration,
and maintenance once individual family optimizations have evidence.
