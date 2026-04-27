#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: reconcile-expected.sh [REPORT] [--allowlist PATH] [--mode MODE]

Reconcile a compliance report against expected-failures.json. If REPORT is
omitted, the latest report in PROM_SHIM_COMPLIANCE_ARTIFACT_DIR is used; when
unset, the default is harness/artifacts/compliance/latest.

Options:
  --allowlist PATH  Expected-failures allowlist path.
  --mode MODE       Apply mode-tagged allowlist entries for this mode.
  -h, --help        Show this help.
EOF
}

CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
REPO_ROOT=$(cd "$ROOT/../.." && pwd)
DEFAULT_ARTIFACT_DIR="${PROM_SHIM_COMPLIANCE_ARTIFACT_DIR:-${REPO_ROOT}/harness/artifacts/compliance/latest}"
allowlist="${ROOT}/expected-failures.json"
report=""
mode=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --allowlist) allowlist="$2"; shift 2 ;;
    --mode)      mode="$2"; shift 2 ;;
    -h|--help)   usage; exit 0 ;;
    -*)          echo "unknown flag: $1" >&2; exit 2 ;;
    *)           report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$DEFAULT_ARTIFACT_DIR"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $DEFAULT_ARTIFACT_DIR/" >&2
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

BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
args=(--action reconcile --report "$report" --allowlist "$allowlist")
[[ -n "$mode" ]] && args+=(--mode "$mode")
"$BIN" "${args[@]}"
