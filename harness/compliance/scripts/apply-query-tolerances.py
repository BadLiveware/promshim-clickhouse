#!/usr/bin/env python3
import argparse
import json
import math
import re
import sys
import urllib.parse
import urllib.request
from typing import Any, Dict, List, Optional, Tuple


def load_json(path: str) -> Dict[str, Any]:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def fetch_query_range(base_url: str, test_case: Dict[str, Any]) -> Dict[str, Any]:
    params = {
        "query": test_case["query"],
        "start": test_case["start"],
        "end": test_case["end"],
        "step": f"{int(test_case['resolution'] / 1_000_000_000)}s",
    }
    url = base_url.rstrip("/") + "/api/v1/query_range?" + urllib.parse.urlencode(params)
    with urllib.request.urlopen(url, timeout=15) as resp:
        payload = json.loads(resp.read().decode("utf-8"))
    if payload.get("status") != "success":
        raise RuntimeError(f"query_range failed for {test_case['query']!r} against {base_url}: {payload}")
    return payload["data"]


def series_key(series: Dict[str, Any]) -> Tuple[Tuple[str, str], ...]:
    return tuple(sorted((series.get("metric") or {}).items()))


def parse_sample_value(raw: str) -> float:
    if raw == "NaN":
        return math.nan
    if raw in {"Inf", "+Inf"}:
        return math.inf
    if raw == "-Inf":
        return -math.inf
    return float(raw)


def values_close(lhs: float, rhs: float, fraction: float, margin: float) -> bool:
    if math.isnan(lhs) and math.isnan(rhs):
        return True
    if math.isinf(lhs) or math.isinf(rhs):
        return lhs == rhs
    diff = abs(lhs - rhs)
    if diff <= margin:
        return True
    scale = max(abs(lhs), abs(rhs))
    return scale > 0 and diff <= fraction * scale


def compare_matrix_with_tolerance(ref_data: Dict[str, Any], test_data: Dict[str, Any], fraction: float, margin: float) -> Tuple[bool, str]:
    if ref_data.get("resultType") != test_data.get("resultType"):
        return False, f"result type mismatch: {ref_data.get('resultType')} vs {test_data.get('resultType')}"
    if ref_data.get("resultType") != "matrix":
        return False, f"unsupported result type for tolerance processing: {ref_data.get('resultType')}"

    ref_series = {series_key(s): s for s in ref_data.get("result", [])}
    test_series = {series_key(s): s for s in test_data.get("result", [])}
    if set(ref_series) != set(test_series):
        return False, "series label sets differ"

    for key, ref_series_value in ref_series.items():
        test_series_value = test_series[key]
        ref_values = ref_series_value.get("values", [])
        test_values = test_series_value.get("values", [])
        if len(ref_values) != len(test_values):
            return False, f"sample count differs for series {dict(key)}"
        for idx, (ref_pair, test_pair) in enumerate(zip(ref_values, test_values)):
            if ref_pair[0] != test_pair[0]:
                return False, f"timestamp differs for series {dict(key)} at index {idx}"
            ref_value = parse_sample_value(ref_pair[1])
            test_value = parse_sample_value(test_pair[1])
            if not values_close(ref_value, test_value, fraction, margin):
                return False, (
                    f"value differs for series {dict(key)} at ts {ref_pair[0]}: "
                    f"ref={ref_pair[1]} test={test_pair[1]} margin={margin} fraction={fraction}"
                )
    return True, ""


def matching_tolerance(tolerances: List[Dict[str, Any]], query: str) -> Optional[Dict[str, Any]]:
    for tol in tolerances:
        exact = tol.get("query")
        regex = tol.get("query_regex")
        if exact and exact == query:
            return tol
        if regex and re.search(regex, query):
            return tol
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="Apply repo-level per-query tolerances to a compliance report.")
    parser.add_argument("--report", required=True)
    parser.add_argument("--expected", required=True)
    parser.add_argument("--reference-url", required=True)
    parser.add_argument("--test-url", required=True)
    args = parser.parse_args()

    report = load_json(args.report)
    expected = load_json(args.expected)
    tolerances = expected.get("tolerances", [])
    if not tolerances:
        print("tolerance-adjust: no configured tolerances", file=sys.stderr)
        return 0

    adjusted = 0
    for result in report.get("results", []):
        if not result.get("diff"):
            continue
        if result.get("unexpectedFailure") or result.get("unexpectedSuccess") or result.get("unsupported"):
            continue

        test_case = result.get("testCase") or {}
        tolerance = matching_tolerance(tolerances, test_case.get("query", ""))
        if tolerance is None:
            continue

        fraction = float(tolerance.get("fraction", 0.0))
        margin = float(tolerance.get("margin", 0.0))
        ref_data = fetch_query_range(args.reference_url, test_case)
        test_data = fetch_query_range(args.test_url, test_case)
        ok, detail = compare_matrix_with_tolerance(ref_data, test_data, fraction, margin)
        if not ok:
            print(f"tolerance-adjust: {tolerance.get('id', '<unnamed>')} did not match within tolerance for {test_case.get('query')!r}: {detail}", file=sys.stderr)
            continue

        result["diff"] = ""
        result["toleranceApplied"] = {
            "id": tolerance.get("id"),
            "query": tolerance.get("query"),
            "queryRegex": tolerance.get("query_regex"),
            "fraction": fraction,
            "margin": margin,
            "reason": tolerance.get("reason", ""),
        }
        adjusted += 1

    if adjusted:
        with open(args.report, "w", encoding="utf-8") as fh:
            json.dump(report, fh, separators=(",", ":"))
        print(f"tolerance-adjust: cleared {adjusted} diff(s)", file=sys.stderr)
    else:
        print("tolerance-adjust: no diffs cleared", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
