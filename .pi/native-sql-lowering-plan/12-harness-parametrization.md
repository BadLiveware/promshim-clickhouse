# 12 — Harness parametrization for native-lowering safety

## Why

The differential harness at `harness/` and `internal/promharness/` runs each
corpus query **once**, with a single base time, a single step, and a single
seeded dataset shape (`PROM_HARNESS_POINTS=10` by default). Each `QuerySpec`
carries at most one `TimeOffsetSeconds` / `StartOffsetSeconds` /
`EndOffsetSeconds`. That was adequate for the aggregation / selector
parity the harness was built for, but it is weak against the edge-case
families that Phase 6b native range-function lowering will surface:

- range / step boundary inclusivity (the workaround in commit `0d44c2b`
  lives exactly here)
- range windows that straddle the seeded dataset's start or end
- series that appear or disappear inside a lookback window
- step values that do not evenly divide the range
- counter-reset handling in `rate` / `irate` / `increase` / `delta`

Parameterizing the harness along a **small, curated** set of time-window
and step-range axes before path-2 lowerings start landing turns "did it
regress" from a single-point check into real coverage of the corners
path-2 code will hit. See
[00-status-and-drift.md](./00-status-and-drift.md) for the path-2 vs
path-3 policy this supports.

## Scope (three layers, ordered by ROI)

### Layer 1 — per-query time-window matrix

**Do this first.**

- extend the corpus schema (or the comparator) to let each query run at
  several offsets, not just a single `timeOffsetSeconds` /
  `startOffsetSeconds` / `endOffsetSeconds`
- default offset set:
  - **early** — just after the seeded dataset starts
  - **middle** — roughly the midpoint of the dataset
  - **late** — just before the seeded dataset ends
  - **boundary** — placed so that a range window straddles the seed start
- each variant appears as its own row in `compare-report.json`, named
  explicitly (e.g. `query_name/offset=boundary`)
- no seeded-data changes required

### Layer 2 — range × step sweep for range queries

- for range-query corpus entries, run them with several `(range, step)`
  combinations
- default combos:
  - step evenly divides range
  - step does not evenly divide range
  - step > range / 2
  - step = range
- gated by an **opt-in marker** on corpus entries so aggregation-only
  queries do not fan out pointlessly

### Layer 3 — dataset shape variants

**Optional, later.**

- a second seeded dataset with counter resets, gaps, and series that
  appear or disappear mid-range
- the comparator runs against both seeded datasets and merges the results
- meaningful once native `rate` / `increase` / `delta` lowerings are in
  flight; not worth building before

## Distinct tasks

1. **Extend `QuerySpec` with offset list fields**
   - allow something like `TimeOffsets []int64` and/or
     `StartEndOffsets [][2]int64` alongside the existing single-offset
     fields in `internal/promharness/types.go`
   - keep single-offset entries working unchanged so the existing corpus
     does not need to be rewritten

2. **Extend the comparator to iterate variants**
   - expand each corpus entry into its variants at runtime in
     `internal/promharness/compare.go`
   - report results under explicit variant names in `QueryComparison`
     (add an optional `Variant` field; keep the schema
     backward-compatible so existing consumers of
     `artifacts/compare-report.json` do not break)

3. **Add Layer 2 opt-in for range queries**
   - add a `RangeStepMatrix` marker on range-query corpus entries
   - when set, the runner expands the query across the default
     `(range, step)` combos

4. **Document variant semantics**
   - short section in `harness/README.md` describing the default offset
     set and the `(range, step)` matrix
   - link from [09-phase-6-range-functions-and-subqueries.md](./09-phase-6-range-functions-and-subqueries.md)

5. **Keep triage output actionable**
   - re-check the severity / bucket classifier in
     `internal/promharness/compare.go` so a single underlying cause does
     not multiply into many p0 rows — consider collapsing identical
     `(severity, bucket, detail)` rows for the same base query, or at
     least tagging them as a single-cause cluster in the report

