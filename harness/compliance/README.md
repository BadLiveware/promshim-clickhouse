# PromQL Compliance Harness

Runs the upstream Prometheus PromQL compliance suite against promshim and reference Prometheus, both backed by the same scraped fixture.

## Layout

- `docker-compose.yml` — ClickHouse 26.3, Prometheus 2.53.0 (reference), promshim.
- `prom-compliance/` — submodule; upstream `prometheus/compliance` tester.
- `test-promshim.yml` — tester config (endpoints, query window, tweaks).
- `scripts/run-compliance.sh` — runs the suite, emits JSON report to `artifacts/`.
- `scripts/classify-failures.sh` — buckets failures by pattern (regex-matched in the script).

## Running

```
cd harness/compliance
docker compose up -d
scripts/run-compliance.sh
scripts/classify-failures.sh artifacts/compliance-report-<stamp>.json
```

The tester hits `29090` (Prom reference) and `29091` (promshim) with a pinned end_time inside the scraped fixture window.

## Current state — 536/539 passing (99.4%)

The remaining 3 failures are documented below. All are tracked as **known limitations** rather than bugs in the shim.

### 1. topk tie-break ordering (1 failure)

**Query:** `topk without(instance) (2, demo_memory_usage_bytes)`

At one timestep, `instance=10001` and `instance=10002` both have value `173015040` (exact tie). Prometheus keeps `10002`; the shim keeps `10001`.

**Root cause.** Prom's `topk` heap uses strict `<` on value (`engine.go:3865`), so first-seen wins on tie. Iteration is `for si := range inputMatrix` — TSDB series-ref order, determined by scrape discovery time. For this fixture the observed order at that step is `(10002, 10000, 10001)`.

Neither `labels.Hash()` nor `labels.StableHash()` reproduces that order — both give alphabetical `(10000, 10001, 10002)`. No label-derivable tie-break can match exactly; reproducing it would require mirroring Prom's storage layer.

**Impact.** 1/539 = 0.2%. Cosmetic — affects only exact-value ties, which are rare in real data.

### 2. Nested subquery timeouts (2 failures)

**Queries:**
- `avg_over_time(rate(demo_cpu_usage_seconds_total[1m])[2m:10s])` — ~11s
- `max_over_time((time() - max(demo_batch_last_success_timestamp_seconds) < 1000)[5m:10s] offset 5m)` — ~19s

Both return correct results but exceed the compliance tester's hard-coded 10s HTTP timeout (`comparer.go:78`).

**Root cause.** These go through the delegated path — the shim issues one request to ClickHouse's `prometheusQueryRange`, and the work happens there. Nested subqueries are inherently O(outer_steps × inner_steps); CH's experimental PromQL engine evaluates the inner expression per subquery step without batching.

**Impact.** 2/539 = 0.4%. Correctness is fine; only timing-sensitive.

**Expected to retire.** Consistent with the shim's strategic intent (bridge, not destination): these go away as ClickHouse's PromQL implementation matures — no shim code needs to change.

## Fixture

Prometheus scrapes `demo.promlabs.com:10000..10002` into ClickHouse via the Kafka/ingest path. Fixture window is frozen — `docker compose stop prometheus` pins it so compliance runs are reproducible. Adjust `end_time` in `test-promshim.yml` if the fixture is refreshed.
