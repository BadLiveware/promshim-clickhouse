# 06 — Documentation, Make targets, and migration cleanup

## Goal

Make the new sweep workflow discoverable and hard to misuse. Users and agents
should stop hand-writing long-range seeding loops, manually switching transports,
and accidentally benchmarking against the compliance stack.

## Dependencies

All prior plans:

1. [`01-stack-isolation-and-seeding.md`](01-stack-isolation-and-seeding.md)
2. [`02-bench-runner-schema-and-modes.md`](02-bench-runner-schema-and-modes.md)
3. [`03-dense-datasets-and-processing-corpora.md`](03-dense-datasets-and-processing-corpora.md)
4. [`04-sweep-orchestrator-and-estimates.md`](04-sweep-orchestrator-and-estimates.md)
5. [`05-matrix-reporting-and-memory.md`](05-matrix-reporting-and-memory.md)

## Scope

### In scope

- Update README and harness docs.
- Add Make targets.
- Improve `--help` output.
- Document new benchmark stack and safe reset/status commands.
- Document common workflows.
- Mark old manual loops as low-level/debug workflows.
- Document artifact layout and review guidance.

### Out of scope

- New benchmark features beyond documenting the implemented sweep.

## Documentation locations

Update as appropriate:

```text
README.md
harness/README.md
Makefile
scripts/run-sweep.sh --help
scripts/run-bench.sh --help
scripts/seed-long-range.sh --help
```

## Make targets

Add at least:

```make
sweep:
	./scripts/run-sweep.sh

sweep-smoke:
	./scripts/run-sweep.sh smoke

sweep-estimate-heavy:
	./scripts/run-sweep.sh heavy --estimate

bench-status:
	./scripts/run-sweep.sh --bench-status
```

Keep existing targets working unless there is an explicit migration note.

## `--help` structure

`run-sweep.sh --help` should prioritize common usage before advanced axes:

1. What the command does.
2. Presets.
3. Common examples.
4. Data setup / pre-seeded behavior.
5. Named artifacts.
6. Dry-run / estimate.
7. Advanced axes.
8. Stack maintenance.
9. Exit status categories.

## Required examples

Document these workflows:

```bash
# Preview the default run.
./scripts/run-sweep.sh --dry-run

# Balanced default using pre-seeded data.
./scripts/run-sweep.sh --name pr-42-default

# One-time sparse setup.
./scripts/run-sweep.sh --setup --profile 7d,30d --density sparse --target both

# One-time dense setup.
./scripts/run-sweep.sh --setup --profile 7d,30d --density dense --target both

# Heavy benchmark estimate.
./scripts/run-sweep.sh heavy --estimate

# Heavy benchmark.
./scripts/run-sweep.sh heavy --name dense-calibration-april

# Transport comparison.
./scripts/run-sweep.sh --suite bench --transport http,native --profile 7d,30d

# Compliance-only transport check.
./scripts/run-sweep.sh --suite compliance --transport http,native --compliance-mode all

# Mode/memory comparison for CBE investigation.
./scripts/run-sweep.sh --suite bench --profile 7d --density sparse --mode prefer,force_supported,off --memory summary

# Reset benchmark data only.
./scripts/run-sweep.sh --bench-reset --yes
```

## Key concepts to document

### Stack isolation

Explain clearly:

- Compliance stack is for correctness and frozen fixture.
- Benchmark stack is for long-range/dense data and timing.
- Benchmark setup never writes to compliance volumes by default.
- Benchmark reset never deletes compliance volumes.

### Pre-seeded data

Explain seed policies:

```text
reuse    default; require existing data
missing  seed only missing data
always   deliberately write again
never    skip seed checks/writes
setup    seed missing selected data and exit
```

### Sparse vs dense

Explain:

- sparse is fast and broad;
- dense targets real processing latency;
- target Prometheus p50 band is advisory;
- disk estimates are rough and scale with `instances-per-job`.

### Named artifacts

Explain artifact root:

```text
harness/artifacts/sweeps/<run-name>/
```

Show important files:

```text
manifest.json
summary.md
bench/<transport>/<density>/<profile>/bench-report.json
memory/<transport>/<density>/<profile>/memory-summary.json
compliance/<transport>/<mode>/compliance-report.json
```

### Dry-run and estimate

Explain that:

- `--dry-run` has no side effects;
- `--estimate` implies dry-run unless `--execute` is passed;
- runtime/disk estimates are rough and environment-dependent;
- estimates use prior manifests when available.

### Memory trade-offs

Explain how to read CH vs promshim memory:

- native SQL often shifts memory into ClickHouse;
- tier 3/4 can shift memory into promshim;
- mode matrices keep domains separate;
- memory reports are advisory unless a local baseline is provided.

### Baseline policy

Explain:

- strategy/roundtrip/coverage regressions can be hard failures;
- dense/heavy wall-clock is advisory by default;
- users can supply local baselines for stricter checks;
- ProfileEvents are preferred for ClickHouse optimization claims.

## Deprecate manual loops in docs

Mark these as low-level/debug only:

```bash
for p in 7d 30d 1y; do ./scripts/seed-long-range.sh --profile "$p" --target ch; done
PROM_SHIM_CLICKHOUSE_TRANSPORT=http ./scripts/run-bench.sh --long-range all --matrix
```

Replace with:

```bash
./scripts/run-sweep.sh --setup --profile all --density sparse --target both
./scripts/run-sweep.sh --suite bench --transport http,native --profile all --density sparse
```

## Troubleshooting section

Include:

- Missing data: run printed `--setup` command.
- Port conflict: show benchmark/compliance ports.
- Disk pressure: use `--estimate`, reset benchmark volumes, reduce density.
- Compliance contamination concern: verify benchmark stack endpoints.
- Slow dense setup: seed once, reuse thereafter.
- Matrix too large: use category view or selected axes.
- Memory unavailable: cgroup peak may be unavailable; CH memory still reported.

## Validation

```bash
./scripts/run-sweep.sh --help
make sweep
make sweep-estimate-heavy
```

Review docs manually for:

- no recommended workflow writes benchmark data to compliance stack;
- named artifact paths are clear;
- dry-run/estimate behavior is clear;
- memory trade-off caveats are clear;
- old low-level scripts remain documented but are not the primary path.

## Exit criteria

- Users can discover common workflows from `--help`.
- README/harness docs explain benchmark stack isolation.
- Make targets expose the common sweep paths.
- Manual loops are replaced by sweep examples.
- Troubleshooting covers missing data, disk, ports, and memory caveats.
