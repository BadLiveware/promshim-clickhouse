#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/run-bench.sh [options]

Run the promshim native-SQL lowering tripwire benchmark against the
compliance stack (Prometheus :29090, promshim :29091 sharing the frozen
1h30m fixture). Completes in ~10s against an already-running stack;
add --bring-up and it's ~30s total. Runs in the foreground — do NOT
add a minutes-long timeout wrapper.

Options:
  --bring-up         Start the compliance stack first via
                     scripts/run-compliance.sh --keep-up --skip-classify,
                     then run the bench against it.
  --corpus PATH      Bench corpus JSON
                     (default: harness/corpus/bench-native-lowering.json).
  --long-range [7d|30d|1y|all]
                     Run the named long-range dashboard profile, or "all"
                     to run 7d, 30d, and 1y sequentially. Each profile sets
                     --corpus harness/corpus/bench-native-lowering-<P>.json
                     and --eval-time to that profile's pinned end_time
                     (7d→2026-03-22, 30d→2026-02-22, 1y→2025-03-22).
                     Argument is optional; defaults to 7d. Requires
                     ./scripts/seed-long-range.sh --profile <P> beforehand.
                     Skips Prometheus comparison (data only lives in CH).
  --eval-time RFC3339  Override the bench evaluation time passed to
                       promshim-bench (default pinned to the 10m fixture).
  --prom-url URL     Prometheus base URL (default http://localhost:29090).
  --shim-url URL     promshim base URL (default http://localhost:29091).
  --ch-url URL       ClickHouse HTTP URL for --memory summary (default http://localhost:28123).
  --artifact-dir DIR Directory for benchmark artifacts (default harness/artifacts).
  --baseline PATH    Baseline bench report; exits non-zero on regressions
                     (default: harness/bench/baseline.json when present).
  --no-baseline      Disable baseline discovery/gating for this run.
  --update-baseline  Rewrite the baseline file from this run's results.
  --shim-modes LIST  Comma-separated v2 shim modes, e.g. prefer,force_supported,off.
  --routing-policies LIST
                     Comma-separated routing_policy values for v2 reports, e.g. strict,cost_shadow.
  --include-prom BOOL Include Prometheus timing in v2 reports (default true).
  --artifact-name NAME
                     Artifact file name for v2 reports.
  --run-label K=V    Run label for v2 reports; may be repeated.
  --memory MODE      Memory capture for v2 reports: off|summary|detailed.
                     summary writes memory-summary*.json from ClickHouse query_log;
                     detailed also writes whole-run pprof snapshots when
                     PROM_SHIM_ENABLE_PPROF=1 on promshim.
  --clickhouse-profile MODE
                     ClickHouse per-query profile artifacts: off|summary|auto|processors.
                     summary writes query-log/ProfileEvents and native SQL samples;
                     auto also captures processor rollups for slow/heavy rows;
                     processors captures processor rollups for every row.
  --legacy-report    Force legacy v1 report output.
  --repeats N        Timed repeats per (query, mode) (default: 10).
  --warmup N         Warmup repeats per (query, mode) (default: 2).
  --ready-timeout N  Seconds to wait for endpoints (default: 60).
  --matrix           Print a Markdown native-SQL vs Prometheus matrix
                     (sorted by N/P ratio, descending) after the bench
                     finishes. Reads harness/artifacts/bench-report.json.
  -h, --help         Show this help text.
EOF
}

log() {
  printf '[%s] %s\n' "$(date +'%Y-%m-%d %H:%M:%S')" "$1"
}

fatal() {
  echo "Error: $*" >&2
  exit 1
}

ensure_command() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    fatal "Required command not found: $cmd"
  fi
}

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
acquire_run_lock "stack"

DEFAULT_CORPUS="harness/corpus/bench-native-lowering.json"
DEFAULT_BASELINE="harness/bench/baseline.json"

BRING_UP=0
CORPUS="$DEFAULT_CORPUS"
PROM_URL="http://localhost:29090"
SHIM_URL="http://localhost:29091"
CH_URL="http://localhost:28123"
CH_USER="default"
CH_PASSWORD="otel"
ARTIFACT_DIR="harness/artifacts"
BASELINE=""
NO_BASELINE=0
UPDATE_BASELINE=0
REPEATS=10
WARMUP=2
READY_TIMEOUT=60
MATRIX=0
LONG_RANGE=""
EVAL_TIME=""
SHIM_MODES=""
ROUTING_POLICIES=""
INCLUDE_PROM=""
ARTIFACT_NAME=""
RUN_LABELS=()
MEMORY_MODE=""
CLICKHOUSE_PROFILE_MODE="off"
LEGACY_REPORT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bring-up)        BRING_UP=1; shift ;;
    --corpus)          CORPUS="$2"; shift 2 ;;
    --prom-url)        PROM_URL="$2"; shift 2 ;;
    --shim-url)        SHIM_URL="$2"; shift 2 ;;
    --ch-url)          CH_URL="$2"; shift 2 ;;
    --ch-user)         CH_USER="$2"; shift 2 ;;
    --ch-password)     CH_PASSWORD="$2"; shift 2 ;;
    --artifact-dir)    ARTIFACT_DIR="$2"; shift 2 ;;
    --baseline)        BASELINE="$2"; shift 2 ;;
    --no-baseline)     NO_BASELINE=1; BASELINE=""; shift ;;
    --update-baseline) UPDATE_BASELINE=1; shift ;;
    --shim-modes)      SHIM_MODES="$2"; shift 2 ;;
    --routing-policies) ROUTING_POLICIES="$2"; shift 2 ;;
    --include-prom)    INCLUDE_PROM="$2"; shift 2 ;;
    --artifact-name)   ARTIFACT_NAME="$2"; shift 2 ;;
    --run-label)       RUN_LABELS+=("$2"); shift 2 ;;
    --memory)          MEMORY_MODE="$2"; shift 2 ;;
    --clickhouse-profile) CLICKHOUSE_PROFILE_MODE="$2"; shift 2 ;;
    --legacy-report)   LEGACY_REPORT=1; shift ;;
    --repeats)         REPEATS="$2"; shift 2 ;;
    --warmup)          WARMUP="$2"; shift 2 ;;
    --ready-timeout)   READY_TIMEOUT="$2"; shift 2 ;;
    --matrix)          MATRIX=1; shift ;;
    --long-range)
      # Optional argument: --long-range [7d|30d|1y|all], default 7d.
      if [[ $# -ge 2 && "$2" =~ ^(7d|30d|1y|all)$ ]]; then
        LONG_RANGE="$2"; shift 2
      else
        LONG_RANGE="7d"; shift
      fi
      ;;
    --eval-time)       EVAL_TIME="$2"; shift 2 ;;
    -h|--help)         usage; exit 0 ;;
    *)                 fatal "Unknown argument: $1" ;;
  esac
