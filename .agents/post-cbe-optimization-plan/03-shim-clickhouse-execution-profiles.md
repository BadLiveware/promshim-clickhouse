# 03. Shim-owned ClickHouse execution profiles

## Purpose and scope

Define and implement promshim-owned ClickHouse tuning that is isolated to
promshim sessions or statements. This is separate from operator/server tuning:
promshim may set safe per-query options on its own requests, but it must not
assume global ClickHouse settings or mutate server-wide behavior.

This slice starts with safety, traceability, and version-aware settings
allowlisting. Performance-oriented profiles come later and must be justified by
query-family measurements.

## Prerequisites

- Stage 01 ClickHouse tuning inventory exists.
- Explain output can expose selected settings profile, specific settings, and
  reasons.
- CBE candidate metadata can carry settings-profile eligibility or has a planned
  place for doing so.

## Affected areas

- ClickHouse client/session construction.
- Statement/query settings emission.
- CBE candidate metadata.
- Explain output, headers, metrics, and sweep artifacts.
- Config and per-request override policy.

## Requirements

- Settings must be allowlisted and scoped to promshim-owned ClickHouse queries.
- Profiles should prefer shape-driven wins (better bounds, pruning, transfer
  reduction) over fragile manual forcing of planner behavior.
- Settings must be version-aware when ClickHouse support varies.
- Unsupported settings must be visible and must not silently change semantics.
- Settings must have bounded, stable profile names.
- Explain output must show:
  - selected settings profile;
  - individual settings applied;
  - settings skipped or unavailable;
  - reason codes;
  - candidate or query-family association.
- Safety settings can be introduced before performance profiles. Performance
  settings require measured evidence.
- ClickHouse settings examples must be checked against the local
  `~/code/external/ClickHouse` checkout or versioned docs/source before they are
  added to promshim's allowlist.

## Implementation tasks

### 1. Build the settings allowlist

The first pass should bias toward settings with clear semantics and measurable
signals, especially settings that help repeated selective reads, safety, and
traceability.

- [ ] Survey `~/code/external/ClickHouse` for relevant settings, default values,
  scope, version availability, ProfileEvents, query-log fields, and EXPLAIN
  behavior.
- [ ] Survey `~/code/external/hyperdx` or similar ClickHouse-backed
  observability tools for practical operational/query-shape settings, treating
  them as examples rather than authority.
- [ ] Classify candidate settings by scope:
  - query/statement;
  - session/user profile;
  - server/operator only;
  - unsafe/out of scope.
- [ ] Record minimum ClickHouse version or feature-detection requirement for
  version-sensitive settings.
- [ ] Define validation signals for each setting:
  - lower memory;
  - lower rows/bytes read;
  - fewer function executions;
  - fewer round trips;
  - bounded latency;
  - safer timeout/cancellation behavior.
- [ ] Explicitly classify:
  - `use_query_condition_cache` as a candidate for repeated selective dashboard
    workloads;
  - query cache as freshness-sensitive and likely unsuitable for PromQL default
    paths;
  - PREWHERE-related tuning as something to measure per query family rather than
    a blanket profile default.
- [ ] Reject settings whose behavior is not understood or cannot be validated.

### 2. Add traceability settings and query identity

- [ ] Ensure every promshim ClickHouse query carries a stable query ID or log
  comment that can be correlated with request/explain/sweep artifacts.
- [ ] Include candidate ID, strategy, query family, normalized PromQL hash, and
  sweep/benchmark marker where safe and bounded.
- [ ] Avoid raw PromQL or high-cardinality label values in metrics labels.
- [ ] Preserve enough query-log detail to join ProfileEvents to CBE decisions.

### 3. Add safety-first execution profile

Start with settings that protect the shim and ClickHouse rather than trying to
win latency.

- [ ] Add explicit query timeout/cancellation behavior.
- [ ] Add bounded memory settings where appropriate.
- [ ] Add read/result row or byte caps where compatible with route safety.
- [ ] Enforce read-only behavior if not already guaranteed by the client/user.
- [ ] Surface cap failures as candidate rejection or user-facing errors according
  to existing policy.
- [ ] Keep strict/reference fallback behavior for missing estimates or over-cap
  candidates.

### 4. Define measured performance profiles

Introduce a small set of names, but do not populate aggressive settings until
measurements justify them.

Candidate profiles:

- `default_safe` — traceability plus safety caps.
- `repeated_selective` — repeated selective dashboard/profile candidate that may
  experiment with query condition cache when evidence supports it.
- `tiny_instant` — low-overhead profile for small instant selectors.
- `simple_range` — balanced profile for bounded range selectors.
- `long_range_scan` — throughput-oriented profile for wide scans.
- `aggregation_heavy` — aggregation-focused profile with memory safeguards.
- `join_heavy` — vector-matching or SQL-join profile, likely shadow-only at
  first.
- `subtree_pushdown` — profile for hybrid plans where transfer volume matters.
- `benchmark_control` — controlled profile for apples-to-apples experiments.

Profiles should stay small in number and clearly explain why a family is mapped
there. Do not create many near-duplicate profiles that differ only in cosmetic
SQL-shape preferences.

For each profile:

- [ ] Define eligible query families and candidate types.
- [ ] Define required estimates and caps.
- [ ] Define settings and defaults.
- [ ] Define fallback behavior if the setting is unavailable.
- [ ] Define expected ProfileEvents or runtime signals.

### 5. Integrate profiles with CBE

- [ ] Let candidates declare compatible settings profiles.
- [ ] Include settings profile in cost estimates when it materially changes
  expected work or resource usage.
- [ ] Avoid exploding the candidate space early: start with one profile per
  candidate unless a specific A/B experiment is being run.
- [ ] Reject or shadow candidates when required settings are unavailable or
  untrusted.

## Validation tasks

Fast checks:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
```

Settings visibility smoke through explain once implemented:

```bash
go test ./internal/promshim/httpapi ./internal/promshim
```

Named sweeps for safety/profile validation:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-ch-settings-safe-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --shim-modes prefer,force_supported,off \
  --corpus-set native \
  --memory summary
```

For performance settings, compare default-safe against one profile at a time and
preserve both sweep directories. Do not accept p50-only wins. Use ProfileEvents
and explain output according to `.pi/skills/measuring-ch-optimizations/SKILL.md`.

## Exit criteria

- [ ] Settings allowlist exists and distinguishes shim-owned query/session
  settings from operator/server recommendations.
- [ ] Query identity/log correlation is visible in explain and sweep artifacts.
- [ ] A safety-first profile exists or is designed with explicit caps and
  fallback behavior.
- [ ] Performance profiles are named and gated but not broadly enabled without
  evidence.
- [ ] Unsupported settings are observable and do not silently change behavior.
- [ ] No global ClickHouse server tuning is performed by promshim.

## Handoff to next file

After this slice, stage 04 can combine IR plan-shape optimization with
query-family-specific ClickHouse settings profiles. Stage 05 documents the
separate operator-facing server profile.
