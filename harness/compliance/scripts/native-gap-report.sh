#!/usr/bin/env bash
# native-gap-report.sh — summarize a native-mode compliance report by gap
# category. Native gaps are tracked OPENLY (no allowlist) because we're
# actively working on closing them; this script produces the visible
# inventory of what remains.
#
# Bucket definitions (in order):
#   1. diff_failure      — the shim returned results but they diverged
#                          from Prom 3.x. Real native-SQL correctness bugs.
#   2. unsupported       — planner refused to lower the query (root-plan
#                          rejection, etc). These are missing native-SQL
#                          coverage, not bugs.
#   3. unexpected_failure_other — everything else (bad_data, etc.)
#
# Usage:
#   native-gap-report.sh                   # latest native report (compliance-report-native-*.json)
#   native-gap-report.sh REPORT_PATH       # specific report
#
# Exits 0 always. This is an informational script, not a gate.

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

report="${1:-}"
if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-native-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no native-mode report found in $ROOT/artifacts/" >&2
    exit 2
  fi
fi

if [[ ! -f "$report" ]]; then
  echo "report not found: $report" >&2
  exit 2
fi

echo "native-mode compliance gap report"
echo "report: $report"
echo

totals=$(jq '{
  total: .totalResults,
  passed: [.results[] | select(.diff == "" and (.unexpectedFailure // "") == "" and (.unexpectedSuccess // false) == false)] | length,
  diff_failure: [.results[] | select(.diff != "")] | length,
  unsupported_root: [.results[] | select((.unexpectedFailure // "") | contains("requires a native_sql root plan"))] | length,
  unexpected_failure_other: [.results[] | select(
    (.unexpectedFailure // "") != ""
    and ((.unexpectedFailure // "") | contains("requires a native_sql root plan") | not)
  )] | length
}' < "$report")

total=$(jq -r '.total' <<<"$totals")
passed=$(jq -r '.passed' <<<"$totals")
diff_failure=$(jq -r '.diff_failure' <<<"$totals")
unsupported=$(jq -r '.unsupported_root' <<<"$totals")
other=$(jq -r '.unexpected_failure_other' <<<"$totals")

printf 'total queries      : %d\n' "$total"
printf 'passing on native  : %d (%.1f%%)\n' "$passed" "$(awk "BEGIN{printf \"%.1f\", ($passed/$total)*100}")"
printf 'diff failures      : %d  (native lowered but returned wrong values — real bugs)\n' "$diff_failure"
printf 'unsupported root   : %d  (planner refuses to lower — coverage gaps)\n' "$unsupported"
printf 'other errors       : %d\n' "$other"
echo

if (( diff_failure > 0 )); then
  echo "== diff failures (native-SQL correctness bugs) =="
  jq -r '.results[]
    | select(.diff != "")
    | "QUERY: \(.testCase.query)\n  DIFF: \(.diff | .[0:200])...\n"' < "$report"
fi

if (( unsupported > 0 )); then
  echo "== unsupported-root queries (native lowering not yet implemented for these shapes) =="
  echo "Grouped by normalized shape (metrics/numbers/strings collapsed):"
  echo
  jq -r '.results[]
    | select((.unexpectedFailure // "") | contains("requires a native_sql root plan"))
    | .testCase.query' < "$report" \
    | sed -E 's/[0-9]+(\.[0-9]+)?/N/g; s/demo_[a-z_]+/<metric>/g; s/"[^"]*"/"X"/g; s/\{[^}]*\}//g' \
    | sort | uniq -c | sort -rn
  echo
fi

if (( other > 0 )); then
  echo "== other unexpected failures =="
  jq -r '.results[]
    | select(
        (.unexpectedFailure // "") != ""
        and ((.unexpectedFailure // "") | contains("requires a native_sql root plan") | not)
      )
    | "QUERY: \(.testCase.query)\n  ERR : \(.unexpectedFailure)\n"' < "$report"
fi

echo "note: native gaps are tracked openly — they are NOT allowlisted in"
echo "      expected-failures.json. Each number above should trend down as"
echo "      native lowering coverage grows."
exit 0