done

if [[ -n "$LONG_RANGE" ]]; then
  if [[ "$LONG_RANGE" == "all" ]]; then
    # Re-invoke self once per profile so each gets its own promshim-bench
    # process (and its own bench-report.json artifact path). Stop on first
    # non-zero status so the optimization loop sees real regressions.
    SELF="${BASH_SOURCE[0]}"
    STATUS=0
    for P in 7d 30d 1y; do
      log "Long-range profile ${P} starting."
      PASSTHROUGH=(--long-range "$P")
      if (( BRING_UP == 1 ));        then PASSTHROUGH+=(--bring-up); fi
      if (( UPDATE_BASELINE == 1 )); then PASSTHROUGH+=(--update-baseline); fi
      if (( NO_BASELINE == 1 ));     then PASSTHROUGH+=(--no-baseline); fi
      if (( MATRIX == 1 ));          then PASSTHROUGH+=(--matrix); fi
      if (( LEGACY_REPORT == 1 ));   then PASSTHROUGH+=(--legacy-report); fi
      PASSTHROUGH+=(--repeats "$REPEATS" --warmup "$WARMUP" --ready-timeout "$READY_TIMEOUT" --prom-url "$PROM_URL" --shim-url "$SHIM_URL" --ch-url "$CH_URL" --ch-user "$CH_USER" --ch-password "$CH_PASSWORD" --artifact-dir "$ARTIFACT_DIR")
      if [[ -n "$BASELINE" ]];      then PASSTHROUGH+=(--baseline "$BASELINE"); fi
      if [[ -n "$SHIM_MODES" ]];    then PASSTHROUGH+=(--shim-modes "$SHIM_MODES"); fi
      if [[ -n "$ROUTING_POLICIES" ]]; then PASSTHROUGH+=(--routing-policies "$ROUTING_POLICIES"); fi
      if [[ -n "$INCLUDE_PROM" ]];  then PASSTHROUGH+=("--include-prom=${INCLUDE_PROM}"); fi
      if [[ -n "$ARTIFACT_NAME" ]]; then PASSTHROUGH+=(--artifact-name "$ARTIFACT_NAME"); fi
      if [[ -n "$MEMORY_MODE" ]];   then PASSTHROUGH+=(--memory "$MEMORY_MODE"); fi
      if [[ "$CLICKHOUSE_PROFILE_MODE" != "off" ]]; then PASSTHROUGH+=(--clickhouse-profile "$CLICKHOUSE_PROFILE_MODE"); fi
      for label in "${RUN_LABELS[@]}"; do PASSTHROUGH+=(--run-label "$label"); done
      set +e
      "$SELF" "${PASSTHROUGH[@]}"
      sub=$?
      set -e
      if (( sub != 0 )); then
        log "Long-range profile ${P} failed (status=${sub})."
        STATUS=$sub
      fi
    done
    exit "$STATUS"
  fi

  case "$LONG_RANGE" in
    7d)  PROFILE_END_TIME="2026-03-22T21:45:42Z" ;;
    30d) PROFILE_END_TIME="2026-02-22T21:45:42Z" ;;
    1y)  PROFILE_END_TIME="2025-03-22T21:45:42Z" ;;
    *)   fatal "Unknown --long-range profile: $LONG_RANGE (want 7d|30d|1y|all)" ;;
  esac
  if [[ "$CORPUS" == "$DEFAULT_CORPUS" ]]; then
    CORPUS="harness/corpus/bench-native-lowering-${LONG_RANGE}.json"
  fi
  if [[ -z "$EVAL_TIME" ]]; then
    EVAL_TIME="$PROFILE_END_TIME"
  fi
