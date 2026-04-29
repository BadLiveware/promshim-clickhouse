# Iteration 88: avg-memory boundary clamp execution

## Scope
Tier-2/native-SQL only.

## Candidate
Boundary-grid fan-out mitigation in cumulative avg path:
- clamp `lower_prev_bound` to `required_start_ms - 1ms` so pre-range ASOF probes do not request timestamps earlier than the guaranteed selector-read floor.

## Code change
- `internal/promshim/storage/selector_sql.go`
  - in `buildRangeWindowSelectorCumulativeAvgPerStepSQL` / `gridSQL`, changed:
    - before: `eval_ts - toIntervalMillisecond(offset + lookback + 1)`
    - after: `greatest(eval_ts - toIntervalMillisecond(offset + lookback + 1), fromUnixTimestamp64Milli(required_start_ms) - toIntervalMillisecond(1))`

## Focused validation
```bash
go test ./internal/promshim/storage ./internal/promshim/native/renderer -run 'TestBuildRangeWindowSelectorCumulativeAvg|TestLowerAggregationAvgOverTimeUsesCumulativeRowsFastPath|TestLowerRangeFunctionAvgOverTimeUsesCumulativeRowsFastPath|TestLowerAggregationGolden|TestLowerRangeFunctionGolden'
```

## Served-safety pre-gate
Artifact:
- `harness/artifacts/bench/standalone/20260429-iter88-avg-boundary-clamp-pregate/`

Outcome:
- served successfully (`force_supported/strict`, `native_sql`)
- no `HTTP 502`

## Measurement signal
Compared to latest comparable avg-memory row run (iter85 pre-gate):
- prior shim p50: `7092.41ms`
- current shim p50: `6305.19ms`
- delta: `-787.22ms` (`-11.1%`)

ClickHouse profile summary:
- prior CH p50: `7074.5ms`
- current CH p50: `6230.5ms`
- delta: `-844.0ms` (`-11.9%`)
- memory p95: unchanged class (`~8.3GiB`)
- selected rows p50: unchanged class (`~710.1M`)
- join rows p50: unchanged class (`~119.2M`)

## Interpretation
- Safety gate passed.
- Material latency win with stable resource-shape counters (improvement likely from reduced unnecessary pre-range boundary work rather than cardinality reduction).

## Decision
Accepted.
