# Differential Prometheus vs promshim Harness

This harness starts a disposable Prometheus + ClickHouse + promshim stack, generates a deterministic metric dataset from a seed, remote-writes the exact same samples to both backends, and compares Prometheus query results against `promshim -> ClickHouse`.

A full `scripts/run-harness.sh` run (all suites) completes in ~25 seconds on a warm docker cache (~90s cold, dominated by image builds). It runs in the foreground and does not need a minutes-long timeout.

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

With no arguments this runs every suite we care about:

1. **differential** — `harness/corpus/queries.json` across all configured subjects.
2. **dashboard** — `harness/corpus/common-dashboard-subset.json` with `--subjects shim`.
3. **compliance** — the upstream PromQL compliance tester (delegates to `./scripts/run-compliance.sh`, which brings up the separate compliance stack with its frozen fixture).
4. **bench** — the Prom-vs-promshim native-SQL tripwire (delegates to `./scripts/run-bench.sh`). Uses `harness/corpus/bench-native-lowering.json`, compares wall-clock, strategy, and ClickHouse round-trip count against `harness/bench/baseline.json`, and exits non-zero on a strategy change, CH-roundtrip increase, latency regression (>+25% AND >+3 ms), or strategy flap across repeats.

Suites 1 and 2 share a single main-harness stack and a single seed run. Suites 3 and 4 run against the independent `harness/compliance/` stack. Each suite prints its own summary; the runner exits 0 as long as the tooling itself completes and the bench detects no regressions.

Useful options:

- `--suite <name>` to run a single suite only: `differential`, `dashboard`, `compliance`, `bench`, or `all` (default).
- `--subjects <list>` to restrict compare subjects, e.g. `shim` or `shim,promclick`.
- `--dataset-variants <list>` to seed multiple dataset shapes in one run, e.g. `baseline,resets_gaps`.
- `--native-only` to force `native_lowering_mode=force_supported` for every query row that does not already set an explicit mode.
- `--no-build` to skip rebuilding images.
- `--keep-up` to keep stacks running for inspection/debugging.
- `--init-retries <n>` to tune ClickHouse init retries.
- `--ready-timeout <n>` to tune the compliance stack readiness wait.

Passing `--theme`, `--all-themes`, or `--corpus` implies `--suite differential`.

### Sweep workflow for benchmark/compliance runs

Use `scripts/run-sweep.sh` when comparing compliance, transports, long-range
profiles, active-series targets, or execution modes. It uses a benchmark-only stack under
`harness/bench/` for long-range benchmark data so the frozen
`harness/compliance/` fixture is not contaminated.

Common commands:

```bash
# Show benchmark stack endpoints and seed markers.
./scripts/run-sweep.sh --bench-status

# Preview selected work and rough data size; no side effects.
./scripts/run-sweep.sh --dry-run --estimate --profile 7d --active-series-preset fast

# Seed missing benchmark-only data once, then reuse it in normal sweeps.
./scripts/run-sweep.sh --setup --profile all --active-series-preset fast --target both

# Run a named sweep and write harness/artifacts/bench/sweeps/<name>/.
./scripts/run-sweep.sh --name local-default

# Processing benchmark preview at the profile-50k cardinality preset.
./scripts/run-sweep.sh --profile 7d --active-series-preset profile-50k --corpus-set processing --estimate

# Delete benchmark data only. Compliance volumes are not touched.
./scripts/run-sweep.sh --bench-reset --yes
```

Seed policies:

- `reuse` — default for normal sweeps; fail if selected data is missing.
- `missing` — default for `--setup`; seed only missing targets.
- `always` — deliberately write selected data again.
- `never` — skip seed checks and writes.

Active-series / workload presets:

- `fast` — default, targets about 5k active series with the legacy dense generator.
- `profile-50k` — legacy dense stress fixture targeting about 50k active series.
- `profile-500k` — legacy dense stress fixture targeting about 500k active series.
- `dashboard-50k` — histogram-heavy mixed workload modeled from production-shape profiling: mostly HTTP/API bucket series, mixed 15s/60s/5m sample intervals, and series that start during the window.
- `envoy-heavy-50k` — Envoy-dominated histogram stress shape with 15s bucket series, skewed labels, and tail-spike/bimodal histogram value patterns.
- `churn-50k` — mixed workload with sparse counters and series lifetimes that start/end inside the benchmark window.

