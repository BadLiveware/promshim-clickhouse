#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: export-ci-reports.sh [ARTIFACT_DIR]

Convert compliance JSON reports into CI-friendly artifacts:
  - ci/junit-<mode>.xml for test reporters
  - ci/summary.md for GitHub job summaries

ARTIFACT_DIR defaults to harness/artifacts/compliance/latest.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../../.." && pwd)
ARTIFACT_DIR="${1:-${REPO_ROOT}/harness/artifacts/compliance/latest}"
ALLOWLIST="${REPO_ROOT}/harness/compliance/expected-failures.json"
OUT_DIR="${ARTIFACT_DIR}/ci"

if [[ ! -d "$ARTIFACT_DIR" ]]; then
  echo "compliance artifact directory not found: ${ARTIFACT_DIR}" >&2
  exit 1
fi

BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
mkdir -p "$OUT_DIR"
: > "${OUT_DIR}/summary.md"

export_one() {
  local mode="$1"
  local pattern="${ARTIFACT_DIR}/compliance-report-${mode}-*.json"
  local report
  report=$(ls -t $pattern 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "No ${mode} compliance report found under ${ARTIFACT_DIR}" >&2
    return 0
  fi

  "$BIN" --output-format junit --mode "$mode" --report "$report" --allowlist "$ALLOWLIST" --output "${OUT_DIR}/junit-${mode}.xml"
  "$BIN" --output-format markdown --mode "$mode" --report "$report" --allowlist "$ALLOWLIST" >> "${OUT_DIR}/summary.md"
}

export_one prefer
export_one native

echo "Compliance CI reports written to ${OUT_DIR}"
