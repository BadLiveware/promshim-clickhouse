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
  --theme <name>        Run against a single themed corpus from draft-grafana-top-panel-shortlist.themes/.
  --all-themes          Run against every theme corpus in sequence (seeds once, compares per theme).
                        Per-theme reports are written to artifacts/compare-report-{theme}.json.
  --corpus <path>       Run against a specific corpus JSON relative to harness/corpus/
                        (for example: native-lowering-starter.json).
  --subjects <list>     Restrict compare subjects to a comma-separated subset
                        (for example: shim or shim,promclick).
  --dataset-variants <list>
                        Seed multiple dataset shapes in one run (for example:
                        baseline,resets_gaps). Default keeps the legacy single
                        dataset shape.
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
THEME=""
ALL_THEMES=0
CUSTOM_CORPUS=""
SUBJECTS=""
DATASET_VARIANTS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --theme)
      THEME="$2"
      shift 2
      ;;
    --all-themes)
      ALL_THEMES=1
      shift
      ;;
    --corpus)
      CUSTOM_CORPUS="$2"
      shift 2
      ;;
    --subjects)
      SUBJECTS="$2"
      shift 2
      ;;
    --dataset-variants)
      DATASET_VARIANTS="$2"
      shift 2
      ;;
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

if [[ -n "$THEME" ]] && (( ALL_THEMES == 1 )); then
  fatal "--theme and --all-themes are mutually exclusive"
fi

if [[ -n "$CUSTOM_CORPUS" ]] && { [[ -n "$THEME" ]] || (( ALL_THEMES == 1 )); }; then
  fatal "--corpus cannot be combined with --theme or --all-themes"
fi

CORPUS_PATH_OVERRIDE=""
if [[ -n "$THEME" ]]; then
  CORPUS_PATH_OVERRIDE="/app/harness/corpus/draft-grafana-top-panel-shortlist.themes/${THEME}.json"
elif [[ -n "$CUSTOM_CORPUS" ]]; then
  if [[ ! -f "${HARNESS_DIR}/corpus/${CUSTOM_CORPUS}" ]]; then
    fatal "Corpus file not found under harness/corpus/: ${CUSTOM_CORPUS}"
  fi
  CORPUS_PATH_OVERRIDE="/app/harness/corpus/${CUSTOM_CORPUS}"
fi

THEMES_HOST_DIR="${HARNESS_DIR}/corpus/draft-grafana-top-panel-shortlist.themes"

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
seed_env_args=()
if [[ -n "$DATASET_VARIANTS" ]]; then
  seed_env_args+=(-e "PROM_HARNESS_DATASET_VARIANTS=${DATASET_VARIANTS}")
fi
docker compose --profile jobs run --rm "${seed_env_args[@]}" seed

run_compare() {
  local corpus_path="$1" report_dest="$2" label="$3"
  local env_args=(-e "PROM_HARNESS_CORPUS_PATH=${corpus_path}")
  if [[ -n "$SUBJECTS" ]]; then
    env_args+=(-e "PROM_HARNESS_SUBJECTS=${SUBJECTS}")
  fi
  log "Running compare: ${label}"
  docker compose --profile jobs run --rm "${env_args[@]}" compare || true
  if [[ "${report_dest}" != "${HARNESS_DIR}/artifacts/compare-report.json" ]]; then
    cp "${HARNESS_DIR}/artifacts/compare-report.json" "${report_dest}"
  fi
  print_compare_summary "${report_dest}" "${label}"
}

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
else
  corpus="${CORPUS_PATH_OVERRIDE:-/app/harness/corpus/queries.json}"
  run_compare "$corpus" "${HARNESS_DIR}/artifacts/compare-report.json" "${THEME:-default}"
fi

log "Harness run completed successfully."
