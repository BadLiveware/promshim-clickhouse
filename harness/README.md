# Differential Prometheus vs promshim Harness

This harness starts a disposable Prometheus + ClickHouse + promshim stack, generates a deterministic metric dataset from a seed, remote-writes the exact same samples to both backends, and compares Prometheus query results against `promshim -> ClickHouse`.

## Components

- `prometheus`
  - query oracle
  - remote-write receiver enabled
- `clickhouse`
  - `TimeSeries` storage + `/write` handler
- `promshim`
  - points at ClickHouse `observability.prometheus`
- `seed`
  - one-shot deterministic remote-writer
- `compare`
  - one-shot differential query comparator

## Usage

### One-command workflow (recommended)

From the repo root:

```bash
./scripts/run-harness.sh
```

Useful options:

- `--no-build` to skip rebuilding images
- `--keep-up` to keep the stack running for inspection/debugging
- `--init-retries <n>` to tune ClickHouse init retries

### Manual workflow

```bash
cd harness
mkdir -p artifacts

docker compose build promshim seed compare

docker compose up -d clickhouse prometheus promshim
# initialize ClickHouse TimeSeries schema
docker compose run --rm clickhouse-init
# generate deterministic samples and dual-write them
docker compose --profile jobs run --rm seed
# compare Prometheus direct results vs promshim
docker compose --profile jobs run --rm compare
```

Clean up:

```bash
docker compose down -v
rm -rf artifacts/*
```

## Seed/job controls

Useful environment variables:

- `PROM_HARNESS_SEED`
- `PROM_HARNESS_STEP_SECONDS`
- `PROM_HARNESS_POINTS`
- `PROM_HARNESS_BASE_UNIX_SECONDS` (optional; if omitted the seed job picks a recent base time and writes it to `artifacts/seed-manifest.json`)

Comparator outputs:

- `artifacts/seed-manifest.json`
- `artifacts/compare-report.json`

## Query corpus

The main stable corpus lives in:

- `harness/corpus/queries.json`

It focuses on the implemented compatibility subset and the stable query set used for parity gating:

- selectors and matchers
- aggregations
- scalar/vector-scalar behavior
- local label transforms (`label_join`, `label_replace`)
- matrix-consuming local paths and supported nested compositions:
  - `last_over_time`, `sum_over_time`, `avg_over_time`, `max_over_time`, `min_over_time`, `count_over_time`
  - nested binary compositions over matrix functions where both sides are implemented locally
- matrix-root selector subquery behavior (`subquery_matrix_root_selector_instant`)
- absent-family support:
  - `absent(...)`
  - `absent_over_time(...)`
- sparse/disappearing-series parity probes for staleness-sensitive selectors and absence windows

### Stable parity status (known supported set)

- `./scripts/run-harness.sh` runs the full corpus comparison.
- Current tracked corpus currently includes 86 entries in `harness/corpus/queries.json`, of which 82 are success-case checks and 4 are explicit error probes. In the latest run they pass under their expected status.
- Intentionally excluded / unstable query families are intentionally kept out of the stable corpus and documented in:
  - `.pi/phase9-delegated-divergence-catalog.md`

#### Excluded by design

The following query families remain excluded from the stable corpus because they are known delegated-subquery divergence classes and are now explicit hard errors:

- `rate(...[range:step])`
- `irate(...[range:step])`
- `increase(...[range:step])`
- `delta(...[range:step])`
- `idelta(...[range:step])`
- `deriv(...[range:step])`
- `changes(...[range:step])`

As these are handled explicitly with `unsupported` errors, they are not suitable for success-case differential parity assertions until local implementation or delegated parity guarantees are introduced.

### Native SQL lowering starter corpus

For the native SQL lowering roadmap there is also a smaller focused starter corpus:

- `harness/corpus/native-lowering-starter.json`
- `harness/corpus/native-lowering-starter.metadata.json`

This corpus is intended for frequent runs while the planner/type-extraction and native-lowering work is in flight. It keeps a small baseline across the first roadmap buckets:

- selectors
- aggregations
- joins / vector matching
- counters / rate family
- subqueries

Run it with:

```bash
./scripts/run-harness.sh --corpus native-lowering-starter.json
```

Extend the corpus cautiously as new features land:
- add new corpus rows only after parity is verified in `./artifacts/compare-report.json` and corresponding unit/integration coverage exists.
