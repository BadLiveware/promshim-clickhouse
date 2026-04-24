# 04 — Sweep orchestrator, named runs, dry-run, and estimates

## Goal

Build the one-command user interface that coordinates stack lifecycle, seeding,
compliance, benchmarks, transport sweeps, named artifacts, dry-runs, and
estimates.

## Dependencies

- [`01-stack-isolation-and-seeding.md`](01-stack-isolation-and-seeding.md)
- [`02-bench-runner-schema-and-modes.md`](02-bench-runner-schema-and-modes.md)
- [`03-dense-datasets-and-processing-corpora.md`](03-dense-datasets-and-processing-corpora.md)

## Scope

### In scope

- `scripts/run-sweep.sh` thin wrapper.
- `cmd/promshim-sweep` Go orchestrator.
- Presets and axis parsing.
- Named run artifacts.
- `--dry-run`, `--estimate`, `--execute`.
- Stack lifecycle for compliance and benchmark stacks.
- Transport recreation in the correct stack.
- Seed policy application.
- Manifest writing.
- Status classification.

### Out of scope

- Final detailed matrix rendering.
- Detailed memory pprof capture.
- Documentation polish beyond help text.

## Primary command

```bash
./scripts/run-sweep.sh [preset] [options]
```

Presets:

```text
smoke
default
heavy
full
```

Core axes:

```text
--suite compliance,bench,profile
--transport http,native
--profile 10m,7d,30d,1y,all
--density sparse,dense,all
--mode prefer,force_supported,off,all
--compliance-mode prefer,native,all
--seed reuse|missing|always|never
--target both|ch|prom
--bench-stack isolated
--matrix category,query,transport,mode,density,profile,all
--memory off|summary|detailed
```

## Presets

### `smoke`

```text
suite: compliance,bench
compliance-mode: prefer
transport: current/default
profile: 10m
mode: prefer,force_supported
seed: never
repeats: 3
warmup: 1
profile-events: off
memory: off
matrix: category
```

### `default`

```text
suite: compliance,bench
compliance-mode: prefer,native
transport: native
profile: 7d,30d
density: sparse
mode: prefer,force_supported
seed: reuse
target: both
repeats: 5
warmup: 1
profile-events: auto
memory: summary
matrix: category,profile,mode
```

### `heavy`

```text
suite: bench
transport: native
profile: 7d,30d
density: dense
mode: prefer,force_supported
seed: reuse
target: both
repeats: 3
warmup: 1
profile-events: on
memory: summary
matrix: category,query,profile,mode,density
```

### `full`

```text
suite: compliance,bench
compliance-mode: prefer,native
transport: http,native
profile: 7d,30d,1y
density: sparse,dense
mode: prefer,force_supported,off
seed: reuse
target: both
repeats: 5
warmup: 1
profile-events: on
memory: summary
matrix: all
```

## Named runs

Options:

```text
--name NAME
--run-id NAME     alias for --name
--overwrite
```

Artifact root:

```text
harness/artifacts/sweeps/<run-name>/
```

Sanitize names:

- lowercase by default;
- replace whitespace/path separators with `-`;
- allow only `[a-z0-9._-]` after sanitization;
- trim repeated separators;
- reject empty names.

If no name is provided, generate:

```text
YYYYMMDDTHHMMSSZ-<preset>
```

Collision policy:

- fail if directory contains a manifest;
- suggest a new `--name` or `--overwrite`;
- `--overwrite` may replace only the selected run directory, never artifact root
  or data volumes.

## Dry-run

`--dry-run` resolves the plan and exits without side effects.

It prints:

- selected preset and expanded axes;
- run name/slug/artifact root;
- stack actions;
- seed checks;
- datasets and expected presence;
- compliance passes;
- benchmark jobs;
- memory/profile-events policy;
- matrices and expected files;
- setup commands for missing data.

It should not require stacks to be running. If stacks are running and cheap
checks are available, it may report live presence; otherwise report `unknown`.

## Estimate

`--estimate` implies dry-run unless paired with `--execute`.

It prints dry-run output plus:

