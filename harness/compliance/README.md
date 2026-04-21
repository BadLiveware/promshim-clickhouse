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

**Root cause.** These queries hit **rung 3** (native-SQL matrix source + local execution): the whole-query classifier rejects any root that isn't a plain selector, so `max_over_time(...)[5m:10s]` and `avg_over_time(rate(...)[2m:10s])` fall through to subtree-plus-local. The Go planner fans out an outer evaluation per step, each invoking an inner subquery evaluation per inner step — O(outer × inner) shim-issued ClickHouse queries. Profiling a single `avg_over_time(rate(...)[2m:10s])` run showed ~793 ClickHouse requests issued by the shim; the slowness is on **our** side, not ClickHouse's. Correctness is fine because each fan-out call evaluates the correct inner window; only the issue count and round-trip cost blow the compliance tester's budget.

**Impact.** 2/539 = 0.4%.

**Expected to retire along two axes:**
1. **Shim-side (rung 2 coverage):** teach native SQL to transpile these subquery shapes into a single ClickHouse query (no fan-out). Aligns with the shim's strategic intent — move queries from rung 3 (subtree+local) up to rung 2 (native SQL).
2. **Upstream (rung 1 coverage):** as ClickHouse's `prometheusQueryRange` verifies parity on these constructs, the whole-query classifier adds them to the allowlist and they graduate to rung 1 — the shim stops touching them.

## Fixture

Prometheus scrapes `demo.promlabs.com:10000..10002` into ClickHouse via the Kafka/ingest path. Fixture window is frozen — `docker compose stop prometheus` pins it so compliance runs are reproducible. Adjust `end_time` in `test-promshim.yml` if the fixture is refreshed.
