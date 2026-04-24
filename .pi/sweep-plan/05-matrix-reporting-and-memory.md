# 05 — Matrix reporting, ProfileEvents, and memory trade-offs

## Goal

Make sweep output decision-useful. Users should be able to compare transports,
profiles, densities, and execution modes from one named run, including latency,
strategy, ClickHouse ProfileEvents, and memory trade-offs relevant to future CBE
choices.

## Dependencies

- [`02-bench-runner-schema-and-modes.md`](02-bench-runner-schema-and-modes.md)
- [`04-sweep-orchestrator-and-estimates.md`](04-sweep-orchestrator-and-estimates.md)

## Scope

### In scope

- Matrices from sweep manifests.
- Category/query/transport/mode/profile/density views.
- ProfileEvents joins from benchmark ClickHouse.
- Memory summary and detailed artifacts.
- CBE-focused interpretation of native SQL vs tier 3 vs local/off.
- Summary Markdown and machine-readable JSON.

### Out of scope

- Implementing CBE routing.
- Changing execution semantics.
- Making dense wall-clock baselines hard gates by default.

## Matrix views

Required views:

1. Category matrix.
2. Query matrix.
3. Transport matrix: `http` vs `native` for same profile/density/mode.
4. Mode matrix: `prefer` vs `force_supported` vs `off`.
5. Profile matrix: `7d`, `30d`, `1y` for same density/mode/transport.
6. Density matrix: `sparse` vs `dense` for same profile/mode/transport.
7. Compliance matrix: transport x compliance mode.
8. Memory matrix: execution mode x memory domain.

Columns should include, where available:

```text
Prom p50/p95
Shim p50/p95
Shim/Prom ratio
Strategy
Fallback reason
CH round trips
CH millis
CH memory p50/max
SelectedRows
SelectedBytes / ReadCompressedBytes
FunctionExecute
MemoryTrackerUsage
Shim heap delta
Shim RSS delta
Container current/peak
Prom target band
```

For category rows, default to median within each category/profile bucket and
include count. Query-level matrix should show exact rows.

## ProfileEvents capture

Use benchmark ClickHouse only. Never query compliance ClickHouse for benchmark
ProfileEvents.

Required ClickHouse signals:

- `query_duration_ms` p50/p90/max;
- `memory_usage` p50/p95/max;
- `read_rows` / `read_bytes`;
- `result_rows`;
- `ProfileEvents` map, especially:
  - `SelectedRows`;
  - `SelectedBytes`;
  - `ReadCompressedBytes`;
  - `FunctionExecute`;
  - `MemoryTrackerUsage`;
  - array/function counters when present.

Join on stable `X-Promshim-Log-Comment` names where possible. Fall back to
normalized query only for legacy artifacts.

## Memory trade-off measurement

Add:

```text
--memory off|summary|detailed
```

Default:

```text
--memory summary
```

### Why separate domains

Native SQL can shift memory into ClickHouse. Tier 3/4 can shift memory into
promshim. A single merged number hides the trade-off. Report domains separately:

| Domain | Captures | Signal |
|---|---|---|
| ClickHouse query memory | native SQL, delegated PromQL, subtree pushdown | `system.query_log.memory_usage`, `ProfileEvents.MemoryTrackerUsage` |
| promshim process memory | planning, decode, local executor, response construction | Go runtime metrics and process RSS snapshots |
| container memory | coarse promshim footprint | cgroup current/peak when available |

### Summary mode

For `--memory summary`:

- serialize memory-sensitive comparisons;
- capture CH memory from benchmark `system.query_log`;
- capture promshim heap/RSS around each query/mode group;
- capture cgroup current/peak when available;
- write `memory-summary.json` beside bench artifacts;
- add memory columns to mode matrices.

Example artifact:

```text
harness/artifacts/sweeps/<run-name>/memory/native/sparse/7d/memory-summary.json
```

### Detailed mode

For `--memory detailed`:

- default to a smaller selected query set unless user requests all;
- optionally force GC before measured groups and record that fact;
- capture pprof heap/allocs snapshots;
- write artifacts like:

```text
harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/heap-before.pb.gz
harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/heap-after.pb.gz
harness/artifacts/sweeps/<run-name>/memory/native/dense/7d/<query>/<mode>/allocs.pb.gz
```

### Interpretation for CBE

Mode matrix should make it easy to ask:

> For this query/profile/density, did native SQL reduce promshim memory enough to
> justify any ClickHouse memory increase, or would local execution be a better
> small-data choice?

Interpretation rules:

- Native mode memory = ClickHouse query memory + promshim overhead.
- Tier 3 memory = subtree ClickHouse memory + promshim local executor memory.
- Tier 4/off memory = mostly promshim memory plus storage fetch overhead.
- Go heap deltas are noisy due to GC/reuse; use relative comparisons within the
  same sweep, not absolute guarantees.
- RSS may not shrink after a query; report peak/current and deltas without
  over-interpreting flat deltas.
- Memory reports are advisory unless the user supplies an explicit local memory
  baseline.

## Summary output

Write:

```text
harness/artifacts/sweeps/<run-name>/summary.md
harness/artifacts/sweeps/<run-name>/summary.json
```

Summary should include:

- selected axes;
- stack endpoints;
- seed state;
- compliance status;
- benchmark status;
- top slow queries;
- target-band status;
- strategy changes/flaps;
- memory trade-off highlights;
- links to detailed matrix files.

## Script updates

Update `scripts/bench-matrix.sh` to read either:

```bash
./scripts/bench-matrix.sh 7d:path/to/report.json 30d:path/to/report.json
```

or:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json
```

## Implementation tasks

1. Normalize v2 bench reports into a matrix input model.
2. Add sweep manifest reader to matrix tooling.
3. Generate category and query matrices.
4. Generate axis comparison matrices.
5. Join ProfileEvents captures by log comment.
6. Add memory summary capture and artifact writing.
7. Add detailed pprof capture mode.
8. Add memory columns to matrices.
9. Generate top-level summary Markdown and JSON.
10. Add tests for matrix aggregation and median behavior.

## Validation

```bash
./scripts/run-sweep.sh --preset default --format both
./scripts/bench-matrix.sh --sweep harness/artifacts/sweeps/<run-name>/manifest.json --per-query
./scripts/run-sweep.sh heavy --memory summary --name memory-heavy
./scripts/run-sweep.sh --suite bench --profile 7d --density sparse --mode prefer,force_supported,off --memory detailed --name memory-detail
```

## Risks

- Memory numbers can be over-interpreted if collapsed into one total.
- cgroup peak may not be available on all systems.
- pprof capture can perturb timing.
- Matrix output can become too large for full sweeps.

## Exit criteria

- Matrices can be generated from a sweep manifest.
- Mode matrix shows latency, strategy, ProfileEvents, and memory domains.
- Memory summaries are written for `--memory summary`.
- Detailed heap artifacts are written for `--memory detailed`.
- Summary output helps evaluate native vs tier 3/local trade-offs without
  opening raw JSON.