The realistic presets keep the existing `demo_*` metric names so current
benchmark corpora still query them, but they change label skew, histogram bucket
fanout, sample intervals, lifetimes, and value shapes. `1y` remains a legacy
stress-only horizon; realistic workload presets intentionally reject 1y seeding
because representative 1y data is too large or too downsampled to be meaningful.
Use `--estimate` before setup. Estimates report conservative headroom plus an
observed ClickHouse compressed-size approximation based on the current seeded
benchmark stack; measured storage after seeding remains authoritative.

Artifacts live under `harness/artifacts/bench/sweeps/<run-name>/` and include
`manifest.json`, `summary.md`, `summary.json`, v2 benchmark reports named by
profile/active-series/corpus, `memory-summary-*.json`, and optional `memory-detail-*/`
pprof snapshots for `--memory detailed`. `run-bench.sh --prometheus-profile runtime`
adds per-row Prometheus runtime samples to v2 benchmark reports (`promProfile`),
including process RSS, Go heap, allocation, and CPU deltas sampled from
Prometheus `/metrics` around one measured Prometheus query. Build matrix views with:

```bash
./scripts/bench-matrix.sh --sweep harness/artifacts/bench/sweeps/local-default/manifest.json
./scripts/bench-matrix.sh --sweep harness/artifacts/bench/sweeps/local-default/manifest.json --per-query
```

If disk pressure appears, check `--estimate`, reduce active-series/profile selection,
or reset benchmark volumes with `--bench-reset --yes`. ClickHouse diagnostic logs
for the benchmark stack are kept off persistent benchmark data volumes. Local
harness promshim containers enable `PROM_SHIM_ENABLE_PPROF=1` so detailed memory
snapshots can be collected; production deployments should leave pprof disabled
unless access is protected.

### Profiling a real Prometheus workload shape

Use `scripts/profile-prometheus-workload.py` to collect bounded evidence for a
more realistic synthetic seed mix. The script is conservative by default: it runs
sequential HTTP requests, logs each request and artifact write to stderr, writes
JSON artifacts as each request completes, waits between requests, caps lookback
windows to the configured retention horizon, and does not run PromQL top-k probes
unless asked. This matters for production HA pairs where broad
`count({__name__!=""})` or `topk(count by (...))` queries can take about a minute.

Safe metadata/status capture:

```bash
PROM_URL=https://prometheus.example.com \
  ./scripts/profile-prometheus-workload.py \
    --out harness/artifacts/prometheus-workload/prod-ha-a
```

For IAP-fronted Prometheus, pass the browser cookie through `PROM_COOKIE` or
`--cookie`. A full Cookie header is sent as-is; a bare token is sent as
`GCP_IAAP_AUTH_TOKEN=<token>` unless `--cookie-name` is set.

```bash
PROM_URL=https://prometheus.example.com \
PROM_COOKIE='GCP_IAAP_AUTH_TOKEN=...' \
  ./scripts/profile-prometheus-workload.py \
    --out harness/artifacts/prometheus-workload/prod-ha-a
```

Bounded PromQL capture for a 15d-retention Prometheus:

```bash
PROM_URL=https://prometheus.example.com \
  ./scripts/profile-prometheus-workload.py \
    --out harness/artifacts/prometheus-workload/prod-ha-a-promql \
    --include-promql \
    --max-top-metrics 15 \
    --max-top-label-values 15 \
    --density-metrics 3 \
    --density-window 1h \
    --density-window 24h \
    --retention 15d \
    --delay 10
```

Run it against one member of an HA pair first; if both members scrape the same
targets, do not sum their active-series counts as distinct workload. The output
contains raw responses plus `summary.md`. Use the summary to design seed profiles
with separate knobs for active series at evaluation time, series seen over the
window, samples per series, histogram bucket mix, scrape interval, and churn.

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
- `PROM_HARNESS_BASE_UNIX_SECONDS` (optional; if omitted the seed job picks a recent base time and writes it to `harness/artifacts/compare/seed-manifest.json`)
- `PROM_HARNESS_DATASET_VARIANTS` (optional comma-separated list such as `baseline,resets_gaps,churn_stale,histogram_burst`; when unset the harness keeps the legacy single dataset shape)

Seeded dataset shapes currently include:
- `baseline` — the default deterministic fixture
- `resets_gaps` — counter reset plus post-midpoint sampling gaps
- `churn_stale` — stronger series churn / staleness behavior
- `histogram_burst` — post-midpoint histogram / latency burst behavior

