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
| Aggregations | No (today) | Yes | Yes |
| Scalar/vector arithmetic | No (today) | Yes | Yes |
| Vector-vector joins | No (today) | Yes | Yes |
| Pointwise math/trig/date transforms | No (today) | Yes | Yes |
| Scalar/date builtins (`time`, `pi`) | No | Yes | Yes |
| `scalar(v)` | No | Yes | Yes |
| `info(...)` | No | Yes | Yes |
| Range/counter family | No | Yes | Yes |
| `quantile_over_time` | No | Yes | Yes |
| Sort family | No | Yes | Yes |
| `round` | No | Yes | Yes |
| `label_replace`, `label_join` | No | Yes | Yes |
| Histogram helper family | No | Yes | Yes |
| `absent`, `absent_over_time` | No | Yes | Yes |

### Path notes

#### Path 1 — whole-query delegation

This path is intentionally conservative today. It is mostly useful for simple whole-query delegation where ClickHouse can safely handle the entire query directly. Aggregation roots, subqueries, binary operators, and most function calls are currently excluded by the capability map.

#### Path 2 — native SQL lowering

This is the main native execution path in the repo today. For the current ClickHouse `TimeSeries` storage model and the repo's vendored Prometheus parser surface, the Path 2 compliance matrix is now fully green: the repo-owned native SQL lowering path covers the previously open aggregation, join/set, range/counter/subquery, histogram-helper, sort, round, label-mutation, absent, info, and function-cleanup gaps.

Two validation notes remain worth calling out explicitly:

- the main native-only checkpoint corpora are now green again after the final closure work, including `native-lowering-starter.json`, `common-dashboard-subset.json`, and the targeted themed/native-only corpora used to close the last feature slices
- the current reference Prometheus image used by the harness still rejects `info(...)` and `histogram_quantiles(...)` at parse time, so those two functions cannot be differential-compared in that environment even though repo tests and the compliance matrix are green

#### Path 3 — local execution

This remains the broadest execution surface operationally because it is still the fallback when whole-query delegation is disabled/rejected and when a request is intentionally served locally (for example in rollout baselines and shadow comparisons). However, the previous family-level native-SQL gaps are now closed; Path 3 remains primarily the compatibility/fallback executor rather than the home of a large local-only PromQL feature set.
