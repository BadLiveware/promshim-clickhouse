#!/usr/bin/env bash
# reconcile-expected.sh - reconcile a compliance report against expected-failures.json.
set -euo pipefail
CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
allowlist="${ROOT}/expected-failures.json"
report=""
mode=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --allowlist) allowlist="$2"; shift 2 ;;
    --mode)      mode="$2"; shift 2 ;;
    -h|--help)   sed -n '2,8p' "$0"; exit 0 ;;
    -*)          echo "unknown flag: $1" >&2; exit 2 ;;
    *)           report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $ROOT/artifacts/" >&2
    exit 2
  fi
fi
if [[ ! -f "$allowlist" ]]; then
  echo "allowlist not found: $allowlist" >&2
  exit 2
fi

if [[ -n "$report" && ! -f "$report" && -f "$CALLER_PWD/$report" ]]; then
  report="$CALLER_PWD/$report"
fi

REPO_ROOT=$(cd "$ROOT/../.." && pwd)
BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
args=(--action reconcile --report "$report" --allowlist "$allowlist")
[[ -n "$mode" ]] && args+=(--mode "$mode")
"$BIN" "${args[@]}"