- series count;
- points per series;
- total samples per store;
- missing samples to seed;
- remote-write POST count;
- rough ingest bytes;
- benchmark request count;
- compliance pass count;
- matrix count;
- memory measurement overhead;
- disk footprint estimate;
- diagnostic log overhead note;
- rough runtime class;
- historical runtime estimates from prior manifests where available.

Runtime estimates are rough and environment-dependent. Use buckets when no
history exists:

```text
short (<2 min)
medium (2-15 min)
long (15-60 min)
very_long (>60 min)
```

## Manifest schema

Every run writes:

```text
harness/artifacts/sweeps/<run-name>/manifest.json
```

Include:

```json
{
  "schemaVersion": 1,
  "runName": "pr-42-native-default",
  "runSlug": "pr-42-native-default",
  "runId": "20260424T213000Z-pr-42-native-default",
  "artifactRoot": "harness/artifacts/sweeps/pr-42-native-default",
  "git": { "sha": "...", "dirty": false },
  "preset": "default",
  "axes": {
    "suites": ["compliance", "bench"],
    "transports": ["native"],
    "profiles": ["7d", "30d"],
    "densities": ["sparse"],
    "benchModes": ["prefer", "force_supported"],
    "complianceModes": ["prefer", "native"],
    "benchStack": "isolated"
  },
  "stacks": {
    "compliance": { "prometheusURL": "http://localhost:29090" },
    "bench": { "prometheusURL": "http://localhost:29190" }
  },
  "seed": { "policy": "reuse", "datasets": [] },
  "estimate": { "runtimeClass": "medium", "memoryMode": "summary" },
  "artifacts": [],
  "status": {
    "overall": "pass|fail|partial",
    "complianceFailures": 0,
    "nativeGaps": 0,
    "benchRegressions": 0,
    "limitFailures": 0,
    "toolFailures": 0
  }
}
```

## Stack lifecycle

- Acquire `compliance-stack` lock for compliance phases.
- Acquire `bench-stack` lock for benchmark/setup/profile phases.
- Build promshim unless `--no-build`.
- Start compliance stack only for compliance phases.
- Start benchmark stack only for benchmark/setup/profile phases.
- Recreate only the relevant stack's promshim container per transport.
- Run compliance only against compliance endpoints.
- Run seed/bench/ProfileEvents only against benchmark endpoints.
- Stop containers unless `--keep-up`, preserving volumes.

## Implementation tasks

1. Add `scripts/run-sweep.sh` wrapper.
2. Add `cmd/promshim-sweep` with flag parsing.
3. Implement preset expansion and axis validation.
4. Implement named run slug/collision behavior.
5. Implement dry-run plan builder.
6. Implement estimate calculations and history lookup.
7. Implement stack lifecycle helpers.
8. Implement seed policy orchestration.
9. Invoke compliance and bench lower-level commands with correct endpoints.
10. Write manifest incrementally.
11. Classify failures.
12. Add help output focused on presets first.

## Validation

```bash
./scripts/run-sweep.sh smoke --name smoke-local --dry-run
./scripts/run-sweep.sh heavy --name heavy-preview --estimate
./scripts/run-sweep.sh --name native-7d-sparse \
  --suite bench \
  --transport native \
  --profile 7d \
  --density sparse \
  --mode prefer,force_supported \
  --seed reuse
./scripts/run-sweep.sh --name compliance-transport-check \
  --suite compliance \
  --transport http,native \
  --compliance-mode prefer \
  --keep-up
```

## Risks

- Orchestrator can hide lower-level failures if status classification is too
  coarse.
- Incorrect endpoint wiring could contaminate compliance data.
- Dry-run could drift from real execution if not built from same plan object.
- Named artifact collisions can destroy evidence if `--overwrite` is unsafe.

## Exit criteria

- `--dry-run` has no Docker, remote-write, bench, or artifact side effects.
- `--estimate` reports useful dataset/request/runtime/disk/memory information.
- Named runs write under expected artifact paths.
- Default sweep uses pre-seeded data or fails with setup instructions.
- Compliance and benchmark phases use the correct isolated stacks.
