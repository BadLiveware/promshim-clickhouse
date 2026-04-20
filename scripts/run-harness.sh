#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/run-harness.sh [options]

Run the full differential Prometheus vs promshim harness workflow in one command.

Default flow:
  1) docker compose build promshim seed compare
  2) docker compose up -d clickhouse prometheus promshim
  3) docker compose run --rm clickhouse-init   (with retries)
  4) docker compose --profile jobs run --rm seed
  5) docker compose --profile jobs run --rm compare
  6) docker compose down -v

Options:
  --no-build            Skip image build step.
  --keep-up             Keep containers/network/volumes after completion.
  --init-retries <n>    Retry attempts for clickhouse-init (default: 10).
  -h, --help            Show this help text.
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
HARNESS_DIR="${REPO_ROOT}/harness"

BUILD_IMAGES=1
KEEP_UP=0
INIT_RETRIES=10

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build)
      BUILD_IMAGES=0
      shift
      ;;
    --keep-up)
      KEEP_UP=1
      shift
      ;;
    --init-retries)
      INIT_RETRIES="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fatal "Unknown argument: $1"
      ;;
  esac
done

if ! [[ "$INIT_RETRIES" =~ ^[0-9]+$ ]] || [[ "$INIT_RETRIES" -lt 1 ]]; then
  fatal "--init-retries must be a positive integer"
fi

ensure_command docker

if [[ ! -d "$HARNESS_DIR" ]] || [[ ! -f "$HARNESS_DIR/docker-compose.yml" ]]; then
  fatal "Harness directory not found or invalid: $HARNESS_DIR"
fi

cleanup() {
  if (( KEEP_UP == 0 )); then
    log "Stopping harness stack (docker compose down -v)."
    docker compose down -v >/dev/null 2>&1 || true
  else
    log "Leaving harness stack running (--keep-up)."
  fi
}

cd "$HARNESS_DIR"
trap cleanup EXIT

if (( BUILD_IMAGES == 1 )); then
  log "Building harness images (promshim/seed/compare)."
  docker compose build promshim seed compare
else
  log "Skipping image build (--no-build)."
fi

log "Starting harness services (clickhouse/prometheus/promshim)."
docker compose up -d clickhouse prometheus promshim

log "Initializing ClickHouse schema."
init_ok=0
for attempt in $(seq 1 "$INIT_RETRIES"); do
  if docker compose run --rm clickhouse-init; then
    init_ok=1
    break
  fi
  log "clickhouse-init attempt ${attempt}/${INIT_RETRIES} failed; retrying in 2s."
  sleep 2
done
if (( init_ok == 0 )); then
  fatal "clickhouse-init failed after ${INIT_RETRIES} attempts"
fi

log "Seeding deterministic dataset."
docker compose --profile jobs run --rm seed

log "Running differential compare job."
docker compose --profile jobs run --rm compare

if command -v jq >/dev/null 2>&1 && [[ -f "${HARNESS_DIR}/artifacts/compare-report.json" ]]; then
  log "Compare summary:"
  jq '[.results[].status] | group_by(.) | map({status:.[0], count:length})' "${HARNESS_DIR}/artifacts/compare-report.json"
else
  log "Compare report written to ${HARNESS_DIR}/artifacts/compare-report.json"
fi

log "Harness run completed successfully."
