#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/run-harness.sh [options]

Run the differential + compliance harnesses against promshim.
Full run completes in ~25s on a warm docker cache; ~90s cold including
image builds. Runs in the foreground — do NOT add a minutes-long timeout
wrapper.

Default (no args) runs every suite:
  1) differential  — harness/corpus/queries.json (all subjects)
  2) dashboard     — harness/corpus/common-dashboard-subset.json (--subjects shim)
  3) compliance    — upstream PromQL compliance tester (delegates to
                     scripts/run-compliance.sh)
  4) bench         — Prom-vs-promshim native-SQL tripwire
                     (delegates to scripts/run-bench.sh; shares the compliance
                     stack and fixture)

Suites 1+2 share a single main-harness stack (clickhouse/prometheus/promshim)
and a single seed. Suites 3+4 run against the separate compliance stack with
its frozen fixture. Each suite prints its own summary; the runner exits 0 as
long as the tooling itself completes and no bench regressions are detected.

Suite selection:
  --suite <name>        all (default) | differential | dashboard | compliance | bench

Differential-only knobs (imply --suite differential when used alone):
  --theme <name>        Run one themed corpus from
                        draft-grafana-top-panel-shortlist.themes/.
  --all-themes          Run every theme corpus in sequence
                        (per-theme reports at artifacts/compare-report-{theme}.json).
  --corpus <path>       Run one corpus JSON under harness/corpus/.
  --subjects <list>     Restrict compare subjects (e.g. shim or shim,promclick).
  --dataset-variants <list>
                        Seed multiple dataset shapes (e.g. baseline,resets_gaps).
  --native-only         Force native_lowering_mode=force_supported.

Common:
  --no-build            Skip image builds.
  --keep-up             Keep all stacks up after the run.
  --init-retries <n>    clickhouse-init retry attempts (default: 10).
  --ready-timeout <n>   Seconds to wait for compliance endpoints (default: 60).
  -h, --help            Show this help.
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
THEMES_HOST_DIR="${HARNESS_DIR}/corpus/draft-grafana-top-panel-shortlist.themes"

SUITE=""
BUILD_IMAGES=1
KEEP_UP=0
INIT_RETRIES=10
READY_TIMEOUT=60
THEME=""
ALL_THEMES=0
CUSTOM_CORPUS=""
SUBJECTS=""
DATASET_VARIANTS=""
NATIVE_ONLY=0
DIFFERENTIAL_OVERRIDE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --suite)             SUITE="$2"; shift 2 ;;
    --theme)             THEME="$2"; DIFFERENTIAL_OVERRIDE=1; shift 2 ;;
    --all-themes)        ALL_THEMES=1; DIFFERENTIAL_OVERRIDE=1; shift ;;
    --corpus)            CUSTOM_CORPUS="$2"; DIFFERENTIAL_OVERRIDE=1; shift 2 ;;
    --subjects)          SUBJECTS="$2"; shift 2 ;;
    --dataset-variants)  DATASET_VARIANTS="$2"; shift 2 ;;
    --native-only)       NATIVE_ONLY=1; shift ;;
    --no-build)          BUILD_IMAGES=0; shift ;;
    --keep-up)           KEEP_UP=1; shift ;;
    --init-retries)      INIT_RETRIES="$2"; shift 2 ;;
    --ready-timeout)     READY_TIMEOUT="$2"; shift 2 ;;
    -h|--help)           usage; exit 0 ;;
    *)                   fatal "Unknown argument: $1" ;;
  esac
done

if [[ -z "$SUITE" ]]; then
  if (( DIFFERENTIAL_OVERRIDE == 1 )); then
    SUITE="differential"
  else
    SUITE="all"
  fi
fi

case "$SUITE" in
  all|differential|dashboard|compliance|bench) ;;
  *) fatal "--suite must be one of: all, differential, dashboard, compliance, bench" ;;
esac

if ! [[ "$INIT_RETRIES" =~ ^[0-9]+$ ]] || [[ "$INIT_RETRIES" -lt 1 ]]; then
  fatal "--init-retries must be a positive integer"