fi

case "$CLICKHOUSE_PROFILE_MODE" in
  off|summary|auto|processors) ;;
  *) fatal "--clickhouse-profile must be off|summary|auto|processors (got: $CLICKHOUSE_PROFILE_MODE)" ;;
esac

ensure_command go
ensure_command curl

cd "$REPO_ROOT"

if [[ ! -f "$CORPUS" ]]; then
  fatal "Corpus file not found: $CORPUS"
fi

if (( NO_BASELINE == 0 )) && [[ -z "$BASELINE" ]] && [[ -f "$DEFAULT_BASELINE" ]]; then
  BASELINE="$DEFAULT_BASELINE"
  log "Using default baseline: $BASELINE"
fi

if (( BRING_UP == 1 )); then
  log "Bringing up compliance stack (--bring-up)."
  "${REPO_ROOT}/scripts/run-compliance.sh" --keep-up --skip-classify --ready-timeout "$READY_TIMEOUT"
fi

wait_for_http() {
  local name="$1" url="$2" deadline=$(( $(date +%s) + READY_TIMEOUT ))
  while (( $(date +%s) < deadline )); do
    if curl -sf -o /dev/null "$url"; then
      return 0
    fi
    sleep 1
  done
  fatal "${name} did not become ready within ${READY_TIMEOUT}s (${url})"
}

log "Checking Prometheus and promshim readiness."
wait_for_http "Prometheus reference" "${PROM_URL}/-/ready"
wait_for_http "promshim"             "${SHIM_URL}/-/ready"

log "Probing promshim -> ClickHouse integration."
smoke_deadline=$(( $(date +%s) + READY_TIMEOUT ))
smoke_ok=0
while (( $(date +%s) < smoke_deadline )); do
  if curl -fsS "${SHIM_URL}/api/v1/query?query=up" >/dev/null 2>&1; then
    smoke_ok=1
    break
  fi
  sleep 1
done
if (( smoke_ok == 0 )); then
  fatal "promshim did not successfully serve a query within ${READY_TIMEOUT}s"
fi

BIN="$(mktemp -d)/promshim-bench"
log "Building cmd/promshim-bench -> ${BIN}"
go build -o "$BIN" ./cmd/promshim-bench

ARGS=(
  --corpus "$CORPUS"
  --artifact-dir "$ARTIFACT_DIR"
  --prom-url "$PROM_URL"
  --shim-url "$SHIM_URL"
  --repeats "$REPEATS"
  --warmup "$WARMUP"
)
if [[ -n "$BASELINE" ]]; then
  ARGS+=(--baseline "$BASELINE")
fi
if (( UPDATE_BASELINE == 1 )); then
  ARGS+=(--update-baseline)
fi
if [[ -n "$SHIM_MODES" ]]; then
  ARGS+=(--shim-modes "$SHIM_MODES")
fi
if [[ -n "$ROUTING_POLICIES" ]]; then
  ARGS+=(--routing-policies "$ROUTING_POLICIES")
fi
if [[ -n "$INCLUDE_PROM" ]]; then
  ARGS+=("--include-prom=${INCLUDE_PROM}")
fi
if [[ -n "$ARTIFACT_NAME" ]]; then
  ARGS+=(--artifact-name "$ARTIFACT_NAME")
fi
if [[ -n "$MEMORY_MODE" ]]; then
  ARGS+=(--memory "$MEMORY_MODE")
fi
if [[ "$CLICKHOUSE_PROFILE_MODE" != "off" ]]; then
  if (( LEGACY_REPORT == 1 )); then
    fatal "--clickhouse-profile requires v2 reports; remove --legacy-report"
  fi
  if [[ -z "$SHIM_MODES" ]]; then
    ARGS+=(--shim-modes prefer)
  fi
fi
if (( LEGACY_REPORT == 1 )); then
  ARGS+=(--legacy-report)
fi
for label in "${RUN_LABELS[@]}"; do
  ARGS+=(--run-label "$label")
done
if [[ -n "$EVAL_TIME" ]]; then
  ARGS+=(--eval-time "$EVAL_TIME")
fi

REPORT_NAME="${ARTIFACT_NAME:-bench-report.json}"
REPORT_PATH="${REPO_ROOT}/${ARTIFACT_DIR}/${REPORT_NAME}"
MEMORY_STEM="${REPORT_NAME%.json}"
MEMORY_SUMMARY_PATH="${REPO_ROOT}/${ARTIFACT_DIR}/memory-summary-${MEMORY_STEM}.json"
MEMORY_DETAIL_DIR="${REPO_ROOT}/${ARTIFACT_DIR}/memory-detail-${MEMORY_STEM}"
CLICKHOUSE_PROFILE_SUMMARY_PATH="${REPO_ROOT}/${ARTIFACT_DIR}/clickhouse-profile-${MEMORY_STEM}.json"
CLICKHOUSE_PROFILE_MD_PATH="${REPO_ROOT}/${ARTIFACT_DIR}/clickhouse-profile-${MEMORY_STEM}.md"
CLICKHOUSE_PROFILE_DIR="${REPO_ROOT}/${ARTIFACT_DIR}/profiles"

