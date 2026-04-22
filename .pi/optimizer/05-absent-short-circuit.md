# 05 — `absent` / `absent_over_time` short-circuit

`absent(m{foo="x"})` returns the synthesized 1-valued series iff no series
with metric `m` and `foo="x"` exists in the lookback window. That question
is answered entirely by `time_series_tags` — it is a single EXISTS check.
Today the shim routes it through the full selector pipeline, rendering the
child fragment's `(tags, time_series)` matrix over the data table even though
the final result only cares about whether any rows came back. Short-circuit
to a presence probe against `time_series_tags`.

## Problem

Concrete queries:

- `absent(up{job="missing"})` — instant
- `absent_over_time(up{job="missing"}[5m])` — instant
- `absent(up{job="missing"})` — range mode (a synthesized step-grid of 1s
  for steps where no series is live)

Current SQL envelope for instant `absent(child)`
(`internal/promshim/native/renderer/join.go:301-327`):

```sql
SELECT <tagsSQL> AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp,
       toFloat64(1) AS value
FROM (SELECT count() AS sample_count
      FROM (<full child selector SQL>) AS absent_child) AS probe
WHERE probe.sample_count = 0
```

The child SQL, when the selector is an instant-vector, is rendered via
`buildInstantSelectorSourceSQL` (`selector_sql.go:174-210`) which JOINs
`timeSeriesTags` against `timeSeriesData` and `argMax`-reduces per series.
All of that work exists only to be reduced to a single `count() = 0`. For a
selector that matches no series the join still executes and returns 0 rows;
for a selector that matches 5000 series we read the data table for all of
them before the outer `count()` fires.

For `absent_over_time(m[r])` the child is a range-vector source which
renders via `buildRangeMatrixSelectorSourceSQL` and groupArrays a
`(timestamp, value)` array per matched series before the outer probe counts
samples.

For range-mode `absent`, `renderAbsentFragment` builds a
`(grid × presence_probe)` left-anti join that synthesizes 1-samples at
every step lacking presence. The presence probe currently relies on the
fully-materialised child matrix.

## Current behavior

- `internal/promshim/native/renderer/join.go:301-401` —
  `renderAbsentFragment` always calls `renderFragmentSubquery` on the
  child, which produces the full pipeline.
- `internal/promshim/native/renderer/join.go:358-394` —
  `renderAbsentOverTimeWindowedSource` renders the range-mode windowed
  per-series arrays before counting samples.
- `internal/promshim/native/optimizer.go:829-853` —
  `semanticBarriersForFragment` correctly tags absent_over_time as a
  "range_window_materialization_boundary", but no rewrite exists for the
  simpler shape.
- `internal/promshim/native/optimizer.go:211-218` —
  `applyFunctionPatternRewrites` does not recognize `absent`.

## Proposed technique

A new optimizer pass `PassAbsentPresenceProbe` that replaces
`FragmentKindAbsent` fragments whose child is a plain selector (no value-
level predicates, no transforms, no histogram projection, no info join)
with a new `FragmentKindAbsentProbe` fragment that carries
`{Func, OutputMetric, SelectorMatchers, MetricName, Lookback, Offset}`.

**Instant `absent`:**

```sql
SELECT <output_tags> AS tags,
       fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp,
       toFloat64(1) AS value
WHERE NOT EXISTS (
  SELECT 1
  FROM timeSeriesTags(db, table) AS src
  WHERE <matchers>
    AND src.max_time >= fromUnixTimestamp64Milli({eval_minus_lookback_ms:Int64})
    AND src.min_time <= fromUnixTimestamp64Milli({evaluation_ms:Int64})
  LIMIT 1
)
```

The `LIMIT 1` inside the EXISTS is redundant semantically (EXISTS
short-circuits on first hit) but communicates intent and matches the
ClickHouse-side execution plan.

**Instant `absent_over_time(m[r])`:** identical to `absent` with
`eval_minus_lookback_ms = evaluation_ms - range_ms - offset_ms`. No sample
reading.

