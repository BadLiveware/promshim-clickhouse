# PromQL Conformance Suite Plan

## Goal

Run the upstream [prometheus/compliance](https://github.com/prometheus/compliance)
PromQL suite against the shim periodically, so that breadth-of-coverage
gaps surface as concrete failing test cases instead of being remembered
only when a user's query 400s.

This is a **breadth** signal. It is complementary to — and does not
replace — the differential harness in `harness/`, which is a **depth**
signal that catches correctness drift on queries we claim to support.

## Scope

In:

- the `promql/` conformance subsuite of the compliance repo, which
  covers language surface: selectors, aggregators, range and instant
  functions, binary operators, subqueries, offset/at-modifier semantics
- baselining current failures against the tiers in
  `.pi/promql-coverage-plan/` so each tier's completion produces a
  measurable drop in the failure count
- scheduled / on-demand runs, not CI-on-every-commit

Out:

- the `alert_generator/` subsuite — the shim is a read-only PromQL
  endpoint; alert rule evaluation is not in scope
- the `remote_write_sender/` subsuite — the shim's write path is
  already tested by the existing harness via the seed stage
- native histogram tests — classic buckets only, per the harness
  coverage plan's existing non-goal
- treating conformance as the acceptance gate for behavior changes;
  the differential harness remains the gate

## How it runs

The `promql/` subsuite is a Go test binary driven by a YAML config that
lists target HTTP endpoints. Point it at two endpoints:

1. `http://prometheus:9090` — oracle
2. `http://promshim:9090` — subject

It replays the same corpus against both and diffs results. Our shim
already listens on `:9090` inside the harness docker-compose network,
so wiring is a config addition, not a service addition.

Invocation lives as a new harness profile, alongside `jobs`:

```yaml
conformance:
  build:
    context: ..
    dockerfile: harness/Dockerfile
    target: promql-conformance
  depends_on:
    - prometheus
    - promshim
  environment:
    CONFORMANCE_TARGET_ORACLE: http://prometheus:9090
    CONFORMANCE_TARGET_SUBJECT: http://promshim:9090
    CONFORMANCE_OUTPUT_DIR: /artifacts/conformance
  volumes:
    - ./artifacts:/artifacts
  profiles: ["conformance"]
```

Run with `docker compose --profile conformance run --rm conformance`.

## Baselining

A single run against current `main` will produce N failures. Those are
not regressions — they are existing coverage gaps. Capture the baseline:

1. Run the suite once, dump per-test pass/fail into
   `harness/artifacts/conformance/baseline.json`.
2. Tag each failure with its owning tier from the coverage plan:
   - tier 1 trivial (expected to close with
     [01-tier-1-trivial.md](./promql-coverage-plan/01-tier-1-trivial.md))
   - tier 2 moderate ([02-tier-2-moderate.md](./promql-coverage-plan/02-tier-2-moderate.md))
   - tier 3 per-item
   - lowering plan (e.g., subqueries-under-range-functions)
   - **unknown** — genuinely surprising; investigate before triaging
3. Commit the baseline. A re-run diffs against it: `new failures since
   baseline` is the real signal.

The "unknown" bucket is the most valuable output of the first run. It
surfaces coverage gaps the coverage plan didn't anticipate.

## Cadence

- **Manual trigger** on demand
- **Pre-milestone** — run before cutting a coverage-plan tier as done,
  to confirm the expected conformance cases went green and no
  previously-green case regressed
- **Monthly** as a background sweep to catch upstream additions

No CI-on-every-commit until the baseline is near-zero. Until then the
noise-to-signal is too low for it to be useful as a gate.

## Not-goals

- do **not** treat every conformance failure as a bug — most are
  documented gaps
- do **not** vendor or fork the upstream suite; track `main` and update
  the baseline file when the suite adds cases
- do **not** run the suite inside the differential harness's artifact
  directory without a subdirectory; keep the two outputs separate so
  harness tooling doesn't have to distinguish them

## Acceptance

This plan is complete when:

- a `conformance` profile exists in `harness/docker-compose.yml`
- the conformance runner is reproducible via a single `docker compose
  --profile conformance run` command
- `harness/artifacts/conformance/baseline.json` exists in the repo,
  with each failure tagged by owning plan / tier
- the conformance-plan entry is cross-linked from
  `.pi/promql-coverage-plan/README.md` so future tier work knows to
  re-baseline

## Cross-references

- [.pi/promql-coverage-plan/](./promql-coverage-plan/) — the tiers that
  own the conformance gaps
- [.pi/native-sql-lowering-plan/](./native-sql-lowering-plan/) — Phase 6b
  and Phase 7 pick up conformance cases that require native lowering
- `harness/` — the existing differential harness, unchanged by this plan
