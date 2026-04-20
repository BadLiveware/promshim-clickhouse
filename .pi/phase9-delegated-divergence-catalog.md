# Phase 9 — Delegated subquery divergence catalog (Prometheus vs shim/ClickHouse)

This catalog tracks known mismatch classes for delegated subquery behavior.

## Class A — Matrix-root selector subquery in instant queries

- Example query:
  - `harness_requests_total[5m:60s]`
- Historical observation:
  - Prometheus and delegated ClickHouse path differed on point-count boundaries.
- Mitigation status:
  - **Mitigated** by forcing local execution for delegated-compatible matrix-root subqueries in instant mode.
- Validation status:
  - Stable harness corpus case added: `subquery_matrix_root_selector_instant` (passing).

## Class B — Delegated rate-family subquery arithmetic/rate differences

- Example queries:
  - `rate(harness_requests_total[5m:60s])`
  - `increase(coredns_dns_request_size_bytes_count[5m:60s])`
  - `delta(up[5m:30s])`
- Historical observation:
  - Numeric mismatches were observed in delegated evaluation (e.g. `0.04` vs `0.041666...`) for subquery-based rate-family functions.
- Likely cause:
  - ClickHouse delegated expression planner differs on subquery/range boundary and difference semantics for these functions.
- Mitigation status:
  - **Mitigated** via explicit unsupported boundaries for this whole class.
- Current handling:
  - Analyzer now rejects these subquery forms explicitly (`difficulty=hard`) instead of silently delegating potentially divergent semantics:
    - `rate`, `irate`, `increase`, `delta`, `idelta`, `deriv`, `changes`
  - Keep out of stable differential corpus until local parity implementation is added.

## Class C — Delegated parser/semantic feature asymmetries

- Scope:
  - Expression forms supported in Prometheus parser/runtime but not identically handled by delegated ClickHouse parser/runtime for some advanced combinations.
- Mitigation status:
  - **Partially mitigated** where shim-side rewrites exist (`@ start()/@ end()` rewriting).
  - **Open** for other advanced delegated subquery forms.

## Excluded/unstable cases (kept out of stable differential corpus)

1. `rate(harness_requests_total[5m:60s])`
   - Reason: historical numerical mismatch in delegated subquery evaluation (`0.04` vs `0.041666...`) for current ClickHouse behavior.
   - Status: excluded until targeted local fallback or delegated parity mitigation lands.

2. `irate(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

3. `increase(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

4. `delta(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

5. `idelta(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

6. `deriv(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

7. `changes(harness_requests_total[5m:60s])`
   - Reason: delegated-subquery arithmetic-rate family semantic sensitivity; aligned with Class B mitigation.
   - Status: excluded until dedicated local implementation or delegated parity mitigation lands.

## Next mitigation target recommendation

1. Keep Class A locked with stable harness case (`subquery_matrix_root_selector_instant`).
2. For Class B, either:
   - implement local `rate` over local subquery windows for targeted shapes, or
   - add selective local fallback/explicit unsupported boundary for delegated mismatch classes.