fi
if ! [[ "$READY_TIMEOUT" =~ ^[0-9]+$ ]] || [[ "$READY_TIMEOUT" -lt 1 ]]; then
  fatal "--ready-timeout must be a positive integer"
fi

if [[ -n "$THEME" ]] && (( ALL_THEMES == 1 )); then
  fatal "--theme and --all-themes are mutually exclusive"
fi
if [[ -n "$CUSTOM_CORPUS" ]] && { [[ -n "$THEME" ]] || (( ALL_THEMES == 1 )); }; then
  fatal "--corpus cannot be combined with --theme or --all-themes"
fi
if (( DIFFERENTIAL_OVERRIDE == 1 )) && [[ "$SUITE" != "differential" ]]; then
  fatal "--theme/--all-themes/--corpus only apply to --suite differential"
fi

ensure_command docker
ensure_command curl

if [[ ! -d "$HARNESS_DIR" ]] || [[ ! -f "$HARNESS_DIR/docker-compose.yml" ]]; then
  fatal "Harness directory not found or invalid: $HARNESS_DIR"
fi

MAIN_STACK_STARTED=0
COMPLIANCE_STACK_STARTED=0

cleanup() {
  if (( KEEP_UP == 1 )); then
    log "Leaving stacks running (--keep-up)."
    return
  fi
  if (( MAIN_STACK_STARTED == 1 )); then
    log "Stopping main harness stack (docker compose down -v)."
    (cd "$HARNESS_DIR" && docker compose down -v >/dev/null 2>&1 || true)
  fi
  if (( COMPLIANCE_STACK_STARTED == 1 )); then
    log "Stopping compliance stack (docker compose down)."
    (cd "${REPO_ROOT}/harness/compliance" && docker compose down >/dev/null 2>&1 || true)
  fi
}
trap cleanup EXIT

