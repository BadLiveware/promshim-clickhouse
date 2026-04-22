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
  --baseline PATH    Baseline bench report; exits non-zero on regressions
                     (default: harness/bench/baseline.json when present).
  --update-baseline  Rewrite the baseline file from this run's results.
  --repeats N        Timed repeats per (query, mode) (default: 10).
  --warmup N         Warmup repeats per (query, mode) (default: 2).
  --ready-timeout N  Seconds to wait for endpoints (default: 60).
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
DEFAULT_CORPUS="harness/corpus/bench-native-lowering.json"
DEFAULT_BASELINE="harness/bench/baseline.json"

BRING_UP=0
CORPUS="$DEFAULT_CORPUS"
BASELINE=""
UPDATE_BASELINE=0
REPEATS=10
WARMUP=2
READY_TIMEOUT=60

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bring-up)        BRING_UP=1; shift ;;
    --corpus)          CORPUS="$2"; shift 2 ;;
    --baseline)        BASELINE="$2"; shift 2 ;;
    --update-baseline) UPDATE_BASELINE=1; shift ;;
    --repeats)         REPEATS="$2"; shift 2 ;;
    --warmup)          WARMUP="$2"; shift 2 ;;
    --ready-timeout)   READY_TIMEOUT="$2"; shift 2 ;;
    -h|--help)         usage; exit 0 ;;
    *)                 fatal "Unknown argument: $1" ;;
  esac
done

ensure_command go
ensure_command curl

cd "$REPO_ROOT"

if [[ ! -f "$CORPUS" ]]; then
  fatal "Corpus file not found: $CORPUS"
fi

if [[ -z "$BASELINE" ]] && [[ -f "$DEFAULT_BASELINE" ]]; then
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

log "Checking Prometheus (:29090) and promshim (:29091) readiness."
wait_for_http "Prometheus reference" "http://localhost:29090/-/ready"
wait_for_http "promshim"             "http://localhost:29091/-/ready"

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

BIN="$(mktemp -d)/promshim-bench"
log "Building cmd/promshim-bench -> ${BIN}"
go build -o "$BIN" ./cmd/promshim-bench

ARGS=(
  --corpus "$CORPUS"
  --artifact-dir "harness/artifacts"
  --repeats "$REPEATS"
  --warmup "$WARMUP"
)
if [[ -n "$BASELINE" ]]; then
  ARGS+=(--baseline "$BASELINE")
fi
if (( UPDATE_BASELINE == 1 )); then
  ARGS+=(--update-baseline)
fi

log "Running promshim-bench ${ARGS[*]}"
set +e
"$BIN" "${ARGS[@]}"
STATUS=$?
set -e

if (( STATUS == 0 )); then
  log "Bench completed with no regressions."
else
  log "Bench exited non-zero (status=${STATUS})."
fi

exit "$STATUS"
