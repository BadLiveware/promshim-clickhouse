#!/usr/bin/env bash
# ch-profile-capture.sh — wraps run-bench.sh, captures per-query ClickHouse
# ProfileEvents from system.query_log into a structured JSON report.
#
# The compliance stack already runs each bench query N times (default 10+2).
# This script reads system.query_log *after* the bench to aggregate
# ProfileEvents, query_duration_ms, read_rows/bytes, memory — with near-zero
# added runtime (~1–2s: a flush + one aggregation SELECT).
#
# Emits harness/artifacts/ch-profile.json alongside bench-report.json.
#
# Usage:
#   ./scripts/ch-profile-capture.sh [-- run-bench flags...]
#     ./scripts/ch-profile-capture.sh --matrix --repeats 10
#
# Requires: jq, curl, running compliance stack (ClickHouse on :28123).
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
acquire_run_lock "ch-profile-capture"

CH_URL="${CH_URL:-http://localhost:28123}"
CH_USER="${CH_USER:-default}"
CH_PASS="${CH_PASS:-otel}"
ARTIFACT_DIR="${ARTIFACT_DIR:-${REPO_ROOT}/harness/artifacts}"
PROFILE_OUT="${PROFILE_OUT:-${ARTIFACT_DIR}/ch-profile.json}"

ch_query() {
  curl -sfS --data-binary @- "${CH_URL}/?${2:-}" \
    -u "${CH_USER}:${CH_PASS}" <<<"$1"
}

ch_query_check() {
  if ! ch_query "SELECT 1" >/dev/null 2>&1; then
    echo "error: ClickHouse not reachable at ${CH_URL}" >&2
    echo "  Bring up the compliance stack first: ./scripts/run-compliance.sh --keep-up --skip-classify" >&2
    exit 2
  fi
}

ch_query_check

mkdir -p "$ARTIFACT_DIR"

# Timestamp with microsecond resolution; CH system.query_log.event_time_microseconds
# is DateTime64(6). Use toUnixTimestamp64Micro so we can subtract cleanly.
T0=$(ch_query "SELECT toUnixTimestamp64Micro(now64(6))")
echo "[ch-profile] capture baseline t0=${T0} (unix µs)"

echo "[ch-profile] running bench: ./scripts/run-bench.sh $*"
BENCH_STATUS=0
"${REPO_ROOT}/scripts/run-bench.sh" "$@" || BENCH_STATUS=$?

# Flush system.query_log buffers before reading. Cheap: tens of ms.
ch_query "SYSTEM FLUSH LOGS" >/dev/null

# Aggregate per normalized SQL shape. ProfileEvents is Map(String, UInt64);
# sumMap sums values per key. We aggregate min/median to surface invariant
# cost rather than latency noise.
AGG_SQL=$(cat <<SQL
SELECT
  log_comment                                    AS log_comment,
  normalizeQuery(query)                          AS normalized_query,
  any(query)                                     AS example_query,
  count()                                        AS runs,
  round(quantile(0.5)(query_duration_ms), 2)     AS p50_ms,
  round(min(query_duration_ms), 2)               AS min_ms,
  round(quantile(0.9)(query_duration_ms), 2)     AS p90_ms,
  round(quantile(0.5)(read_rows))                AS p50_read_rows,
  round(quantile(0.5)(read_bytes))               AS p50_read_bytes,
  round(quantile(0.5)(result_rows))              AS p50_result_rows,
  round(quantile(0.5)(memory_usage))             AS p50_memory_bytes,
  sumMap(ProfileEvents)                          AS profile_events_sum,
  arrayMap(k -> (k, toUInt64(sumMap(ProfileEvents)[k] / count())),
           arraySort(mapKeys(sumMap(ProfileEvents)))) AS profile_events_avg
FROM system.query_log
WHERE event_time_microseconds >= fromUnixTimestamp64Micro(${T0})
  AND type = 'QueryFinish'
  AND is_initial_query
  AND query_kind = 'Select'
  AND query NOT LIKE '%system.query_log%'
  AND query NOT LIKE '%system.query_thread_log%'
  AND query NOT ILIKE 'EXPLAIN%'
  AND query NOT ILIKE 'SYSTEM %'
  AND has(databases, 'observability')
GROUP BY log_comment, normalized_query
ORDER BY p50_ms DESC
FORMAT JSONEachRow
SQL
)

echo "[ch-profile] aggregating system.query_log entries"
ROWS=$(ch_query "$AGG_SQL")

# Build the top-level document. jq -s wraps JSONEachRow lines into an array.
jq -s --arg t0 "$T0" --arg ch "$CH_URL" \
      --arg bench_status "$BENCH_STATUS" \
      --arg git_sha "$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
      --arg git_desc "$(git -C "$REPO_ROOT" log -1 --pretty='%h %s' 2>/dev/null || echo unknown)" '
  {
    schema_version: 1,
    captured_at:    (now | todate),
    baseline_unix_us: ($t0 | tonumber),
    bench_status:   ($bench_status | tonumber),
    clickhouse_url: $ch,
    git:            { sha: $git_sha, description: $git_desc },
    queries: .
  }
' <<<"$ROWS" >"$PROFILE_OUT"

ROW_COUNT=$(jq '.queries | length' "$PROFILE_OUT")
echo "[ch-profile] wrote ${PROFILE_OUT} (${ROW_COUNT} distinct normalized queries)"

exit "$BENCH_STATUS"
