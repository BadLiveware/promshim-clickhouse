# Unified sweep implementation plan

## Purpose

This folder splits the umbrella design in
`.pi/unified-benchmark-compliance-sweep-plan.md` into ordered, implementation-
sized plans. The goal is to avoid overloading implementation context and to keep
review boundaries clear.

The end state is one user-friendly command:

```bash
./scripts/run-sweep.sh [preset/options]
```

that can run compliance and benchmark sweeps across transports, profiles,
densities, execution modes, matrices, memory signals, dry-runs, estimates, and
named artifact outputs — while keeping benchmark data isolated from the frozen
compliance fixture.

## Execution order

Implement these plans in order:

1. [`01-stack-isolation-and-seeding.md`](01-stack-isolation-and-seeding.md)
2. [`02-bench-runner-schema-and-modes.md`](02-bench-runner-schema-and-modes.md)
3. [`03-dense-datasets-and-processing-corpora.md`](03-dense-datasets-and-processing-corpora.md)
4. [`04-sweep-orchestrator-and-estimates.md`](04-sweep-orchestrator-and-estimates.md)
5. [`05-matrix-reporting-and-memory.md`](05-matrix-reporting-and-memory.md)
6. [`06-docs-and-migration.md`](06-docs-and-migration.md)

Each plan is intended to be independently reviewable and to leave the repository
in a useful state if work pauses after that file.

## Dependency graph

```text
01 stack/data safety
  -> 02 bench report schema and execution modes
  -> 03 dense datasets and processing corpora
  -> 04 sweep orchestrator, dry-run, estimates, named artifacts
  -> 05 matrices, ProfileEvents, memory trade-off reporting
  -> 06 docs, make targets, migration cleanup
```

## Hard constraints carried through all plans

- Benchmark setup and benchmark execution must not write to compliance
  Prometheus or ClickHouse volumes.
- Compliance runs use the existing compliance stack and its frozen fixture.
- Benchmark runs use an isolated benchmark stack by default.
- Pre-seeded data is preferred. Normal runs reuse existing benchmark datasets
  and fail with setup instructions when data is missing.
- Dense data is for benchmark realism, not compliance.
- `force_supported` must remain native SQL root only.
- The compliance allowlist remains for accepted deviations only.
- Tier 3/4 behavior may be measured, but this work must not expand tier 3/4
  feature coverage.
- Artifacts must be stable, named, and non-overwritten unless `--overwrite` is
  explicitly selected.
- Heavy wall-clock baselines are machine-sensitive. Do not gate dense/heavy
  latency by committed baseline unless an explicit local baseline is supplied.

## Default user experience target

```bash
# One-time setup on a fresh machine / reset benchmark volume.
./scripts/run-sweep.sh --setup --profile 7d,30d --density sparse --target both

# Balanced default sweep using pre-seeded data.
./scripts/run-sweep.sh --name pr-42-default

# Preview heavy benchmark cost without executing.
./scripts/run-sweep.sh heavy --estimate

# Dense benchmark setup and run.
./scripts/run-sweep.sh --setup --profile 7d,30d --density dense --target both
./scripts/run-sweep.sh heavy --name dense-calibration-april
```

## Final acceptance criteria across all split plans

- `./scripts/run-sweep.sh` with no arguments runs a balanced correctness +
  benchmark sweep from pre-seeded data.
- Missing benchmark data fails fast with an exact `--setup` command.
- `--name` controls artifact output under
  `harness/artifacts/sweeps/<run-name>/`.
- `--dry-run` and `--estimate` have no side effects and show useful stack,
  dataset, runtime, artifact, and memory-measurement information.
- Benchmark setup and benchmark runs use isolated benchmark volumes; compliance
  volumes remain untouched.
- Transport comparisons recreate the correct promshim container instead of only
  setting environment on the bench process.
- Dense processing rows report Prometheus target-band classification.
- Mode matrices show latency and memory trade-offs for `prefer`,
  `force_supported`, and `off`/local execution where selected.
- Compliance failures, native gaps, benchmark regressions, limit failures, and
  tooling failures are classified separately.
