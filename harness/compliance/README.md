# PromQL Compliance Harness

Runs the upstream Prometheus PromQL compliance suite against promshim and reference Prometheus, both backed by the same deterministic remote-write fixture.

A full two-pass run (prefer + native-only) finishes in ~15 seconds on a warm docker cache. Runs in the foreground; no minutes-long timeout needed.

## Layout

- `docker-compose.yml` — ClickHouse 26.3, Prometheus 3.5.2 (LTS, reference), promshim.
- `docker-compose.native-only.yml` — override that forces promshim into `native_lowering_mode=force_supported`; unsupported shapes fail explicitly instead of falling back to local evaluation. Used for pass #2.
- `prom-compliance/` — submodule; upstream `prometheus/compliance` tester.
- `test-promshim.yml` — tester config (endpoints, query window, tweaks).
- `scripts/run-compliance.sh` — runs one pass against whatever promshim is up; emits JSON reports under `../../harness/artifacts/compliance/` and reconciles against the allowlist (skipped in `--mode native`). When no artifact directory is provided, low-level helper scripts read/write `../../harness/artifacts/compliance/latest`.
- `scripts/patch-queries-for-prom3.py` — generates `patched-queries.yml` in the selected compliance artifact directory from the upstream corpus, dropping `should_fail: true` markers on entries Prom 3.x now accepts (UTF-8 label names). Upstream's corpus was last refreshed for Prom 2.26, so the tester would otherwise hard-abort at `comparer.go:95` the moment such a query returns success.
- `scripts/reconcile-expected.sh` — matches the report against `expected-failures.json`; any drift fails.
- `scripts/classify-failures.sh` — buckets failures by pattern (regex-matched).
- `scripts/native-gap-report.sh` — categorized breakdown of a native-mode report (diff failures, unsupported-root shapes, other errors). Informational only; never gates.
- `../../scripts/run-compliance.sh` — top-level runner: brings the stack up, seeds or verifies the deterministic fixture, runs pass #1 (prefer) and pass #2 (native-only), tears down.

## Running

Two-pass run from the repo root:

```
scripts/run-compliance.sh
```

That brings the stack up, writes reports to `../../harness/artifacts/compliance/<timestamp>/`, updates `../../harness/artifacts/compliance/latest` when possible, seeds the deterministic fixture into fresh Prometheus and ClickHouse volumes, verifies the expected fixture counts through both APIs, runs pass #1 against the default `prefer` mode (reconciled against `expected-failures.json`), recreates promshim with the `native-only` override, runs pass #2 (informational gap report), and tears the stack down.

Single pass from this directory (stack must already be up):

```
cd harness/compliance
docker compose up -d
scripts/run-compliance.sh --mode prefer --suffix prefer
scripts/classify-failures.sh ../../harness/artifacts/compliance/latest/compliance-report-prefer-<stamp>.json
```

The tester hits `29090` (Prom reference) and `29091` (promshim) with a pinned `end_time` inside the deterministic fixture window.

## Philosophy: gaps stay visible

The shim's native-SQL path is under active development. Any PromQL shape the shim doesn't yet handle is a **gap**, not an "expected failure." Gaps must stay visible so we keep pressure on ourselves to close them.

The allowlist (`expected-failures.json`) is reserved strictly for failures that are **not** shim gaps — failures driven by reference-side implementation details we can't reproduce exactly (e.g., Prometheus's TSDB iteration order leaking into `topk` tie-breaks). If you're tempted to add a shim-side limitation, don't; let it fail loudly until it's fixed.

## Two passes

Each full run does two passes against the same frozen fixture:

1. **`prefer` mode (gated)** — the default runtime lowering mode. The shim lowers whatever it can to native SQL and falls back to local evaluation for everything else. Reconciled against `expected-failures.json`; any unexpected failure exits non-zero.
2. **`native-only` mode (informational)** — `PROM_SHIM_NATIVE_LOWERING_MODE=force_supported`. Unsupported shapes return an explicit error instead of falling back. Produces a categorized gap inventory via `native-gap-report.sh`. Never gates — the numbers are the work queue, and they trend down as native lowering coverage grows.

## Allowlist (`expected-failures.json`)

No deviations are currently accepted for the deterministic compliance fixture.

The allowlist remains intentionally available for narrow cases where exact Prometheus behavior depends on reference-side storage internals or an immaterial primitive-level difference. Entries must be exact and specific; stale allowlist entries fail reconciliation so a missing fixture cannot make CI pass vacuously.

## Known gaps (visible; not allowlisted)

These surface as failures every run. They're tracked openly here, not in `expected-failures.json`:

### UTF-8 label-name destinations in `label_replace` / `label_join`

- `label_replace(demo_num_cpus, "~invalid", "", "src", "(.*)")`
- `label_join(demo_num_cpus, "~invalid", "-", "instance")`

Under Prom 2.x both errored (invalid label name). Prom 3.x relaxed label names to full UTF-8, so both now succeed in the reference. The shim still rejects `~invalid` with `bad_data: invalid destination label name`. Upstream's `should_fail: true` markers reflect Prom 2.x; `patch-queries-for-prom3.py` strips them so the tester doesn't hard-abort at `comparer.go:95`. The resulting unexpected-failure rows are real — the shim needs Prom 3.x UTF-8 label-name support. Will close when that lands.

### Native-mode coverage gaps

Pass #2 catches everything the native-SQL path doesn't yet cover. Run `scripts/native-gap-report.sh` against the latest native-mode report for a categorized view:

- `diff_failure` — native lowered the query but returned wrong values. These are **real native-SQL correctness bugs**.
- `unsupported_root` — planner refused to lower (root-plan rejection). Missing coverage.
- `other` — bad_data, timeouts, etc.

Each number should trend down over time. None are allowlistable.

## Fixture

The top-level runner seeds a deterministic `demo_*` fixture through remote write into both reference Prometheus and ClickHouse when fresh volumes are empty. It then verifies, at the pinned `end_time` in `test-promshim.yml`, that both APIs expose the expected fixture marker and core series counts before any compliance query runs.

If old scraped data or a partially seeded fixture is present, the runner fails before the compliance pass and asks you to reset the compliance volumes:

```bash
cd harness/compliance
docker compose down -v
```

This prevents empty or stale fixtures from producing vacuous green CI runs.