Comparator outputs:

- `harness/artifacts/compare/seed-manifest.json`
- `harness/artifacts/compare/compare-report.json`

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

That command runs the themed splits (`selector`, `aggregation`, `rate-family`, `range-selector`, `histogram`, `binary-arithmetic`, `comparison`, `set-operator`, `subquery`, `vector-matching`, `label-mutation`, `range-function`) and writes one compare report per theme into `harness/artifacts/compare/`. The full shortlist is useful for promotion planning and gap discovery; the stable common-dashboard subset is the current gate.

To collect PromQL candidates from a live Grafana instance, use the cookie-based extractor:

```bash
GRAFANA_URL=https://grafana.example.com \
GRAFANA_COOKIE="$(pbpaste)" \
  ./scripts/dump-grafana-promql.py --out harness/artifacts/grafana/promql-dump
```

The extractor writes raw dashboard JSON plus `queries.jsonl` and `queries.csv` under the selected output directory. Treat the output as exploratory input for corpus curation, not as a stable gate until queries are reviewed and promoted into a harness corpus.

### Corpus catalog and defaults

See `harness/corpus/README.md` for the corpus catalog, default-corpus policy, and guidance on which splits are stable gates, benchmark inputs, focused native-only probes, or exploratory dashboard-promotion corpora.

### Native SQL lowering starter corpus

For the native SQL lowering roadmap there are also smaller focused corpora:

- `harness/corpus/native-lowering-starter.json`
- `harness/corpus/native-lowering-starter.metadata.json`
- `harness/corpus/native-rollout-modes.json`

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

The native rollout modes corpus is a smaller shim-only parity check for rollout controls on the normal query endpoints. It exercises `native_lowering_mode=off|prefer|explain|shadow` and explicit `explain=1` requests on queries where the served result should still match Prometheus exactly:

```bash
./scripts/run-harness.sh --corpus native-rollout-modes.json --subjects shim
```

### Native measurement prerequisites corpus

A dedicated native-only measurement corpus now lives at:

- `harness/corpus/native-measurement-prereqs.json`
- `harness/corpus/native-measurement-prereqs.metadata.json`

It combines:

- the existing native-lowering starter rows
- explicit expected-error probes
- range step / time offset expansion rows
- dataset-variant parity rows for reset/gap, churn/staleness, and histogram-burst shapes

Run it with:

```bash
./scripts/run-harness.sh \
  --corpus native-measurement-prereqs.json \
  --subjects shim \
  --native-only \
  --dataset-variants baseline,resets_gaps,churn_stale,histogram_burst
```

This is the shortest command that exercises native measurement prerequisites in one harness pass without relying on silent local fallback.

To align the native measurement inventory with the read-only Prometheus compliance query suite without changing `harness/compliance/prom-compliance/`, generate the compliance-alignment report with:

```bash
go run ./cmd/promshim-promql-compliance
```

That writes:

- `harness/artifacts/matrices/native-promql-compliance-alignment.json`
- `harness/artifacts/matrices/native-promql-compliance-alignment.md`

and expands the upstream `promql-test-queries.yml` variant matrix so the current native measurement surface can be compared against the same query families the compliance suite expects.

Corpus rows can also set:
- `"nativeLoweringMode": "off|explain|shadow|prefer|force_supported|local_pushdown"`
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

When both `rangeOffsets` and `rangeStepMatrix` are set, the report variant name combines them (for example `boundary/step_eq_range`). This is most useful for promshim-specific rollout validation; in practice the native rollout modes corpus scopes rows to `"subjects": ["shim"]` so optional promclick behavior is not treated as a rollout gate.

A small focused harness-variant probe corpus is available at:
- `harness/corpus/harness-variant-probes.json`

Run it with:

```bash
./scripts/run-harness.sh --corpus harness-variant-probes.json --subjects shim
```

A small focused dataset-variant probe corpus is also available at:
- `harness/corpus/dataset-variant-probes.json`

Run it with:

```bash
./scripts/run-harness.sh --corpus dataset-variant-probes.json --subjects shim --dataset-variants baseline,resets_gaps,churn_stale,histogram_burst
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
- add new corpus rows only after parity is verified in `harness/artifacts/compare/compare-report.json` and corresponding unit/integration coverage exists.
- if a row should only run against specific optional subjects, set `"subjects": ["shim"]` or `"subjects": ["promclick"]`; omitting `subjects` compares against all configured subjects.
