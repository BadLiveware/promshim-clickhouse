#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
SUBMODULE="${ROOT}/prom-compliance/promql"
TESTER="${SUBMODULE}/promql-compliance-tester"

if [[ ! -x "$TESTER" ]]; then
  echo ">> Building compliance tester..."
  (cd "$SUBMODULE" && go build ./cmd/promql-compliance-tester)
fi

OUTPUT_DIR="${ROOT}/artifacts"
mkdir -p "$OUTPUT_DIR"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUTPUT_FILE="${OUTPUT_DIR}/compliance-report-${STAMP}.json"

echo ">> Running compliance tester → ${OUTPUT_FILE}"
"$TESTER" \
  -config-file="${SUBMODULE}/promql-test-queries.yml" \
  -config-file="${ROOT}/test-promshim.yml" \
  -output-format=json \
  -output-passing \
  > "$OUTPUT_FILE" || echo ">> Tester exited non-zero (expected when failures exist)"

echo ">> Report written to: ${OUTPUT_FILE}"
echo ">> Summary:"
jq '{
  total: .totalResults,
  passed: [.results[] | select(.diff == "" and (.unexpectedFailure // "") == "" and (.unexpectedSuccess // false) == false and (.unsupported // false) == false)] | length,
  diff_failure: [.results[] | select(.diff != "")] | length,
  unexpected_failure: [.results[] | select((.unexpectedFailure // "") != "")] | length,
  unexpected_success: [.results[] | select(.unexpectedSuccess == true)] | length,
  unsupported: [.results[] | select(.unsupported == true)] | length
}' < "$OUTPUT_FILE"

echo
echo ">> Reconciling against expected-failures allowlist"
"${ROOT}/scripts/reconcile-expected.sh" "$OUTPUT_FILE" || echo ">> Reconcile flagged drift (exit 1) — see above"
