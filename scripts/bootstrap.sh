#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/bootstrap.sh [--full-reset]

Starts the compose stack and verifies CloudBeaver has the preconfigured ClickHouse connection.

Options:
  --full-reset   Remove the CloudBeaver container + data volume before starting so
                 initial seed files are reloaded as a fresh first-run.
  -h, --help     Show this help message.
EOF
}

FULL_RESET=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --full-reset)
      FULL_RESET=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required but not installed/available." >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required but not installed/available." >&2
  exit 1
fi

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

glog() {
  printf '[%s] %s\n' "$(date +'%Y-%m-%d %H:%M:%S')" "$1"
}

# Return non-zero on timeout.
wait_for_clickhouse() {
  glog "Waiting for ClickHouse..."
  for _ in $(seq 1 30); do
    if docker compose exec -T clickhouse bash -lc 'clickhouse-client --query "SELECT 1" >/dev/null 2>&1'; then
      glog "ClickHouse is ready."
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for ClickHouse." >&2
  return 1
}

# Return non-zero on timeout.
wait_for_cloudbeaver_graphql() {
  glog "Waiting for CloudBeaver GraphQL..."
  for _ in $(seq 1 40); do
    status=$(curl -sS -o /tmp/cb_gql.json -w '%{http_code}' \
      -H 'Content-Type: application/json' \
      -d '{"query":"query { serverConfig { anonymousAccessEnabled } }"}' \
      http://localhost:8978/api/gql 2>/dev/null || true)

    if [[ "${status:-}" == "200" ]]; then
      glog "CloudBeaver GraphQL is ready."
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for CloudBeaver GraphQL. Last response: ${status:-<no response>}" >&2
  return 1
}

has_prewired_connection() {
  python - <<'PY'
import json

path = '/tmp/cb_gql.json'
try:
    with open(path, 'r', encoding='utf-8') as f:
        payload = json.load(f)
except Exception:
    raise SystemExit(1)

connections = (payload.get('data', {}) or {}).get('userConnections', [])
for c in connections:
    if not isinstance(c, dict) or c.get('id') != 'observability-clickhouse':
        continue
    if (c.get('authNeeded') is False) and (c.get('credentialsSaved') is True):
        raise SystemExit(0)
raise SystemExit(1)
PY
}

query_connections() {
  curl -sS -H 'Content-Type: application/json' \
    -d '{"query":"query { userConnections(projectId:\"g_GlobalConfiguration\") { id name authNeeded credentialsSaved saveCredentials } }"}' \
    http://localhost:8978/api/gql > /tmp/cb_gql.json
}

verify_connection() {
  glog "Verifying preconfigured datasource auto-auth status..."
  if ! query_connections; then
    echo "Unable to reach CloudBeaver GraphQL endpoint." >&2
    return 1
  fi

  if has_prewired_connection; then
    glog "✅ Preconfigured CloudBeaver datasource is visible and auto-authenticated."
    return 0
  fi

  for _ in $(seq 1 10); do
    sleep 2
    if ! query_connections; then
      continue
    fi
    if has_prewired_connection; then
      glog "✅ Preconfigured CloudBeaver datasource is visible and auto-authenticated."
      return 0
    fi
  done

  echo "⚠️ CloudBeaver still does not expose preconfigured ClickHouse with pre-auth." >&2
  echo "If this persists, run: ./scripts/bootstrap.sh --full-reset" >&2
  return 1
}

if (( FULL_RESET )); then
  glog "Full reset requested: removing CloudBeaver container + data volume."
  docker compose stop cloudbeaver || true
  docker compose rm -f cloudbeaver || true
  docker volume rm -f ch-observability_cloudbeaver-data || true
fi

glog "Starting compose stack..."
docker compose up -d

wait_for_clickhouse
wait_for_cloudbeaver_graphql

if verify_connection; then
  glog "Done."
  echo "Open CloudBeaver: http://localhost:8978"
  echo "Login: cbadmin / admin"
  echo "ClickHouse datasource: Local ClickHouse (observability)"
  exit 0
fi

echo "Bootstrap failed."
docker compose logs --no-color --tail=120 cloudbeaver
exit 1