capture_pprof_snapshot() {
  local label="$1"
  if [[ "$MEMORY_MODE" != "detailed" ]]; then
    return 0
  fi
  mkdir -p "$MEMORY_DETAIL_DIR"
  local base="${SHIM_URL%/}/debug/pprof"
  local failed=0
  if ! curl -fsS --max-time 15 "${base}/heap?gc=1" -o "${MEMORY_DETAIL_DIR}/heap-${label}.pb.gz"; then
    failed=1
  fi
  if ! curl -fsS --max-time 15 "${base}/allocs" -o "${MEMORY_DETAIL_DIR}/allocs-${label}.pb.gz"; then
    failed=1
  fi
  if ! curl -fsS --max-time 15 "${base}/goroutine?debug=1" -o "${MEMORY_DETAIL_DIR}/goroutine-${label}.txt"; then
    failed=1
  fi
  if (( failed == 1 )); then
    log "Detailed pprof snapshot '${label}' incomplete; ensure PROM_SHIM_ENABLE_PPROF=1 on promshim."
    return 0
  fi
  log "Captured detailed pprof snapshot '${label}' in ${MEMORY_DETAIL_DIR}."
}

write_memory_detail_manifest() {
  if [[ "$MEMORY_MODE" != "detailed" ]]; then
    return 0
  fi
  python3 - "$MEMORY_DETAIL_DIR" "$REPORT_PATH" "$SHIM_URL" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
report_path = pathlib.Path(sys.argv[2])
shim_url = sys.argv[3]
files = sorted(str(p.relative_to(path)) for p in path.glob('*') if p.is_file()) if path.exists() else []
path.mkdir(parents=True, exist_ok=True)
(path / 'manifest.json').write_text(json.dumps({
    'schemaVersion': 1,
    'sourceReport': str(report_path),
    'promshimURL': shim_url,
    'note': 'Whole-run pprof snapshots. Query-level detailed capture is intentionally deferred because it perturbs timings and needs serialized selected query groups.',
    'files': files,
}, indent=2) + '\n')
PY
}

capture_memory_summary() {
  local report_path="$1" output_path="$2"
  if [[ "$MEMORY_MODE" == "off" || -z "$MEMORY_MODE" ]]; then
    return 0
  fi
  if [[ ! -f "$report_path" ]]; then
    log "Memory summary skipped; report not found: $report_path"
    return 0
  fi
  python3 - "$CH_URL" "$CH_USER" "$CH_PASSWORD" "$SHIM_URL" "$report_path" "$output_path" <<'PY'
import json, pathlib, re, sys, urllib.parse, urllib.request
ch_url, user, password, shim_url, report_path, output_path = sys.argv[1:7]
report = json.loads(pathlib.Path(report_path).read_text())
comments = []
run_label = ((report.get("runLabels") or {}).get("run") or "").strip()
def safe_part(value):
    return ''.join(c if c.isalnum() or c in '_.-' else '_' for c in str(value or '').strip()) or 'unknown'
for row in report.get("rows", []):
    safe_name = safe_part(row.get("name") or "unknown")
    for mode_key, result in (row.get("shim") or {}).items():
        mode = result.get("nativeLoweringMode") or mode_key.split('@', 1)[0]
        comment = "promshim-bench"
        if run_label:
            comment += f" run={safe_part(run_label)}"
        comment += f" query={safe_name} mode={safe_part(mode)}"
        if result.get("routingPolicy"):
            comment += f" policy={safe_part(result.get('routingPolicy'))}"
        comments.append(comment)
comments = sorted(set(comments))
summary = {"schemaVersion": 1, "sourceReport": str(pathlib.Path(report_path)), "clickhouseURL": ch_url, "promshimURL": shim_url, "clickHouseQueryLog": [], "promshimMetricsAfter": {}, "errors": []}
try:
    with urllib.request.urlopen(shim_url.rstrip('/') + '/metrics', timeout=5) as r:
        metrics = r.read().decode()
    wanted = {"go_memstats_heap_alloc_bytes", "go_memstats_heap_inuse_bytes", "go_memstats_heap_sys_bytes", "process_resident_memory_bytes"}
    for line in metrics.splitlines():
        if not line or line.startswith('#') or '{' in line:
            continue
        parts = line.split()
        if len(parts) == 2 and parts[0] in wanted:
            try:
                summary["promshimMetricsAfter"][parts[0]] = float(parts[1])
            except ValueError:
                pass
except Exception as exc:
    summary["errors"].append("promshim metrics: " + str(exc))
if comments:
    flush_req = urllib.request.Request(ch_url.rstrip('/') + '/', data=b'SYSTEM FLUSH LOGS')
    token = (f"{user}:{password}").encode()
    import base64
    flush_req.add_header('Authorization', 'Basic ' + base64.b64encode(token).decode())
    try:
        urllib.request.urlopen(flush_req, timeout=10).read()
    except Exception as exc:
        summary["errors"].append("flush logs: " + str(exc))
    literals = ','.join("'" + c.replace("'", "''") + "'" for c in comments)
    sql = f"""
SELECT
  log_comment AS logComment,
  count() AS queryCount,
  quantile(0.5)(query_duration_ms) AS queryDurationP50Ms,
  quantile(0.9)(query_duration_ms) AS queryDurationP90Ms,
  max(query_duration_ms) AS queryDurationMaxMs,
  quantile(0.5)(memory_usage) AS memoryP50Bytes,
  quantile(0.95)(memory_usage) AS memoryP95Bytes,
  max(memory_usage) AS memoryMaxBytes,
  sum(read_rows) AS readRows,
  sum(read_bytes) AS readBytes,
  sum(result_rows) AS resultRows,
  sum(ProfileEvents['SelectedRows']) AS selectedRows,
  sum(ProfileEvents['SelectedBytes']) AS selectedBytes,
  sum(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytes,
  sum(ProfileEvents['FunctionExecute']) AS functionExecute,
  sum(ProfileEvents['MemoryTrackerUsage']) AS memoryTrackerUsage
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= now() - INTERVAL 6 HOUR
  AND log_comment IN ({literals})
GROUP BY log_comment
ORDER BY log_comment
FORMAT JSONEachRow
"""
    req = urllib.request.Request(ch_url.rstrip('/') + '/', data=sql.encode())
    req.add_header('Authorization', 'Basic ' + base64.b64encode(token).decode())
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            body = r.read().decode()
        for line in body.splitlines():
            if line.strip():
                summary["clickHouseQueryLog"].append(json.loads(line))
    except Exception as exc:
        summary["errors"].append(str(exc))
found = {r.get("logComment") for r in summary["clickHouseQueryLog"]}
summary["missingLogComments"] = [c for c in comments if c not in found]
path = pathlib.Path(output_path)
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(summary, indent=2) + "\n")
print(f"Wrote {path}")
PY
}

