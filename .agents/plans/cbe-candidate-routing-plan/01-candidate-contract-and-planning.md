# 01. Candidate contract and planning

## Purpose and scope

Introduce a first-class CBE candidate model for tiers 2, 3, and 4 without
changing served behavior. This slice turns the current strict-vs-local override
logic into an explicit candidate-planning pipeline that can be inspected,
tested, and extended safely.

Scope is scaffolding and observability only:

- identify candidates;
- separate support/correctness eligibility from cost ranking;
- attach stable candidate IDs and rejection reasons;
- expose candidate metadata in explain/debug paths;
- keep `strict`, `cost_shadow`, and `cost_prefer` served behavior unchanged.

## Prerequisites

- Current gated cost-routing foundation is merged.
- `AGENTS.md` CBE policy allows tiers 2, 3, and 4 as routing candidates.
- Existing tests for routing policy, query classification, and explain metadata
  pass.

## Affected areas

- `internal/promshim/service.go`
- `internal/promshim/routing_policy.go`
- `internal/promshim/query_cost_class.go`
- `internal/promshim/cost_shadow.go`
- `internal/promshim/httpapi/router.go`
- new candidate model file, e.g. `internal/promshim/cbe_candidates.go`
- service and routing tests

## Implementation tasks

- [ ] Add candidate identifiers with stable enum values.
  - Include at least `native_sql`, `local_pushdown`, and `full_local`.
  - Keep room for future tier-3 variants such as native-SQL subtree pushdown and
    PromQL-subtree delegation if those are distinguishable today.
- [ ] Add `ExecutionCandidate` and `CandidateDecision` domain structs.
  - Candidate fields should include ID, tier, strategy, cost family, support
    state, known-correct state, estimates, caps, rejection reasons, and whether
    it is strict/reference/selected/served.
  - Do not store raw PromQL or high-cardinality matcher values in structures that
    feed metrics labels.
- [ ] Implement candidate planning for existing strict paths.
  - Native SQL candidate exists when strict planning produces native SQL.
  - Full-local candidate exists when the local executor already supports the
    query.
  - Tier-3/local-pushdown candidate exists only when current planning can produce
    that shape; do not add new semantic coverage in this slice.
- [ ] Separate candidate gates.
  - `unsupported_shape`: candidate cannot execute this query.
  - `known_divergence`: candidate is supported but not allowed by correctness
    evidence.
  - `missing_estimate`, `stale_estimate`, `over_cap`, `low_confidence`,
    `family_gate_disabled`, and `policy_ignored` should remain stable reasons.
- [ ] Keep served behavior unchanged.
  - `strict` serves strict/reference.
  - `cost_shadow` serves strict/reference.
  - `cost_prefer` should continue to use current gated local override behavior
    until later slices replace it with candidate ranking.
- [ ] Add candidate metadata to explain responses.
  - Include strict/reference candidate and candidate list.
  - Include rejected candidates with bounded reasons.
  - Avoid exposing implementation pointers, raw SQL in new fields, or raw labels.
- [ ] Add targeted comments where strict fallback is required by safety.
  - Explain why known-correct and cap checks happen before cost ranking.

## Validation tasks

- [ ] Unit-test candidate planning for representative shapes:
  - native SQL strict candidate;
  - full local candidate;
  - already-local strict fallback;
  - unsupported candidate rejection;
  - `force_supported` ignores non-native candidates.
- [ ] Unit-test explain output includes candidate list and stable reasons.
- [ ] Run:

```bash
go test ./internal/promshim/... ./internal/promharness ./cmd/promshim-bench
```

- [ ] Run a dry-run sweep to ensure report generation remains stable:

```bash
./scripts/run-sweep.sh --dry-run --estimate --name cbe-candidate-contract-dry-run
```

## Compatibility, docs, and cleanup

- [ ] Keep existing routing headers stable.
- [ ] Do not remove existing `X-Promshim-Strict-Strategy` or
  `X-Promshim-Selected-Strategy` fields.
- [ ] If explain JSON changes are additive, document the new candidate field in
  `README.md`.
- [ ] Do not change `force_supported`, `off`, or native-lowering `shadow`
  behavior.

## Exit criteria

- [ ] Candidate structs and stable IDs exist.
- [ ] Candidate planning is test-covered and does not change served behavior.
- [ ] Explain output can show candidate lists and rejection reasons.
- [ ] Existing routing-policy and service tests pass.
- [ ] No new tier-3/tier-4 semantic coverage was introduced.

## Handoff to next file

After candidates are explicit, move to
[`02-estimates-and-warmup-lifecycle.md`](02-estimates-and-warmup-lifecycle.md)
to make candidate estimates reproducible and warmable.
