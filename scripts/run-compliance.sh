#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/run-compliance.sh [options]

Run the PromQL compliance suite against reference Prometheus and promshim
in one command. Full two-pass run completes in ~15s on a warm docker cache;
~60s cold including image builds. Runs in the foreground — do NOT add a
minutes-long timeout wrapper.

Default flow:
  1) docker compose build promshim         (in harness/compliance)
  2) docker compose up -d                  (clickhouse + prometheus + promshim)
  3) wait for Prometheus + promshim to answer
  4) optionally run Go integration tests against the ready compliance stack
     (--run-integration-tests)
  5) pass #1 (prefer mode, gated by allowlist)
     - harness/compliance/scripts/run-compliance.sh --mode prefer --suffix prefer
     - harness/compliance/scripts/classify-failures.sh (latest report)
  6) recreate promshim with NATIVE-ONLY override (force_supported)
  7) pass #2 (native mode, informational gap report)
     - harness/compliance/scripts/run-compliance.sh --mode native --suffix native
     - harness/compliance/scripts/native-gap-report.sh (latest native report)
  8) docker compose down                   (volumes preserved)

The ClickHouse schema and Prometheus TSDB fixture are persisted in docker
volumes, so repeat runs reuse the deterministic remote-write fixture window
configured in harness/compliance/test-promshim.yml. Fresh empty volumes are
seeded automatically; stale or partial fixture data fails before compliance.

Options:
  --no-build         Skip the promshim image build.
  --keep-up          Leave the stack running after the tester finishes.
  --skip-classify    Skip the classify-failures.sh summary step.
  --skip-native      Skip pass #2 (native-only). Useful when iterating on
                     allowlist/prefer-mode behavior.
  --skip-prefer      Skip pass #1 (prefer). Useful when only native
                     coverage matters.
  --run-integration-tests
                     Run Go integration tests against the ready compliance
                     ClickHouse fixture before the compliance corpus.
  --ready-timeout N  Seconds to wait for endpoints to answer (default: 60).
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
# shellcheck source=lib/artifacts.sh
source "${REPO_ROOT}/scripts/lib/artifacts.sh"
acquire_run_lock "stack"

COMPLIANCE_DIR="${REPO_ROOT}/harness/compliance"
COMPLIANCE_RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
COMPLIANCE_ARTIFACT_DIR="$(artifact_abs "compliance/${COMPLIANCE_RUN_ID}")"
COMPLIANCE_LATEST_LINK="$(artifact_abs "compliance/latest")"

BUILD_IMAGES=1
KEEP_UP=0
SKIP_CLASSIFY=0
SKIP_NATIVE=0
SKIP_PREFER=0
RUN_INTEGRATION_TESTS=0
READY_TIMEOUT=60

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build)        BUILD_IMAGES=0; shift ;;
    --keep-up)         KEEP_UP=1; shift ;;
    --skip-classify)   SKIP_CLASSIFY=1; shift ;;
    --skip-native)     SKIP_NATIVE=1; shift ;;
    --skip-prefer)     SKIP_PREFER=1; shift ;;
    --run-integration-tests) RUN_INTEGRATION_TESTS=1; shift ;;
    --ready-timeout)   READY_TIMEOUT="$2"; shift 2 ;;
    -h|--help)         usage; exit 0 ;;
    *)                 fatal "Unknown argument: $1" ;;
  esac
done

if (( SKIP_NATIVE == 1 )) && (( SKIP_PREFER == 1 )); then
  fatal "--skip-native and --skip-prefer together would run neither pass"
fi

if ! [[ "$READY_TIMEOUT" =~ ^[0-9]+$ ]] || [[ "$READY_TIMEOUT" -lt 1 ]]; then
  fatal "--ready-timeout must be a positive integer"
fi

ensure_command docker
ensure_command curl

if [[ ! -d "$COMPLIANCE_DIR" ]] || [[ ! -f "$COMPLIANCE_DIR/docker-compose.yml" ]]; then
  fatal "Compliance harness directory not found: $COMPLIANCE_DIR"
