# TimeSeries tag primitive scout

ClickHouse exposes TimeSeries tag/group helpers such as:

- `timeSeriesExtractTag`
- `timeSeriesGroupToTags`
- `timeSeriesIdToGroup`

These looked like possible replacements for promshim's current manual tag-array
and Map expressions. A focused scout on the benchmark ClickHouse 26.3 fixture did
not produce an implementation candidate.

## Findings

The functions exist:

```sql
SELECT name
FROM system.functions
WHERE name IN ('timeSeriesExtractTag', 'timeSeriesGroupToTags', 'timeSeriesIdToGroup')
ORDER BY name
```

returned all three function names.

However, `timeSeriesExtractTag` is not a drop-in replacement for promshim's
`Array(Tuple(String, String))` tag arrays:

```sql
SELECT timeSeriesExtractTag([('job','api'),('instance','i')], 'job')
```

failed with:

```text
Argument #1 of function timeSeriesExtractTag has wrong type Array(Tuple(String, String)), it must be UInt64
```

The benchmark TimeSeries tags table uses the default `UUID` id type:

```text
id UUID DEFAULT reinterpretAsUUID(sipHash128(metric_name, all_tags))
```

A direct probe with `timeSeriesIdToGroup(id)` over `timeSeriesTags(...)` also
failed on this default UUID fixture. That means the tag/group primitives are not
a safe general replacement for the current tag-array projection paths in the
default schema.

## Decision

Do not implement tag primitive lowering for the current default TimeSeries
schema.

The existing manual tag-array expressions remain the portable path for UUID
TimeSeries deployments. Revisit only when one of these is true:

- the deployment uses an id type and TimeSeries group representation accepted by
  `timeSeriesIdToGroup` / `timeSeriesExtractTag`;
- ClickHouse adds overloads that accept `Map` or `Array(Tuple(String,String))`
  tags directly;
- EXPLAIN/ProfileEvents evidence on a compatible schema shows executor-visible
  work reduction without label semantic changes.

## Validation commands

```bash
docker exec bench-clickhouse-1 clickhouse-client --user default --password otel \
  --query "SELECT name FROM system.functions WHERE name IN ('timeSeriesExtractTag','timeSeriesGroupToTags','timeSeriesIdToGroup') ORDER BY name FORMAT TSV"

docker exec bench-clickhouse-1 clickhouse-client --user default --password otel \
  --query "SELECT timeSeriesExtractTag([('job','api'),('instance','i')], 'job') FORMAT TSV"

docker exec bench-clickhouse-1 clickhouse-client --user default --password otel \
  --query "DESCRIBE TABLE timeSeriesTags('observability','prometheus') FORMAT TSV"
```
