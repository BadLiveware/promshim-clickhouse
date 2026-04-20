#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/export-grafana-dashboards.sh [options]

Export all Grafana dashboards via the Grafana HTTP API.

Authentication defaults to none, which works when Grafana is openly reachable
inside the network. For auth-protected Grafana, you can pass either:
- --token / GRAFANA_TOKEN for bearer auth
- --cookie / GRAFANA_COOKIE for a raw Cookie header (useful for IAP)
- --cookie-file / GRAFANA_COOKIE_FILE for a curl/Netscape cookie jar file

Options:
  --url <url>              Grafana base URL (default: http://127.0.0.1:3000)
  --output-dir <path>      Output directory (default: ./scratch/grafana-dashboards)
  --token <token>          Optional Grafana API bearer token
  --cookie <value>         Optional raw Cookie header value
  --cookie-file <path>     Optional cookie jar file for curl (-b)
  --header <header>        Optional extra HTTP header; repeatable
  --org-id <id>            Optional X-Grafana-Org-Id header
  --page-size <n>          Search page size (default: 5000)
  --raw-response           Save full /api/dashboards/uid response instead of .dashboard only
  --extract-promql         Also write unique panel target expr values to promql.txt
  -h, --help               Show this help text

Environment:
  GRAFANA_URL              Default value for --url
  GRAFANA_TOKEN            Default value for --token
  GRAFANA_COOKIE           Default value for --cookie
  GRAFANA_COOKIE_FILE      Default value for --cookie-file
  GRAFANA_ORG_ID           Default value for --org-id

Examples:
  ./scripts/export-grafana-dashboards.sh
  ./scripts/export-grafana-dashboards.sh --url http://grafana.monitoring.svc:3000
  ./scripts/export-grafana-dashboards.sh --extract-promql --output-dir ./scratch/grafana-export
  ./scripts/export-grafana-dashboards.sh --url https://grafana.example.com --cookie 'name=value; other=value'
  ./scripts/export-grafana-dashboards.sh --url https://grafana.example.com --cookie-file ./cookies.txt
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

sanitize_path_component() {
  local input="$1"
  local cleaned
  cleaned=$(printf '%s' "$input" | tr '/:' '__' | tr -cd '[:alnum:] ._-' | sed -E 's/[[:space:]]+/ /g; s/^ +//; s/ +$//')
  if [[ -z "$cleaned" ]]; then
    cleaned="unnamed"
  fi
  printf '%s' "$cleaned"
}

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GRAFANA_URL="${GRAFANA_URL:-http://127.0.0.1:3000}"
GRAFANA_TOKEN="${GRAFANA_TOKEN:-}"
GRAFANA_COOKIE="${GRAFANA_COOKIE:-}"
GRAFANA_COOKIE_FILE="${GRAFANA_COOKIE_FILE:-}"
GRAFANA_ORG_ID="${GRAFANA_ORG_ID:-}"
OUTPUT_DIR="${REPO_ROOT}/scratch/grafana-dashboards"
PAGE_SIZE=5000
RAW_RESPONSE=0
EXTRACT_PROMQL=0
EXTRA_HEADERS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      GRAFANA_URL="$2"
      shift 2
      ;;
    --output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --token)
      GRAFANA_TOKEN="$2"
      shift 2
      ;;
    --cookie)
      GRAFANA_COOKIE="$2"
      shift 2
      ;;
    --cookie-file)
      GRAFANA_COOKIE_FILE="$2"
      shift 2
      ;;
    --header)
      EXTRA_HEADERS+=("$2")
      shift 2
      ;;
    --org-id)
      GRAFANA_ORG_ID="$2"
      shift 2
      ;;
    --page-size)
      PAGE_SIZE="$2"
      shift 2
      ;;
    --raw-response)
      RAW_RESPONSE=1
      shift
      ;;
    --extract-promql)
      EXTRACT_PROMQL=1
      shift
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

if ! [[ "$PAGE_SIZE" =~ ^[0-9]+$ ]] || [[ "$PAGE_SIZE" -lt 1 ]]; then
  fatal "--page-size must be a positive integer"
fi

OUTPUT_DIR="${OUTPUT_DIR/#\~/$HOME}"
GRAFANA_COOKIE_FILE="${GRAFANA_COOKIE_FILE/#\~/$HOME}"

ensure_command curl
ensure_command jq

CURL_ARGS=(
  -fsS
  -H "Accept: application/json"
)