## Non-goals for this chunk

- **do not** replace the single seeded dataset with a matrix of seeded
  datasets (that's Layer 3)
- **do not** expand the corpus itself here — this chunk is about running
  existing queries across more points, not adding queries
- **do not** chase exhaustive offset coverage — the aim is a small,
  curated set that catches known edge classes

## Validation

- existing parity run still passes with no new diffs when the default
  offset set reduces to a single entry (regression guard for the runner
  change itself)
- layered rollout: land Layer 1, bake for a while, then land Layer 2;
  avoid a single PR that doubles and then triples the comparison count
- each layer's PR includes a sample `compare-report.json` artifact
  showing the new variant naming

## Current status

As of 2026-04-21:

- The original pre-Phase-6 harness gap is only **partly** resolved.
- The harness already supported single per-query timing fields
  (`timeOffsetSeconds`, `startOffsetSeconds`, `endOffsetSeconds`,
  `stepSeconds`) and later gained `compareMode`, `subjects`,
  `nativeLoweringMode`, and `explain` controls.
- **Layer 1 is now delivered** for curated per-query offset variants:
  - instant-query rows may set `timeOffsets`
  - range-query rows may set `rangeOffsets`
  - compare results keep the base `name` and add an explicit `variant`
    field in `compare-report.json`
  - existing single-offset corpus rows continue to work unchanged
- **Layer 2 is now delivered** for an opt-in range-query step sweep:
  - range-query rows may set `rangeStepMatrix: true`
  - the default per-range matrix currently emits:
    - `step_evenly_divides_range`
    - `step_not_evenly_divides_range`
    - `step_gt_range_over_2`
    - `step_eq_range`
  - when combined with `rangeOffsets`, variant names are joined
    (for example `boundary/step_eq_range`)
- **Layer 3 is now delivered as an initial multi-dataset slice**:
  - the seed job may be asked to generate multiple dataset shapes with
    `PROM_HARNESS_DATASET_VARIANTS` (or `./scripts/run-harness.sh --dataset-variants ...`)
  - compare results keep per-row `datasetVariant` metadata while preserving
    legacy single-dataset behavior by default
  - the currently seeded dataset shapes are:
    - `baseline`
    - `resets_gaps` (counter reset + post-midpoint gaps)
    - `churn_stale` (series churn / staleness)
    - `histogram_burst` (post-midpoint histogram burst)
  - corpus rows may now select or exclude dataset shapes with:
    - `datasetVariants`
    - `excludeDatasetVariants`
- Focused / broader example corpora now exist at:
  - `harness/corpus/phase12-harness-variants.json`
  - `harness/corpus/phase12-dataset-variants.json`
  - `harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.json`
  - themed splits under `harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.themes/`
- **Task 5 triage clustering is now delivered**:
  - repeated non-ok rows with the same base query + severity + bucket +
    detail are tagged with `causeCluster` / `causeClusterSize`
  - `compare-report.json` also emits a top-level `clusters` summary so
    one underlying issue does not need to be triaged as many unrelated p0/p1 rows

## When to apply each layer

- **Phase 1 refactor** (type extraction from the planner): **do not block
  on this.** A refactor that preserves behavior should pass the existing
  86-query corpus unchanged; Layer 1/2 will not find refactor regressions
  and will slow the feedback loop. Ship Phase 1 on the existing harness.
- **Phase 6b native lowering**: Layer 1 should land **before** the first
  native range-function lowering. Layer 2 should land with or before the
  first `rate` / `increase` / `delta` native lowering.
- **Phase 7 shadow mode**: Layer 1 and Layer 2 results become the
  shadow-mode input corpus. Layer 3 is now available for focused
  counter-reset, churn/staleness, and histogram-burst checks when
  shadow-mode divergences or dashboard-derived shortlist rows suggest
  those behaviors are relevant.
