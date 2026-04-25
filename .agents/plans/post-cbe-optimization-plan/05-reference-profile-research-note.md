# Stage 05 research note: reference ClickHouse deployment profile

This note summarizes first-party sources to use for `05-reference-clickhouse-deployment-profile.md`, plus concrete guidance on what to adopt, what to avoid, and what assumptions to state explicitly.

## First-party source set (authoritative)

- ClickHouse Operator overview:
  - https://clickhouse.com/docs/clickhouse-operator/overview
- Official operator repository:
  - https://github.com/ClickHouse/clickhouse-operator
- Operator example manifests / chart defaults:
  - https://github.com/ClickHouse/clickhouse-operator/blob/main/examples/minimal.yaml
  - https://github.com/ClickHouse/clickhouse-operator/blob/main/examples/custom_configuration.yaml
  - https://github.com/ClickHouse/clickhouse-operator/blob/main/dist/chart/values.yaml
- ClickHouse settings docs:
  - Session/user settings: https://clickhouse.com/docs/operations/settings/settings
  - Server settings: https://clickhouse.com/docs/operations/server-configuration-parameters/settings
- Explainability / planning:
  - EXPLAIN: https://clickhouse.com/docs/sql-reference/statements/explain
- Query/runtime observability surfaces:
  - query_log: https://clickhouse.com/docs/operations/system-tables/query_log
  - query_thread_log: https://clickhouse.com/docs/operations/system-tables/query_thread_log
  - processors_profile_log: https://clickhouse.com/docs/operations/system-tables/processors_profile_log
  - processes: https://clickhouse.com/docs/operations/system-tables/processes

Non-first-party sources (Altinity, blogs, vendor posts) can be supporting context only, not normative guidance.

---

## What to copy into stage 05

### 1) Structure and separation model
Copy this exact documentation split:

- **Operator/deployment profile (stage 05):** advisory infra guidance.
- **Promshim-owned per-query/session settings (stage 03):** bounded allowlist, version-aware, explain-visible.
- **CBE route selection and candidate policy:** separate from server tuning.

This preserves the project rule that correctness and safe routing cannot depend on hidden global server tuning.

### 2) Observability minimum for reproducible claims
Adopt a minimum evidence contract rooted in `system.query_log` + `query_thread_log` + `processors_profile_log` + EXPLAIN:

- stable query correlation keys (`query_id` and/or bounded log comment);
- per-query duration, memory, read rows/bytes, ProfileEvents;
- plan/pipeline snapshots for claim-specific changes;
- explicit linkage from sweep artifact -> promshim request metadata -> ClickHouse logs.

### 3) Operator profile as a benchmark assumption, not requirement
For stage 05 wording, copy this stance:

- recommendations are **advisory** unless correctness truly requires otherwise;
- benchmark output must label the environment/profile used;
- deviations from reference profile should be expected to shift performance results.

### 4) Conservative defaults posture
Copy a conservative posture for deployment guidance:

- prefer predictable caps and bounded concurrency over max-throughput-first defaults;
- describe parallelism vs contention tradeoffs for dashboard latency;
- keep distributed guidance explicitly caveated if measured evidence is single-node only.

---

## What to avoid in stage 05

1. **Do not turn stage 05 into a tuning cookbook of unverified “best settings.”**
   Every recommended setting needs provenance and expected measurable signal.

2. **Do not blur server settings vs session settings.**
   If promshim can set it per query/session, it belongs in stage 03 contracts, not implicit stage 05 dependence.

3. **Do not claim portability of performance without profile disclosure.**
   Avoid language implying benchmark numbers are universal across storage/cache/concurrency shapes.

4. **Do not rely on non-first-party operator docs as authority.**
   Altinity and ecosystem material can inspire checks, but recommendations should cite ClickHouse docs/operator repo first.

5. **Do not introduce correctness coupling to global tuning.**
   Stage 05 must not imply that certain global server settings are mandatory for semantic correctness.

6. **Do not overstate distributed behavior unless validated.**
   Mark distributed fanout/join/aggregation guidance as deferred or conditional when evidence is missing.

---

## Assumptions to state explicitly in stage 05

Add an assumptions block that includes:

1. **Workload shape assumptions**
   - read-heavy PromQL API traffic;
   - mixed short dashboard and long-range analytical queries;
   - ingestion/merge pressure concurrent with reads.

2. **Topology assumptions**
   - whether evidence is single-node or distributed;
   - coordinator-vs-replica request path expectations.

3. **Storage/cache assumptions**
   - local SSD expectation and retention behavior;
   - cache warm/cold state implications;
   - any query-cache caution for PromQL freshness semantics.

4. **Version/operator assumptions**
   - ClickHouse version family used for measurements;
   - operator version/chart defaults referenced.

5. **Evidence assumptions**
   - required logs/tables enabled for profile events and query correlation;
   - sweep artifact naming and isolation discipline (`run-sweep.sh`, benchmark stack only).

6. **Scope assumptions**
   - profile is a recommended starting point, not a correctness contract;
   - promshim still defaults to strict-safe routing behavior when cost/confidence/cap signals are insufficient.

---

## Suggested insertion points in `05-reference-clickhouse-deployment-profile.md`

- Under **Requirements**: add a bullet requiring first-party-source precedence and explicit evidence level tags (`required`, `advisory`, `benchmark-reference-only`).
- Under **Implementation task 2/3/4**: add explicit mapping from each recommendation to validation signal (`query_log`, ProfileEvents, EXPLAIN, sweep delta).
- Under **Validation tasks**: add a checklist item that all deployment recommendations include source URL + evidence level + known non-goals.

---

## Ready-to-use evidence-level labels

Use these tags per recommendation row/item:

- `required-for-correctness` (rare; must cite code path/semantic dependency)
- `advisory-for-performance`
- `benchmark-reference-only`
- `experimental-not-default`

This keeps stage 05 reviewable and prevents accidental coupling between deployment tuning and promshim correctness policy.
