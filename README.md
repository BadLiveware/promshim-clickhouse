# ch-observability

Private HA playground for ClickHouse + OpenTelemetry observability.

Current storage split:
- logs/traces -> OTel-native ClickHouse tables
- metrics -> ClickHouse `TimeSeries` table (`observability.prometheus`) via Prometheus remote-write

## Quick start

```bash
./scripts/bootstrap-kind.sh
```

## Manual render/apply

```bash
helm template ch-observability-poc ./chart/ch-observability-poc --namespace example-namespace \
  | kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -f -

helm template ch-observability-cnpg ./chart/ch-observability-cnpg --namespace example-namespace \
  | kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -f -
```

## Useful access points

- ClickHouse: `http://localhost:8123`
- OTLP gRPC: `localhost:4317`
- OTLP HTTP: `localhost:4318`
- Collector health: `localhost:13133`
- Grafana: `http://localhost:3000` (`admin` / `admin`)
- CloudBeaver: `http://localhost:8978`

## Validation

```bash
kubectl -n example-namespace port-forward svc/clickhouse 8123:8123
```

Then query:

```bash
curl -u otel:otel 'http://127.0.0.1:8123/?query=SELECT%20count()%20FROM%20system.databases'
```

## Prometheus shim debug/explain

The Go promshim exposes the normal Prometheus-compatible endpoints:

- `/api/v1/query`
- `/api/v1/query_range`
- `/metrics`

It also exposes plan-only debug endpoints:

- `/api/v1/query_explain`
- `/api/v1/query_range_explain`

These return the parsed execution plan, chosen strategy, estimates, and fallback reasons without executing the query against ClickHouse.

Examples:

```bash
curl 'http://127.0.0.1:9090/api/v1/query_explain?query=sum%20by%20(job)%20(up)&time=300'

curl 'http://127.0.0.1:9090/api/v1/query_range_explain?query=sum%20by%20(job)%20(label_join(up,%20"joined",%20"/",%20"job",%20"namespace"))&start=0&end=300&step=30'
```

For side-by-side debugging, the normal query endpoints also accept `explain=1` (or `explain=true`). When present, the response keeps the normal Prometheus `status` + `data` payload and adds a top-level `plan` object. Default requests without `explain` are unchanged.

`/metrics` exposes Prometheus-format process-local rollout signals for shadow mode, including shadow comparison counters partitioned by status/category/compare-mode and served/shadow plan/eval duration histograms.

Examples:

```bash
curl 'http://127.0.0.1:9090/api/v1/query?query=1%20%2B%202&time=300&explain=1'

curl 'http://127.0.0.1:9090/api/v1/query_range?query=1%20%2B%202&start=0&end=120&step=60&explain=true'
```

## PromQL execution paths and support matrix

promshim currently has three execution paths:

- **Path 1 — whole-query delegation**: send the entire PromQL query to ClickHouse's native PromQL support
- **Path 2 — native SQL lowering**: lower supported PromQL fragments into repo-owned ClickHouse SQL
- **Path 3 — local execution**: evaluate in promshim's Go executor

The default rollout mode is configurable with:

- `PROM_SHIM_NATIVE_LOWERING_MODE=off|explain|shadow|prefer|force_supported`

Request-level `native_lowering_mode=...` still overrides the process default.

### Rollout mode behavior

| Rollout mode | Path 1 delegated | Path 2 native SQL | Path 3 local | Notes |
|---|---:|---:|---:|---|
| `off` | No | No | Yes | Local-only baseline |
| `prefer` | Yes | Yes | Yes | Normal adaptive mode |
| `explain` | Yes | Yes | Yes | Same planning freedom as `prefer`, but forces explain output |
| `shadow` | Shadow-only | Shadow-only | Yes | Serves local baseline and runs a native/delegated candidate in shadow |
| `force_supported` | No special preference | Yes, required | No fallback if root is not native | Errors unless the final root plan is `native_sql` |

### PromQL family support matrix

| PromQL family | Path 1 whole-query delegation | Path 2 native SQL | Path 3 local |
|---|---:|---:|---:|
| Simple selectors | Limited | Yes | Yes |
| Aggregations | No (today) | Yes, subset | Yes |
| Scalar/vector arithmetic | No (today) | Yes, subset | Yes |
| Vector-vector joins | No (today) | Yes, subset | Yes |
| Pointwise math/trig/date transforms | No (today) | Mostly yes | Yes |
| Scalar/date builtins (`time`, `pi`) | No | Yes | Yes |
| `scalar(v)` | No | Yes | Yes |
| `info(...)` | No | Yes, subset | Yes |
| Range/counter family | No | Broad supported subset | Yes |
| `quantile_over_time` | No | No, intentional keep-local | Yes |
| Sort family | No | No | Yes |
| `round` | No | No | Yes |
| `label_replace`, `label_join` | No | No | Yes |
| Histogram helper family | No | No | Yes |
| `absent`, `absent_over_time` | No | No | Yes |

### Path notes

#### Path 1 — whole-query delegation

This path is intentionally conservative today. It is mostly useful for simple whole-query delegation where ClickHouse can safely handle the entire query directly. Aggregation roots, subqueries, binary operators, and most function calls are currently excluded by the capability map.

#### Path 2 — native SQL lowering

This is the main native execution path in the repo today. Supported native families include:

- selector-backed roots
- pushdown-safe aggregations: `sum`, `count`, `min`, `max`, `avg`, `stddev`, `stdvar`, `quantile`, `group`
- source-expression transforms: `abs`, `ceil`, `floor`, `sgn`, `exp`, `ln`, `log2`, `log10`, `sqrt`, trig/hyperbolic functions, `deg`, `rad`, `timestamp`, date/time extractors, clamp family with literal bounds
- synthetic scalar/date builtins: `time()`, `pi()`, zero-arg date functions
- `scalar(v)`
- `info(...)` for the supported single-info-metric subset
- range/counter/window family: `last_over_time`, `sum_over_time`, `avg_over_time`, `min_over_time`, `max_over_time`, `count_over_time`, `stddev_over_time`, `stdvar_over_time`, `present_over_time`, `mad_over_time`, `rate`, `irate`, `increase`, `delta`, `idelta`, `changes`, `deriv`, `resets`, `predict_linear`, `double_exponential_smoothing`, `holt_winters`

Intentional exception:

- `quantile_over_time` remains local-only by design

#### Path 3 — local execution

This is the broadest support surface and the fallback path for supported queries that are not delegated or natively lowered. In addition to the native-supported subset, local-only support currently covers families such as:

- sort functions
- `round`
- `label_replace`, `label_join`
- histogram helpers: `histogram_quantile`, `histogram_count`, `histogram_sum`, `histogram_avg`, `histogram_fraction`
- `vector`
- `absent`, `absent_over_time`
- `quantile_over_time`
