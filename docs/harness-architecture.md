# Harness architecture

Promshim's harness tooling has two jobs:

1. Keep correctness and performance validation easy to run from the repository root.
2. Keep the policy and report logic testable enough that validation artifacts can be trusted during optimization work.

The public entrypoints remain shell scripts because they coordinate Docker Compose, local locks, cleanup traps, and external tools. The reusable logic behind those entrypoints should live in Go when it parses reports, applies policy, builds artifact schemas, or makes decisions that need unit tests.

## Command responsibilities

| Command | Primary responsibility | Stack / endpoints | Artifact outputs |
|---|---|---|---|
| `scripts/run-compliance.sh` | Start the frozen compliance stack, run prefer-mode compliance, optionally run native-only gap reporting, and tear down unless `--keep-up` is set. | Compliance stack: Prometheus `:29090`, promshim `:29091`, ClickHouse HTTP `:28123`, remote write `:29092/write`. | `harness/compliance/artifacts/compliance-report-{prefer,native}-*.json`; terminal reconciliation and gap summaries. |
| `scripts/run-sweep.sh` | Run the combined compliance plus benchmark workflow, including benchmark stack setup/status, seed policy checks, benchmark execution, and sweep artifact assembly. | Benchmark stack: Prometheus `:29190`, promshim `:29191`, ClickHouse HTTP `:28124`, ClickHouse remote write `:29192/write`. Compliance pass delegates to the compliance stack above. | `harness/artifacts/sweeps/<run-name>/manifest.json`, `summary.json`, `summary.md`, benchmark reports, memory summaries, optional profile artifacts. |
| `scripts/run-bench.sh` | Run benchmark corpora against configured Prometheus/promshim endpoints and optionally collect memory and ClickHouse profile artifacts. | Defaults to the compliance stack for short fixture benchmarks; sweep passes benchmark-stack endpoints for long-range/dense data. | v2 benchmark reports, `memory-summary-*.json`, optional `memory-detail*/manifest.json`, `clickhouse-profile-*.json`, `clickhouse-profile-*.md`, and `profiles/...` per-query details. |
| `scripts/seed-long-range.sh` | Seed long-range benchmark data into the benchmark stack via `cmd/ch-seed-long`. | Benchmark Prometheus and ClickHouse write endpoints. | Seed markers in both backends; command log output. |
| `scripts/bench-matrix.sh` | Render Markdown matrix views from benchmark reports or sweep manifests. | No stack access. | Markdown to stdout. |
| `scripts/ch-explain.sh` and profile helpers | Capture query-log and explain artifacts for focused SQL diagnosis. | Caller-selected ClickHouse and promshim endpoints, commonly compliance stack ports. | `harness/artifacts/ch-explain/...` and profile JSON/Markdown artifacts. |

## Stack isolation

The compliance and benchmark stacks are intentionally separate.

| Service | Compliance endpoint | Benchmark endpoint |
|---|---:|---:|
| Prometheus | `http://localhost:29090` | `http://localhost:29190` |
| promshim | `http://localhost:29091` | `http://localhost:29191` |
| ClickHouse HTTP | `http://localhost:28123` | `http://localhost:28124` |
| ClickHouse remote write | `http://localhost:29092/write` | `http://localhost:29192/write` |

Benchmark reset operations must only affect benchmark volumes. The compliance fixture is frozen correctness data and must not be reused for long-range or dense benchmark seeding.

## Shell-owned behavior

Shell remains the right place for workflow orchestration that is mostly process control:

- Docker Compose lifecycle and compose-file selection.
- Lock acquisition through `scripts/lib/run-lock.sh`.
- Cleanup traps for local stacks.
- User-facing compatibility wrappers and help text.
- Launching Go binaries and external tools with the correct environment.
- Destructive benchmark reset confirmation and volume deletion.

Shell should avoid owning policy or complex report transformations when the behavior can be expressed and tested in Go.

## Go-owned behavior

