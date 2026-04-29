# Physical-decision telemetry plumbing

## Scope
Tier-2/native-SQL workflow safety prerequisite.

## Goal
Expose compact per-query physical-decision summary in normal query responses and capture it in benchmark artifacts, so future optimization hypotheses are strategy-level and falsifiable.

## Changes
- `internal/promshim/httpapi/router.go`
  - Added `Response.PhysicalDecisions` field.
  - Emits `X-Promshim-Physical-Decisions` header when present.
- `internal/promshim/service.go`
  - Added `physicalDecisionSummary(explain local.ExplainNode)` helper.
  - Populates `Response.PhysicalDecisions` for normal instant/range query responses from selected explain node decisions (including children).
- `internal/promharness/bench.go`
  - Added `physicalDecisions` capture from `X-Promshim-Physical-Decisions` header.
  - Stores it as `physicalDecisions` in v2 shim mode result rows.

## Validation
- `go test ./internal/promshim/httpapi ./internal/promshim ./internal/promharness -run 'TestParseHeaders|TestRunBenchV2|TestHandler|TestQueryRangeExplainIncludesPhysicalDecisions'`

## Runtime verification
- Bench run in progress (`proc_29`) to verify telemetry appears in bench artifact.

## Runtime verification
Bench artifact:
- `harness/artifacts/bench/standalone/20260429-iter35-physical-decision-telemetry/bench-report.json`

Observed per-row shim metadata now includes physical decisions:
- `physicalDecisions": "fused_range_aggregation=native_grid_sum_aggregation,query_settings=set_max_threads"`

## Decision
Accepted.

This is enabling infrastructure for safer strategy-level optimization work. It does not alter query semantics; it surfaces existing selected physical decisions in response headers and bench artifacts.

## Validation summary
- `go test ./internal/promshim/httpapi ./internal/promshim ./internal/promharness -run 'TestParseHeaders|TestRunBenchV2|TestHandler|TestQueryRangeExplainIncludesPhysicalDecisions'`
- Bench verification run completed successfully with telemetry captured.
