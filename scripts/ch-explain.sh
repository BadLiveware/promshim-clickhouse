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
#   ./scripts/ch-explain.sh '<clickhouse-sql>' --input sql [flags]
# Flags:
#   --input promql|sql            Interpret the positional query (default: promql).
#   --sql                         Alias for --input sql; runs concrete ClickHouse SQL directly.
#   --mode instant|range          PromQL mode only (default: instant)
#   --range-seconds N             PromQL range mode only (default: 300)
#   --step N                      PromQL range mode only (default: 15)
#   --eval-time <RFC3339>         PromQL eval time (default: compliance fixture end_time)
#   --output DIR                  (default: harness/artifacts/explain/<timestamp>)
#   --shim-url URL                (default: http://localhost:29091)
#   --ch-url URL                  (default: http://localhost:28123)
#   --log-comment COMMENT         bounded query_log correlation comment
#   --native-mode MODE            PromQL mode native_lowering_mode request parameter
#   --routing-policy POLICY       PromQL mode routing_policy request parameter
#   --skip-syntax                 omit EXPLAIN SYNTAX
#   --skip-plan                   omit EXPLAIN PLAN
#   --skip-pipeline               omit EXPLAIN PIPELINE
#   --skip-estimate               omit EXPLAIN ESTIMATE
#   -h, --help
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
# shellcheck source=lib/artifacts.sh
source "${REPO_ROOT}/scripts/lib/artifacts.sh"

CH_URL="${CH_URL:-http://localhost:28123}"
CH_USER="${CH_USER:-default}"
CH_PASS="${CH_PASS:-otel}"
SHIM_URL="${SHIM_URL:-http://localhost:29091}"
OUTPUT_DIR=""
MODE=instant
RANGE_SECONDS=300
STEP=15
EVAL_TIME=2026-04-21T21:45:42Z
LOG_COMMENT=""
NATIVE_MODE=""
ROUTING_POLICY=""
INPUT_MODE=promql
DO_SYNTAX=1
DO_PLAN=1
DO_PIPELINE=1
DO_ESTIMATE=1

