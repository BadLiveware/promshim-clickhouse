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
- `--dataset-variants <list>` to seed multiple dataset shapes in one run, e.g. `baseline,resets_gaps`
- `--native-only` to force `native_lowering_mode=force_supported` for every query row that does not already set an explicit mode
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
- `PROM_HARNESS_DATASET_VARIANTS` (optional comma-separated list such as `baseline,resets_gaps,churn_stale,histogram_burst`; when unset the harness keeps the legacy single dataset shape)

Seeded dataset shapes currently include:
- `baseline` — the default deterministic fixture
- `resets_gaps` — counter reset plus post-midpoint sampling gaps
- `churn_stale` — stronger series churn / staleness behavior
- `histogram_burst` — post-midpoint histogram / latency burst behavior

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

### Common dashboard differential subset

The current **stable** repo-owned common dashboard differential subset is:

- `harness/corpus/common-dashboard-subset.json`
- `harness/corpus/common-dashboard-subset.metadata.json`

It is carved from the broader exploratory top-panel shortlist corpus:

- `harness/corpus/draft-grafana-top-panel-shortlist.json`
- themed splits in `harness/corpus/draft-grafana-top-panel-shortlist.themes/`

The stable subset excludes the currently known failing shortlist candidates and is the corpus to use when you want a dashboard-focused differential gate that is expected to stay green.

Run the stable subset with:

```bash
./scripts/run-harness.sh --corpus common-dashboard-subset.json --subjects shim
```

For broader exploratory dashboard coverage, run the themed shortlist with:

```bash
./scripts/run-harness.sh --all-themes --subjects shim
```

That command runs the themed splits (`selector`, `aggregation`, `rate-family`, `range-selector`, `histogram`, `binary-arithmetic`, `comparison`, `set-operator`, `subquery`, `vector-matching`, `label-mutation`, `range-function`) and writes one compare report per theme into `harness/artifacts/`. The full shortlist is useful for promotion planning and gap discovery; the stable common-dashboard subset is the current gate.

### Native SQL lowering starter corpus

For the native SQL lowering roadmap there are also smaller focused corpora:

- `harness/corpus/native-lowering-starter.json`
- `harness/corpus/native-lowering-starter.metadata.json`
- `harness/corpus/phase7-rollout.json`

The native-lowering starter corpus is intended for frequent runs while the planner/type-extraction and native-lowering work is in flight. It keeps a small baseline across the first roadmap buckets:

- selectors
- aggregations
- joins / vector matching
- counters / rate family
- subqueries

Run it with:

```bash
./scripts/run-harness.sh --corpus native-lowering-starter.json
```

The Phase 7 rollout corpus is a smaller shim-only parity check for rollout controls on the normal query endpoints. It exercises `native_lowering_mode=off|prefer|explain|shadow` and explicit `explain=1` requests on queries where the served result should still match Prometheus exactly:

```bash
./scripts/run-harness.sh --corpus phase7-rollout.json --subjects shim
```

### Path 2 measurement prerequisites corpus

A dedicated native-only measurement corpus now lives at:

- `harness/corpus/path2-measurement-prereqs.json`
- `harness/corpus/path2-measurement-prereqs.metadata.json`

It combines:

- the existing native-lowering starter rows
- explicit expected-error probes
- range step / time offset expansion rows
- dataset-variant parity rows for reset/gap, churn/staleness, and histogram-burst shapes

Run it with:

```bash
./scripts/run-harness.sh \
  --corpus path2-measurement-prereqs.json \
  --subjects shim \
  --native-only \
  --dataset-variants baseline,resets_gaps,churn_stale,histogram_burst
```

This is the shortest command that exercises the P0/P1/P2 measurement prerequisites in one harness pass without relying on silent local fallback.

To align the Path 2 inventory with the read-only Prometheus compliance query suite without changing `harness/compliance/prom-compliance/`, generate the compliance-alignment report with:

