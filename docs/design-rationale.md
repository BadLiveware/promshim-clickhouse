# Design rationale and trade-offs


Prometheus is excellent at scraping, PromQL, alerting, and the day-to-day
operator workflow around metrics. The hard parts are HA and long-term storage.
For this use case, the awkward workload is **infrequent, large queries**: a
Thanos-style deployment can either keep enough query/store resources running to
answer those queries quickly, which wastes resources while idle, or scale down
aggressively and then have too little query capacity when a large historical
query finally arrives. Other Prometheus-compatible long-term stores solve parts
of this problem, but they still add another metrics storage/query system to run.

This repo explores a different shape: **ClickHouse as the only metrics store**.
The hope is that ClickHouse can own both hot analytical queries and long-term
retention, including native TTLs and object-storage offload, without keeping a
parallel Prometheus+Thanos or Prometheus+ClickHouse storage stack alive.

ClickHouse's `TimeSeries` and PromQL support are still limited compared with
Prometheus itself. Promshim exists to close that read-side compatibility gap: it
lets normal Prometheus dashboards, alert queries, and tools talk to a
Prometheus-compatible API while the underlying samples live only in ClickHouse.
That design chooses an explicit set of trade-offs:

### Benefits

- **Keeps one metrics store.** ClickHouse is the target system of record instead
  of a sidecar analytics copy beside Prometheus or Thanos.
- **Preserves existing PromQL/Grafana workflows** while moving storage and
  long-term retention toward ClickHouse.
- **Keeps correctness measurable** with differential and upstream compliance
  harnesses.
- **Allows gradual rollout** through `prefer`, `shadow`, and `force_supported`
  modes.
- **Can retire shim code incrementally** as ClickHouse whole-query PromQL support
  grows.
- **Fits bursty historical querying better.** The target workload is long idle
  periods punctuated by large queries, where ClickHouse can use OLAP execution
  and storage-tiering features instead of keeping a distributed TSDB query mesh
  hot at all times.
- **Uses ClickHouse where it is strong**: large scans, aggregations, label
  filtering, retention, and object-storage-backed long-range data.

### Costs and limitations

- **It emulates Prometheus semantics over a different storage engine.** Some
  Prometheus behavior depends on TSDB implementation details rather than pure
  PromQL semantics.
- **ClickHouse `TimeSeries` is experimental.** Upstream schema/function changes
  can affect promshim.
- **Not every Prometheus API is implemented.** Promshim serves query and
  metadata endpoints, not scraping, recording rules, alerting, federation,
  admin APIs, or remote write.
- **Performance varies by query shape.** Native SQL is often the goal, but some
  shapes are still slower than Prometheus or require careful SQL-shape work.
- **Local fallback is for safety, not product direction.** New feature work
  should expand whole-query delegation or native SQL lowering, not the lower
  fallback tiers.
- **Operational maturity is intentionally PoC-grade.** This repository is a
  migration playground and validation harness, not a packaged production
  distribution.