discover_themes() {
  local theme_file theme_name
  for theme_file in "${THEMES_HOST_DIR}"/*.json; do
    [[ "$theme_file" == *.metadata.json ]] && continue
    theme_name=$(basename "$theme_file" .json)
    [[ "$theme_name" == summary ]] && continue
    printf '%s\n' "$theme_name"
  done
}

print_compare_summary() {
  local report_path="$1" label="$2"
  if ! command -v jq >/dev/null 2>&1 || [[ ! -f "$report_path" ]]; then
    return
  fi
  log "Compare summary (${label}):"
  jq '{
    status: ([.results[].status] | group_by(.) | map({key:.[0], value:length}) | from_entries),
    severity: ([.results[] | (.severity // "ok")] | group_by(.) | map({key:.[0], value:length}) | from_entries),
    bucket: ([.results[] | select(.status != "ok") | (.bucket // "other")] | group_by(.) | map({key:.[0], value:length}) | from_entries)
  }' "$report_path"
}

run_compare() {
  local corpus_path="$1" report_dest="$2" label="$3" subjects_override="${4:-}"
  local env_args=(-e "PROM_HARNESS_CORPUS_PATH=${corpus_path}")
  local subjects_effective="${subjects_override:-${SUBJECTS}}"
  if [[ -n "$subjects_effective" ]]; then
    env_args+=(-e "PROM_HARNESS_SUBJECTS=${subjects_effective}")
  fi
  if (( NATIVE_ONLY == 1 )); then
    env_args+=(-e "PROM_HARNESS_NATIVE_LOWERING_MODE=force_supported")
  fi
  log "Running compare: ${label}"
  docker compose --profile jobs run --rm "${env_args[@]}" compare || true
  if [[ "${report_dest}" != "${HARNESS_DIR}/artifacts/compare-report.json" ]]; then
    cp "${HARNESS_DIR}/artifacts/compare-report.json" "${report_dest}"
  fi
  print_compare_summary "${report_dest}" "${label}"
}

start_main_stack_and_seed() {
  cd "$HARNESS_DIR"

  if (( BUILD_IMAGES == 1 )); then
    log "Building main harness images (promshim/seed/compare)."
    docker compose build promshim seed compare
  else
    log "Skipping main image build (--no-build)."
  fi

  log "Starting main harness stack (clickhouse/prometheus/promshim)."
  docker compose up -d clickhouse prometheus promshim
  MAIN_STACK_STARTED=1

  log "Initializing ClickHouse schema."
  local init_ok=0 attempt
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
  local seed_env_args=()
  if [[ -n "$DATASET_VARIANTS" ]]; then
    seed_env_args+=(-e "PROM_HARNESS_DATASET_VARIANTS=${DATASET_VARIANTS}")
  fi
  docker compose --profile jobs run --rm "${seed_env_args[@]}" seed
}

run_differential_suite() {
  if (( ALL_THEMES == 1 )); then
    mapfile -t themes < <(discover_themes)
    if [[ ${#themes[@]} -eq 0 ]]; then
      fatal "No theme corpus files found in ${THEMES_HOST_DIR}"
    fi
    log "Running all ${#themes[@]} themes: ${themes[*]}"
    for theme in "${themes[@]}"; do
      run_compare \
        "/app/harness/corpus/draft-grafana-top-panel-shortlist.themes/${theme}.json" \
        "${HARNESS_DIR}/artifacts/compare-report-${theme}.json" \
        "${theme}"
    done
    log "All theme reports written to ${HARNESS_DIR}/artifacts/compare-report-{theme}.json"
    return
  fi

  local corpus
  if [[ -n "$THEME" ]]; then
    corpus="/app/harness/corpus/draft-grafana-top-panel-shortlist.themes/${THEME}.json"
  elif [[ -n "$CUSTOM_CORPUS" ]]; then
    if [[ ! -f "${HARNESS_DIR}/corpus/${CUSTOM_CORPUS}" ]]; then
      fatal "Corpus file not found under harness/corpus/: ${CUSTOM_CORPUS}"
    fi
    corpus="/app/harness/corpus/${CUSTOM_CORPUS}"
  else
    corpus="/app/harness/corpus/queries.json"
  fi

  run_compare "$corpus" "${HARNESS_DIR}/artifacts/compare-report.json" "${THEME:-differential}"
}

run_dashboard_suite() {
  if [[ ! -f "${HARNESS_DIR}/corpus/common-dashboard-subset.json" ]]; then
    fatal "Dashboard corpus missing: harness/corpus/common-dashboard-subset.json"
  fi
  run_compare \
    "/app/harness/corpus/common-dashboard-subset.json" \
    "${HARNESS_DIR}/artifacts/compare-report-dashboard.json" \
    "dashboard" \
    "shim"
}

run_compliance_suite() {
  local args=()
  if (( BUILD_IMAGES == 0 )); then args+=(--no-build); fi
  if (( KEEP_UP == 1 )) || [[ "$SUITE" == "all" ]]; then
    args+=(--keep-up)
    COMPLIANCE_STACK_STARTED=1
  fi
  args+=(--ready-timeout "$READY_TIMEOUT")
  log "Delegating to scripts/run-compliance.sh ${args[*]}"
  "${REPO_ROOT}/scripts/run-compliance.sh" "${args[@]}" || true
}

run_bench_suite() {
  # The bench reuses the compliance stack. When SUITE=all we explicitly keep
  # the compliance stack up so bench can run against the same fixture without
  # re-running the compliance tester. When SUITE=bench alone, we bring up the
  # compliance stack ourselves.
  local args=(--ready-timeout "$READY_TIMEOUT")
  if [[ "$SUITE" == "bench" ]]; then
    args+=(--bring-up)
    COMPLIANCE_STACK_STARTED=1
  fi
  log "Delegating to scripts/run-bench.sh ${args[*]}"
  "${REPO_ROOT}/scripts/run-bench.sh" "${args[@]}" || true
}

log "Suite: ${SUITE}"

case "$SUITE" in
  differential|dashboard|all)
    start_main_stack_and_seed
    case "$SUITE" in
      differential) run_differential_suite ;;
      dashboard)    run_dashboard_suite ;;
      all)          run_differential_suite; run_dashboard_suite ;;
    esac
    ;;
esac

case "$SUITE" in
  compliance|all) run_compliance_suite ;;
esac

case "$SUITE" in
  bench|all) run_bench_suite ;;
esac

log "Harness run completed."