fi

mkdir -p "$COMPLIANCE_ARTIFACT_DIR"
if [[ -L "$COMPLIANCE_LATEST_LINK" || ! -e "$COMPLIANCE_LATEST_LINK" ]]; then
  ln -sfn "$COMPLIANCE_RUN_ID" "$COMPLIANCE_LATEST_LINK"
else
  log "Not updating compliance/latest because it exists and is not a symlink: $COMPLIANCE_LATEST_LINK"
fi
log "Compliance artifacts: $COMPLIANCE_ARTIFACT_DIR"

cleanup() {
  if (( KEEP_UP == 0 )); then
    log "Stopping compliance stack (docker compose down)."
    (cd "$COMPLIANCE_DIR" && docker compose down >/dev/null 2>&1 || true)
  else
    log "Leaving compliance stack running (--keep-up)."
  fi
}

cd "$COMPLIANCE_DIR"
trap cleanup EXIT

if (( BUILD_IMAGES == 1 )); then
  log "Building promshim image."
  docker compose build promshim
else
  log "Skipping image build (--no-build)."
fi

log "Starting compliance stack (clickhouse/prometheus/promshim)."
docker compose up -d

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

wait_for_tcp() {
  local name="$1" host="$2" port="$3" deadline=$(( $(date +%s) + READY_TIMEOUT ))
  while (( $(date +%s) < deadline )); do
    if (echo >"/dev/tcp/${host}/${port}") >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fatal "${name} did not become ready within ${READY_TIMEOUT}s (${host}:${port})"
}

log "Waiting for ClickHouse (:28123), Prometheus (:29090), promshim (:29091)."
wait_for_http "ClickHouse"           "http://localhost:28123/ping"
if [[ "${PROM_SHIM_CLICKHOUSE_TRANSPORT:-native}" == "native" ]]; then
  wait_for_tcp "ClickHouse native" "localhost" "29000"
fi
wait_for_http "Prometheus reference" "http://localhost:29090/-/ready"
wait_for_http "promshim"             "http://localhost:29091/-/ready"

COMPLIANCE_EVAL_TIME=$(awk -F"'" '/end_time:/ {print $2; exit}' "${COMPLIANCE_DIR}/test-promshim.yml")
if [[ -z "$COMPLIANCE_EVAL_TIME" ]]; then
  fatal "Could not read query_time_parameters.end_time from ${COMPLIANCE_DIR}/test-promshim.yml"
fi

scalar_equals() {
  local base_url="$1" query="$2" eval_time="$3" expected="$4" response
  response=$(curl -fsS --get "${base_url}/api/v1/query" \
    --data-urlencode "query=${query}" \
    --data-urlencode "time=${eval_time}") || return 1
  RESPONSE="$response" python3 - "$expected" <<'PY'
import json, math, os, sys
expected = float(sys.argv[1])
data = json.loads(os.environ["RESPONSE"])
result = data.get("data", {}).get("result", [])
if data.get("status") != "success" or len(result) != 1:
    sys.exit(1)
try:
    actual = float(result[0]["value"][1])
except (KeyError, IndexError, TypeError, ValueError):
    sys.exit(1)
if not math.isclose(actual, expected, rel_tol=0, abs_tol=1e-9):
    sys.exit(1)
PY
}

