# Cost routing policies


Cost routing is an opt-in routing policy layered on top of the native lowering
mode. Strict routing remains the default and keeps the priority order above:
whole-query delegation, native SQL, local with pushdown, then full local.
`force_supported`, `off`, and the existing native-lowering `shadow` mode ignore
cost routing so they continue to serve as native-only, local-baseline, and
native-shadow visibility modes.

The global policy is controlled by `PROM_SHIM_ROUTING_POLICY` and can be
overridden per request with `routing_policy=...`.

| Policy | Served result | Use case |
|---|---|---|
| `strict` | First successful tier in priority order | Default and rollback behavior. |
| `cost_shadow` | Strict result | Computes the cost decision and may run bounded alternate candidates in the background for evidence. |
| `cost_prefer` | Strict unless all cost gates pass | Opt-in local/dev rollout for bounded families with estimates, hard caps, and a predicted win. |

Local overrides under `cost_prefer` require explicit family gates through
`PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES`, for example
`selector_instant,rate_instant`. Served requests use cache-only estimates:
cold, missing, or stale selector statistics choose strict/reference behavior
until `cost_shadow` or another bounded warmup path refreshes the estimate cache.
Disabled families, hard-cap violations, low-confidence predictions, histogram
helpers without their own evidence, and broad range-query candidates also fall
back to strict routing. Removing the family gate or setting
`PROM_SHIM_ROUTING_POLICY=strict` is the configuration-only rollback path.

Successful query responses include stable routing headers such as
`X-Promshim-Routing-Policy`, `X-Promshim-Routing-Decision`,
`X-Promshim-Strict-Strategy`, `X-Promshim-Selected-Strategy`,
`X-Promshim-Strict-Candidate`, `X-Promshim-Selected-Candidate`,
`X-Promshim-Served-Candidate`, `X-Promshim-Routing-Reason`, and
`X-Promshim-Cost-Family`. Explain responses include the same routing metadata,
enabled cost-routing local families, and CBE candidate metadata showing the
strict/reference candidate, selected candidate, served candidate, candidate
eligibility, and bounded rejection reasons.

In `cost_shadow`, promshim continues to serve strict/reference results while it
ranks eligible candidates, chooses at most one bounded alternate candidate
(`native_sql`, `local_pushdown`, or `full_local`), and records candidate-level
outcomes. Alternate execution is skipped when the selected candidate is already
served or when there is no executable eligible candidate.

### Current served CBE family gates

Current `cost_prefer` served-candidate enablement is intentionally narrow:

- `rate_instant` (short-window, single-selector instant `rate`/`increase`) may
  serve local when estimates/caps/margins/family-gate checks pass.
- selector/histogram/range families remain strict unless later bounded evidence
  explicitly enables them.
- strict-local fallback paths are not treated as CBE wins (`strict_reference_already_local`).

### Validation bundle required before enabling a new family/candidate

Before enabling any additional served CBE family/candidate, preserve a named
artifact bundle with all of:

1. shadow sparse sweep for the candidate family,
2. warmed cost-prefer differential sweep for the candidate family,
3. long-range sparse negative control,
4. dense/cardinality negative control,
5. strict rollback verification,
6. strict compliance sweep.

Use `go run ./cmd/promshim-routing-calibrate --sweep ...` over that bundle to
refresh `.pi/cost-routing-calibration.json` and `.pi/cost-routing-calibration.md`.
Do not expand served families without this evidence bundle.