Go should own harness behavior that needs stable schemas, policy tests, or careful data handling:

- Benchmark report parsing and normalization.
- Sweep manifest and summary construction.
- Artifact discovery and relative-path normalization.
- ClickHouse query-log/ProfileEvents summarization.
- Native SQL and processor profile artifact indexing.
- Benchmark matrix rendering.
- Compliance expected-failure reconciliation.
- Native-only compliance gap classification.
- Profile, density, corpus, and seed-policy validation where the same rules are needed in more than one workflow.

This boundary keeps scripts readable while making validation policy reviewable through unit tests.

## Public artifact contracts

These files are consumed by developers and review workflows and should be preserved or explicitly versioned when changed.

### Sweep artifacts

Directory:

```text
harness/artifacts/sweeps/<run-name>/
```

Important files:

| Artifact | Contract |
|---|---|
| `manifest.json` | Machine-readable run manifest containing run name, artifact directory, selected axes, endpoints, report paths, memory artifacts, ClickHouse profile summaries, and provenance. Paths should be relative to the repository root. |
| `summary.json` | Machine-readable rollup with counts, strategy histograms, profile/memory/profile-artifact counts, and selected run labels. |
| `summary.md` | Human-readable run summary with selected axes, status, report links, strategy/routing highlights, and slow-row summaries when reports are present. |
| `bench-report-*.json` | v2 benchmark report emitted by `cmd/promshim-bench`; includes row-level timings, strategies, routing metadata, run labels, and memory mode. |
| `memory-summary-*.json` | ClickHouse query-log/ProfileEvents and promshim metrics snapshot correlated to benchmark run labels. |
| `memory-detail*/manifest.json` | Whole-run pprof snapshot manifest for detailed memory mode. |
| `clickhouse-profile-*.json` | Per-query ClickHouse profile summary keyed by benchmark rows and log comments. JSON keeps total counters and per-execution percentile counters distinct. |
| `clickhouse-profile-*.md` | Human-readable ClickHouse profile highlights using per-execution counters. |
| `profiles/<query>__<mode>__<policy>/...` | Per-query profile details such as native SQL samples, query-log summaries, and optional processor rollups. Paths referenced from JSON should stay relative. |

### Compliance artifacts

Directory:

```text
harness/compliance/artifacts/
```

Important files:

| Artifact | Contract |
|---|---|
| `compliance-report-prefer-*.json` | Prefer-mode differential report. This is the gating correctness report and is reconciled against `harness/compliance/expected-failures.json`. |
| `compliance-report-native-*.json` | Native-only informational report. Gaps are categorized and kept visible; they are not allowlisted. |
| Patched query corpus | Generated compatibility corpus for Prometheus 3.x compliance runs. |

Compliance expected-failure policy is intentionally narrow. Allowlist behavior should be covered by tests before any implementation changes to reconciliation logic.

## Reliability rules

- Treat artifact schemas as reviewable contracts.
- Prefer additive schema changes with `schemaVersion` or backward-compatible readers.
- Keep relative artifact paths in generated JSON and Markdown.
- Preserve the distinction between per-execution metrics and totals in ClickHouse profile output.
- Keep prefer-mode compliance as the correctness gate.
- Keep native-only compliance gap reporting informational.
- Keep benchmark stack status, seeding, and reset operations isolated from compliance fixtures.
- Use run labels/log comments when correlating benchmark rows with ClickHouse `system.query_log` so old or manual queries are not mixed into summaries.

## Migration approach

The safest migration is behavior-preserving extraction:

1. Add Go packages/commands that reproduce existing script-generated artifacts from fixtures.
2. Add tests for existing schemas and policy decisions.
3. Update scripts to delegate one responsibility at a time.
4. Run focused script syntax checks and workflow smoke tests after each extraction.
5. Only then consider UX or schema improvements.

This keeps the current developer commands stable while reducing the amount of policy and reporting code embedded in shell.