capture_clickhouse_profile() {
  local report_path="$1" output_path="$2" markdown_path="$3" profiles_dir="$4"
  if [[ "$CLICKHOUSE_PROFILE_MODE" == "off" ]]; then
    return 0
  fi
  if [[ ! -f "$report_path" ]]; then
    log "ClickHouse profile skipped; report not found: $report_path"
    return 0
  fi
  python3 - "$CLICKHOUSE_PROFILE_MODE" "$CH_URL" "$CH_USER" "$CH_PASSWORD" "$SHIM_URL" "$report_path" "$output_path" "$markdown_path" "$profiles_dir" <<'PY'
import base64, json, pathlib, re, sys, urllib.request

mode, ch_url, user, password, shim_url, report_path, output_path, markdown_path, profiles_dir = sys.argv[1:10]
report_path = pathlib.Path(report_path)
profiles_dir = pathlib.Path(profiles_dir)
report = json.loads(report_path.read_text())
token = base64.b64encode(f"{user}:{password}".encode()).decode()

def safe_part(value):
    return ''.join(c if c.isalnum() or c in '_.-' else '_' for c in str(value or '').strip()) or 'unknown'

def ch_query(sql, timeout=30):
    req = urllib.request.Request(ch_url.rstrip('/') + '/', data=sql.encode())
    req.add_header('Authorization', 'Basic ' + token)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode()

def ch_json_each_row(sql, timeout=30):
    body = ch_query(sql, timeout=timeout)
    return [json.loads(line) for line in body.splitlines() if line.strip()]

comments = []
comment_meta = {}
run_label = ((report.get('runLabels') or {}).get('run') or '').strip()
for row in report.get('rows', []):
    safe_name = safe_part(row.get('name') or 'unknown')
    for mode_key, result in (row.get('shim') or {}).items():
        native_mode = result.get('nativeLoweringMode') or mode_key.split('@', 1)[0]
        comment = "promshim-bench"
        if run_label:
            comment += f" run={safe_part(run_label)}"
        comment += f" query={safe_name} mode={safe_part(native_mode)}"
        if result.get('routingPolicy'):
            comment += f" policy={safe_part(result.get('routingPolicy'))}"
        comments.append(comment)
        comment_meta[comment] = {
            'queryName': row.get('name'),
            'query': row.get('query'),
            'endpoint': row.get('endpoint'),
            'mode': native_mode,
            'routingPolicy': result.get('routingPolicy'),
            'strategy': result.get('strategy'),
            'shimP50Ms': result.get('p50Ms'),
            'promP50Ms': (row.get('prom') or {}).get('p50Ms'),
            'shimPromRatio': ((result.get('p50Ms') or 0) / (row.get('prom') or {}).get('p50Ms')) if (row.get('prom') or {}).get('p50Ms') else None,
        }
comments = sorted(set(comments))
summary = {
    'schemaVersion': 1,
    'mode': mode,
    'sourceReport': str(report_path),
    'clickhouseURL': ch_url,
    'promshimURL': shim_url,
    'rows': [],
    'errors': [],
}
if not comments:
    pathlib.Path(output_path).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(output_path).write_text(json.dumps(summary, indent=2) + '\n')
    raise SystemExit(0)
try:
    ch_query('SYSTEM FLUSH LOGS', timeout=10)
except Exception as exc:
    summary['errors'].append('flush logs: ' + str(exc))

literals = ','.join("'" + c.replace("'", "''") + "'" for c in comments)
sql = f"""
SELECT
  log_comment AS logComment,
  count() AS queryCount,
  quantile(0.5)(query_duration_ms) AS queryDurationP50Ms,
  quantile(0.9)(query_duration_ms) AS queryDurationP90Ms,
  max(query_duration_ms) AS queryDurationMaxMs,
  quantile(0.5)(memory_usage) AS memoryP50Bytes,
  quantile(0.95)(memory_usage) AS memoryP95Bytes,
  max(memory_usage) AS memoryMaxBytes,
  sum(read_rows) AS readRowsTotal,
  quantile(0.5)(read_rows) AS readRowsP50,
  sum(read_bytes) AS readBytesTotal,
  quantile(0.5)(read_bytes) AS readBytesP50,
  sum(result_rows) AS resultRowsTotal,
  quantile(0.5)(result_rows) AS resultRowsP50,
  sum(ProfileEvents['SelectedRows']) AS selectedRowsTotal,
  quantile(0.5)(ProfileEvents['SelectedRows']) AS selectedRowsP50,
  sum(ProfileEvents['SelectedBytes']) AS selectedBytesTotal,
  quantile(0.5)(ProfileEvents['SelectedBytes']) AS selectedBytesP50,
  sum(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytesTotal,
  quantile(0.5)(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytesP50,
  sum(ProfileEvents['JoinBuildTableRowCount']) AS joinBuildTableRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinBuildTableRowCount']) AS joinBuildTableRowCountP50,
  sum(ProfileEvents['JoinProbeTableRowCount']) AS joinProbeTableRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinProbeTableRowCount']) AS joinProbeTableRowCountP50,
  sum(ProfileEvents['JoinResultRowCount']) AS joinResultRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinResultRowCount']) AS joinResultRowCountP50,
  sum(ProfileEvents['FilterTransformPassedRows']) AS filterTransformPassedRowsTotal,
  quantile(0.5)(ProfileEvents['FilterTransformPassedRows']) AS filterTransformPassedRowsP50,
  sum(ProfileEvents['FunctionExecute']) AS functionExecuteTotal,
  quantile(0.5)(ProfileEvents['FunctionExecute']) AS functionExecuteP50,
  sum(ProfileEvents['RealTimeMicroseconds']) AS realTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['RealTimeMicroseconds']) AS realTimeMicrosecondsP50,
  sum(ProfileEvents['UserTimeMicroseconds']) AS userTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['UserTimeMicroseconds']) AS userTimeMicrosecondsP50,
  sum(ProfileEvents['SystemTimeMicroseconds']) AS systemTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['SystemTimeMicroseconds']) AS systemTimeMicrosecondsP50,
  sum(ProfileEvents['DiskReadElapsedMicroseconds']) AS diskReadElapsedMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['DiskReadElapsedMicroseconds']) AS diskReadElapsedMicrosecondsP50,
  argMax(query_id, query_duration_ms) AS sampleQueryId,
  argMax(query, query_duration_ms) AS sampleNativeSQL
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= now() - INTERVAL 6 HOUR
  AND log_comment IN ({literals})
GROUP BY log_comment
ORDER BY log_comment
FORMAT JSONEachRow
"""
try:
    rows = ch_json_each_row(sql, timeout=30)
except Exception as exc:
    summary['errors'].append('query_log: ' + str(exc))
    rows = []
found = {r.get('logComment') for r in rows}
summary['missingLogComments'] = [c for c in comments if c not in found]
profiles_dir.mkdir(parents=True, exist_ok=True)

def needs_processors(row, meta):
    if mode == 'processors':
        return True
    if mode != 'auto':
        return False
    ratio = meta.get('shimPromRatio') or 0
    return (
        (row.get('queryDurationP50Ms') or 0) >= 500 or
        (row.get('memoryP95Bytes') or 0) >= 1_000_000_000 or
        (row.get('joinResultRowCountP50') or 0) >= 100_000_000 or
        (row.get('filterTransformPassedRowsP50') or 0) >= 100_000_000 or
        (ratio >= 2 and (row.get('queryDurationP50Ms') or 0) >= 100)
    )

def write_tsv(path, rows, columns):
    with open(path, 'w') as f:
        f.write('\t'.join(columns) + '\n')
        for row in rows:
            f.write('\t'.join(str(row.get(c, '')) for c in columns) + '\n')

for row in rows:
    comment = row.get('logComment')
    meta = comment_meta.get(comment, {})
    query_dir = profiles_dir / safe_part((meta.get('queryName') or 'unknown') + '__' + (meta.get('mode') or 'unknown') + '__' + (meta.get('routingPolicy') or 'strict'))
    query_dir.mkdir(parents=True, exist_ok=True)
    native_path = query_dir / 'native.sql'
    native_path.write_text((row.get('sampleNativeSQL') or '') + '\n')
    native_rel = native_path.relative_to(pathlib.Path(output_path).parent)
    query_sample = dict(row)
    query_sample.pop('sampleNativeSQL', None)
    (query_dir / 'query-log-summary.json').write_text(json.dumps(query_sample, indent=2) + '\n')
    out = {**meta, **query_sample, 'nativeSQLPath': str(native_rel)}
    if needs_processors(row, meta) and row.get('sampleQueryId'):
        proc_sql = f"""
SELECT
  name,
  count() AS processors,
  sum(elapsed_us) AS elapsedMicroseconds,
  round(elapsedMicroseconds / 1000000, 6) AS elapsedSeconds,
  sum(input_rows) AS inputRows,
  sum(output_rows) AS outputRows,
  sum(input_bytes) AS inputBytes,
  sum(output_bytes) AS outputBytes
FROM system.processors_profile_log
WHERE query_id = '{str(row.get('sampleQueryId')).replace("'", "''")}'
GROUP BY name
ORDER BY elapsedMicroseconds DESC
FORMAT JSONEachRow
"""
        step_sql = f"""
SELECT
  plan_step_name AS planStepName,
  name,
  count() AS processors,
  sum(elapsed_us) AS elapsedMicroseconds,
  round(elapsedMicroseconds / 1000000, 6) AS elapsedSeconds,
  sum(input_rows) AS inputRows,
  sum(output_rows) AS outputRows
FROM system.processors_profile_log
WHERE query_id = '{str(row.get('sampleQueryId')).replace("'", "''")}'
GROUP BY plan_step_name, name
ORDER BY elapsedMicroseconds DESC
FORMAT JSONEachRow
"""
        try:
            proc_rows = ch_json_each_row(proc_sql, timeout=30)
            step_rows = ch_json_each_row(step_sql, timeout=30)
            (query_dir / 'processors-by-name.json').write_text(json.dumps(proc_rows, indent=2) + '\n')
            (query_dir / 'processors-by-step.json').write_text(json.dumps(step_rows, indent=2) + '\n')
            write_tsv(query_dir / 'processors-by-name.tsv', proc_rows, ['name','processors','elapsedMicroseconds','elapsedSeconds','inputRows','outputRows','inputBytes','outputBytes'])
            write_tsv(query_dir / 'processors-by-step.tsv', step_rows, ['planStepName','name','processors','elapsedMicroseconds','elapsedSeconds','inputRows','outputRows'])
            out['processorsByNamePath'] = str((query_dir / 'processors-by-name.tsv').relative_to(pathlib.Path(output_path).parent))
            out['topProcessors'] = proc_rows[:5]
        except Exception as exc:
            out['processorProfileError'] = str(exc)
    summary['rows'].append(out)

pathlib.Path(output_path).parent.mkdir(parents=True, exist_ok=True)
pathlib.Path(output_path).write_text(json.dumps(summary, indent=2) + '\n')

hot = sorted(summary['rows'], key=lambda r: (r.get('queryDurationP50Ms') or 0, r.get('memoryP95Bytes') or 0), reverse=True)[:10]
lines = ['# ClickHouse profile summary', '', f"Source report: `{report_path}`", '', '| Query | Mode | CH p50 | Mem p95 | Read rows | Join rows | Filter rows | Native SQL |', '|---|---|---:|---:|---:|---:|---:|---|']
def human(n):
    if n is None: return ''
    try: n = float(n)
    except Exception: return str(n)
    for suffix in ['', 'K', 'M', 'B', 'T']:
        if abs(n) < 1000:
            return f"{n:.1f}{suffix}" if suffix else str(int(n))
        n /= 1000
    return f"{n:.1f}P"
def pick_metric(row, base):
    return row.get(base + 'P50', row.get(base))

def human_bytes(n):
    if n is None: return ''
    try: n = float(n)
    except Exception: return str(n)
    for suffix in ['B', 'KiB', 'MiB', 'GiB', 'TiB']:
        if abs(n) < 1024:
            return f"{n:.1f}{suffix}"
        n /= 1024
    return f"{n:.1f}PiB"
for r in hot:
    lines.append(f"| `{r.get('queryName')}` | `{r.get('mode')}` | {r.get('queryDurationP50Ms', '')}ms | {human_bytes(r.get('memoryP95Bytes'))} | {human(pick_metric(r, 'readRows'))} | {human(pick_metric(r, 'joinResultRowCount'))} | {human(pick_metric(r, 'filterTransformPassedRows'))} | `{r.get('nativeSQLPath')}` |")
lines.append('')
if summary.get('errors'):
    lines.append('## Errors')
    lines.extend(f"- {e}" for e in summary['errors'])
pathlib.Path(markdown_path).write_text('\n'.join(lines) + '\n')
print(f"Wrote {output_path}")
print(f"Wrote {markdown_path}")
if hot:
    print("ClickHouse profile highlights:")
    for r in hot[:5]:
        top = ''
        if r.get('topProcessors'):
            top = ' top=' + ', '.join(f"{p.get('name')} {p.get('elapsedSeconds')}s" for p in r['topProcessors'][:3])
        print(f"  {r.get('queryName')} mode={r.get('mode')} ch_p50={r.get('queryDurationP50Ms')}ms mem_p95={human_bytes(r.get('memoryP95Bytes'))} read_p50={human(pick_metric(r, 'readRows'))} join_p50={human(pick_metric(r, 'joinResultRowCount'))} filter_p50={human(pick_metric(r, 'filterTransformPassedRows'))}{top}")
PY
}