if [[ -n "$GRAFANA_TOKEN" ]]; then
  CURL_ARGS+=( -H "Authorization: Bearer ${GRAFANA_TOKEN}" )
fi

if [[ -n "$GRAFANA_COOKIE" ]]; then
  CURL_ARGS+=( -H "Cookie: ${GRAFANA_COOKIE}" )
fi

if [[ -n "$GRAFANA_COOKIE_FILE" ]]; then
  CURL_ARGS+=( -b "$GRAFANA_COOKIE_FILE" )
fi

if [[ -n "$GRAFANA_ORG_ID" ]]; then
  CURL_ARGS+=( -H "X-Grafana-Org-Id: ${GRAFANA_ORG_ID}" )
fi

for header in "${EXTRA_HEADERS[@]}"; do
  CURL_ARGS+=( -H "$header" )
done

api_get() {
  local path="$1"
  curl "${CURL_ARGS[@]}" "${GRAFANA_URL%/}${path}"
}

mkdir -p "$OUTPUT_DIR"

TMP_DIR=$(mktemp -d)
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

SEARCH_NDJSON="$TMP_DIR/search.ndjson"
EXPORT_NDJSON="$TMP_DIR/export.ndjson"
PROMQL_TMP="$TMP_DIR/promql.txt"

log "Fetching dashboard list from ${GRAFANA_URL%/}"
page=1
while :; do
  response=$(api_get "/api/search?type=dash-db&limit=${PAGE_SIZE}&page=${page}") || fatal "Failed to fetch dashboard search page ${page}. If Grafana requires auth, pass --token, --cookie, or --cookie-file."
  count=$(jq 'length' <<<"$response")
  if [[ "$count" -eq 0 ]]; then
    break
  fi
  jq -c '.[]' <<<"$response" >> "$SEARCH_NDJSON"
  log "Fetched search page ${page} (${count} dashboards)"
  if [[ "$count" -lt "$PAGE_SIZE" ]]; then
    break
  fi
  page=$((page + 1))
done

if [[ ! -s "$SEARCH_NDJSON" ]]; then
  fatal "No dashboards found via /api/search?type=dash-db"
fi

total=$(wc -l < "$SEARCH_NDJSON" | tr -d ' ')
log "Exporting ${total} dashboards into ${OUTPUT_DIR}"

while IFS= read -r item; do
  uid=$(jq -r '.uid // empty' <<<"$item")
  title=$(jq -r '.title // "untitled"' <<<"$item")
  folder=$(jq -r '.folderTitle // "General"' <<<"$item")
  url=$(jq -r '.url // empty' <<<"$item")

  if [[ -z "$uid" ]]; then
    log "Skipping dashboard with missing uid: ${title}"
    continue
  fi

  safe_folder=$(sanitize_path_component "$folder")
  safe_title=$(sanitize_path_component "$title")
  target_dir="${OUTPUT_DIR}/${safe_folder}"
  target_file="${target_dir}/${uid}-${safe_title}.json"

  mkdir -p "$target_dir"

  dashboard_response=$(api_get "/api/dashboards/uid/${uid}") || fatal "Failed to export dashboard uid=${uid}"
  if (( RAW_RESPONSE == 1 )); then
    jq '.' <<<"$dashboard_response" > "$target_file"
  else
    jq '.dashboard' <<<"$dashboard_response" > "$target_file"
  fi

  jq -cn \
    --arg uid "$uid" \
    --arg title "$title" \
    --arg folder "$folder" \
    --arg url "$url" \
    --arg path "$target_file" \
    '{uid:$uid,title:$title,folder:$folder,url:$url,path:$path}' >> "$EXPORT_NDJSON"

  if (( EXTRACT_PROMQL == 1 )); then
    jq -r '.. | objects | .expr? // empty' "$target_file" >> "$PROMQL_TMP"
  fi

  log "Exported ${folder} / ${title}"
done < "$SEARCH_NDJSON"

jq -s '.' "$EXPORT_NDJSON" > "${OUTPUT_DIR}/index.json"

if (( EXTRACT_PROMQL == 1 )); then
  sort -u "$PROMQL_TMP" | sed '/^$/d' > "${OUTPUT_DIR}/promql.txt"
  query_count=$(wc -l < "${OUTPUT_DIR}/promql.txt" | tr -d ' ')
  log "Wrote ${query_count} unique panel expr queries to ${OUTPUT_DIR}/promql.txt"
fi

log "Wrote dashboard index to ${OUTPUT_DIR}/index.json"
log "Grafana dashboard export completed successfully."
