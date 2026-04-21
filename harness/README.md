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

- `--subjects <list>` to restrict compare subjects globally, e.g. `shim` or `shim,promclick`
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
- promoted native range / counter lowering rows for the supported Phase 6 subset:
  - direct-selector `rate`, `increase`, `sum_over_time`, `count_over_time`
  - supported subquery-backed `rate(sum by (...) (...)[range:step])`
- matrix-root selector subquery behavior (`subquery_matrix_root_selector_instant`)
- absent-family support:
  - `absent(...)`
  - `absent_over_time(...)`
- sparse/disappearing-series parity probes for staleness-sensitive selectors and absence windows

### Stable parity status (known supported set)

- `./scripts/run-harness.sh` runs the full configured-subject corpus comparison.
- `./scripts/run-harness.sh --subjects shim` runs the Prometheus-vs-promshim gating view without optional promclick noise.
- The tracked stable corpus currently includes 94 entries in `harness/corpus/queries.json`, of which 91 are success-case checks and 3 are explicit error probes.
- Phase 6 native-lowering rows that are now considered stable have been promoted into the main corpus. Rows that are not meaningful against optional promclick behavior are scoped with `"subjects": ["shim"]` so Prometheus remains the only oracle for that row.

#### Excluded / limited by design

Some query families are still unsuitable for all-subject success-case parity assertions because optional promclick support diverges or does not implement them yet, especially subquery-backed rate-family cases. Where native promshim support is stable and Prometheus-backed parity is useful, the stable corpus now carries shim-only rows rather than treating optional promclick behavior as a blocker.

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
- if a row should only run against specific optional subjects, set `"subjects": ["shim"]` or `"subjects": ["promclick"]`; omitting `subjects` compares against all configured subjects.
