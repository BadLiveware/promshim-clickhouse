#!/usr/bin/env bash
# ch-explain.sh — on-demand: run a PromQL through the shim, capture the
# lowered SQL(s) it sent to ClickHouse, and dump EXPLAIN SYNTAX / PLAN /
# PIPELINE / ESTIMATE for each.
#
# The key use case: answer "is this 'CSE alias' rewrite actually doing
# anything, or does ClickHouse fold it away before execution?" If EXPLAIN
# SYNTAX output is identical across two commits, the rewrite is cosmetic.
#
# Runtime budget: ~1–3 seconds per invocation regardless of bench size,
# because it runs a single PromQL query and inspects its lowered SQL(s).
#
# Usage:
#   ./scripts/ch-explain.sh '<promql>' [flags]
# Flags:
#   --mode instant|range          (default: instant)
#   --range-seconds N             (default: 300, range mode only)
#   --step N                      (default: 15, range mode only)
#   --eval-time <RFC3339>         (default: compliance fixture end_time from baseline eval-time)
#   --output DIR                  (default: harness/artifacts/ch-explain/<timestamp>)
#   --shim-url URL                (default: http://localhost:29091)
#   --skip-syntax                 omit EXPLAIN SYNTAX
#   --skip-plan                   omit EXPLAIN PLAN
#   --skip-pipeline               omit EXPLAIN PIPELINE json=1
#   --skip-estimate               omit EXPLAIN ESTIMATE
#   -h, --help
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
acquire_run_lock "stack"

CH_URL="${CH_URL:-http://localhost:28123}"
CH_USER="${CH_USER:-default}"
CH_PASS="${CH_PASS:-otel}"
SHIM_URL="${SHIM_URL:-http://localhost:29091}"
OUTPUT_DIR=""
MODE=instant
RANGE_SECONDS=300
STEP=15
EVAL_TIME=2026-04-21T21:45:42Z
DO_SYNTAX=1
DO_PLAN=1
DO_PIPELINE=1
DO_ESTIMATE=1

usage() { sed -n '1,/^set -e/p' "$0" | head -n 28; exit "${1:-0}"; }

[[ $# -gt 0 ]] || usage 64
PROMQL="$1"; shift

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)           MODE="$2"; shift 2 ;;
    --range-seconds)  RANGE_SECONDS="$2"; shift 2 ;;
    --step)           STEP="$2"; shift 2 ;;
    --eval-time)      EVAL_TIME="$2"; shift 2 ;;
    --output)         OUTPUT_DIR="$2"; shift 2 ;;
    --shim-url)       SHIM_URL="$2"; shift 2 ;;
    --skip-syntax)    DO_SYNTAX=0; shift ;;
    --skip-plan)      DO_PLAN=0; shift ;;
    --skip-pipeline)  DO_PIPELINE=0; shift ;;
    --skip-estimate)  DO_ESTIMATE=0; shift ;;
    -h|--help)        usage 0 ;;
    *) echo "unknown flag: $1" >&2; usage 64 ;;
  esac
done

[[ -n "$OUTPUT_DIR" ]] || OUTPUT_DIR="${REPO_ROOT}/harness/artifacts/ch-explain/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUTPUT_DIR"

ch_query() {
  # arg1: SQL; arg2: optional URL query params; stdout: response body
  curl -sfS --data-binary @- "${CH_URL}/?${2:-}" -u "${CH_USER}:${CH_PASS}" <<<"$1"
}

EVAL_UNIX=$(date -u -d "$EVAL_TIME" +%s 2>/dev/null || gdate -u -d "$EVAL_TIME" +%s)
START_UNIX=$((EVAL_UNIX - RANGE_SECONDS))
END_UNIX=$EVAL_UNIX

# 1. Snapshot the query_log cutoff so we can identify shim-issued queries.
T0=$(ch_query "SELECT toUnixTimestamp64Micro(now64(6))")

# 2. Issue the PromQL via shim. We use curl -G with --data-urlencode so the
#    query string survives reserved chars.
case "$MODE" in
  instant)
    echo "[ch-explain] shim: instant query at eval_time=${EVAL_TIME}"
    SHIM_BODY=$(curl -sfS -G "${SHIM_URL}/api/v1/query" \
      --data-urlencode "query=${PROMQL}" \
      --data-urlencode "time=${EVAL_UNIX}" \
      -w '\n{"http_status":%{http_code}}\n')
    ;;
  range)
    echo "[ch-explain] shim: range query ${START_UNIX}→${END_UNIX} step=${STEP}s"
    SHIM_BODY=$(curl -sfS -G "${SHIM_URL}/api/v1/query_range" \
      --data-urlencode "query=${PROMQL}" \
      --data-urlencode "start=${START_UNIX}" \
      --data-urlencode "end=${END_UNIX}" \
      --data-urlencode "step=${STEP}" \
      -w '\n{"http_status":%{http_code}}\n')
    ;;
  *) echo "unknown --mode: $MODE" >&2; exit 64 ;;
