#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: native-gap-report.sh [REPORT]

Summarize a native-mode compliance report by gap category. If REPORT is omitted,
the latest native report in PROM_SHIM_COMPLIANCE_ARTIFACT_DIR is used; when
unset, the default is harness/artifacts/compliance/latest.
EOF
}

CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
REPO_ROOT=$(cd "$ROOT/../.." && pwd)
DEFAULT_ARTIFACT_DIR="${PROM_SHIM_COMPLIANCE_ARTIFACT_DIR:-${REPO_ROOT}/harness/artifacts/compliance/latest}"
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ $# -gt 1 ]]; then
  echo "too many arguments" >&2
  usage >&2
  exit 2
fi
report="${1:-}"
if [[ -z "$report" ]]; then
  report=$(ls -t "$DEFAULT_ARTIFACT_DIR"/compliance-report-native-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no native-mode report found in $DEFAULT_ARTIFACT_DIR/" >&2
    exit 2
  fi
fi
if [[ -n "$report" && ! -f "$report" && -f "$CALLER_PWD/$report" ]]; then
  report="$CALLER_PWD/$report"
fi
if [[ ! -f "$report" ]]; then
  echo "report not found: $report" >&2
  exit 2
fi

BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
"$BIN" --action native-gap --report "$report"