capture_pprof_snapshot before

log "Running promshim-bench ${ARGS[*]}"
set +e
"$BIN" "${ARGS[@]}"
STATUS=$?
set -e

capture_pprof_snapshot after
write_memory_detail_manifest

if (( STATUS == 0 )); then
  log "Bench completed with no regressions."
else
  log "Bench exited non-zero (status=${STATUS})."
fi

# Preserve a per-profile copy so `--long-range all` leaves three reports
# side-by-side and scripts/bench-matrix.sh can join across them.
capture_memory_summary "$REPORT_PATH" "$MEMORY_SUMMARY_PATH"
capture_clickhouse_profile "$REPORT_PATH" "$CLICKHOUSE_PROFILE_SUMMARY_PATH" "$CLICKHOUSE_PROFILE_MD_PATH" "$CLICKHOUSE_PROFILE_DIR"

if [[ -n "$LONG_RANGE" ]]; then
  src="${REPO_ROOT}/${ARTIFACT_DIR}/bench-report.json"
  dst="${REPO_ROOT}/${ARTIFACT_DIR}/bench-report-${LONG_RANGE}.json"
  if [[ -f "$src" ]]; then
    cp "$src" "$dst"
    log "Saved per-profile report: $dst"
  fi
fi

if (( MATRIX == 1 )); then
  report="$REPORT_PATH"
  if [[ ! -f "$report" ]]; then
    log "--matrix requested but report not found: $report"
  elif ! command -v jq >/dev/null 2>&1; then
    log "--matrix requested but jq is not installed; skipping matrix."
  elif jq -e '.schemaVersion == 2' "$report" >/dev/null 2>&1; then
    echo
    echo "## Shim mode vs Prometheus matrix"
    echo
    echo "V2 report: one row per query/mode. S/P = shim_p50 / prom_p50."
    echo
    echo "| Query | Endpoint | Mode | Strategy | CH rts | CH ms | Prom p50 (ms) | Prom band | Shim p50 (ms) | S/P |"
    echo "|---|---|---|---|---:|---:|---:|---|---:|---:|"
    jq -r '
      def r2: if . == null then "—" else (. * 100 | round / 100 | tostring) end;
      def r1: if . == null or . == 0 then "—" else ((. * 10 | round / 10 | tostring) + "×") end;
      .rows[] as $row
      | ($row.prom.p50Ms // null) as $prom
      | ($row.shim | to_entries[])
      | . as $mode
      | "| \($row.name) | \($row.endpoint) | \($mode.key) | \($mode.value.strategy // "") | \($mode.value.chRoundtrips // 0) | \($mode.value.chMillis // 0) | \($prom | r2) | \($row.promBand // "n/a") | \($mode.value.p50Ms | r2) | \((if $prom and $prom > 0 then ($mode.value.p50Ms / $prom) else null end) | r1) |"
    ' "$report"
    echo
  else
    echo
    echo "## Native-SQL vs Prometheus matrix"
    echo
    echo "Sorted by native/prom ratio (highest first). N/P < 1 means native beat Prom."
    echo "F/N compares the local-evaluator fallback to native at the same query (< 1 means"
    echo "the fallback is faster)."
    echo
    echo "| Query | Endpoint | Strategy | CH rts | CH ms | Prom p50 (ms) | Native p50 (ms) | N/P | Fallback p50 (ms) | F/N |"
    echo "|---|---|---|---:|---:|---:|---:|---:|---:|---:|"
    jq -r '
      def r2: if . == null then "—" else (. * 100 | round / 100 | tostring) end;
      def r1: if . == null then "—" else ((. * 10 | round / 10 | tostring) + "×") end;
      .rows
      | sort_by(.nativePromRatio // 0)
      | reverse
      | .[]
      | "| \(.name) | \(.endpoint) | \(.strategy) | \(.chRoundtrips) | \(.chMillis) | \(.promP50Ms | r2) | \(.nativeP50Ms | r2) | \(.nativePromRatio | r1) | \(.fallbackP50Ms | r2) | \(.fallbackNativeRatio | r1) |"
    ' "$report"
    echo
  fi
fi

exit "$STATUS"
