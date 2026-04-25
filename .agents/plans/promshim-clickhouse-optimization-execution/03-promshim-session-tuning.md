# 03. Promshim-owned ClickHouse session and query tuning

## Purpose and scope

Find ClickHouse settings that promshim can safely apply to its own queries or
sessions. This is separate from server/operator tuning: settings here must be
allowlisted, version-aware, explain-visible, artifact-visible, and rollbackable
by promshim configuration.

## Prerequisites

- Benchmark reference profile from file 02 is fixed or explicitly unchanged.
- Settings profile plumbing exists in `internal/promshim/storage/settings_profile.go`.
- Benchmark reports capture `settingsProfile`.
- Query-log correlation is reliable.

## Affected areas

- `internal/promshim/storage/settings_profile.go`
- `internal/promshim/storage/settings_profile_test.go`
- `internal/promshim/options.go`
- `internal/promshim/service.go`
- `internal/promshim/local/explain.go`
- `harness/bench/docker-compose.yml`
- `harness/compliance/docker-compose.yml` only for rollback/env propagation
- `scripts/run-sweep.sh`
- `README.md`
- `docs/clickhouse-tuning-inventory.md`

## Candidate settings and profiles

Evaluate candidates only through named profiles. Do not add hidden ad-hoc query
parameters.

1. `benchmark_control`
   - Purpose: reduce measurement variance, not production default.
   - Candidate knobs: bounded `max_threads`, disabled result cache, explicit
     logging/profile settings if query-scoped.
   - Expected signal: lower run-to-run variance without changing correctness.

2. `tiny_instant`
   - Purpose: reduce overhead for tiny selector/instant families.
   - Candidate knobs: lower `max_threads`, short timeout, avoid expensive caches.
   - Expected signal: lower CH millis or wall-clock for tiny native queries
     without hurting moderate selector rows.

3. `repeated_selective`
   - Purpose: improve repeated selective dashboard queries.
   - Candidate knobs: `use_query_condition_cache` when ClickHouse version and
     analyzer behavior support it.
   - Expected signal: repeated-run ProfileEvents or planning/filter counters
     improve; result freshness is unchanged because this is not query cache.

4. `aggregation_heavy`
   - Purpose: bound memory and improve heavy aggregations.
   - Candidate knobs: `max_threads`, external aggregation/spill thresholds,
     memory caps only when errors remain understandable.
   - Expected signal: lower peak memory or fewer timeouts on dense aggregation
     controls without broad p50 regression.

5. `long_range_scan`
   - Purpose: stabilize wide range scans.
   - Candidate knobs: thread/concurrency profile and read caps tied to estimates.
   - Expected signal: lower p95 and bounded memory for 30d/1y sparse controls;
     no false failures for legitimate long-range queries.

6. Explicitly rejected by default: result query cache.
   - It can mask optimizer work and has freshness semantics that PromQL users may
     not expect. Only test under an opt-in experiment with a documented freshness
     contract.

## Implementation tasks

1. Add profile experiment harness support.
   - Make `scripts/run-sweep.sh` pass `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE` to
     benchmark promshim containers if it does not already.
   - Ensure report rows show the selected profile and concrete settings/skips in
     explain output.
   - Acceptance: one smoke run with `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=none`
     and one with `default_safe` differ in profile metadata only when no
     performance settings are applied.

2. Promote one profile at a time from provenance-only to applied settings.
   - Start with `benchmark_control` or `tiny_instant` because risk is bounded.
   - Add exact allowlist values and version checks in `settings_profile.go`.
   - Add skip reasons for unsupported versions, disabled evidence gates, or
     unset optional caps.
   - Acceptance: unit tests cover applied settings, skipped settings, invalid
     values, and version gates.

3. Run profile A/B experiments.
   - Compare `none`, `default_safe`, and the candidate profile on the same corpus
     and benchmark reference profile.
   - Include negative controls where the profile should not be used.
   - Acceptance: artifact notes identify whether the profile should serve by
     default, serve only when explicitly configured, remain experiment-only, or
     be rejected.

4. Add profile selection rules.
   - If a profile should be automatically selected by query family, implement it
     as a visible, explainable decision with override controls.
   - If automatic selection is not proven, keep the profile opt-in only.
   - Acceptance: explain output states profile name, reason, applied/skipped
     settings, and rollback control.

5. Document safe promshim-owned tuning.
   - Update README and `docs/clickhouse-tuning-inventory.md` with applied
     settings, risks, validation artifacts, and rollback.
   - Acceptance: docs do not instruct users to change server settings for
     promshim-owned query tuning.

## Validation tasks

Fast checks:

```bash
go test ./internal/promshim/storage ./internal/promshim ./internal/promshim/httpapi
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
git diff --check
```

Profile experiment pattern:

```bash
PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=none ./scripts/run-sweep.sh \
  --name settings-profile-none-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer,force_supported \
  --corpus-set native --memory summary

PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=<candidate> ./scripts/run-sweep.sh \
  --name settings-profile-<candidate>-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer,force_supported \
  --corpus-set native --memory summary
```

For every run:

```bash
./scripts/bench-matrix.sh --sweep <manifest.json> --per-query
jq '{rows: (.clickHouseQueryLog|length), missing: (.missingLogComments|length), errors}' <memory-summary.json>
```

Compliance gate before automatic profile selection:

```bash
PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE=<candidate> ./scripts/run-sweep.sh \
  --name settings-profile-<candidate>-compliance --skip-bench
```

## Exit criteria

- At least one promshim-owned profile is tested with A/B artifacts and adopted,
  kept opt-in, or rejected with evidence.
- Any applied performance setting has an allowlist entry, value bounds, version
  behavior, explain provenance, benchmark artifact evidence, and rollback.
- No result-query cache default is introduced.
- Docs clearly separate promshim-owned per-query settings from ClickHouse
  operator/server tuning.

## Handoff to next file

Use the selected safe settings profile as a controlled variable for native SQL
and IR optimization. Do not attribute SQL optimization wins to settings-profile
changes unless the artifacts isolate those variables.