usage() {
  cat <<'EOF'
Usage:
  scripts/ch-explain.sh '<promql>' [options]
  scripts/ch-explain.sh '<clickhouse-sql>' --input sql [options]
  scripts/ch-explain.sh '<clickhouse-sql>' --sql [options]

Purpose:
  One-stop query workbench for promshim/ClickHouse performance and correctness
  debugging. For PromQL input, the script runs the query through promshim,
  captures the ClickHouse SQL promshim issued, captures promshim explain/routing
  metadata, and dumps ClickHouse query_log/ProfileEvents/settings plus EXPLAIN
  artifacts. For SQL input, it runs the concrete ClickHouse SQL directly and
  captures the same query_log/ProfileEvents/settings and EXPLAIN artifacts.

Common examples:
  # PromQL instant query against the default compliance stack.
  scripts/ch-explain.sh 'up' --mode instant --native-mode prefer

  # PromQL range query against the isolated benchmark stack / local dev shim.
  scripts/ch-explain.sh 'rate(sum by (job) (demo_cpu_usage_seconds_total)[5m:1m])' \
    --mode range --range-seconds 86400 --step 300 \
    --eval-time 2026-03-14T21:45:42Z \
    --shim-url http://localhost:29291 \
    --ch-url http://localhost:28124 --ch-user default --ch-pass otel \
    --native-mode prefer --routing-policy strict \
    --output harness/artifacts/explain/subquery-rate-over-aggregate

  # Direct ClickHouse SQL, useful after editing q1/query-clean.sql or testing a setting.
  scripts/ch-explain.sh 'SELECT 1 SETTINGS max_threads=4' --sql \
    --ch-url http://localhost:28124 --ch-user default --ch-pass otel

Input options:
  --input promql|sql       Interpret the positional query. Default: promql.
  --sql                    Alias for --input sql.

PromQL request options:
  --mode instant|range     PromQL endpoint to call. Default: instant.
  --range-seconds N        Range width for --mode range. Default: 300.
  --step N                 Range step seconds for --mode range. Default: 15.
  --eval-time RFC3339      Evaluation/end time. Default: 2026-04-21T21:45:42Z.
  --shim-url URL           promshim base URL. Default: http://localhost:29091.
  --native-mode MODE       Request native_lowering_mode, e.g. prefer, force_supported.
  --routing-policy POLICY  Request routing_policy, e.g. strict, cost_shadow.

ClickHouse options:
  --ch-url URL             ClickHouse HTTP URL. Default: http://localhost:28123.
  --ch-user USER           ClickHouse user. Default: default.
  --ch-pass PASS           ClickHouse password. Default: otel.
  --log-comment COMMENT    Correlation tag for system.query_log. Auto-generated by default.

Artifact options:
  --output DIR             Output directory. Default: harness/artifacts/explain/<timestamp>.
  --skip-syntax            Skip EXPLAIN SYNTAX.
  --skip-plan              Skip EXPLAIN PLAN indexes=1, actions=1, optimize=1.
  --skip-pipeline          Skip EXPLAIN PIPELINE.
  --skip-estimate          Skip EXPLAIN ESTIMATE.
  -h, --help               Show this help.

Artifacts written:
  README.md                         Human-readable index with inline summaries.
  query-log.jsonl                   Raw system.query_log rows for captured statements.
  query-log-summary.tsv             One row per statement: duration, rows, memory,
                                    max_threads, CPU time, FunctionExecute, joins.
  qN/query.sql                      Exact ClickHouse SQL that ran.
  qN/query-clean.sql                SQL with trailing FORMAT/SETTINGS stripped for EXPLAIN/diffing.
  qN/settings.tsv                   Effective ClickHouse settings from query_log.
  qN/profile-events.tsv             All ProfileEvents for the statement.
  qN/profile-events-top.tsv         ProfileEvents sorted by value descending.
  qN/explain-syntax.sql             ClickHouse EXPLAIN SYNTAX output unless skipped.
  qN/explain-plan.txt               ClickHouse EXPLAIN PLAN output unless skipped.
  qN/explain-pipeline.txt           ClickHouse EXPLAIN PIPELINE output unless skipped.
  qN/explain-estimate.json          ClickHouse EXPLAIN ESTIMATE output unless skipped.

PromQL-only artifacts:
  shim-response.txt                 Raw promshim API response plus HTTP status line.
  shim-summary.json / .tsv          HTTP status, Prometheus status/error, result type,
                                    series count, point count.
  promshim-explain.json             promshim explain endpoint response.
  promshim-explain-summary.tsv      Transport, settings profile, routing/strategy summary.

Notes:
  - The script takes the project stack lock because query_log windows and stack
    ports are shared resources.
  - Use --input sql when you already have ClickHouse SQL and want the same
    settings/ProfileEvents/EXPLAIN bundle without going through promshim.
  - For durable optimization evidence, prefer this script over ad-hoc curl plus
    system.query_log snippets.
EOF
  exit "${1:-0}"
}

if [[ $# -eq 0 || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage $([[ $# -eq 0 ]] && echo 64 || echo 0)
fi
QUERY_TEXT="$1"; shift

while [[ $# -gt 0 ]]; do
  case "$1" in
    --input)          INPUT_MODE="$2"; shift 2 ;;
    --sql)            INPUT_MODE=sql; shift ;;
    --mode)           MODE="$2"; shift 2 ;;
    --range-seconds)  RANGE_SECONDS="$2"; shift 2 ;;
    --step)           STEP="$2"; shift 2 ;;
    --eval-time)      EVAL_TIME="$2"; shift 2 ;;
    --output)         OUTPUT_DIR="$2"; shift 2 ;;
    --shim-url)       SHIM_URL="$2"; shift 2 ;;
    --ch-url)         CH_URL="$2"; shift 2 ;;
    --ch-user)        CH_USER="$2"; shift 2 ;;
    --ch-pass)        CH_PASS="$2"; shift 2 ;;
    --log-comment)    LOG_COMMENT="$2"; shift 2 ;;
    --native-mode)    NATIVE_MODE="$2"; shift 2 ;;
    --routing-policy) ROUTING_POLICY="$2"; shift 2 ;;
    --skip-syntax)    DO_SYNTAX=0; shift ;;
    --skip-plan)      DO_PLAN=0; shift ;;
    --skip-pipeline)  DO_PIPELINE=0; shift ;;
    --skip-estimate)  DO_ESTIMATE=0; shift ;;
    -h|--help)        usage 0 ;;
    *) echo "unknown flag: $1" >&2; usage 64 ;;
  esac
done

[[ -n "$OUTPUT_DIR" ]] || OUTPUT_DIR="$(artifact_abs "explain/$(date +%Y%m%d-%H%M%S)")"
mkdir -p "$OUTPUT_DIR"
if [[ -z "$LOG_COMMENT" ]]; then
  LOG_COMMENT="ch-explain-$(date +%Y%m%d-%H%M%S)-$$"