esac

echo "$SHIM_BODY" >"${OUTPUT_DIR}/shim-response.txt"

# 3. Flush + pull the SQL(s) that the shim issued. Filter to the right
#    user/database and skip self-queries.
ch_query "SYSTEM FLUSH LOGS" >/dev/null

SQLS_SQL=$(cat <<SQL
SELECT query
FROM system.query_log
WHERE event_time_microseconds >= fromUnixTimestamp64Micro(${T0})
  AND type = 'QueryFinish'
  AND is_initial_query
  AND query_kind = 'Select'
  AND has(databases, 'observability')
  AND query NOT LIKE '%system.query_log%'
  AND query NOT ILIKE 'EXPLAIN%'
  AND query NOT ILIKE 'SYSTEM %'
ORDER BY event_time_microseconds ASC
FORMAT JSONEachRow
SQL
)

mapfile -t SQLS < <(ch_query "$SQLS_SQL" | jq -r '.query')

if [[ ${#SQLS[@]} -eq 0 ]]; then
  echo "[ch-explain] no matching SQL found in system.query_log" >&2
  echo "  check: is the compliance stack up? did the shim answer the request?" >&2
  exit 3
fi

echo "[ch-explain] captured ${#SQLS[@]} lowered SQL statement(s)"

# 4. For each captured SQL, dump EXPLAIN variants and the raw SQL.
#
# Strip trailing `SETTINGS ... FORMAT X` / `FORMAT X` clauses — otherwise
# EXPLAIN inherits them (e.g. JSONEachRow wraps each output line in JSON,
# which wrecks diffs). We keep the original in query.sql for reference.
strip_suffix() {
  # Remove trailing FORMAT clause, then trailing SETTINGS clause (order matters).
  local sql="$1"
  sql=$(sed -E 's/[[:space:]]+FORMAT[[:space:]]+[A-Za-z0-9_]+[[:space:]]*$//i' <<<"$sql")
  sql=$(sed -E 's/[[:space:]]+SETTINGS[[:space:]]+[^;]+$//i'                <<<"$sql")
  printf '%s' "$sql"
}

INDEX=0
for SQL in "${SQLS[@]}"; do
  INDEX=$((INDEX+1))
  QDIR="${OUTPUT_DIR}/q${INDEX}"
  mkdir -p "$QDIR"
  printf '%s\n' "$SQL" >"${QDIR}/query.sql"

  CLEAN_SQL=$(strip_suffix "$SQL")

  if (( DO_SYNTAX == 1 )); then
    ch_query "EXPLAIN SYNTAX ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-syntax.sql" || echo "SYNTAX failed" >"${QDIR}/explain-syntax.sql"
  fi
  if (( DO_PLAN == 1 )); then
    ch_query "EXPLAIN PLAN indexes=1, actions=1, optimize=1 ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-plan.txt" || echo "PLAN failed" >"${QDIR}/explain-plan.txt"
  fi
  if (( DO_PIPELINE == 1 )); then
    ch_query "EXPLAIN PIPELINE json=1 ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-pipeline.json" || echo "{}" >"${QDIR}/explain-pipeline.json"
  fi
  if (( DO_ESTIMATE == 1 )); then
    ch_query "EXPLAIN ESTIMATE ${CLEAN_SQL} FORMAT JSONEachRow" >"${QDIR}/explain-estimate.json" || echo "{}" >"${QDIR}/explain-estimate.json"
  fi

  echo "[ch-explain] q${INDEX}: $(wc -l <"${QDIR}/query.sql") lines SQL; artifacts in ${QDIR}"
done

# 5. Summary index.
{
  echo "# ch-explain capture"
  echo
  echo "- promql: \`${PROMQL}\`"
  echo "- mode: ${MODE}"
  echo "- eval_time: ${EVAL_TIME}"
  echo "- git: $(git -C "$REPO_ROOT" log -1 --pretty='%h %s' 2>/dev/null || echo unknown)"
  echo "- captured_sqls: ${#SQLS[@]}"
  echo
  for i in $(seq 1 "${#SQLS[@]}"); do
    echo "## q${i}"
    echo
    echo "- SQL: \`q${i}/query.sql\`"
    (( DO_SYNTAX   == 1 )) && echo "- EXPLAIN SYNTAX: \`q${i}/explain-syntax.sql\`"
    (( DO_PLAN     == 1 )) && echo "- EXPLAIN PLAN: \`q${i}/explain-plan.txt\`"
    (( DO_PIPELINE == 1 )) && echo "- EXPLAIN PIPELINE: \`q${i}/explain-pipeline.json\`"
    (( DO_ESTIMATE == 1 )) && echo "- EXPLAIN ESTIMATE: \`q${i}/explain-estimate.json\`"
    echo
  done
} >"${OUTPUT_DIR}/README.md"

echo "[ch-explain] done → ${OUTPUT_DIR}"