```bash
go run ./cmd/promshim-promql-compliance
```

That writes:

- `.pi/path2-promql-compliance-alignment.json`
- `.pi/path2-promql-compliance-alignment.md`

and expands the upstream `promql-test-queries.yml` variant matrix so the current Path 2 measurement surface can be compared against the same query families the compliance suite expects.

Corpus rows can also set:
- `"nativeLoweringMode": "off|explain|shadow|prefer|force_supported"`
- `"explain": true`
- `"timeOffsets": [{"name": "early", "timeOffsetSeconds": 60}, ...]` for instant-query variants
- `"rangeOffsets": [{"name": "boundary", "startOffsetSeconds": 0, "endOffsetSeconds": 240}, ...]` for range-query variants
- `"rangeStepMatrix": true` for opt-in Layer 2 range-query step sweeps
- `"datasetVariants": ["baseline", "resets_gaps", "churn_stale", "histogram_burst"]` to restrict a row to specific seeded dataset shapes
- `"excludeDatasetVariants": [...]` to remove specific dataset shapes from an otherwise broad row

These are sent as normal HTTP query parameters (`native_lowering_mode`, `explain=1`). Variant-expanded rows appear in `compare-report.json` as separate results sharing the base query `name` and carrying an explicit `variant` field. Multi-dataset runs also tag each row with `datasetVariant`. For repeated non-ok rows with the same base query + severity + bucket + detail, the report now also tags them with `causeCluster` and `causeClusterSize`, and emits a top-level `clusters` summary so one underlying issue can be triaged as a single cause. For `rangeStepMatrix`, the harness currently emits a small default matrix per range variant:
- `step_evenly_divides_range`
- `step_not_evenly_divides_range`
- `step_gt_range_over_2`
- `step_eq_range`

When both `rangeOffsets` and `rangeStepMatrix` are set, the report variant name combines them (for example `boundary/step_eq_range`). This is most useful for promshim-specific rollout validation; in practice the Phase 7 corpus scopes rows to `"subjects": ["shim"]` so optional promclick behavior is not treated as a rollout gate.

A small focused example corpus for Layer 1/2 is available at:
- `harness/corpus/phase12-harness-variants.json`

Run it with:

```bash
./scripts/run-harness.sh --corpus phase12-harness-variants.json --subjects shim
```

A small focused example corpus for Layer 3 dataset variants is also available at:
- `harness/corpus/phase12-dataset-variants.json`

Run it with:

```bash
./scripts/run-harness.sh --corpus phase12-dataset-variants.json --subjects shim --dataset-variants baseline,resets_gaps,churn_stale,histogram_burst
```

A focused histogram-native validation corpus also lives at:
- `harness/corpus/histogram-native-support.json`
- `harness/corpus/histogram-native-support.metadata.json`

It currently gates the oracle-compatible classic histogram quantile shapes against Prometheus in native-only mode, including the `histogram_burst` dataset variant:

```bash
./scripts/run-harness.sh --corpus histogram-native-support.json --subjects shim --native-only --dataset-variants baseline,histogram_burst
```

The broader dashboard-promotion corpora now also carry auto-annotated dataset-variant hints derived from the shortlist metadata (`families` + `retargetingHint`):
- `harness/corpus/draft-grafana-top-panel-shortlist.json`
- `harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.json`
- themed splits in:
  - `harness/corpus/draft-grafana-top-panel-shortlist.themes/`
  - `harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.themes/`

These remain exploratory/promotion-oriented corpora rather than stable gates, but they now routinely exercise reset/gap, churn/staleness, and histogram-burst shapes where the shortlist metadata suggests those behaviors are relevant.

Extend the corpus cautiously as new features land:
- add new corpus rows only after parity is verified in `./artifacts/compare-report.json` and corresponding unit/integration coverage exists.
- if a row should only run against specific optional subjects, set `"subjects": ["shim"]` or `"subjects": ["promclick"]`; omitting `subjects` compares against all configured subjects.