fi
if [[ ! "$LOG_COMMENT" =~ ^[A-Za-z0-9_.:-]{1,120}$ ]]; then
  echo "--log-comment must match [A-Za-z0-9_.:-]{1,120}" >&2
  exit 64
fi
case "$INPUT_MODE" in
  promql|sql) ;;
  *) echo "--input must be promql or sql" >&2; exit 64 ;;
esac

acquire_run_lock "stack"

ch_query() {
  # arg1: SQL; arg2: optional URL query params; stdout: response body
  curl -sfS --data-binary @- "${CH_URL}/?${2:-}" -u "${CH_USER}:${CH_PASS}" <<<"$1"
}

EVAL_UNIX=$(date -u -d "$EVAL_TIME" +%s 2>/dev/null || gdate -u -d "$EVAL_TIME" +%s)
START_UNIX=$((EVAL_UNIX - RANGE_SECONDS))
END_UNIX=$EVAL_UNIX

# 1. Snapshot the query_log cutoff so we can identify shim-issued queries.
T0=$(ch_query "SELECT toUnixTimestamp64Micro(now64(6))")

curl_shim() {
  local endpoint="$1"; shift
  local args=(-sS -G "${SHIM_URL}${endpoint}" -H "X-Promshim-Log-Comment: ${LOG_COMMENT}")
  if [[ -n "$NATIVE_MODE" ]]; then
    args+=(--data-urlencode "native_lowering_mode=${NATIVE_MODE}")
  fi
  if [[ -n "$ROUTING_POLICY" ]]; then
    args+=(--data-urlencode "routing_policy=${ROUTING_POLICY}")
  fi
  args+=("$@" -w '\n{"http_status":%{http_code}}\n')
  curl "${args[@]}"
}

