# Native TimeSeries grid-function lowering

This note captures promshim's tier-2/native use of ClickHouse's TimeSeries C++
grid functions inside SQL lowering for supported range `rate(...)` operators.

This path crosses a boundary between tier-2 hand-written SQL and ClickHouse's
native TimeSeries PromQL primitives. It is enabled by default for the narrow
validated rate-range shapes, but remains behind an explicit rollback gate:
`PROM_SHIM_NATIVE_GRID_FUNCTIONS=off` returns those shapes to promshim's
SQL-level `deltaSumTimestamp` kernel.

## Motivation

Current tier-2 range-rate SQL computes rate windows manually with SQL joins,
per-evaluation grouping, and `deltaSumTimestamp`. This preserves composability
but leaves a lot of work in SQL expression execution:

- build an evaluation grid;
- join samples to each evaluation timestamp;
- group by `(id, eval_ts)`;
- compute counter deltas in SQL;
- reattach/project tags;
- optionally aggregate by labels.

ClickHouse already ships native TimeSeries functions for some of this work:

- `timeSeriesRateToGrid`
- `timeSeriesInstantRateToGrid`
- `timeSeriesDeltaToGrid`
- `timeSeriesInstantDeltaToGrid`
- `timeSeriesLastToGrid`

Using them inside tier-2 SQL keeps SQL-level composability for outer
aggregation while moving per-series rate grid computation into vectorized engine
code.

## Implemented shape

For range `rate(selector[lookback])`, promshim groups matched samples per series,
computes the full output grid with `timeSeriesRateToGrid`, zips that value array
with the evaluation timestamps, drops null points, and projects Prometheus label
sets. The row-producing shape is conceptually:

```sql
WITH
  fromUnixTimestamp64Milli({start_ms:Int64}) AS start_ts,
  fromUnixTimestamp64Milli({end_ms:Int64}) AS end_ts,
  toDecimal64({step_ms:Int64}, 3) / 1000 AS step_seconds,
  toDecimal64({lookback_ms:Int64}, 3) / 1000 AS lookback_seconds
SELECT
  final_tags AS tags,
  arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM
(
  SELECT
    arrayFilter(tag -> tag.1 != '__name__', series.tags) AS final_tags,
    point.1 AS timestamp,
    point.2 AS value
  FROM
  (
    SELECT
      any(series.tags) AS tags,
      arrayJoin(timeSeriesRateToGrid(
        start_ts,
        end_ts,
        step_seconds,
        lookback_seconds,
        d.timestamp,
        d.value
      )) AS point
    FROM timeSeriesData(`db`.`table`) AS d
    INNER JOIN (<matched series SQL>) AS series ON d.id = series.id
    WHERE
      d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND
      d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})
    GROUP BY d.id
  )
)
GROUP BY final_tags
ORDER BY final_tags
```

For `sum by (job) (rate(...))`, promshim avoids materializing per-point rows
where possible: it keeps each per-series grid as an array, groups by the
requested label projection, and combines aligned arrays with `sumForEach` plus
presence/NaN masks. Conceptually:

```sql
SELECT
  grouping_tags AS tags,
  arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM
(
  SELECT
    arrayFilter(tag -> has(['job'], tag.1), tags) AS grouping_tags,
    timestamp,
    sum(value) AS value
  FROM (<per-series native-grid rows>)
  GROUP BY grouping_tags, timestamp
)
GROUP BY tags
ORDER BY tags
```

## Required semantic checks

Do not assume additional native grid functions are Prometheus-identical just
because ClickHouse exposes them. Before broadening the current served `rate(...)`
path or adding new function families, compare against existing reference/native
modes for:

- short-window rate such as `rate(demo_cpu_usage_seconds_total[15s])`;
- counter reset handling;
- stale marker / `NaN` handling;
- one-sample windows;
- exact boundary inclusion at lookback start/end;
- offset selectors;
- range endpoint and step alignment;
- `irate`, `delta`, `idelta`, and `last_over_time` separately.

The existing short-window guard must remain unless native functions prove
correct on the compliance corpus and targeted fixtures.

## Rollout gate

Runtime gate:

- `PROM_SHIM_NATIVE_GRID_FUNCTIONS=prefer` (default): use native-grid lowering
  for supported tier-2 range `rate(...)` shapes that pass the existing guards.
- `PROM_SHIM_NATIVE_GRID_FUNCTIONS=off`: rollback to promshim's SQL-level rate
  kernel.

This is a tier-2 implementation detail, not whole-query delegation: the outer
SQL still handles aggregation, tag projection, transforms, and composition.

## Measurement plan

Use the same artifacts as normal tier-2 optimization attempts:

1. Baseline: latest accepted sweep or focused run with
   `PROM_SHIM_NATIVE_GRID_FUNCTIONS=off` for `range_rate` and `range_sum_rate`
   rows.
2. Candidate: same corpus/profile/density/modes with the default
   `PROM_SHIM_NATIVE_GRID_FUNCTIONS=prefer`.
3. Required comparisons:
   - `strategy` remains `native_sql`;
   - `chRoundtrips` stays at `1`;
   - `FunctionExecute/query` should drop materially;
   - `SelectedRows/query` may stay similar unless the function changes scan
     shape;
   - p50/header must improve enough to clear the higher complexity bar;
   - no unrelated native-row tracked-counter regressions above guardrail.
4. Correctness:
   - `go test ./internal/promshim/...`;
   - compliance clean against the expected allowlist;
   - targeted short-window rate check.

## Why this should not be a peephole

This path changes the execution kernel for rate-family functions. It is a large
win on validated fixture rows, but it also inherits ClickHouse function
semantics and version behavior. Keep it as a deliberate gated path with explain
visibility and an `off` rollback, not as an untracked peephole.

## Not covered

The ClickHouse source audit says generic `_over_time` grid functions such as
`avg_over_time`, `min_over_time`, and `max_over_time` are not implemented yet.
Keep those on the existing hand-written SQL paths until upstream ships native
functions and they pass the same semantic checks.
