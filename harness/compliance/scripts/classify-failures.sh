#!/usr/bin/env bash
# classify-failures.sh - bucket compliance-report failures by shape
#
# Usage:
#   classify-failures.sh                    # summary table of failure buckets
#   classify-failures.sh -b BUCKET          # dump full details for one bucket
#   classify-failures.sh -b BUCKET -n 5     # same, but show only first N
#   classify-failures.sh REPORT_PATH        # explicit report file
#
# Defaults to the most recent artifacts/compliance-report-*.json.
#
# Buckets:
#   unsupported_501            - test side returned HTTP 501 (unimplemented)
#   unimplemented_function     - test side errored "not implemented"
#   server_error_502           - test side returned HTTP 502 (backend crash)
#   server_error_500           - test side returned HTTP 500
#   http_error_timeout         - transport-level error (likely 10s timeout)
#   other_unexpected_failure   - some other error from test side
#   unexpected_success         - test side passed a query that Prom rejects
#   topk_bottomk_ordering      - topk/bottomk query; tie-break differences
#   staleness_nan_marker       - diff contains "NaN @[..." sample-pair lines
#   inf_nan_edge               - diff contains both Inf and NaN (edge cases)
#   empty_on_test              - reference has series, test side returned empty
#   small_value_divergence     - <=2 changed lines, both sides non-empty (float precision)
#   other_diff                 - everything else

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

bucket=""
limit=0
report=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -b|--bucket)   bucket="$2"; shift 2 ;;
    -n|--limit)    limit="$2"; shift 2 ;;
    -h|--help)     sed -n '2,/^$/p' "$0"; exit 0 ;;
    -*)            echo "unknown flag: $1" >&2; exit 2 ;;
    *)             report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $ROOT/artifacts/" >&2
    exit 1
  fi
fi

read -r -d '' CLASSIFY <<'JQ' || true
def bucket:
  if .unsupported == true then "unsupported_501"
  elif (.unexpectedFailure // "") != "" then
    (if   .unexpectedFailure | test("not implemented")       then "unimplemented_function"
     elif .unexpectedFailure | test("server error: 502")     then "server_error_502"
     elif .unexpectedFailure | test("server error: 500")     then "server_error_500"
     elif .unexpectedFailure | test("^Get \"")               then "http_error_timeout"
     else "other_unexpected_failure" end)
  elif .unexpectedSuccess == true then "unexpected_success"
  elif (.diff // "") != "" then
    (if   .testCase.query | test("^(topk|bottomk)\\b")       then "topk_bottomk_ordering"
     elif .diff | test("NaN @\\[")                           then "staleness_nan_marker"
     elif (.diff | test("Inf")) and (.diff | test("NaN"))    then "inf_nan_edge"
     elif (.diff | test("(^|\n)\\+ ")) | not                 then "empty_on_test"
     elif (.diff | split("\n") | map(select(startswith("+ ") or startswith("- "))) | length) <= 2
                                                             then "small_value_divergence"
     else "other_diff" end)
  else "passed" end;
JQ

if [[ -z "$bucket" ]]; then
  echo "report: $report"
  jq -r --argjson all "$(jq '.totalResults' "$report")" "
    $CLASSIFY
    .results
    | map({b: bucket, q: .testCase.query})
    | group_by(.b)
    | map({b: .[0].b, n: length, samples: [.[0:3] | .[].q]})
    | sort_by(-.n)
    | (\"\"),
      (\"  COUNT  BUCKET                      SAMPLE QUERIES\"),
      (\"  -----  ------                      --------------\"),
      (.[] |
        \"  \" + ((.n | tostring) | . + \"     \" | .[0:5]) +
        \"  \" + (.b | . + \"                            \" | .[0:26]) +
        \"  \" + ((.samples[0] // \"\") | .[0:80]) +
        (if (.samples | length) > 1 then
          \"\n  \" + (\"                                    \"[0:33]) + ((.samples[1]) | .[0:80])
         else \"\" end) +
        (if (.samples | length) > 2 then
          \"\n  \" + (\"                                    \"[0:33]) + ((.samples[2]) | .[0:80])
         else \"\" end)
      ),
      (\"\"),
      (\"  total \(\$all) / passed \([.[] | select(.b == \"passed\") | .n] | add // 0) / failed \([.[] | select(.b != \"passed\") | .n] | add // 0)\")
  " "$report"
else
  jq -r --arg b "$bucket" --argjson n "$limit" "
    $CLASSIFY
    .results
    | map(select(bucket == \$b))
    | (if \$n > 0 then .[0:\$n] else . end)
    | .[]
    | \"QUERY:  \(.testCase.query)\" +
      (if (.unexpectedFailure // \"\") != \"\" then \"\nERROR:  \(.unexpectedFailure)\" else \"\" end) +
      (if (.diff // \"\") != \"\" then \"\nDIFF:\n\(.diff | .[0:2500])\" else \"\" end) +
      \"\n\" + (\"-\" * 80)
  " "$report"
fi
