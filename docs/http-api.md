# HTTP API


Promshim currently implements these HTTP surfaces:

| Endpoint | Purpose |
|---|---|
| `GET`, `POST /api/v1/query` | Prometheus instant query API |
| `GET`, `POST /api/v1/query_range` | Prometheus range query API |
| `GET`, `POST /api/v1/labels` | Label-name metadata over the ClickHouse `TimeSeries` table |
| `GET /api/v1/label/{name}/values` | Label-value metadata |
| `GET`, `POST /api/v1/series` | Series metadata |
| `GET`, `POST /api/v1/query_explain` | Plan-only instant-query explain output |
| `GET`, `POST /api/v1/query_range_explain` | Plan-only range-query explain output |
| `GET`, `POST /api/v1/format_query` | Prometheus query formatting helper |
| `GET`, `POST /api/v1/parse_query` | Prometheus query parse-tree helper |
| `GET /api/v1/metadata` | Compatibility endpoint; returns empty metadata |
| `GET /api/v1/targets` | Compatibility endpoint; returns empty target sets |
| `GET /api/v1/rules` | Compatibility endpoint; returns empty rule groups |
| `GET /api/v1/alerts` | Compatibility endpoint; returns empty alerts |
| `OPTIONS /*` | Compatibility preflight handler |
| `GET /metrics` | Prometheus-format promshim process/shadow-mode metrics |
| `GET /health`, `GET /-/healthy` | Process health probes |
| `GET /-/ready` | Readiness probe; returns 200 only when promshim can run a lightweight ClickHouse query |

`POST` endpoints accept Prometheus-compatible `application/x-www-form-urlencoded` parameters in the request body; form parameters take precedence over duplicate URL query parameters, matching Go/Prometheus form parsing behavior.

Normal query responses use the Prometheus response envelope:

```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": []
  }
}
```

Successful query responses also include advisory headers:

- `X-Promshim-Strategy` — root execution strategy, such as `delegated_promql`,
  `native_sql`, `local`, `chunked_local`, or `chunked_native`. Native range
  auto-chunking uses `chunked_native` so benchmark and response headers make the
  resource-safety path visible; explain output also includes the selected
  `chunkPointsPerSeries` for range chunk plans.
- `X-Promshim-Fallback-Reason` — why a lower-priority strategy was used, when
  available.
- `X-Promshim-CH-Roundtrips` — ClickHouse request count observed while serving
  the HTTP request.
- `X-Promshim-CH-Millis` — cumulative ClickHouse request time observed by
  promshim.

`explain=1` or `explain=true` on the normal query endpoints keeps the regular
Prometheus `data` payload and adds a top-level `plan` object. The dedicated
`*_explain` endpoints return the plan without executing the query.