write_shim_summary() {
  local response_file="$1"
  local body_file="${OUTPUT_DIR}/shim-body.json"
  local http_file="${OUTPUT_DIR}/shim-http-status.json"
  sed '$d' "$response_file" >"$body_file"
  tail -n 1 "$response_file" >"$http_file"
  jq -n --slurpfile body "$body_file" --slurpfile http "$http_file" '
    def result: ($body[0].data.result // []);
    def point_count:
      if ($body[0].data.resultType // "") == "matrix" then ([result[]?.values | length] | add // 0)
      elif ($body[0].data.resultType // "") == "vector" then (result | length)
      elif ($body[0].data.resultType // "") == "scalar" then 1
      else 0 end;
    {
      http_status: ($http[0].http_status // null),
      prom_status: ($body[0].status // ""),
      error_type: ($body[0].errorType // ""),
      error: ($body[0].error // ""),
      result_type: ($body[0].data.resultType // ""),
      series_count: (result | length),
      point_count: point_count
    }
  ' >"${OUTPUT_DIR}/shim-summary.json"
  jq -r '["http_status","prom_status","error_type","error","result_type","series_count","point_count"], [.http_status,.prom_status,.error_type,.error,.result_type,.series_count,.point_count] | @tsv' \
    "${OUTPUT_DIR}/shim-summary.json" >"${OUTPUT_DIR}/shim-summary.tsv"
}

write_promshim_explain() {
  local endpoint="$1"; shift
  local args=(-sfS -G "${SHIM_URL}${endpoint}")
  if [[ -n "$NATIVE_MODE" ]]; then
    args+=(--data-urlencode "native_lowering_mode=${NATIVE_MODE}")
  fi
  if [[ -n "$ROUTING_POLICY" ]]; then
    args+=(--data-urlencode "routing_policy=${ROUTING_POLICY}")
  fi
  if curl "${args[@]}" "$@" >"${OUTPUT_DIR}/promshim-explain.json"; then
    jq -r '
      ["clickhouse_transport","settings_profile","settings_family","settings_candidate","strict_strategy","selected_strategy","served_candidate","routing_decision","routing_reason","cost_family"],
      [
        (.data.clickHouseTransport // ""),
        (.data.clickHouseSettingsProfile.name // ""),
        (.data.clickHouseSettingsProfile.family // ""),
        (.data.clickHouseSettingsProfile.candidate // ""),
        (.data.routing.strictStrategy // .data.routing.strictCandidate // ""),
        (.data.routing.selectedStrategy // .data.routing.selectedCandidate // ""),
        (.data.routing.servedCandidate // ""),
        (.data.routing.routingDecision // ""),
        (.data.routing.routingReason // ""),
        (.data.routing.costFamily // "")
      ] | @tsv
    ' "${OUTPUT_DIR}/promshim-explain.json" >"${OUTPUT_DIR}/promshim-explain-summary.tsv" || true
  else
    echo "[ch-explain] promshim explain capture failed; continuing" >&2
    rm -f "${OUTPUT_DIR}/promshim-explain.json"
  fi
}

# 2. Issue the query. PromQL goes through promshim; ClickHouse SQL runs
#    directly with the same log_comment setting so the rest of the artifact
#    path is shared.
if [[ "$INPUT_MODE" == "sql" ]]; then
  echo "[ch-explain] clickhouse sql: direct query log_comment=${LOG_COMMENT}"
  ch_query "$QUERY_TEXT" "log_comment=${LOG_COMMENT}" >"${OUTPUT_DIR}/clickhouse-response.txt"
else
  SHIM_CURL_EXIT=0
  case "$MODE" in
    instant)
      echo "[ch-explain] shim: instant query at eval_time=${EVAL_TIME} log_comment=${LOG_COMMENT}"
      set +e
      SHIM_BODY=$(curl_shim "/api/v1/query" \
        --data-urlencode "query=${QUERY_TEXT}" \
        --data-urlencode "time=${EVAL_UNIX}")
      SHIM_CURL_EXIT=$?
      set -e
      ;;
    range)
      echo "[ch-explain] shim: range query ${START_UNIX}→${END_UNIX} step=${STEP}s log_comment=${LOG_COMMENT}"
      set +e
      SHIM_BODY=$(curl_shim "/api/v1/query_range" \
        --data-urlencode "query=${QUERY_TEXT}" \
        --data-urlencode "start=${START_UNIX}" \
        --data-urlencode "end=${END_UNIX}" \
        --data-urlencode "step=${STEP}")
      SHIM_CURL_EXIT=$?
      set -e
      ;;
    *) echo "unknown --mode: $MODE" >&2; exit 64 ;;
  esac
  echo "$SHIM_BODY" >"${OUTPUT_DIR}/shim-response.txt"
  write_shim_summary "${OUTPUT_DIR}/shim-response.txt"

  SHIM_HTTP_STATUS=$(jq -r '.http_status // 0' "${OUTPUT_DIR}/shim-http-status.json" 2>/dev/null || echo 0)
  if [[ "$SHIM_CURL_EXIT" -ne 0 ]]; then
    echo "[ch-explain] shim request transport failed (exit=${SHIM_CURL_EXIT}); continuing with available diagnostics" >&2
  fi
  if [[ "$SHIM_HTTP_STATUS" =~ ^[0-9]+$ ]] && (( SHIM_HTTP_STATUS >= 400 )); then
    echo "[ch-explain] shim returned HTTP ${SHIM_HTTP_STATUS}; continuing to capture query-log diagnostics" >&2
  fi

  case "$MODE" in
    instant)
      write_promshim_explain "/api/v1/query_explain" \
        --data-urlencode "query=${QUERY_TEXT}" \
        --data-urlencode "time=${EVAL_UNIX}" ;;
    range)
      write_promshim_explain "/api/v1/query_range_explain" \
        --data-urlencode "query=${QUERY_TEXT}" \
        --data-urlencode "start=${START_UNIX}" \
        --data-urlencode "end=${END_UNIX}" \
        --data-urlencode "step=${STEP}" ;;
  esac
fi

# 3. Flush + pull the SQL(s) that were issued. Filter to the right
#    user/database and skip self-queries.
ch_query "SYSTEM FLUSH LOGS" >/dev/null

QUERY_LOG_SQL=$(cat <<SQL
SELECT
  event_time_microseconds,
  type,
  query_id,
  query_duration_ms,
  read_rows,
  read_bytes,
  result_rows,
  memory_usage,
  Settings,
  ProfileEvents,
  query
FROM system.query_log
WHERE event_time_microseconds >= fromUnixTimestamp64Micro(${T0})
  AND type IN ('QueryFinish', 'ExceptionWhileProcessing')
  AND is_initial_query
  AND query_kind = 'Select'
  AND Settings['log_comment'] = '${LOG_COMMENT}'
  AND query NOT LIKE '%system.query_log%'
  AND query NOT ILIKE 'EXPLAIN%'
  AND query NOT ILIKE 'SYSTEM %'
ORDER BY event_time_microseconds ASC
FORMAT JSONEachRow
SQL
)

ch_query "$QUERY_LOG_SQL" >"${OUTPUT_DIR}/query-log.jsonl"
jq -sr '
  [
    "statement",
    "type",
    "query_id",
    "query_duration_ms",
    "read_rows",
    "read_bytes",
    "result_rows",
    "memory_usage",
    "max_threads",
    "selected_rows",
    "selected_bytes",
    "read_compressed_bytes",
    "function_execute",
    "real_time_us",
    "user_time_us",
    "system_time_us",
    "join_build_rows",
    "join_probe_rows",
    "join_result_rows"
  ],
  (to_entries[] | [
    (.key + 1),
    (.value.type // ""),
    (.value.query_id // ""),
    (.value.query_duration_ms // 0),
    (.value.read_rows // 0),
    (.value.read_bytes // 0),
    (.value.result_rows // 0),
    (.value.memory_usage // 0),
    (.value.Settings.max_threads // ""),
    (.value.ProfileEvents.SelectedRows // 0),
    (.value.ProfileEvents.SelectedBytes // 0),
    (.value.ProfileEvents.ReadCompressedBytes // 0),
    (.value.ProfileEvents.FunctionExecute // 0),
    (.value.ProfileEvents.RealTimeMicroseconds // 0),
    (.value.ProfileEvents.UserTimeMicroseconds // 0),
    (.value.ProfileEvents.SystemTimeMicroseconds // 0),
    (.value.ProfileEvents.JoinBuildTableRowCount // 0),
    (.value.ProfileEvents.JoinProbeTableRowCount // 0),
    (.value.ProfileEvents.JoinResultRowCount // 0)
  ])
  | @tsv
' "${OUTPUT_DIR}/query-log.jsonl" >"${OUTPUT_DIR}/query-log-summary.tsv"
SQL_COUNT=$(jq -sr 'length' "${OUTPUT_DIR}/query-log.jsonl")

if [[ "$SQL_COUNT" -eq 0 ]]; then
  echo "[ch-explain] no matching SQL found in system.query_log" >&2
  echo "  continuing to preserve shim/explain artifacts" >&2
else
  echo "[ch-explain] captured ${SQL_COUNT} lowered SQL statement(s)"
fi

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

if (( SQL_COUNT > 0 )); then
  for INDEX in $(seq 1 "$SQL_COUNT"); do
    ROW_INDEX=$((INDEX-1))
    SQL=$(jq -sr --argjson idx "$ROW_INDEX" -r '.[$idx].query' "${OUTPUT_DIR}/query-log.jsonl")
    QDIR="${OUTPUT_DIR}/q${INDEX}"
    mkdir -p "$QDIR"
    printf '%s\n' "$SQL" >"${QDIR}/query.sql"

    CLEAN_SQL=$(strip_suffix "$SQL")
    printf '%s
' "$CLEAN_SQL" >"${QDIR}/query-clean.sql"
    jq -sr --argjson idx "$ROW_INDEX" '
      ["setting", "value"],
      ((.[$idx].Settings // {}) | to_entries | sort_by(.key)[] | [.key, (.value | tostring)])
      | @tsv
    ' "${OUTPUT_DIR}/query-log.jsonl" >"${QDIR}/settings.tsv"
    jq -sr --argjson idx "$ROW_INDEX" '
      ["event", "value"],
      ((.[$idx].ProfileEvents // {}) | to_entries | sort_by(.key)[] | [.key, (.value | tostring)])
      | @tsv
    ' "${OUTPUT_DIR}/query-log.jsonl" >"${QDIR}/profile-events.tsv"
    jq -sr --argjson idx "$ROW_INDEX" '
      ["event", "value"],
      ((.[$idx].ProfileEvents // {}) | to_entries | sort_by(.value | tonumber? // 0) | reverse[] | [.key, (.value | tostring)])
      | @tsv
    ' "${OUTPUT_DIR}/query-log.jsonl" >"${QDIR}/profile-events-top.tsv"

    if (( DO_SYNTAX == 1 )); then
      ch_query "EXPLAIN SYNTAX ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-syntax.sql" || echo "SYNTAX failed" >"${QDIR}/explain-syntax.sql"
    fi
    if (( DO_PLAN == 1 )); then
      ch_query "EXPLAIN PLAN indexes=1, actions=1, optimize=1 ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-plan.txt" || echo "PLAN failed" >"${QDIR}/explain-plan.txt"
    fi
    if (( DO_PIPELINE == 1 )); then
      ch_query "EXPLAIN PIPELINE ${CLEAN_SQL} FORMAT TSVRaw" >"${QDIR}/explain-pipeline.txt" || echo "PIPELINE failed" >"${QDIR}/explain-pipeline.txt"
    fi
    if (( DO_ESTIMATE == 1 )); then
      ch_query "EXPLAIN ESTIMATE ${CLEAN_SQL} FORMAT JSONEachRow" >"${QDIR}/explain-estimate.json" || echo "{}" >"${QDIR}/explain-estimate.json"
    fi

    echo "[ch-explain] q${INDEX}: $(wc -l <"${QDIR}/query.sql") lines SQL; artifacts in ${QDIR}"
  done
fi

# 5. Summary index.
{
  echo "# ch-explain capture"
  echo
  echo "- input: ${INPUT_MODE}"
  if [[ "$INPUT_MODE" == "sql" ]]; then
    echo "- clickhouse_sql: \`$(printf '%s' "$QUERY_TEXT" | tr '\n' ' ' | cut -c1-180)\`"
    echo "- clickhouse_response: \`clickhouse-response.txt\`"
  else
    echo "- promql: \`${QUERY_TEXT}\`"
    echo "- mode: ${MODE}"
    echo "- eval_time: ${EVAL_TIME}"
    echo "- shim_response: \`shim-response.txt\`"
    echo "- shim_summary: \`shim-summary.tsv\`"
    [[ -f "${OUTPUT_DIR}/promshim-explain.json" ]] && echo "- promshim_explain: \`promshim-explain.json\`"
    [[ -f "${OUTPUT_DIR}/promshim-explain-summary.tsv" ]] && echo "- promshim_explain_summary: \`promshim-explain-summary.tsv\`"
  fi
  echo "- log_comment: ${LOG_COMMENT}"
  [[ -n "$NATIVE_MODE" ]] && echo "- native_lowering_mode: ${NATIVE_MODE}"
  [[ -n "$ROUTING_POLICY" ]] && echo "- routing_policy: ${ROUTING_POLICY}"
  echo "- git: $(git -C "$REPO_ROOT" log -1 --pretty='%h %s' 2>/dev/null || echo unknown)"
  echo "- query_log: \`query-log.jsonl\`"
  echo "- query_log_summary: \`query-log-summary.tsv\`"
  echo "- captured_sqls: ${SQL_COUNT}"
  echo
  echo "## Query log summary"
  echo
  echo '```tsv'
  cat "${OUTPUT_DIR}/query-log-summary.tsv"
  echo '```'
  if [[ "$INPUT_MODE" != "sql" && -f "${OUTPUT_DIR}/shim-summary.tsv" ]]; then
    echo
    echo "## Shim summary"
    echo
    echo '```tsv'
    cat "${OUTPUT_DIR}/shim-summary.tsv"
    echo '```'
  fi
  if [[ "$INPUT_MODE" != "sql" && -f "${OUTPUT_DIR}/promshim-explain-summary.tsv" ]]; then
    echo
    echo "## Promshim explain summary"
    echo
    echo '```tsv'
    cat "${OUTPUT_DIR}/promshim-explain-summary.tsv"
    echo '```'
  fi
  echo
  for i in $(seq 1 "${SQL_COUNT}"); do
    echo "## q${i}"
    echo
    echo "- SQL: \`q${i}/query.sql\`"
    echo "- clean SQL for EXPLAIN/diffing: \`q${i}/query-clean.sql\`"
    echo "- settings: \`q${i}/settings.tsv\`"
    echo "- profile events: \`q${i}/profile-events.tsv\`"
    echo "- top profile events: \`q${i}/profile-events-top.tsv\`"
    (( DO_SYNTAX   == 1 )) && echo "- EXPLAIN SYNTAX: \`q${i}/explain-syntax.sql\`"
    (( DO_PLAN     == 1 )) && echo "- EXPLAIN PLAN: \`q${i}/explain-plan.txt\`"
    (( DO_PIPELINE == 1 )) && echo "- EXPLAIN PIPELINE: \`q${i}/explain-pipeline.txt\`"
    (( DO_ESTIMATE == 1 )) && echo "- EXPLAIN ESTIMATE: \`q${i}/explain-estimate.json\`"
    echo
  done
} >"${OUTPUT_DIR}/README.md"

echo "[ch-explain] done → ${OUTPUT_DIR}"
