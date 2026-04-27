# Harness corpus catalog

This directory contains query corpora for different validation jobs. Keep corpus
splits when the rows have different data dependencies, runtime cost, failure
policy, or caller expectations. Do not merge corpora only because their query
syntax overlaps.

## Default corpus policy

| Caller | Default corpus | Dataset / stack | Purpose | Failure policy |
| --- | --- | --- | --- | --- |
| `./scripts/run-harness.sh --suite differential` | `queries.json` | main harness fixture | Broad first-party Prometheus-vs-shim parity coverage. | Failures are shim regressions or known gaps to triage. |
| `./scripts/run-harness.sh --suite dashboard` | `common-dashboard-subset.json` | main harness fixture | Stable dashboard-shaped parity gate promoted from the Grafana shortlist. | Failures are regressions; excluded shortlist rows stay documented in metadata. |
| `./scripts/run-harness.sh --suite compliance` | upstream `promql-test-queries.yml`, patched for Prometheus 3.x at runtime | frozen compliance stack | PromQL semantic compatibility against the upstream compliance suite. | Only policy-approved entries in `harness/compliance/expected-failures.json` are allowed. |
| `./scripts/run-harness.sh --suite bench` / `./scripts/run-bench.sh` | `bench-native-lowering.json` | frozen compliance stack | Short native-lowering tripwire against the small fixture and baseline file. | Benchmark regressions fail when they exceed the gate. |
| `./scripts/run-sweep.sh` sparse benchmark | `bench-native-lowering-7d.json` via `--corpus-set native` | isolated benchmark stack, 7d sparse by default | Default long-range benchmark signal for native lowering, routing, and CBE changes. | Query errors or benchmark command failures fail the bench pass. |
| `./scripts/run-sweep.sh --density dense` | `bench-processing-7d.json` via `--corpus-set processing` | isolated benchmark stack, 7d dense by default | Processing-heavy bounded-output workload where Prometheus timing bands are meaningful. | Query errors or benchmark command failures fail the bench pass. |

Default runs should be boring, stable, and fast enough for routine use. Use
opt-in corpora for native-only gaps, stress data, dashboard promotion, or
optimization experiments.

## Corpus groups

### First-party differential corpora

| Corpus | Rows | Keep split? | Use when |
| --- | ---: | --- | --- |
| `queries.json` | 152 | Yes. It is the broad main-harness default and includes many first-party PromQL shapes. | Checking general shim parity on the main fixture. |
| `common-dashboard-subset.json` | 92 | Yes. It is a promoted dashboard-shaped subset with documented exclusions. | Running the default dashboard suite or checking user-facing dashboard compatibility. |
| `native-lowering-starter.json` | 22 | Yes. It is smaller and roadmap-focused, with metadata buckets. | Iterating on native lowering coverage without the full default corpus. |
| `path2-measurement-prereqs.json` | 30 | Yes. It is native-only and includes expected-error, offset, step, and dataset-variant rows. | Measuring native-only readiness and public error envelopes. |
| `histogram-native-support.json` | 2 | Yes. It is a focused native-only histogram quantile gate. | Checking grouped/rate-fed classic histogram support. |
| `phase7-rollout.json`, `phase10-staleness-probes.json`, `phase12-harness-variants.json`, `phase12-dataset-variants.json` | 2-10 each | Keep for now, but treat as legacy focused probes. Prefer new names that describe behavior when replacing them. | Reproducing the specific rollout, staleness, harness-variant, or dataset-variant checks they encode. |

### Dashboard promotion corpora

| Corpus | Rows | Keep split? | Use when |
| --- | ---: | --- | --- |
| `draft-grafana-top-panel-shortlist.json` | 97 | Yes. It remains exploratory and includes rows not promoted into the stable subset. | Promoting real dashboard shapes after parity is verified. |
| `draft-grafana-top-panel-shortlist.dataset-variants.json` | 79 | Yes. Dataset-variant hints change runtime and failure interpretation. | Exercising shortlist rows across reset/gap, churn/staleness, and histogram-burst shapes. |
| `draft-grafana-top-panel-shortlist.themes/*.json` | varies | Yes. Theme splits are triage aids, not standalone gates. | Narrowing dashboard failures by PromQL family. |
| `draft-grafana-top-panel-shortlist.dataset-variants.themes/*.json` | varies | Yes. Theme plus dataset variants is a distinct triage axis. | Narrowing dataset-sensitive dashboard failures. |

Promotion rule: add a dashboard row to a stable corpus only after parity is
verified and corresponding unit or integration coverage exists. Keep excluded
candidate reasons in metadata rather than hiding them in expected-failure files.

### Benchmark corpora

Family metadata lives in `bench-native-lowering.metadata.json`,
`bench-processing.metadata.json`, and `bench-optimization-tuning.metadata.json`.
Those files record default status, profile/eval-time assumptions, and suggested
commands for the benchmark corpus families.

| Corpus | Rows | Keep split? | Use when |
| --- | ---: | --- | --- |
| `bench-native-lowering.json` | 26 | Yes. It belongs to the frozen compliance-stack tripwire and baseline. | Running `run-bench.sh` standalone or the `bench` suite. |
| `bench-native-lowering-7d.json` | 34 | Yes. It is the default sparse long-range sweep corpus and has more routing/CBE shapes than 30d/1y. | Routine sweep benchmarks and routing comparisons. |
| `bench-native-lowering-30d.json`, `bench-native-lowering-1y.json` | 16 each | Yes. Long windows require different query windows and pinned eval times. | Checking scaling across longer sparse profiles. |
| `bench-processing-7d.json`, `bench-processing-30d.json`, `bench-processing-1y.json` | 8 each | Yes. These are dense/processing workloads with profile-specific windows. | Measuring bounded-output processing cases, especially on dense data. |
| `bench-optimization-tuning.json` | 16 | Yes. It is a compliance-stack optimization experiment corpus. | Local tuning on the small fixture. |
| `bench-optimization-tuning-7d.json` | 15 | Yes. It is the only long-range optimization corpus currently present. | Focused 7d optimization sweeps with `--corpus-set optimization`. |

`--corpus-set optimization` intentionally supports only `--profile 7d` until
matching 30d and 1y corpora exist. This avoids dry-run plans and sweep artifacts
that point at missing files.

## Add or change a corpus

Before adding a corpus or moving rows between corpora, record:

- the intended caller and whether it is a default or opt-in input;
- the required stack, fixture, profile, density, and eval-time assumptions;
- whether failures mean regressions, visible gaps, allowed compliance deviances,
  or benchmark regressions;
- whether rows use exact comparison, structural comparison, native-only mode,
  dataset variants, or advisory Prometheus timing bands;
- the validation command that proves the corpus parses and the caller selects it.

Prefer metadata files for durable rationale when a corpus excludes candidates,
combines sources, or has non-obvious comparison rules.
