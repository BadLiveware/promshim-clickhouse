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
  --baseline PATH    Baseline bench report; exits non-zero on regressions
                     (default: harness/bench/baseline.json when present).
  --update-baseline  Rewrite the baseline file from this run's results.
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
BASELINE=""
UPDATE_BASELINE=0
REPEATS=10
WARMUP=2
READY_TIMEOUT=60
MATRIX=0
LONG_RANGE=""
EVAL_TIME=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bring-up)        BRING_UP=1; shift ;;
    --corpus)          CORPUS="$2"; shift 2 ;;
    --baseline)        BASELINE="$2"; shift 2 ;;
    --update-baseline) UPDATE_BASELINE=1; shift ;;
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
      if (( MATRIX == 1 ));          then PASSTHROUGH+=(--matrix); fi
      PASSTHROUGH+=(--repeats "$REPEATS" --warmup "$WARMUP" --ready-timeout "$READY_TIMEOUT")
      if [[ -n "$BASELINE" ]]; then PASSTHROUGH+=(--baseline "$BASELINE"); fi
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
if [[ -n "$EVAL_TIME" ]]; then
  ARGS+=(--eval-time "$EVAL_TIME")
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

# Preserve a per-profile copy so `--long-range all` leaves three reports
# side-by-side and scripts/bench-matrix.sh can join across them.
if [[ -n "$LONG_RANGE" ]]; then
  src="${REPO_ROOT}/harness/artifacts/bench-report.json"
  dst="${REPO_ROOT}/harness/artifacts/bench-report-${LONG_RANGE}.json"
  if [[ -f "$src" ]]; then
    cp "$src" "$dst"
    log "Saved per-profile report: $dst"
  fi
fi

if (( MATRIX == 1 )); then
  report="${REPO_ROOT}/harness/artifacts/bench-report.json"
  if [[ ! -f "$report" ]]; then
    log "--matrix requested but report not found: $report"
  elif ! command -v jq >/dev/null 2>&1; then
    log "--matrix requested but jq is not installed; skipping matrix."
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
