# Native TimeSeries grid-function lowering

This note captures the next tier-2/native optimization direction after the
`tags_to_columns` schema-awareness work: using ClickHouse's TimeSeries C++ grid
functions inside promshim's SQL lowering for rate-family operators.

This is intentionally an architecture note, not enabled behavior. It crosses a
boundary between current tier-2 hand-written SQL and ClickHouse's native
TimeSeries PromQL primitives, so it should land behind an explicit gate and with
compliance/benchmark evidence.

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

Using them inside tier-2 SQL could keep SQL-level composability for outer
aggregation while moving per-series rate/delta grid computation into vectorized
engine code.

## Candidate shape

For range `rate(selector[lookback])`:

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

For `sum by (job) (rate(...))`, keep the same inner per-series grid, then group
outer rows by the requested label projection:

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

Do not assume the native grid functions are Prometheus-identical just because
ClickHouse exposes them. Before serving this path, compare against existing
reference/native modes for:

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

Suggested runtime gate:

- disabled by default;
- enabled by a new env/request setting, for example
  `PROM_SHIM_NATIVE_GRID_FUNCTIONS=off|shadow|prefer`;
- in `shadow`, execute the native-grid candidate in the background and compare
  response values to the current tier-2 SQL path;
- in `prefer`, serve only query families with clean shadow/compliance evidence.

This should be treated as a tier-2 implementation detail, not whole-query
delegation: the outer SQL still handles aggregation, tag projection, transforms,
and composition.

## Measurement plan

Use the same artifacts as normal tier-2 optimization attempts:

1. Baseline: latest accepted sweep for `range_rate` and `range_sum_rate` rows.
2. Candidate: same corpus/profile/density/modes with native-grid gate enabled.
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

This path changes the execution kernel for rate-family functions. It may be a
large win, but it also inherits ClickHouse function semantics and version
behavior. Land it as a deliberate gated path with explain visibility, not as a
silent replacement for the existing guarded `deltaSumTimestamp` SQL.

## Not covered

The ClickHouse source audit says generic `_over_time` grid functions such as
`avg_over_time`, `min_over_time`, and `max_over_time` are not implemented yet.
Keep those on the existing hand-written SQL paths until upstream ships native
functions and they pass the same semantic checks.