**Range `absent`:** synthesize the 1-valued step-grid only for steps where
no series is live. Presence check uses `time_series_tags` only:

```sql
SELECT <output_tags> AS tags,
       arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
  SELECT grid.eval_ts AS timestamp, toFloat64(1) AS value
  FROM (SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms),
             range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts) AS grid
  LEFT ANTI JOIN (
    SELECT src.id, src.max_time, src.min_time
    FROM timeSeriesTags(db, table) AS src
    WHERE <matchers>
      AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})
      AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
  ) AS live ON live.max_time >= grid.eval_ts - toIntervalMillisecond({lookback_ms:Int64})
           AND live.min_time <= grid.eval_ts
) GROUP BY tags
HAVING length(time_series) > 0
```

ClickHouse may not support ANTI JOIN with a non-equi predicate directly;
fall back to `grid LEFT JOIN (live × grid) … WHERE live.id IS NULL`. The
exact join shape is an implementation detail; the point is the `live` side
reads from `timeSeriesTags`, not from `time_series_data`.

**Range `absent_over_time(m[r])`:** same as range `absent` but with the
`max_time >= grid.eval_ts - lookback` predicate tightened to the
`absent_over_time` range `r` (range-function lookback, not selector
lookback). The output metric tags come from `fragment.Absent.OutputMetric`
and are preserved unchanged.

## Expected gain

- Instant `absent(m)`: zero bytes read from `time_series_data`. Today the
  query reads `time_series_data` proportional to matched series × lookback
  samples. If `absent` is used in an alert rule that runs every 15s, this
  is significant recurring savings.
- Range `absent`: typical range lookback is 1h at 15s step = 240 steps. For
  a metric with 0 matched series, today's path still scans the full
  lookback envelope on the data table (trivially cheap, but not free). For
  a metric with many matched series that happens to have holes, today's
  path reads every sample; the rewrite reads only the per-series extents.
- Query planner: `EXISTS (… LIMIT 1)` is the strongest pushdown signal
  ClickHouse accepts. The optimizer can prune entire shard reads.

## Risk / PromQL semantics caveats

- **Matchers with value-level predicates**: the optimizer must only fire
  when the child is a pure selector. `absent(m > 0)` is
  `absent` over a filtered-by-value selector; the EXISTS probe against
  `timeSeriesTags` cannot answer "are there any samples with value > 0"
  without reading `time_series_data`. Gate on
  `fragment.Absent.Child.Kind == FragmentKindLeafSource` with the leaf's
  wrapper being identity.
- **Stale markers**: Prom's `absent` returns 1 iff *no non-stale sample*
  is present at the evaluation timestamp. `time_series_tags` carries
  sample timestamp extents including stale markers. If a series has its
  most recent sample be a stale marker, `timeSeriesTags` says the series
  is live at that timestamp; Prom says the series is *absent*. This
  matters: the rewrite would be **wrong** for a metric that's actively
  being torn down (last sample == stale).
  - **Mitigation**: the cheapest safe probe is
    `EXISTS (SELECT 1 FROM timeSeriesData d WHERE d.id IN (<matched ids>)
    AND d.timestamp BETWEEN ... AND ... AND NOT isNaN(d.value) LIMIT 1)`.
    That still reads the data table, but it short-circuits on the first
    non-stale sample — orders of magnitude cheaper than `count()`.
  - **Preferred**: keep the EXISTS probe against `time_series_tags` for
    the common case (no stale markers), and guard it behind a capability
    flag that gets disabled for metrics known to use stale markers. In
    practice, Prom's push-based ingestion stale markers are rare in this
    codebase's workloads — verify against ingestion code before shipping.
- **Labels-preservation**: Prom's `absent(m{foo="x", bar="y"})` returns a
  synthetic series labeled `{foo="x", bar="y"}` (only equality matchers
  survive). `fragment.Absent.OutputMetric` already carries the correct
  tag set; this does not change.
