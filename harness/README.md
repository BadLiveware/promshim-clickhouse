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

From the repo root:

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

The initial corpus lives in:

- `harness/corpus/queries.json`

It currently focuses on the already-implemented compatibility subset:

- selectors and matchers
- aggregations
- scalar/vector-scalar behavior
- `label_join`
- `label_replace`
- range equivalents

Extend this corpus as new features land.
