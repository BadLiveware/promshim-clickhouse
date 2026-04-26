# ClickHouse TimeSeries deployment tuning

This document records deployment-level TimeSeries engine choices that multiply
promshim tier-2/native SQL performance. These recommendations are separate from
query lowering: they change the ClickHouse schema or operator configuration and
should be tested on a staging cluster before production rollout.

## Engine facts promshim relies on

For the ClickHouse TimeSeries table engine used by promshim:

- The data table is MergeTree-like and ordered by `(id, timestamp)` by default.
  `id` is `UUID` unless `id_type` is configured; `timestamp` is
  `DateTime64(3)` and `value` is `Float64`.
- The tags table is ordered by `(metric_name, id, min_time, max_time)` with a
  `LowCardinality(String)` `metric_name` and `Map(LowCardinality(String),
  String)` `tags` column.
- No data-table skip indexes or monthly partitions are created by default.
- Settings such as `tags_to_columns`, `id_type`, `store_min_time_and_max_time`,
  `aggregate_min_time_and_max_time`, `filter_by_min_time_and_max_time`, and
  `use_all_tags_column_to_generate_id` change the physical schema and pruning
  behavior.
- Native grid functions currently implemented by ClickHouse include
  `timeSeriesRateToGrid`, `timeSeriesInstantRateToGrid`,
  `timeSeriesDeltaToGrid`, `timeSeriesInstantDeltaToGrid`, and
  `timeSeriesLastToGrid`. The generic `_over_time` aggregate family is not yet
  available in the engine.

## What promshim already exploits

Promshim's native SQL lowering already benefits from the default TimeSeries
layout:

- `src.metric_name = ...` uses the tags-table prefix key.
- `src.max_time >= ...` and `src.min_time <= ...` use per-series time bounds
  when the engine stores them.
- `id IN (...)` and joins on `id` target the data table's `(id, timestamp)`
  ordering.
- Range selector lookup uses an ASOF join on `(id, timestamp)` and must not
  pre-filter stale-marker `NaN` rows before the ASOF match.

## Recommended schema settings

### Promote hot labels with `tags_to_columns`

For high-cardinality labels that are common in matchers or grouping keys,
configure TimeSeries `tags_to_columns`, for example labels such as:

- `instance`
- `pod`
- `node`
- `service`
- `namespace`

When a label is promoted, ClickHouse stores it as a first-class tags-table
column instead of requiring a per-row Map value probe. If the column is
`LowCardinality(String)`, equality and regex predicates can avoid repeated Map
lookup work and benefit from dictionary encoding.

Promshim support:

- Set `PROM_SHIM_PROMOTED_TAG_COLUMNS=instance,pod,node` when the promoted label
  list is known from deployment configuration.
- Or set `PROM_SHIM_DISCOVER_PROMOTED_TAG_COLUMNS=true` to let promshim describe
  `timeSeriesTags(database.table)` at startup and add non-system columns to the
  promoted set. If discovery fails, promshim logs the failure and continues with
  the explicit list only; keep an explicit list for locked-down environments
  where startup metadata queries are not allowed.
- Promshim still uses `mapContains(tags, '<label>')` for narrowed label
  projection presence semantics, but reads the promoted column value when it is
  known to exist.

Rollout guidance:

1. Add the promoted labels to the ClickHouse TimeSeries table definition or
   operator-managed schema.
2. Verify `DESCRIBE TABLE timeSeriesTags(database.table)` shows the label
   columns.
3. Enable `PROM_SHIM_DISCOVER_PROMOTED_TAG_COLUMNS=true` or set the explicit
   comma-separated list.
4. Use `/api/v1/query_explain` to confirm native SQL reads `src.`label`` for
   configured labels rather than `src.tags[...]`.

### Prefer smaller id types when safe

The default series id type is `UUID`. For deployments that can safely use a
smaller hash/id domain, configuring `id_type = UInt64` halves id width compared
with UUID and can reduce memory and CPU in `id IN (...)` sets and join hash
tables.

This is a schema/compatibility decision, not a promshim renderer requirement.
Promshim uses the engine table functions and does not need query changes for
normal reads. At startup it best-effort describes `timeSeriesData(database.table)`
and logs the observed `id` column type so operators can confirm whether a
deployment is using the default `UUID` or a smaller configured id type.

## Recommended data-layout additions

### Partition long-retention data by month

Default TimeSeries data has no `PARTITION BY`. For multi-week or multi-month
retention, monthly partitioning such as `PARTITION BY toYYYYMM(timestamp)` can
reduce part scans for common dashboard windows and improve operational
retention management.

Use this only when it matches the retention and ingest pattern. Benchmark with
representative `7d`, `30d`, and longer windows because partition count and
part-size tradeoffs are deployment-specific.

### Add skip indexes when supported by the managed schema

ClickHouse does not add skip indexes to the TimeSeries inner tables by default.
If your deployment exposes the underlying tables or supports equivalent
operator-managed DDL, consider:

- data table timestamp minmax index:
  `INDEX min_max_ts (timestamp) TYPE minmax GRANULARITY 8`
- tags table Map-key bloom filter:
  `INDEX tag_keys (mapKeys(tags)) TYPE bloom_filter GRANULARITY 4`

The timestamp index helps range predicates prune granules. The Map-key bloom
filter helps metadata and selector paths that test for label presence. These are
schema-level accelerators and should be validated with `EXPLAIN indexes=1` and
query-log read counters before/after.

See `scripts/recommend-indexes.sql` for commented DDL templates. The script is
intentionally not directly executable because inner TimeSeries table names and
operator support differ across deployments.

## Per-series time-bound pruning follow-up

The tags table stores `min_time` and `max_time` per series, while the data table
is ordered by `(id, timestamp)`. Carrying those bounds into data-table joins may
reduce scans for high-churn metrics with short-lived series inside long query
windows. This needs proof because dynamic per-id bounds may or may not be pushed
into ClickHouse's data-table access pattern. See
`docs/per-series-time-bound-pruning.md` for the candidate SQL shape,
prototype plan, and acceptance gates.

## Native grid-function lowering follow-up

ClickHouse's `timeSeriesRateToGrid`, `timeSeriesInstantRateToGrid`,
`timeSeriesDeltaToGrid`, `timeSeriesInstantDeltaToGrid`, and
`timeSeriesLastToGrid` are promising runtime kernels for rate/delta/last paths.
They should be adopted as a deliberate gated tier-2 implementation path, not as
a silent peephole, because they replace promshim's current SQL-level rate
kernel. See `docs/native-grid-function-lowering.md` for the candidate SQL shape,
semantic checks, rollout gate, and measurement plan.

## Optional dense-dashboard rollups

Dense dashboard ranges can be dominated by repeatedly deriving the same
per-label rate series from raw TimeSeries samples. For common dashboards, an
operator-managed rollup table can precompute a fixed semantic shape such as:

```promql
sum by (job) (rate(<counter>[5m]))
```

on a fixed output grid, then serve dashboards by reading the rollup table instead
of recomputing from raw samples. This is an accelerator, not a promshim
requirement: promshim must continue to run against raw `timeSeriesData(...)` when
rollups are absent, stale, or semantically ineligible.

Template SQL lives in `scripts/recommend-rollups.sql`. The template is explicit
about the metric, labels, grid, lookback, refresh window, and output table so an
operator can review the semantic trade-off before enabling it. At startup,
promshim diagnostically checks whether the recommended
`rollup_cpu_rate_5m_1m_by_job` table shape is present. Query-range routing also
reports a non-serving `optional_rollup_cpu_rate_5m_1m_by_job` candidate for the
exact 1-minute `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))` shape.
By default this remains observability only and query routing uses the raw
TimeSeries path. Setting `PROM_SHIM_DENSE_RATE_ROLLUPS=prefer` allows promshim to
serve that exact detected shape from the rollup table; all other shapes, missing
rollups, and malformed rollups fall back to raw TimeSeries. Use rollups only
when all of these are true:

- the dashboard query shape is stable and high-volume enough to justify storage;
- the rollup interval and lookback exactly match the served query family;
- the refresh job reads at least one lookback before the first output timestamp;
- raw TimeSeries fallback remains available for ad hoc or semantically different
  PromQL.

A benchmark-stack scout for a 24h dense `sum by (job) (rate(...[5m]))` row found
that reading a precomputed 1m rollup was about `4 ms` in ClickHouse versus the
raw native path at about `11.5 s`; the one-window rollup refresh itself was about
`97 ms` on the local fixture. This makes rollups promising for fixed dense
dashboards, but they need explicit operator ownership and feature-detected
fallback before promshim routes to them automatically.

## Things not to pursue on current ClickHouse sources

- TimeSeries tag/group primitives as a portable replacement for promshim's tag
  arrays on the default UUID schema. `timeSeriesExtractTag` exists but expects a
  UInt64 group argument, not `Array(Tuple(String,String))`; see
  `docs/timeseries-tag-primitives-scout.md`.
- `groupArrayResample`: not present in the audited ClickHouse source.
- Engine-side `avg_over_time`, `min_over_time`, `max_over_time`, and similar
  generic `_over_time` grid functions: still TODO upstream.
- Assuming default skip indexes or partitions exist: they do not unless your
  deployment configures them.

## Validation checklist

For any schema-level tuning, collect evidence before and after:

- `EXPLAIN indexes=1` or relevant plan output for pruning changes.
- Normalized query-log counters: `SelectedRows`, `SelectedBytes`,
  `ReadCompressedBytes`, `FunctionExecute`, `UserTimeMicroseconds`, and memory
  usage when available.
- Promshim explain output to confirm strategy stays `native_sql` for native
  lowering targets.
- Correctness/compliance checks for any schema change that affects label
  materialization or staleness behavior.