fixture_present_for() {
  local base_url="$1"
  scalar_equals "$base_url" 'count(promshim_compliance_fixture_info{fixture="promql-demo", generator="promshim-compliance-seed"})' "$COMPLIANCE_EVAL_TIME" 1 &&
    scalar_equals "$base_url" 'count(demo_memory_usage_bytes{job="demo"})' "$COMPLIANCE_EVAL_TIME" 12 &&
    scalar_equals "$base_url" 'count(demo_num_cpus{job="demo"})' "$COMPLIANCE_EVAL_TIME" 3 &&
    scalar_equals "$base_url" 'count(demo_api_request_duration_seconds_bucket{job="demo"})' "$COMPLIANCE_EVAL_TIME" 702 &&
    scalar_equals "$base_url" 'absent(demo_intermittent_metric{job="demo"})' "$COMPLIANCE_EVAL_TIME" 1 &&
    scalar_equals "$base_url" 'sum(resets(demo_cpu_usage_seconds_total{job="demo"}[1h]))' "$COMPLIANCE_EVAL_TIME" 18 &&
    scalar_equals "$base_url" 'count(count_values("value", demo_memory_usage_bytes{job="demo"}) > 1)' "$COMPLIANCE_EVAL_TIME" 1
}

fixture_empty_for() {
  local base_url="$1"
  scalar_equals "$base_url" 'absent(promshim_compliance_fixture_info{fixture="promql-demo", generator="promshim-compliance-seed"})' "$COMPLIANCE_EVAL_TIME" 1 &&
    scalar_equals "$base_url" 'absent(demo_memory_usage_bytes{job="demo"})' "$COMPLIANCE_EVAL_TIME" 1 &&
    scalar_equals "$base_url" 'absent(demo_num_cpus{job="demo"})' "$COMPLIANCE_EVAL_TIME" 1 &&
    scalar_equals "$base_url" 'absent(demo_api_request_duration_seconds_bucket{job="demo"})' "$COMPLIANCE_EVAL_TIME" 1
}

assert_fixture_for() {
  local name="$1" base_url="$2"
  fixture_present_for "$base_url" || fatal "${name} does not contain the expected compliance fixture data at ${COMPLIANCE_EVAL_TIME}"
}

log "Checking compliance fixture data at ${COMPLIANCE_EVAL_TIME}."
fixture_state_deadline=$(( $(date +%s) + READY_TIMEOUT ))
fixture_state_known=0
PROM_FIXTURE_PRESENT=0
SHIM_FIXTURE_PRESENT=0
PROM_FIXTURE_EMPTY=0
SHIM_FIXTURE_EMPTY=0
while (( $(date +%s) < fixture_state_deadline )); do
  PROM_FIXTURE_PRESENT=0
  SHIM_FIXTURE_PRESENT=0
  PROM_FIXTURE_EMPTY=0
  SHIM_FIXTURE_EMPTY=0
  fixture_present_for "http://localhost:29090" && PROM_FIXTURE_PRESENT=1
  fixture_present_for "http://localhost:29091" && SHIM_FIXTURE_PRESENT=1
  fixture_empty_for "http://localhost:29090" && PROM_FIXTURE_EMPTY=1
  fixture_empty_for "http://localhost:29091" && SHIM_FIXTURE_EMPTY=1
  if (( (PROM_FIXTURE_PRESENT == 1 || PROM_FIXTURE_EMPTY == 1) && (SHIM_FIXTURE_PRESENT == 1 || SHIM_FIXTURE_EMPTY == 1) )); then
    fixture_state_known=1
    break
  fi
  sleep 1
done
if (( fixture_state_known == 0 )); then
  fatal "Could not classify compliance fixture state within ${READY_TIMEOUT}s; one or both query APIs did not return fixture count queries reliably."
fi

if (( PROM_FIXTURE_PRESENT == 1 && SHIM_FIXTURE_PRESENT == 1 )); then
  log "Compliance fixture already present in Prometheus and ClickHouse."
elif (( PROM_FIXTURE_EMPTY == 1 && SHIM_FIXTURE_EMPTY == 1 )); then
  log "Seeding compliance fixture into Prometheus and ClickHouse."
  (cd "$REPO_ROOT" && go run ./cmd/promshim-compliance-seed \
    --target both \
    --end-time "$COMPLIANCE_EVAL_TIME" \
    --duration 2h \
    --step 5s)
