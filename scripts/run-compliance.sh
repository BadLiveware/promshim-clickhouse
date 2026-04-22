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
  4) pass #1 (prefer mode, gated by allowlist)
     - harness/compliance/scripts/run-compliance.sh --mode prefer --suffix prefer
     - harness/compliance/scripts/classify-failures.sh (latest report)
  5) recreate promshim with NATIVE-ONLY override (force_supported)
  6) pass #2 (native mode, informational gap report)
     - harness/compliance/scripts/run-compliance.sh --mode native --suffix native
     - harness/compliance/scripts/native-gap-report.sh (latest native report)
  7) docker compose down                   (volumes preserved)

The ClickHouse schema and Prometheus TSDB fixture are persisted in docker
volumes, so repeat runs reuse the frozen fixture window configured in
harness/compliance/test-promshim.yml (no rescrape, no end_time edit).

Options:
  --no-build         Skip the promshim image build.
  --keep-up          Leave the stack running after the tester finishes.
  --skip-classify    Skip the classify-failures.sh summary step.
  --skip-native      Skip pass #2 (native-only). Useful when iterating on
                     allowlist/prefer-mode behavior.
  --skip-prefer      Skip pass #1 (prefer). Useful when only native
                     coverage matters.
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
COMPLIANCE_DIR="${REPO_ROOT}/harness/compliance"

BUILD_IMAGES=1
KEEP_UP=0
SKIP_CLASSIFY=0
SKIP_NATIVE=0
SKIP_PREFER=0
READY_TIMEOUT=60

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build)        BUILD_IMAGES=0; shift ;;
    --keep-up)         KEEP_UP=1; shift ;;
    --skip-classify)   SKIP_CLASSIFY=1; shift ;;
    --skip-native)     SKIP_NATIVE=1; shift ;;
    --skip-prefer)     SKIP_PREFER=1; shift ;;
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

log "Waiting for ClickHouse (:28123), Prometheus (:29090), promshim (:29091)."
wait_for_http "ClickHouse"           "http://localhost:28123/ping"
wait_for_http "Prometheus reference" "http://localhost:29090/-/ready"
wait_for_http "promshim"             "http://localhost:29091/-/ready"

# Promshim's /-/ready returns OK as soon as its HTTP server is listening,
# even if ClickHouse isn't yet reachable. Probe a real query so the tester
# doesn't see a wave of 502s during the first second of the run.
log "Probing promshim -> ClickHouse integration."
smoke_deadline=$(( $(date +%s) + READY_TIMEOUT ))
smoke_ok=0
while (( $(date +%s) < smoke_deadline )); do
  if curl -fsS "http://localhost:29091/api/v1/query?query=up" >/dev/null 2>&1; then
    smoke_ok=1
    break
  fi
  sleep 1
done
if (( smoke_ok == 0 )); then
  fatal "promshim did not successfully serve a query within ${READY_TIMEOUT}s"
fi

OVERALL_EXIT=0

if (( SKIP_PREFER == 0 )); then
  log "Pass #1: prefer mode (allowlist-gated)."
  if ! ./scripts/run-compliance.sh --mode prefer --suffix prefer; then
    OVERALL_EXIT=1
  fi

  if (( SKIP_CLASSIFY == 0 )); then
    log "Classifying failures from latest prefer-mode report."
    latest_prefer=$(ls -t artifacts/compliance-report-prefer-*.json 2>/dev/null | head -1 || true)
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
  ./scripts/run-compliance.sh --mode native --suffix native || true

  log "Native-mode gap report:"
  ./scripts/native-gap-report.sh || true
else
  log "Skipping native-only pass (--skip-native)."
fi

if (( OVERALL_EXIT != 0 )); then
  log "Compliance run completed with prefer-mode regressions."
  exit "$OVERALL_EXIT"
fi

log "Compliance run completed."
