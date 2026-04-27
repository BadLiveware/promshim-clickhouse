# Per-series time-bound pruning scout

This note captures the third TimeSeries engine opportunity from the audit:
carrying tags-table `min_time` / `max_time` into data-table reads so sparse,
short-lived series do not force full query-window scans.

## Current decision

Rejected for the current implementation shape.

A guarded implementation was prototyped behind a local
`PROM_SHIM_ENABLE_PER_SERIES_TIME_BOUNDS` flag, then benchmarked on focused 7d
sparse native rows. It preserved `native_sql` routing and one ClickHouse
roundtrip, but it substantially regressed the rate/range rows on the current
fixture. The code path was reverted and the flag was not kept.

Focused baseline vs candidate (`repeats=3`, `warmup=1`, native transport,
`prefer,force_supported`):

| Query | Mode | Baseline p50 | Candidate p50 | Result |
|---|---|---:|---:|---|
| `sum_by_job_range_7d` | prefer | `48.30 ms` | `55.22 ms` | regression |
| `sum_by_job_range_7d` | force_supported | `48.09 ms` | `50.81 ms` | regression |
| `sum_rate_by_job_range_7d` | prefer | `158.69 ms` | `422.36 ms` | severe regression |
| `sum_rate_by_job_range_7d` | force_supported | `164.12 ms` | `425.80 ms` | severe regression |
| `rate_5m_range_1d` | prefer | `97.55 ms` | `209.57 ms` | severe regression |
| `rate_5m_range_1d` | force_supported | `95.14 ms` | `204.54 ms` | severe regression |

Artifacts:

- baseline: `harness/artifacts/bench/sweeps/per-series-bounds-focused-baseline/`
- candidate: `harness/artifacts/bench/sweeps/per-series-bounds-focused-candidate/`

## Current promshim shape

Promshim already uses tags-table time overlap to choose matching series:

```sql
SELECT src.id, ... AS tags
FROM timeSeriesTags(`db`.`table`) AS src
WHERE
  src.metric_name = ... AND
  src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND
  src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
```

Then data-table reads use the query-required global time bounds:

```sql
FROM timeSeriesData(`db`.`table`) AS d
INNER JOIN (<matched series>) AS series ON d.id = series.id
WHERE
  d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND
  d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})
```

That is correct and benefits from `(id, timestamp)` ordering, but for high-churn
series it still asks the data side for the full query-required time interval for
every matching id.

## Rejected candidate shape

The rejected implementation extended the matched-series subquery to carry
bounds:

```sql
SELECT
  src.id,
  src.min_time,
  src.max_time,
  ... AS tags
FROM timeSeriesTags(`db`.`table`) AS src
WHERE ...
```

Then it added per-series data predicates to direct joins:

```sql
WHERE
  d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND
  d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND
  d.timestamp >= series.min_time AND
  d.timestamp <= series.max_time
```

This was not beneficial on the current 7d sparse benchmark. The likely reason is
that the additional dynamic predicates did not improve the `(id, timestamp)`
access pattern enough to offset added expression/join work. For range-rate rows,
the regression was large enough to reject without further tuning.

## Future retry conditions

Do not retry this exact shape on the current fixture. A future attempt needs a
materially different setup, such as:

- a churn-heavy fixture where series lifetimes are much shorter than the query
  window;
- `EXPLAIN indexes=1` or ProfileEvents evidence that dynamic per-id bounds
  actually reduce `SelectedRows`, `SelectedBytes`, or `ReadCompressedBytes`;
- a ClickHouse version or schema layout that can push these predicates into the
  data-table access pattern;
- a separate ASOF-specific prototype that preserves the existing right-side
  stale-marker semantics.

Do not push `NOT isNaN(value)` into ASOF right-side scans; stale marker semantics
require filtering after the ASOF match.