else
  fatal "Compliance fixture is stale or partially present (prometheus_present=${PROM_FIXTURE_PRESENT}, promshim_present=${SHIM_FIXTURE_PRESENT}, prometheus_empty=${PROM_FIXTURE_EMPTY}, promshim_empty=${SHIM_FIXTURE_EMPTY}); reset the compliance volumes so the deterministic fixture can be seeded consistently."
fi

log "Asserting compliance fixture data is queryable from both targets."
fixture_deadline=$(( $(date +%s) + READY_TIMEOUT ))
while (( $(date +%s) < fixture_deadline )); do
  if fixture_present_for "http://localhost:29090" && fixture_present_for "http://localhost:29091"; then
    break
  fi
  sleep 1
done
assert_fixture_for "Prometheus reference" "http://localhost:29090"
assert_fixture_for "promshim/ClickHouse" "http://localhost:29091"

if [[ "${PROM_SHIM_CLICKHOUSE_TRANSPORT:-native}" == "native" ]]; then
  # The native TCP listener can accept a first query while ClickHouse is still
  # finishing startup; give the pool a short stabilization window before the
  # compliance tester fans out concurrent requests.
  sleep 5
fi

if (( RUN_INTEGRATION_TESTS == 1 )); then
  log "Running Go integration tests against the compliance stack."
  (cd "$REPO_ROOT" && make integration-test-report)
fi

OVERALL_EXIT=0

if (( SKIP_PREFER == 0 )); then
  log "Pass #1: prefer mode (allowlist-gated)."
  if ! PROM_SHIM_COMPLIANCE_ARTIFACT_DIR="$COMPLIANCE_ARTIFACT_DIR" ./scripts/run-compliance.sh --mode prefer --suffix prefer; then
    OVERALL_EXIT=1
  fi

  if (( SKIP_CLASSIFY == 0 )); then
    log "Classifying failures from latest prefer-mode report."
    latest_prefer=$(ls -t "${COMPLIANCE_ARTIFACT_DIR}"/compliance-report-prefer-*.json 2>/dev/null | head -1 || true)
    if [[ -n "$latest_prefer" ]]; then
      ./scripts/classify-failures.sh "$latest_prefer" || true
    fi
  else
    log "Skipping classify step (--skip-classify)."
  fi
else
  log "Skipping prefer-mode pass (--skip-prefer)."
fi

if (( SKIP_NATIVE == 0 )); then
  log "Recreating promshim with native-only lowering (force_supported)."
  docker compose -f docker-compose.yml -f docker-compose.native-only.yml up -d promshim >/dev/null

  wait_for_http "promshim (native-only)" "http://localhost:29091/-/ready"
  log "Probing native-only promshim -> ClickHouse integration."
  smoke_deadline=$(( $(date +%s) + READY_TIMEOUT ))
  while (( $(date +%s) < smoke_deadline )); do
    if curl -fsS "http://localhost:29091/api/v1/query?query=up" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done

  log "Pass #2: native-only mode (informational gap report, no allowlist)."
  # Native pass intentionally does NOT fail the overall exit code — gaps
  # are tracked openly in the gap report, not as allowlistable failures.
  PROM_SHIM_COMPLIANCE_ARTIFACT_DIR="$COMPLIANCE_ARTIFACT_DIR" ./scripts/run-compliance.sh --mode native --suffix native || true

  latest_native=$(ls -t "${COMPLIANCE_ARTIFACT_DIR}"/compliance-report-native-*.json 2>/dev/null | head -1 || true)
  if [[ -n "$latest_native" ]]; then
    log "Native-mode gap report:"
    ./scripts/native-gap-report.sh "$latest_native" || true
  else
    log "No native-mode report found under ${COMPLIANCE_ARTIFACT_DIR}; skipping gap report."
  fi
else
  log "Skipping native-only pass (--skip-native)."
fi

if (( OVERALL_EXIT != 0 )); then
  log "Compliance run completed with prefer-mode regressions."
  exit "$OVERALL_EXIT"
fi

log "Compliance run completed."