- **`absent` of a non-selector expression**: `absent(rate(m[5m]))` is
  legal PromQL. Rewrite does NOT apply — the child is a
  `FragmentKindRangeFunction`, not a leaf source. Fallback to the existing
  renderer in that case.
- **Range-mode absent of a non-live metric for the full envelope**:
  synthesized output is a single series with a 1 at every step. Verify
  the ANTI JOIN emits that — the current `buildRangeAbsentSeriesSQL`
  handles this via LEFT JOIN + `WHERE present.sample_count IS NULL`; the
  pushdown keeps the same outer shape, only swapping the presence source.

## Implementation sketch

1. New fragment kind `FragmentKindAbsentProbe` with
   `AbsentProbeFragment { Func, OutputMetric, Selector, StaleSafetyMode }`.
2. Pass `PassAbsentPresenceProbe` in the fragment layer, immediately after
   `PassCommonMatcherInference` so matchers are already inferred. Match on:
   - Fragment kind `FragmentKindAbsent`.
   - Child is a plain leaf source selector (instant-vector or range-vector
     per the function variant).
   - Wrapper is identity; no value transforms.
3. Storage helpers:
   - `BuildAbsentProbeInstantQuerySQL(cfg, selector, evaluationMS,
     outputTagsSQL)` — emits the instant EXISTS form.
   - `BuildAbsentProbeRangeQuerySQL(cfg, selector, startMS, endMS, stepMS,
     lookbackMS, offsetMS, outputTagsSQL)` — emits the range ANTI-JOIN
     form.
4. Renderer case in `renderFragment` for `FragmentKindAbsentProbe`
   dispatching to the storage helpers. Both `absent` and `absent_over_time`
   land here; the difference is only the effective lookback window passed
   to the storage helper.
5. Safety-mode: a `QueryConfig` knob (`AbsentStaleSafety` enum with
   values `TagsOnly`, `DataProbe`). Default `TagsOnly` in development,
   `DataProbe` in production until stale-marker behaviour is audited.
   `DataProbe` variant reads `time_series_data` with a `LIMIT 1` EXISTS
   but still avoids the groupArray.
6. Report: `AppliedRewrites = append(..., "absent_presence_probe")`.

## Test coverage idea

- Unit (optimizer): `TestAbsentProbeInstantRewrite` — plan for
  `absent(up{job="missing"})` produces `FragmentKindAbsentProbe`.
- Unit (optimizer negative): `TestAbsentOfRateSkipped` —
  `absent(rate(m[5m]))` stays as `FragmentKindAbsent` with its current
  child.
- Unit (optimizer negative): `TestAbsentOfValueFilterSkipped` —
  `absent(m > 0)` does not pushdown.
- Golden SQL: instant-mode output contains `EXISTS (SELECT 1 FROM
  timeSeriesTags` and does NOT mention `timeSeriesData(`.
- Golden SQL: range-mode output contains one reference to
  `timeSeriesTags` on the presence side and zero references to
  `timeSeriesData`.
- Integration (harness): `absent(up{job="nonexistent"})` returns the
  synthetic 1 at evaluation time; `absent(up)` where `up` has samples
  returns empty; both under instant and range modes.
- Integration: `absent_over_time(up{job="ephemeral"}[5m])` for a metric
  that appears and disappears within the window; confirm the boundary
  steps match the reference Prom output.
- Stale marker fixture: inject a test case where a series exists in
  `timeSeriesTags` but its last sample is a stale marker; assert that
  with `AbsentStaleSafety=DataProbe` the rewrite correctly reports the
  series as absent, and with `TagsOnly` it incorrectly reports live
  (pinning the known behaviour so we can flip the default consciously).
- Bench: hyperfine `absent(m{job="missing"})` against a fixture with
  10M samples in `timeSeriesData` but zero matching series; expect
  roughly constant-time execution in the rewritten path and linear in
  the legacy path.
